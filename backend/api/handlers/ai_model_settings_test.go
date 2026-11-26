package handlers

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/vietbui/chat-quality-agent/api/middleware"
	"github.com/vietbui/chat-quality-agent/config"
	"github.com/vietbui/chat-quality-agent/db/models"
	"github.com/vietbui/chat-quality-agent/engine"
)

func TestAITaskModelChangesValidateBeforeSaving(t *testing.T) {
	tests := []struct {
		name   string
		values []engine.AITaskModelSetting
		valid  bool
	}{
		{"clear override", []engine.AITaskModelSetting{{Key: engine.AITaskQC}}, true},
		{"valid custom model", []engine.AITaskModelSetting{{Key: engine.PromptImageReader, AITaskModel: engine.AITaskModel{Provider: " OpenRouter ", Model: "vendor/model:free"}}}, true},
		{"unknown task", []engine.AITaskModelSetting{{Key: "ai_api_key"}}, false},
		{"duplicate task", []engine.AITaskModelSetting{{Key: engine.AITaskQC}, {Key: engine.AITaskQC}}, false},
		{"provider without model", []engine.AITaskModelSetting{{Key: engine.AITaskQC, AITaskModel: engine.AITaskModel{Provider: "openai"}}}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := validateAITaskModelChanges(tt.values)
			if (err == nil) != tt.valid {
				t.Fatalf("validation error = %v, want valid = %v", err, tt.valid)
			}
		})
	}
}

func TestDescribeAIFlowRestrictsSharedTenantsAndDetectsSharedRoles(t *testing.T) {
	const id = "3d84ec54-8bba-43fd-a5ce-086b14cec342"
	config := engine.LangflowConfig{
		APIURL: "https://private-langflow.example", APIURLSource: engine.LangflowSourceTenant,
		APIKeySource: engine.LangflowSourceTenant, FlowID: id, FlowIDSource: engine.LangflowSourceTenant,
	}
	view := describeAIFlow(config, nil, "public")
	if !view.Managed || view.FlowID != id || !slices.Equal(view.SharedRoles, []string{"internal", "public"}) {
		t.Fatalf("public fallback role = %+v", view)
	}
	view = describeAIFlow(config, []engine.LangflowConfig{{APIURL: "http://langflow:7860", PublicFlowID: id}}, "internal")
	if view.Managed || view.ManagementProblem == "" {
		t.Fatalf("cross-tenant shared flow must be read-only: %+v", view)
	}
	config.APIKeySource = engine.LangflowSourceEnv
	view = describeAIFlow(config, nil, "internal")
	if view.Managed || view.ManagementProblem == "" {
		t.Fatalf("environment key must make the flow read-only: %+v", view)
	}
}

func TestMySQLAITaskModelsUseScopedProviderCredentials(t *testing.T) {
	database := facebookOAuthTestDatabase(t)
	if err := database.AutoMigrate(&models.AppSetting{}, &models.ActivityLog{}); err != nil {
		t.Fatal(err)
	}
	createFacebookOAuthTenant(t, database, "tenant-a")
	createFacebookOAuthTenant(t, database, "tenant-b")
	t.Setenv("JWT_SECRET", "0123456789abcdef0123456789abcdef")
	t.Setenv("ENCRYPTION_KEY", "0123456789abcdef0123456789abcdef")
	t.Setenv("DB_PASSWORD", "test-password")
	t.Setenv("CQA_MESSENGER_JOURNEY_ENABLED", "false")
	router := gin.New()
	tenant := router.Group("/tenants/:tenantId", func(c *gin.Context) {
		c.Set("tenant_id", c.Param("tenantId"))
		c.Set("tenant_role", "admin")
	})
	tenant.PUT("/settings/ai", middleware.RequirePermission("settings", "w"), SaveAISettings)
	tenant.PUT("/settings/ai/tasks", middleware.RequirePermission("settings", "w"), SaveAITaskModels)
	save := func(tenantID, path, jsonBody string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPut, "/tenants/"+tenantID+"/settings/ai"+path, strings.NewReader(jsonBody))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec
	}

	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct{ Model string }
		if r.URL.Path != "/chat/completions" || r.Header.Get("Authorization") != "Bearer test-proxy-key" ||
			json.NewDecoder(r.Body).Decode(&body) != nil || body.Model != "vendor/test-model" {
			t.Errorf("task called the wrong model or endpoint")
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"ok"}}],"usage":{"prompt_tokens":10,"completion_tokens":4,"cost":0.002}}`)
	}))
	defer proxy.Close()

	if rec := save("tenant-a", "", `{"provider":"openai","api_key":"test-default-key","model":"gpt-5-mini"}`); rec.Code != http.StatusOK {
		t.Fatalf("save default AI settings: %d %s", rec.Code, rec.Body)
	}
	options := `{"provider":"openrouter","api_key":"test-proxy-key","base_url":"` + proxy.URL + `","credentials_only":true}`
	if rec := save("tenant-a", "", options); rec.Code != http.StatusOK {
		t.Fatalf("save provider credentials: %d %s", rec.Code, rec.Body)
	}
	override := `{"tasks":[{"key":"qc_analysis","provider":"openrouter","model":"vendor/test-model"}]}`
	if rec := save("tenant-a", "/tasks", override); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"default_model":"gpt-5-mini"`) {
		t.Fatalf("save task override: %d %s", rec.Code, rec.Body)
	}
	if rec := save("tenant-b", "/tasks", override); rec.Code != http.StatusBadRequest {
		t.Fatalf("tenant-b cannot reuse tenant-a provider credentials: %d %s", rec.Code, rec.Body)
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := engine.ResolveTenantAIProvider("tenant-a", cfg, engine.AITaskQC)
	if err != nil || resolved.Client == nil || resolved.Provider != "openrouter" || resolved.Model != "vendor/test-model" {
		t.Fatalf("resolve task override = %s/%s: %v", resolved.Provider, resolved.Model, err)
	}
	reply, err := resolved.Client.AnalyzeChat(context.Background(), "system", "prompt")
	if err != nil || reply.UsageCostUSD() != 0.002 {
		t.Fatalf("proxy reply cost = %v: %v", reply.UsageCostUSD(), err)
	}
	clear := `{"tasks":[{"key":"qc_analysis","provider":"","model":""}]}`
	if rec := save("tenant-a", "/tasks", clear); rec.Code != http.StatusOK {
		t.Fatalf("clear task override: %d %s", rec.Code, rec.Body)
	}
	resolved, err = engine.ResolveTenantAIProvider("tenant-a", cfg, engine.AITaskQC)
	if err != nil || resolved.Provider != "openai" || resolved.Model != "gpt-5-mini" {
		t.Fatalf("resolve inherited model = %s/%s: %v", resolved.Provider, resolved.Model, err)
	}
}
