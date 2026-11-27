package handlers

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/vietbui/chat-quality-agent/api/middleware"
	"github.com/vietbui/chat-quality-agent/db/models"
	"github.com/vietbui/chat-quality-agent/pkg"
)

func TestPersonalZaloSyncIntervalValidation(t *testing.T) {
	for _, test := range []struct {
		name    string
		value   any
		minutes int
		valid   bool
	}{
		{name: "gateway default", valid: true},
		{name: "one minute", value: float64(1), minutes: 1, valid: true},
		{name: "one day", value: float64(1440), minutes: 1440, valid: true},
		{name: "zero", value: float64(0)},
		{name: "fractional", value: 2.5},
		{name: "too long", value: float64(1441)},
		{name: "string", value: "15"},
	} {
		t.Run(test.name, func(t *testing.T) {
			metadata := map[string]interface{}{"sync_interval": test.value}
			if personalZaloSyncInterval(metadata) != test.minutes || validPersonalZaloSyncInterval(metadata) != test.valid {
				t.Fatalf("unexpected sync interval policy for %v", test.value)
			}
		})
	}
}

func TestPersonalZaloSyncSettingsScopedAndReadOnlyMySQL(t *testing.T) {
	database := facebookOAuthTestDatabase(t)
	createFacebookOAuthTenant(t, database, "tenant")
	const key = "0123456789abcdef0123456789abcdef"
	const importSecret = "personal-zalo-settings-fixture"
	credentials, err := pkg.Encrypt([]byte(`{"import_secret":"`+importSecret+`"}`), key)
	if err != nil {
		t.Fatal(err)
	}
	channel := models.Channel{ID: "personal", TenantID: "tenant", ChannelType: "personal_zalo_import", IsActive: true,
		Name: "Fixture", Metadata: `{"sync_interval":5}`, CredentialsEncrypted: credentials}
	if err := database.Create(&channel).Error; err != nil {
		t.Fatal(err)
	}
	var before models.Channel
	if err := database.First(&before, "id = ?", channel.ID).Error; err != nil {
		t.Fatal(err)
	}

	const path = "/api/internal/imports/personal-zalo/settings"
	router := gin.New()
	router.POST(path, middleware.InternalHMACAuth(middleware.PersonalZaloImportSecretResolver(key, ""), 5*time.Minute), GetPersonalZaloSyncSettings)
	submit := func(tenantID, accountID, requestID, headerID, secret string) *httptest.ResponseRecorder {
		t.Helper()
		body, err := json.Marshal(map[string]string{
			"request_id": requestID, "tenant_id": tenantID, "channel_id": channel.ID, "account_external_id": accountID,
		})
		if err != nil {
			t.Fatal(err)
		}
		stamp := strconv.FormatInt(time.Now().Unix(), 10)
		mac := hmac.New(sha256.New, []byte(secret))
		fmt.Fprintf(mac, "%s.%s.%s", stamp, headerID, body)
		req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-CQA-Request-Id", headerID)
		req.Header.Set("X-CQA-Timestamp", stamp)
		req.Header.Set("X-CQA-Signature", hex.EncodeToString(mac.Sum(nil)))
		response := httptest.NewRecorder()
		router.ServeHTTP(response, req)
		return response
	}
	check := func(response *httptest.ResponseRecorder, status int, body string) {
		t.Helper()
		if response.Code != status || response.Body.String() != body {
			t.Fatalf("settings: got %d %s, want %d %s", response.Code, response.Body, status, body)
		}
	}
	check(submit("tenant", "100", "legacy", "legacy", importSecret), http.StatusOK, `{"active":true,"sync_interval_minutes":5}`)
	var after models.Channel
	if err := database.First(&after, "id = ?", channel.ID).Error; err != nil || after.ExternalID != "" || !after.UpdatedAt.Equal(before.UpdatedAt) {
		t.Fatalf("policy lookup changed channel: account=%q at=%v expected=%v err=%v", after.ExternalID, after.UpdatedAt, before.UpdatedAt, err)
	}
	if err := database.Model(&channel).Updates(map[string]interface{}{
		"external_id": "100", "is_active": false, "metadata": `{"sync_interval":null}`,
	}).Error; err != nil {
		t.Fatal(err)
	}
	check(submit("tenant", "200", "wrong-account", "wrong-account", importSecret), http.StatusConflict, `{"error":"channel_account_mismatch"}`)
	check(submit("tenant", "100", "paused", "paused", importSecret), http.StatusOK, `{"active":false}`)
	check(submit("tenant", "100", "different-signing-id", "headers", importSecret), http.StatusUnauthorized, `{"error":"request_id_mismatch"}`)
	check(submit("other-tenant", "100", "wrong-tenant", "wrong-tenant", importSecret), http.StatusUnauthorized, `{"error":"invalid_internal_target"}`)
	check(submit("tenant", "100", "invalid-signature", "invalid-signature", "wrong-key"), http.StatusUnauthorized, `{"error":"invalid_internal_signature"}`)
}

func TestPersonalZaloSyncIntervalWritesMySQL(t *testing.T) {
	database := facebookOAuthTestDatabase(t)
	createFacebookOAuthTenant(t, database, "tenant")
	channel := models.Channel{ID: "personal", TenantID: "tenant", ChannelType: "personal_zalo_import", IsActive: true,
		Name: "Fixture", Metadata: `{"sync_scope":"direct","sync_interval":"old"}`, CredentialsEncrypted: []byte("fixture")}
	if err := database.Create(&channel).Error; err != nil {
		t.Fatal(err)
	}
	const base = "/channels"
	router := gin.New()
	router.Use(func(c *gin.Context) { c.Set("tenant_id", channel.TenantID) })
	router.POST(base, CreateChannel)
	router.PUT(base+"/:channelId", UpdateChannel)
	submit := func(method, path string, payload map[string]string, status int, expected string) {
		t.Helper()
		body, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(method, path, bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		router.ServeHTTP(response, req)
		if response.Code != status || response.Body.String() != expected {
			t.Fatalf("%s %s: got %d %s, want %d %s", method, path, response.Code, response.Body, status, expected)
		}
	}
	path := base + "/" + channel.ID
	submit(http.MethodPost, base, map[string]string{"name": "New Personal", "channel_type": "personal_zalo_import", "metadata": `{"sync_interval":2.5}`},
		http.StatusBadRequest, `{"error":"invalid_sync_interval"}`)
	submit(http.MethodPut, path, map[string]string{"metadata": `{"sync_interval":0}`}, http.StatusBadRequest, `{"error":"invalid_sync_interval"}`)
	submit(http.MethodPut, path, map[string]string{"metadata": `{"sync_scope":"all"}`}, http.StatusOK, `{"message":"updated"}`)
	submit(http.MethodPut, path, map[string]string{"metadata": `{"sync_interval":30}`}, http.StatusOK, `{"message":"updated"}`)
	var saved models.Channel
	if err := database.First(&saved, "id = ?", channel.ID).Error; err != nil {
		t.Fatal(err)
	}
	metadata, err := decodeMetadataObject(saved.Metadata)
	if err != nil || personalZaloSyncInterval(metadata) != 30 || metadata["sync_scope"] != "all" {
		t.Fatalf("the interval update clobbered settings: %s, err=%v", saved.Metadata, err)
	}
	submit(http.MethodPut, path, map[string]string{"metadata": `{"sync_interval":null}`}, http.StatusOK, `{"message":"updated"}`)
	if err := database.First(&saved, "id = ?", channel.ID).Error; err != nil {
		t.Fatal(err)
	}
	metadata, err = decodeMetadataObject(saved.Metadata)
	if err != nil || metadata["sync_interval"] != nil || metadata["sync_scope"] != "all" {
		t.Fatalf("expected only the interval override to be cleared: %s, err=%v", saved.Metadata, err)
	}
}

func TestPersonalZaloConnectionChecksTheExpectedGatewayAccount(t *testing.T) {
	t.Setenv("JWT_SECRET", "0123456789abcdef0123456789abcdef")
	t.Setenv("ENCRYPTION_KEY", "0123456789abcdef0123456789abcdef")
	t.Setenv("DB_PASSWORD", "fixture")
	setPersonalZaloGatewayAdminSecret(t)
	channel := models.Channel{ID: "channel", ExternalID: "100", TenantID: "tenant"}
	account := personalZaloGatewayAccountRecord{ID: "gateway-account", TenantID: "tenant", ChannelID: "channel", ZaloUID: "100", AccountExternalID: "100", Status: "connected"}
	for _, test := range []struct {
		name        string
		change      func(*personalZaloGatewayAccountRecord)
		unreachable bool
		code        int
		body        string
	}{
		{name: "connected account", code: http.StatusOK, body: `{"message":"connection_successful","status":"ok"}`},
		{name: "account belongs to another tenant", change: func(account *personalZaloGatewayAccountRecord) { account.TenantID = "other" }, code: http.StatusConflict, body: `{"error":"gateway_account_not_found"}`},
		{name: "gateway account disconnected", change: func(account *personalZaloGatewayAccountRecord) { account.Status = "disconnected" }, code: http.StatusConflict, body: `{"error":"gateway_account_not_connected"}`},
		{name: "wrong Zalo UID", change: func(account *personalZaloGatewayAccountRecord) { account.ZaloUID = "200" }, code: http.StatusConflict, body: `{"error":"channel_account_mismatch"}`},
		{name: "gateway unavailable", unreachable: true, code: http.StatusBadGateway, body: `{"error":"gateway_request_failed"}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			gatewayAccount := account
			if test.change != nil {
				test.change(&gatewayAccount)
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.URL.Path != "/api/v1/accounts" || r.Header.Get("Authorization") != "Bearer "+testPersonalZaloGatewayAdminSecret {
					t.Errorf("unexpected gateway check request: %s %s", r.Method, r.URL.Path)
				}
				if test.unreachable {
					w.WriteHeader(http.StatusServiceUnavailable)
					return
				}
				_ = json.NewEncoder(w).Encode(personalZaloGatewayListResponse{Accounts: []personalZaloGatewayAccountRecord{gatewayAccount}})
			}))
			t.Cleanup(server.Close)
			t.Setenv("PERSONAL_ZALO_GATEWAY_BASE_URL", server.URL)
			response := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(response)
			ctx.Request = httptest.NewRequest(http.MethodPost, "/test", nil)
			testPersonalZaloGatewayConnection(ctx, channel)
			if response.Code != test.code || response.Body.String() != test.body {
				t.Fatalf("connection check = %d %s; want %d %s", response.Code, response.Body, test.code, test.body)
			}
		})
	}
}
