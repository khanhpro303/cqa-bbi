package handlers

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/vietbui/chat-quality-agent/config"
	"github.com/vietbui/chat-quality-agent/db/models"
	"github.com/vietbui/chat-quality-agent/engine"
)

func TestMySQLNineRouterSettingsKeepCredentialScope(t *testing.T) {
	database := facebookOAuthTestDatabase(t)
	if err := database.AutoMigrate(&models.AppSetting{}, &models.ActivityLog{}); err != nil {
		t.Fatal(err)
	}
	createFacebookOAuthTenant(t, database, "tenant-router")
	t.Setenv("JWT_SECRET", "0123456789abcdef0123456789abcdef")
	t.Setenv("ENCRYPTION_KEY", "0123456789abcdef0123456789abcdef")
	t.Setenv("DB_PASSWORD", "test-password")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer router-key" {
			t.Error("9router request used the wrong endpoint or credentials")
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"{\"ok\":true}"}}]}`)
	}))
	defer server.Close()
	baseURL := server.URL + "/v1"

	router := gin.New()
	router.Use(func(c *gin.Context) { c.Set("tenant_id", "tenant-router") })
	router.PUT("/ai", SaveAISettings)
	router.PUT("/ai/tasks", SaveAITaskModels)
	router.POST("/ai/test", TestAIKey)
	request := func(method, path, body string, status int, responseText string) {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != status || !strings.Contains(rec.Body.String(), responseText) {
			t.Fatalf("%s %s = %d %s, want %d with %q", method, path, rec.Code, rec.Body, status, responseText)
		}
	}
	settings := func(key, endpoint string, credentialsOnly bool) string {
		body, err := json.Marshal(gin.H{
			"provider": "9router", "api_key": key, "model": "cx/gpt-5.6-luna", "base_url": endpoint, "credentials_only": credentialsOnly,
		})
		if err != nil {
			t.Fatal(err)
		}
		return string(body)
	}

	request(http.MethodPut, "/ai", `{"provider":"openai","api_key":"openai-key","model":"gpt-5-mini"}`, http.StatusOK, "saved")
	request(http.MethodPut, "/ai", settings("router-key", baseURL, true), http.StatusOK, "saved")
	request(http.MethodPut, "/ai/tasks", `{"tasks":[{"key":"qc_analysis","provider":"9router","model":"cx/gpt-5.6-luna"}]}`, http.StatusOK, `"default_provider":"openai"`)
	request(http.MethodPost, "/ai/test", settings("••••••••", baseURL+"/", false), http.StatusOK, "API key works")
	request(http.MethodPost, "/ai/test", settings("••••••••", "https://another.example.com/v1", false), http.StatusBadRequest, "9router_endpoint_changed")
	request(http.MethodPut, "/ai", settings("••••••••", "https://another.example.com/v1", false), http.StatusBadRequest, "9router_endpoint_changed")
	request(http.MethodPut, "/ai", settings("••••••••", baseURL, false), http.StatusOK, "saved")
	request(http.MethodPut, "/ai", `{"provider":"9router","model":"cx/gpt-5.6-luna-next","model_only":true}`, http.StatusOK, "saved")
	request(http.MethodPut, "/ai", `{"provider":"openai","model":" ","model_only":true}`, http.StatusBadRequest, "model_required")
	request(http.MethodPut, "/ai", `{"provider":"gemini","model":"gemini-2.5-flash","model_only":true}`, http.StatusBadRequest, "no_api_key_configured")

	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := engine.ResolveTenantAIProvider("tenant-router", cfg, engine.AITaskClassification)
	if err != nil || resolved.Provider != "9router" || resolved.Model != "cx/gpt-5.6-luna-next" || resolved.Client == nil {
		t.Fatalf("resolve inherited 9router configuration: %s, %v", resolved.Provider, err)
	}
	if response, err := resolved.Client.AnalyzeChat(context.Background(), "Return JSON.", "Ping"); err != nil || response.Provider != "9router" {
		t.Fatalf("analyze using saved 9router configuration: %s, %v", response.Provider, err)
	}
	request(http.MethodPut, "/ai", `{"provider":"openai","api_key":"••••••••","model":"gpt-5-mini"}`, http.StatusOK, "saved")
	request(http.MethodPut, "/ai", `{"provider":"openai","api_key":"rotated-test-key","base_url":"https://rotated.example.com/v1","credentials_only":true}`, http.StatusOK, "saved")
	request(http.MethodPut, "/ai", `{"provider":"openai","model":"gpt-5-nano","model_only":true}`, http.StatusOK, "saved")
	for _, key := range []string{"ai_base_url", "ai_base_url_openai"} {
		if got := getPlainSetting("tenant-router", key); got != "https://rotated.example.com/v1" {
			t.Fatalf("model-only save changed %s to %q", key, got)
		}
	}
}
