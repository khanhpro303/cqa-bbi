package handlers

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"github.com/vietbui/chat-quality-agent/api/contracts"
	"github.com/vietbui/chat-quality-agent/api/middleware"
	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/db/models"
)

func TestPersonalZaloIdentityVerificationCommandAndCRMRedaction(t *testing.T) {
	code := personalZaloPairingPrefix + base64.RawURLEncoding.EncodeToString(make([]byte, 32))
	valid := " VEriFY\t" + code + " "
	if parsed, handled := personalZaloVerificationCommand(valid); !handled || parsed != code {
		t.Fatalf("did not recognize exact command")
	}
	for _, invalid := range []string{"verify", "verify ABCD12", "verify " + code + " extra", "verify pzl_wrong"} {
		if parsed, handled := personalZaloVerificationCommand(invalid); !handled || parsed != "" {
			t.Errorf("wrong command handling for %q", invalid)
		}
	}
	ordinary := "Mời bạn hỏi về từ verify trong tài liệu"
	if parsed, handled := personalZaloVerificationCommand(ordinary); handled || parsed != "" {
		t.Fatal("ordinary message was treated as verification")
	}
	synced := toSyncedMessages([]contracts.ImportMessage{
		{ExternalID: "1", Content: valid},
		{ExternalID: "2", Content: ordinary},
		{ExternalID: "3", Content: "verify là gì"},
		{ExternalID: "4", Content: "Gửi `verify " + code + "` đến tài khoản này."},
	})
	if len(synced) != 4 || synced[0].ExternalID != "1" || strings.Contains(synced[0].Content, code) ||
		synced[1].Content != ordinary || synced[2].Content != "verify là gì" ||
		synced[3].Content != "Gửi `verify [Mã xác thực Zalo đã được ẩn]` đến tài khoản này." {
		t.Fatal("CRM import must preserve messages while masking only personal pairing credentials")
	}
}

func TestPersonalZaloIdentityLinksMySQL(t *testing.T) {
	database := facebookOAuthTestDatabase(t)
	if err := database.AutoMigrate(&models.ZaloWhitelist{}, &models.ZaloCustomer{}, &models.PersonalZaloIdentityLink{}, &models.AppSetting{}); err != nil {
		t.Fatal(err)
	}
	createFacebookOAuthTenant(t, database, "pair-tenant")
	createFacebookOAuthTenant(t, database, "other-tenant")
	channel := models.Channel{ID: "personal", TenantID: "pair-tenant", ChannelType: "personal_zalo_import", ExternalID: "100",
		Name: "Personal", IsActive: true, PersonalZaloAutoReplyEnabled: true, CredentialsEncrypted: []byte("test"), Metadata: "{}"}
	oa := channel
	oa.ID, oa.ChannelType, oa.ExternalID, oa.Name = "oa", "zalo_oa", "200", "OA"
	otherChannel := channel
	otherChannel.ID, otherChannel.ExternalID = "other-personal", "300"
	for _, destination := range []models.Channel{channel, oa, otherChannel} {
		if err := database.Create(&destination).Error; err != nil {
			t.Fatal(err)
		}
	}
	staff := models.ZaloWhitelist{ID: "oa-staff", TenantID: channel.TenantID, ChannelID: oa.ID,
		ZaloUserID: "456", Name: "Nhân viên OA", Status: "active", VerifyToken: "must-not-leak"}
	localStaff := models.ZaloWhitelist{ID: "personal-staff", TenantID: channel.TenantID, ChannelID: channel.ID,
		ZaloUserID: "789", Name: "Nhân viên cá nhân", Status: "active"}
	for _, employee := range []models.ZaloWhitelist{staff, localStaff} {
		if err := database.Create(&employee).Error; err != nil {
			t.Fatal(err)
		}
	}
	customer := models.ZaloCustomer{ID: "approved-customer", TenantID: channel.TenantID, ZaloUserID: "456", Name: "Khách", Status: "approved", CustomerCode: "KH01"}
	otherCustomer := models.ZaloCustomer{ID: "foreign-customer", TenantID: "other-tenant", Name: "Khách khác", Status: "approved", CustomerCode: "KH02"}
	pendingCustomer := models.ZaloCustomer{ID: "pending-customer", TenantID: channel.TenantID, Name: "Khách chờ", Status: "pending_approval"}
	for _, client := range []models.ZaloCustomer{customer, otherCustomer, pendingCustomer} {
		if err := database.Create(&client).Error; err != nil {
			t.Fatal(err)
		}
	}

	request := personalZaloChatbotRequest{TenantID: channel.TenantID, ChannelID: channel.ID, AccountExternalID: channel.ExternalID,
		ThreadType: personalZaloThreadUser, SenderID: "456", ThreadID: "456"}
	assertAgent := func(ch models.Channel, req personalZaloChatbotRequest, want, target string) {
		t.Helper()
		identity, err := resolvePersonalZaloERPIdentity(t.Context(), ch, req)
		if err != nil || identity.AgentType != want || identity.ID != target {
			t.Fatalf("got agent %q identity %q err %v, want %q %q", identity.AgentType, identity.ID, err, want, target)
		}
		if want == "public" && (identity.Customer.ID != customer.ID || identity.Customer.CustomerCode != "KH01") {
			t.Fatal("public grant must resolve the linked CRM customer")
		}
	}
	assertAgent(channel, request, "", "") // OA credentials with the same numeric UID grant nothing here.
	exactStaff := request
	exactStaff.SenderID, exactStaff.ThreadID = "789", "789"
	assertAgent(channel, exactStaff, "private", localStaff.ID)
	group := exactStaff
	group.ThreadType = personalZaloThreadGroup
	assertAgent(channel, group, "", "")
	mismatched := exactStaff
	mismatched.AccountExternalID = "999"
	assertAgent(channel, mismatched, "", "")

	const path = "/channels/personal/personal-zalo/identity-links"
	router := gin.New()
	links := router.Group("/channels/:channelId/personal-zalo/identity-links", func(c *gin.Context) {
		c.Set("tenant_id", channel.TenantID)
		c.Set("tenant_role", c.GetHeader("X-Test-Role"))
	}, middleware.RequireRole("owner", "admin"))
	links.GET("", ListPersonalZaloIdentityLinks)
	links.GET("/options", ListPersonalZaloIdentityOptions)
	links.POST("", InvitePersonalZaloIdentity)
	links.DELETE("/:linkId", DeletePersonalZaloIdentityLink)
	call := func(method, url, role, body string, want int) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, url, strings.NewReader(body))
		req.Header.Set("X-Test-Role", role)
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		if w.Code != want {
			t.Fatalf("%s %s returned %d, want %d: %s", method, url, w.Code, want, w.Body.String())
		}
		return w
	}
	inviteBody := `{"kind":"staff","target_id":"oa-staff"}`
	call(http.MethodPost, path, "member", inviteBody, http.StatusForbidden)
	if err := database.Model(&channel).Update("personal_zalo_auto_reply_enabled", false).Error; err != nil {
		t.Fatal(err)
	}
	if reply := call(http.MethodPost, path, "admin", inviteBody, http.StatusConflict).Body.String(); !strings.Contains(reply, "personal_zalo_pairing_disabled") {
		t.Fatal("issuing a link should fail when the bot cannot receive verification")
	}
	call(http.MethodGet, path, "admin", "", http.StatusOK)
	if err := database.Model(&channel).Update("personal_zalo_auto_reply_enabled", true).Error; err != nil {
		t.Fatal(err)
	}
	call(http.MethodPost, path, "admin", `{"kind":"customer","target_id":"foreign-customer"}`, http.StatusNotFound)
	call(http.MethodPost, path, "admin", `{"kind":"customer","target_id":"pending-customer"}`, http.StatusNotFound)
	options := call(http.MethodGet, path+"/options", "owner", "", http.StatusOK).Body.String()
	if !strings.Contains(options, `"pairing_enabled":true`) || !strings.Contains(options, staff.ID) || !strings.Contains(options, customer.ID) || strings.Contains(options, staff.VerifyToken) ||
		strings.Contains(options, otherCustomer.ID) || strings.Contains(options, pendingCustomer.ID) {
		t.Fatalf("incorrect eligible identities: %s", options)
	}
	invite := func(body string) (id, code string) {
		t.Helper()
		w := call(http.MethodPost, path, "admin", body, http.StatusCreated)
		var reply struct {
			ID      string `json:"id"`
			Command string `json:"verification_command"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &reply); err != nil {
			t.Fatal(err)
		}
		code, _ = personalZaloVerificationCommand(reply.Command)
		if code == "" || reply.ID == "" || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("missing one-time invite fields")
		}
		var stored models.PersonalZaloIdentityLink
		if err := database.Where("id = ?", reply.ID).Take(&stored).Error; err != nil || stored.InviteDigest == nil ||
			*stored.InviteDigest != personalZaloIdentityDigest(code) || stored.SenderID != nil {
			t.Fatalf("invite was not stored as a pending digest: %v", err)
		}
		return reply.ID, code
	}
	staffLink, staffCode := invite(inviteBody)
	call(http.MethodPost, path, "admin", inviteBody, http.StatusConflict)
	if response := call(http.MethodGet, path, "admin", "", http.StatusOK).Body.String(); strings.Contains(response, staffCode) || strings.Contains(response, personalZaloIdentityDigest(staffCode)) {
		t.Fatal("list exposed a pairing credential")
	}
	assertAgent(channel, request, "", "")
	if ok, err := redeemPersonalZaloInvite(t.Context(), otherChannel, request.SenderID, staffCode, time.Now()); ok || err != nil {
		t.Fatalf("another personal channel consumed the grant: %t, %v", ok, err)
	}
	if ok, err := redeemPersonalZaloInvite(t.Context(), channel, request.SenderID, staffCode, time.Now()); !ok || err != nil {
		t.Fatalf("valid staff pairing failed: %t, %v", ok, err)
	}
	if ok, err := redeemPersonalZaloInvite(t.Context(), channel, "999", staffCode, time.Now()); ok || err != nil {
		t.Fatalf("replayed staff pairing: %t, %v", ok, err)
	}
	assertAgent(channel, request, "private", staff.ID)
	customerBody := `{"kind":"customer","target_id":"approved-customer"}`
	expiredLink, expiredCode := invite(customerBody)
	if err := database.Model(&models.PersonalZaloIdentityLink{}).Where("id = ?", expiredLink).
		Update("expires_at", time.Now().Add(-time.Minute)).Error; err != nil {
		t.Fatal(err)
	}
	if ok, err := redeemPersonalZaloInvite(t.Context(), channel, request.SenderID, expiredCode, time.Now()); ok || err != nil {
		t.Fatalf("accepted an expired pairing code: %t, %v", ok, err)
	}
	_, customerCode := invite(customerBody)
	var wg sync.WaitGroup
	outcomes := make(chan bool, 2)
	for range 2 {
		wg.Go(func() {
			ok, err := redeemPersonalZaloInvite(t.Context(), channel, request.SenderID, customerCode, time.Now())
			if err != nil {
				t.Error(err)
			}
			outcomes <- ok
		})
	}
	wg.Wait()
	if first, second := <-outcomes, <-outcomes; first == second {
		t.Fatal("concurrent verification must succeed exactly once")
	}
	assertAgent(channel, request, "private", staff.ID)
	if err := database.Model(&staff).Update("status", "pending").Error; err != nil {
		t.Fatal(err)
	}
	assertAgent(channel, request, "public", customer.ID)
	call(http.MethodDelete, path+"/"+staffLink, "admin", "", http.StatusNoContent)
	if err := database.Model(&customer).Update("status", "rejected").Error; err != nil {
		t.Fatal(err)
	}
	assertAgent(channel, request, "", "")
	if err := database.Where("id = ?", customer.ID).Take(&customer).Error; err != nil || customer.ZaloUserID != "456" {
		t.Fatal("personal linking must preserve the OA customer UID")
	}
}

func TestPersonalZaloIdentityVerificationRateLimit(t *testing.T) {
	if os.Getenv("TEST_REDIS_ADDR") == "" {
		t.Skip("TEST_REDIS_ADDR not set")
	}
	client := redis.NewClient(&redis.Options{Addr: os.Getenv("TEST_REDIS_ADDR")})
	previous := db.RedisClient
	db.RedisClient = client
	t.Cleanup(func() {
		db.RedisClient = previous
		_ = client.Close()
	})
	channel := models.Channel{ID: "personal", TenantID: t.Name(), ChannelType: "personal_zalo_import", ExternalID: "900"}
	caller := personalZaloChatbotRequest{TenantID: channel.TenantID, ChannelID: channel.ID, AccountExternalID: channel.ExternalID,
		ThreadType: personalZaloThreadUser, SenderID: "901", ThreadID: "901",
		Text: "verify " + personalZaloPairingPrefix + base64.RawURLEncoding.EncodeToString(make([]byte, 32))}
	key := "personal_zalo_verify:" + personalZaloIdentityDigest(channel.TenantID+"\x00"+channel.ExternalID+"\x00"+caller.SenderID)
	if err := client.Set(t.Context(), key, personalZaloPairingAttempts, time.Hour).Err(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Del(t.Context(), key).Err() })
	reply, handled, err := handlePersonalZaloIdentityVerification(t.Context(), channel, caller)
	if err != nil || !handled || !strings.Contains(reply, "quá nhiều lần") {
		t.Fatalf("rate limited command was not handled: %t, %v", handled, err)
	}
}
