package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/vietbui/chat-quality-agent/api/middleware"
	"github.com/vietbui/chat-quality-agent/db/models"
)

func TestPendingPersonalZaloApprovalChannelsMySQL(t *testing.T) {
	database := facebookOAuthTestDatabase(t)
	if err := database.AutoMigrate(&models.PersonalZaloThreadApproval{}); err != nil {
		t.Fatal(err)
	}
	const tenant, otherTenant = "pending-approvals", "other-pending-approvals"
	createFacebookOAuthTenant(t, database, tenant)
	createFacebookOAuthTenant(t, database, otherTenant)

	addChannel := func(id, tenantID, account, kind, metadata string, active, autoReply bool) {
		t.Helper()
		channel := models.Channel{
			ID: id, TenantID: tenantID, ExternalID: account, Name: id, ChannelType: kind,
			Metadata: metadata, CredentialsEncrypted: []byte("fixture"), PersonalZaloAutoReplyEnabled: autoReply,
		}
		if err := database.Create(&channel).Error; err != nil {
			t.Fatal(err)
		}
		if !active {
			if err := database.Model(&channel).Update("is_active", false).Error; err != nil {
				t.Fatal(err)
			}
		}
	}
	addApproval := func(channel, account, threadType, threadID, status string, present bool) {
		t.Helper()
		row := models.PersonalZaloThreadApproval{
			ChannelID: channel, AccountExternalID: account, ThreadType: threadType, ThreadID: threadID,
			Status: status, Present: present, FirstSeenAt: time.Now(),
		}
		if err := database.Create(&row).Error; err != nil {
			t.Fatal(err)
		}
	}

	addChannel("Sales", tenant, "110", "personal_zalo_import", `{}`, true, true)
	addChannel("Support", tenant, "220", "personal_zalo_import", `{"chatbot_mode":"chatbot"}`, true, true)
	addChannel("Empty", tenant, "330", "personal_zalo_import", `{}`, true, true)
	addChannel("Off", tenant, "440", "personal_zalo_import", `{}`, true, false)
	addChannel("Inactive", tenant, "550", "personal_zalo_import", `{}`, false, true)
	addChannel("Internbot", tenant, "660", "personal_zalo_import", `{"chatbot_mode":"internbot"}`, true, true)
	addChannel("OA", tenant, "770", "zalo_oa", `{}`, true, true)
	addChannel("Other", otherTenant, "880", "personal_zalo_import", `{}`, true, true)
	addApproval("Sales", "110", "user", "1", personalZaloPending, true)
	addApproval("Sales", "110", "group", "1", personalZaloPending, true)
	addApproval("Sales", "110", "user", "2", personalZaloPending, false)
	addApproval("Sales", "110", "user", "3", personalZaloApproved, true)
	addApproval("Sales", "110", "user", "4", personalZaloRejected, true)
	addApproval("Sales", "998", "user", "1", personalZaloPending, true)
	addApproval("Support", "220", "user", "1", personalZaloPending, true)
	addApproval("Off", "440", "user", "1", personalZaloPending, true)
	addApproval("Inactive", "550", "user", "1", personalZaloPending, true)
	addApproval("Internbot", "660", "user", "1", personalZaloPending, true)
	addApproval("OA", "770", "user", "1", personalZaloPending, true)
	addApproval("Other", "880", "user", "1", personalZaloPending, true)

	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("tenant_id", c.GetHeader("Test-Tenant"))
		c.Set("tenant_role", c.GetHeader("Test-Role"))
	})
	router.GET("/pending", middleware.RequireRole("owner", "admin"), GetPendingPersonalZaloApprovalChannels)

	type channelSummary struct {
		ChannelID string `json:"channel_id"`
		Name      string `json:"name"`
		Count     int    `json:"count"`
	}
	request := func(tenantID, role string, status int) []channelSummary {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/pending", nil)
		req.Header.Set("Test-Tenant", tenantID)
		req.Header.Set("Test-Role", role)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		if w.Code != status {
			t.Fatalf("%s in %s: status %d, want %d: %s", role, tenantID, w.Code, status, w.Body)
		}
		if status != http.StatusOK {
			return nil
		}
		var response struct{ Channels []channelSummary }
		if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		return response.Channels
	}

	request(tenant, "member", http.StatusForbidden)
	if got, want := request(tenant, "admin", http.StatusOK), []channelSummary{{"Sales", "Sales", 2}, {"Support", "Support", 1}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("admin channels = %+v, want %+v", got, want)
	}
	if got, want := request(otherTenant, "owner", http.StatusOK), []channelSummary{{"Other", "Other", 1}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("owner channels = %+v, want %+v", got, want)
	}
	createFacebookOAuthTenant(t, database, "empty-tenant")
	if got := request("empty-tenant", "admin", http.StatusOK); got == nil || len(got) != 0 {
		t.Fatalf("expected an empty array, got %#v", got)
	}
}
