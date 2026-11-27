package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/vietbui/chat-quality-agent/api/middleware"
	"github.com/vietbui/chat-quality-agent/db/models"
)

func TestPersonalZaloInternbotModeTransitions(t *testing.T) {
	legacy, err := decodePersonalZaloInternbotConfig(`{"sync_scope":"all"}`)
	if err != nil || legacy.Mode != personalZaloChatbotMode || legacy.Enabled {
		t.Fatalf("legacy defaults changed: %+v, %v", legacy, err)
	}
	internbot := personalZaloInternbotMode
	enabled := true
	move := "333"
	if _, err := planPersonalZaloInternbotUpdate(legacy, personalZaloInternbotPatch{Mode: &internbot, Enabled: &enabled}, true, false); !errors.Is(err, errPersonalZaloInternbotEnableSeparately) {
		t.Fatalf("mode change allowed implicit activation: %v", err)
	}
	current := personalZaloInternbotConfig{
		Mode: internbot, Enabled: true, SupervisorGroupID: "222", Version: "running", ActivatedAt: time.Now(), SupervisorAccountID: "111",
	}
	moved, err := planPersonalZaloInternbotUpdate(current, personalZaloInternbotPatch{SupervisorGroupID: &move}, true, false)
	if err != nil || moved.Enabled || moved.SupervisorGroupID != move || moved.Version == current.Version || !moved.ActivatedAt.IsZero() || moved.SupervisorAccountID != "" {
		t.Fatalf("changed destination must invalidate queued sends: %+v, %v", moved, err)
	}
	if _, err := planPersonalZaloInternbotUpdate(current, personalZaloInternbotPatch{}, true, true); !errors.Is(err, errPersonalZaloInternbotEnableSeparately) {
		t.Fatalf("legacy reply toggle must remain off while Internbot is selected: %v", err)
	}
	for _, metadata := range []map[string]any{
		{"chatbot_mode": "internbot"},
		{"internbot_enabled": true},
		{"internbot_config_version": "forged"},
	} {
		if _, err := checkPersonalZaloInternbotMetadata("zalo_oa", metadata); !errors.Is(err, errInvalidPersonalZaloInternbotSettings) {
			t.Fatalf("OA accepted private mode metadata: %+v", metadata)
		}
	}
}

func TestPersonalZaloInternbotRosterAndConfigurationMySQL(t *testing.T) {
	database := facebookOAuthTestDatabase(t)
	if err := database.AutoMigrate(&models.PersonalZaloInternbotRoster{}, &models.PersonalZaloRosterState{}, &models.PersonalZaloThreadApproval{}); err != nil {
		t.Fatal(err)
	}
	const tenant, channelID, account = "internbot-tenant", "internbot-channel", "111"
	createFacebookOAuthTenant(t, database, tenant)
	channel := models.Channel{
		ID: channelID, TenantID: tenant, ChannelType: "personal_zalo_import", Name: "Zalo cá nhân",
		IsActive: true, PersonalZaloAutoReplyEnabled: true, ExternalID: account, CredentialsEncrypted: []byte("fixture"), Metadata: `{"sync_scope":"all"}`,
	}
	if err := database.Create(&channel).Error; err != nil {
		t.Fatal(err)
	}
	group := personalZaloRosterEntry{ThreadType: "group", ThreadID: "222", DisplayName: "Giám thị"}
	other := personalZaloRosterEntry{ThreadType: "group", ThreadID: "333", DisplayName: "Khách hàng"}
	friend := personalZaloRosterEntry{ThreadType: "user", ThreadID: "444", DisplayName: "Bạn bè"}
	var roster atomic.Pointer[personalZaloRosterSnapshot]
	setRoster := func(offset time.Duration, entries ...personalZaloRosterEntry) personalZaloRosterSnapshot {
		snapshot := personalZaloRosterSnapshot{AccountExternalID: account, CapturedAt: time.Now().UTC().Add(offset), Entries: entries}
		roster.Store(&snapshot)
		return snapshot
	}
	setRoster(-3*time.Second, group, other, friend)
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/accounts":
			_ = json.NewEncoder(w).Encode(personalZaloGatewayListResponse{Accounts: []personalZaloGatewayAccountRecord{{
				ID: "gateway", TenantID: tenant, ChannelID: channelID, Status: "connected", ZaloUID: account, AccountExternalID: account,
			}}})
		case "/api/v1/accounts/gateway/roster":
			_ = json.NewEncoder(w).Encode(roster.Load())
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(gateway.Close)
	t.Setenv("PERSONAL_ZALO_GATEWAY_BASE_URL", gateway.URL)
	t.Setenv("PERSONAL_ZALO_GATEWAY_ADMIN_SECRET", "internbot-test-gateway-admin-secret")
	t.Setenv("JWT_SECRET", "0123456789abcdef0123456789abcdef")
	t.Setenv("ENCRYPTION_KEY", "0123456789abcdef0123456789abcdef")
	t.Setenv("DB_PASSWORD", "fixture")
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("tenant_id", c.GetHeader("Test-Tenant"))
		c.Set("tenant_role", c.GetHeader("Test-Role"))
		c.Set("internal_request_id", "roster-fixture")
	})
	router.PUT("/channels/:channelId", UpdateChannel)
	router.GET("/channels/:channelId/personal-zalo/groups", middleware.RequireRole("owner", "admin"), GetPersonalZaloInternbotGroups)
	router.POST("/roster", IngestPersonalZaloRoster)
	path := "/channels/" + channelID
	groupsPath := path + "/personal-zalo/groups"
	call := func(method, url, tenantID, role string, payload any, status int) *httptest.ResponseRecorder {
		t.Helper()
		encoded, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(method, url, bytes.NewReader(encoded))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Test-Tenant", tenantID)
		req.Header.Set("Test-Role", role)
		response := httptest.NewRecorder()
		router.ServeHTTP(response, req)
		if response.Code != status {
			t.Fatalf("%s %s returned %d, want %d: %s", method, url, response.Code, status, response.Body)
		}
		return response
	}
	update := func(metadata string, status int) {
		t.Helper()
		call(http.MethodPut, path, tenant, "owner", gin.H{"metadata": metadata}, status)
	}
	load := func() personalZaloInternbotConfig {
		t.Helper()
		if err := database.Where("id = ?", channelID).Take(&channel).Error; err != nil {
			t.Fatal(err)
		}
		config, err := decodePersonalZaloInternbotConfig(channel.Metadata)
		if err != nil {
			t.Fatal(err)
		}
		return config
	}
	push := func(snapshot personalZaloRosterSnapshot) {
		t.Helper()
		payload := struct {
			RequestID string `json:"request_id"`
			TenantID  string `json:"tenant_id"`
			ChannelID string `json:"channel_id"`
			personalZaloRosterSnapshot
		}{"roster-fixture", tenant, channelID, snapshot}
		call(http.MethodPost, "/roster", tenant, "owner", payload, http.StatusNoContent)
	}
	call(http.MethodPut, path, tenant, "member", gin.H{"metadata": `{"chatbot_mode":"internbot"}`}, http.StatusForbidden)
	update(`{"chatbot_mode":"internbot","internbot_enabled":true,"supervisor_group_id":"222"}`, http.StatusConflict)
	update(`{"chatbot_mode":"internbot"}`, http.StatusOK)
	if config := load(); config.Mode != "internbot" || config.Enabled || channel.PersonalZaloAutoReplyEnabled {
		t.Fatalf("mode switch must stop direct replies atomically: %+v", config)
	}
	call(http.MethodPost, "/roster", tenant, "owner", gin.H{
		"request_id": "roster-fixture", "tenant_id": tenant, "channel_id": channelID, "account_external_id": account, "check_only": true,
	}, http.StatusOK)
	push(*roster.Load())
	call(http.MethodGet, groupsPath, "other-tenant", "owner", nil, http.StatusNotFound)
	call(http.MethodGet, groupsPath, tenant, "member", nil, http.StatusForbidden)
	list := call(http.MethodGet, groupsPath+"?q=TH%E1%BB%8A&limit=1", tenant, "owner", nil, http.StatusOK)
	var result struct {
		Status string                       `json:"status"`
		Items  []personalZaloInternbotGroup `json:"items"`
		Total  int                          `json:"total"`
	}
	if err := json.Unmarshal(list.Body.Bytes(), &result); err != nil || result.Status != "ready" || result.Total != 1 || len(result.Items) != 1 || result.Items[0].ID != group.ThreadID {
		t.Fatalf("unexpected group search result: %s (%v)", list.Body, err)
	}
	var count int64
	if err := database.Model(&models.PersonalZaloThreadApproval{}).Where("channel_id = ?", channelID).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("Internbot snapshot modified Chatbot approvals: %d, %v", count, err)
	}
	update(`{"supervisor_group_id":"444","internbot_enabled":true}`, http.StatusConflict)
	update(`{"supervisor_group_id":"222","internbot_enabled":true}`, http.StatusOK)
	active := load()
	if !active.Enabled || active.SupervisorAccountID != account || active.ActivatedAt.IsZero() || active.Version == "" {
		t.Fatalf("valid activation was not bound to the account and config version: %+v", active)
	}
	if present, err := personalZaloInternbotGroupPresent(context.Background(), channel, group.ThreadID); err != nil || !present {
		t.Fatalf("selected group missing: %v, %v", present, err)
	}
	push(setRoster(0, other, friend))
	if config := load(); config.Enabled || config.Version == active.Version {
		t.Fatalf("removed supervisor did not invalidate configuration: %+v", config)
	}
	push(setRoster(time.Second, group, other))
	if config := load(); config.Enabled {
		t.Fatalf("rejoining a removed supervisor automatically enabled Internbot: %+v", config)
	}
	update(`{"chatbot_mode":"chatbot"}`, http.StatusOK)
	call(http.MethodPut, path, tenant, "owner", gin.H{"auto_reply_enabled": true}, http.StatusOK)
	if err := database.Model(&models.PersonalZaloThreadApproval{}).Where("channel_id = ? AND status = ?", channelID, personalZaloApproved).Count(&count).Error; err != nil || count != 2 {
		t.Fatalf("first Chatbot baseline did not approve known groups: %d, %v", count, err)
	}
	if err := database.Where("id = ?", channelID).Delete(&models.Channel{}).Error; err != nil {
		t.Fatalf("roster foreign key blocked channel deletion: %v", err)
	}
	if err := database.Model(&models.PersonalZaloInternbotRoster{}).Where("channel_id = ?", channelID).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("channel deletion left detached Internbot roster rows: %d, %v", count, err)
	}
}
