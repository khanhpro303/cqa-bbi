package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/vietbui/chat-quality-agent/api/middleware"
	"github.com/vietbui/chat-quality-agent/db/models"
)

func TestPersonalZaloStaffApprovalMySQL(t *testing.T) {
	database := facebookOAuthTestDatabase(t)
	if err := database.AutoMigrate(&models.Conversation{}, &models.Message{}, &models.ZaloWhitelist{},
		&models.PersonalZaloIdentityLink{}, &models.ActivityLog{}); err != nil {
		t.Fatal(err)
	}
	const tenant, foreignTenant = "staff-approval-tenant", "other-tenant"
	createFacebookOAuthTenant(t, database, tenant)
	createFacebookOAuthTenant(t, database, foreignTenant)
	channel := models.Channel{ID: "personal", TenantID: tenant, ChannelType: "personal_zalo_import", ExternalID: "100",
		Name: "Personal", IsActive: true, CredentialsEncrypted: []byte("test"), Metadata: "{}"}
	oa := channel
	oa.ID, oa.ChannelType, oa.ExternalID = "oa", "zalo_oa", "200"
	otherChannel := channel
	otherChannel.ID, otherChannel.ExternalID = "other-personal", "300"
	otherOA := oa
	otherOA.ID, otherOA.TenantID = "other-oa", foreignTenant
	for _, ch := range []models.Channel{channel, oa, otherChannel, otherOA} {
		if err := database.Create(&ch).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, employee := range []models.ZaloWhitelist{
		{ID: "existing-oa-staff", TenantID: tenant, ChannelID: oa.ID, ZaloUserID: "10001", Status: "active", Name: "Lâm Khải"},
		{ID: "pending-oa-staff", TenantID: tenant, ChannelID: oa.ID, ZaloUserID: "10002", Status: "active", Name: "Ngọc An"},
		{ID: "other-oa-staff", TenantID: foreignTenant, ChannelID: otherOA.ID, ZaloUserID: "10003", Status: "active"},
		{ID: "conflicting-local-staff", TenantID: tenant, ChannelID: channel.ID, ZaloUserID: "1000", Status: "active"},
	} {
		if err := database.Create(&employee).Error; err != nil {
			t.Fatal(err)
		}
	}
	addConversation := func(id, channelID, account, threadType, peer, senderType, rawSender string) {
		t.Helper()
		conv := models.Conversation{
			ID: id, TenantID: tenant, ChannelID: channelID, CustomerName: "Lâm Khải",
			ExternalUserID: peer, ExternalConversationID: composeImportConversationID(threadType, peer),
			Metadata: fmt.Sprintf(`{"thread_type":%q,"account_external_id":%q}`, threadType, account),
		}
		if err := database.Create(&conv).Error; err != nil {
			t.Fatal(err)
		}
		msg := models.Message{
			ID: id + "-msg", TenantID: tenant, ConversationID: id, ExternalMessageID: id,
			SenderType: senderType, SentAt: time.Now(), Attachments: "[]",
			RawData: fmt.Sprintf(`{"sender_uid":%q,"thread_id":%q}`, rawSender, peer),
		}
		if err := database.Create(&msg).Error; err != nil {
			t.Fatal(err)
		}
	}
	addConversation("eligible", channel.ID, "100", "user", "456", "customer", "456")
	addConversation("second-eligible", channel.ID, "100", "user", "457", "customer", "457")
	addConversation("cross-channel", otherChannel.ID, "300", "user", "600", "customer", "600")
	addConversation("wrong-account", channel.ID, "300", "user", "700", "customer", "700")
	addConversation("group", channel.ID, "100", "group", "800", "customer", "800")
	addConversation("outgoing-only", channel.ID, "100", "user", "900", "agent", "100")
	addConversation("different-incoming-sender", channel.ID, "100", "user", "999", "customer", "900")
	addConversation("local-whitelisted", channel.ID, "100", "user", "1000", "customer", "1000")

	const path = "/channels/personal/personal-zalo/identity-links"
	router := gin.New()
	routes := router.Group("/channels/:channelId/personal-zalo/identity-links", func(c *gin.Context) {
		c.Set("tenant_id", tenant)
		c.Set("tenant_role", c.GetHeader("X-Test-Role"))
	}, middleware.RequireRole("owner", "admin"))
	routes.GET("/direct-conversations", ListPersonalZaloStaffConversations)
	routes.POST("/approve-staff", ApprovePersonalZaloStaffLink)
	routes.DELETE("/:linkId", DeletePersonalZaloIdentityLink)
	call := func(method, suffix, role, body string, want int) []byte {
		t.Helper()
		req := httptest.NewRequest(method, path+suffix, strings.NewReader(body))
		req.Header.Set("X-Test-Role", role)
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		if w.Code != want {
			t.Fatalf("%s %s returned %d, want %d: %s", method, suffix, w.Code, want, w.Body.String())
		}
		return w.Body.Bytes()
	}
	approve := func(role, conversationID, targetID string, want int) []byte {
		t.Helper()
		body := fmt.Sprintf(`{"conversation_id":%q,"target_id":%q}`, conversationID, targetID)
		return call(http.MethodPost, "/approve-staff", role, body, want)
	}
	assertIdentity := func(agent, staffID string) {
		t.Helper()
		req := personalZaloChatbotRequest{TenantID: tenant, ChannelID: channel.ID, AccountExternalID: channel.ExternalID,
			ThreadType: personalZaloThreadUser, ThreadID: "456", SenderID: "456"}
		identity, err := resolvePersonalZaloERPIdentity(t.Context(), channel, req)
		if err != nil || identity.AgentType != agent || identity.ID != staffID {
			t.Fatalf("identity = %+v, %v; want agent %q staff %q", identity, err, agent, staffID)
		}
	}

	call(http.MethodGet, "/direct-conversations", "member", "", http.StatusForbidden)
	for _, search := range []string{"", "?q=L%C3%A2m", "?q=456", "?q=600", "?q=%25"} {
		var choices []struct{ ID string }
		if err := json.Unmarshal(call(http.MethodGet, "/direct-conversations"+search, "owner", "", http.StatusOK), &choices); err != nil {
			t.Fatal(err)
		}
		want := map[string]int{"": 3, "?q=L%C3%A2m": 3, "?q=456": 1, "?q=600": 0, "?q=%25": 0}[search]
		if len(choices) != want || (want == 1 && choices[0].ID != "eligible") {
			t.Fatalf("search %q returned %+v, want %d eligible choices", search, choices, want)
		}
	}
	assertIdentity("", "")
	approve("member", "eligible", "existing-oa-staff", http.StatusForbidden)
	for _, invalid := range []string{"cross-channel", "wrong-account", "group", "outgoing-only", "different-incoming-sender"} {
		approve("admin", invalid, "existing-oa-staff", http.StatusNotFound)
	}
	approve("admin", "eligible", "other-oa-staff", http.StatusNotFound)
	approve("admin", "local-whitelisted", "existing-oa-staff", http.StatusConflict)

	now := time.Now().UTC()
	oldCode := "pending-personal-zalo-test-code"
	digest := personalZaloIdentityDigest(oldCode)
	pending := models.PersonalZaloIdentityLink{ID: "pending-link", TenantID: tenant, ChannelID: channel.ID,
		AccountExternalID: channel.ExternalID, Kind: personalZaloStaffIdentity, TargetID: "existing-oa-staff",
		InviteDigest: &digest, ExpiresAt: now.Add(time.Hour), CreatedAt: now}
	if err := database.Create(&pending).Error; err != nil {
		t.Fatal(err)
	}
	for range 2 {
		body := approve("admin", "eligible", "existing-oa-staff", http.StatusOK)
		var verified models.PersonalZaloIdentityLink
		if err := json.Unmarshal(body, &verified); err != nil {
			t.Fatal(err)
		}
		if verified.ID != pending.ID || verified.VerifiedAt == nil || verified.SenderID == nil || *verified.SenderID != "456" ||
			strings.Contains(string(body), "verification_command") {
			t.Fatalf("unexpected approval response: %s", body)
		}
	}
	if redeemed, err := redeemPersonalZaloInvite(t.Context(), channel, "789", oldCode, now); err != nil || redeemed {
		t.Fatalf("pending code survived approval: redeemed=%t err=%v", redeemed, err)
	}
	assertIdentity("private", "existing-oa-staff")
	approve("admin", "second-eligible", "existing-oa-staff", http.StatusConflict)
	approve("admin", "eligible", "pending-oa-staff", http.StatusConflict)
	if err := database.Model(&models.ZaloWhitelist{}).Where("id = ?", pending.TargetID).Update("status", "pending").Error; err != nil {
		t.Fatal(err)
	}
	assertIdentity("", "")
	approve("admin", "eligible", pending.TargetID, http.StatusNotFound)
	call(http.MethodDelete, "/"+pending.ID, "admin", "", http.StatusNoContent)
	approve("owner", "eligible", "pending-oa-staff", http.StatusOK)
	assertIdentity("private", "pending-oa-staff")
	var approvals int64
	if err := database.Model(&models.ActivityLog{}).Where("action = ? AND resource_id = ?", "personal_zalo.staff_approved", channel.ID).
		Count(&approvals).Error; err != nil || approvals != 2 {
		t.Fatalf("approval audit count = %d, err %v", approvals, err)
	}
}
