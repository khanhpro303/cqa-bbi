package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/db/models"
	"github.com/vietbui/chat-quality-agent/pkg"
)

// TestSendTestTemplateMessageValidation covers the validation branches of
// SendTestTemplateMessage that do not require an actual call to the Zalo API:
// missing channel, wrong channel type, inactive channel, and target user not
// in the active whitelist. The happy path (which would call openapi.zalo.me)
// is verified via the manual smoke test documented in the plan.
func TestSendTestTemplateMessageValidation(t *testing.T) {
	dsn := os.Getenv("TEST_DB_DSN")
	if dsn == "" {
		dsn = "cqa:cqa_password@tcp(127.0.0.1:3306)/cqa?charset=utf8mb4&parseTime=True&loc=UTC"
	}
	if err := db.Connect(dsn, false); err != nil {
		t.Skip("Skipping TestSendTestTemplateMessageValidation: database not available")
		return
	}
	defer db.Close()

	tenantID := "testten-" + pkg.NewUUID()[:8]
	activeChannelID := "testchn-" + pkg.NewUUID()[:8]
	inactiveChannelID := "testchn-" + pkg.NewUUID()[:8]
	wrongTypeChannelID := "testchn-" + pkg.NewUUID()[:8]
	whitelistedUser := "zalo-user-active"

	db.DB.Exec(`INSERT INTO tenants (id, name, slug, settings, created_at, updated_at) VALUES (?, ?, ?, '{}', NOW(), NOW())`,
		tenantID, "Test Tenant", "test-"+tenantID)
	defer db.DB.Exec("DELETE FROM tenants WHERE id = ?", tenantID)
	defer db.DB.Exec("DELETE FROM channels WHERE tenant_id = ?", tenantID)
	defer db.DB.Exec("DELETE FROM zalo_whitelist WHERE tenant_id = ?", tenantID)

	now := time.Now()
	channels := []models.Channel{
		{ID: activeChannelID, TenantID: tenantID, ChannelType: "zalo_oa", Name: "Active OA", IsActive: true, CreatedAt: now, UpdatedAt: now},
		{ID: inactiveChannelID, TenantID: tenantID, ChannelType: "zalo_oa", Name: "Inactive OA", IsActive: false, CreatedAt: now, UpdatedAt: now},
		{ID: wrongTypeChannelID, TenantID: tenantID, ChannelType: "facebook", Name: "FB Page", IsActive: true, CreatedAt: now, UpdatedAt: now},
	}
	for _, ch := range channels {
		if err := db.DB.Create(&ch).Error; err != nil {
			t.Fatalf("seed channel: %v", err)
		}
	}

	whitelist := models.ZaloWhitelist{
		ID:         pkg.NewUUID(),
		TenantID:   tenantID,
		ChannelID:  activeChannelID,
		ZaloUserID: whitelistedUser,
		Name:       "Active Staff",
		Status:     "active",
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	if err := db.DB.Create(&whitelist).Error; err != nil {
		t.Fatalf("seed whitelist: %v", err)
	}

	gin.SetMode(gin.TestMode)

	tests := []struct {
		name       string
		channelID  string
		zaloUserID string
		wantStatus int
		wantError  string
	}{
		{
			name:       "channel not found",
			channelID:  "does-not-exist",
			zaloUserID: whitelistedUser,
			wantStatus: http.StatusNotFound,
			wantError:  "channel_not_found",
		},
		{
			name:       "wrong channel type",
			channelID:  wrongTypeChannelID,
			zaloUserID: whitelistedUser,
			wantStatus: http.StatusBadRequest,
			wantError:  "unsupported_channel_type",
		},
		{
			name:       "channel inactive",
			channelID:  inactiveChannelID,
			zaloUserID: whitelistedUser,
			wantStatus: http.StatusForbidden,
			wantError:  "channel_is_inactive",
		},
		{
			name:       "target not whitelisted",
			channelID:  activeChannelID,
			zaloUserID: "some-other-user",
			wantStatus: http.StatusForbidden,
			wantError:  "not_whitelisted",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body, _ := json.Marshal(map[string]string{"zalo_user_id": tt.zaloUserID})
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
			c.Request.Header.Set("Content-Type", "application/json")
			c.Params = []gin.Param{{Key: "channelId", Value: tt.channelID}}
			c.Set("tenant_id", tenantID)
			c.Set("user_id", "test-owner")

			SendTestTemplateMessage(c)

			if w.Code != tt.wantStatus {
				t.Fatalf("status: want %d, got %d, body=%s", tt.wantStatus, w.Code, w.Body.String())
			}
			var resp map[string]any
			if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
				t.Fatalf("parse response: %v", err)
			}
			if got, _ := resp["error"].(string); got != tt.wantError {
				t.Fatalf("error: want %q, got %q", tt.wantError, got)
			}
		})
	}
}

func TestSendTestTemplateMessageBadRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/", bytes.NewReader([]byte(`{}`)))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Params = []gin.Param{{Key: "channelId", Value: "any"}}
	c.Set("tenant_id", "any-tenant")
	c.Set("user_id", "test-owner")

	SendTestTemplateMessage(c)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d, body=%s", w.Code, w.Body.String())
	}
}
