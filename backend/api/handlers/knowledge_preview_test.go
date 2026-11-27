package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/vietbui/chat-quality-agent/config"
	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/db/models"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

const knowledgePreviewTestKey = "01234567890123456789012345678901"

func TestKnowledgePreviewTokenScopesTenantAndAgent(t *testing.T) {
	token, err := signKnowledgePreviewToken(knowledgePreviewTestKey, "tenant-a", "private")
	if err != nil {
		t.Fatal(err)
	}
	if got := resolveKnowledgePreviewAgent(knowledgePreviewTestKey, "tenant-a", token); got != "private" {
		t.Fatalf("resolve signed preview token = %q, want private", got)
	}
	if got := resolveKnowledgePreviewAgent(knowledgePreviewTestKey, "tenant-b", token); got != "" {
		t.Fatalf("cross-tenant preview token resolved as %q", got)
	}
	if got := resolveKnowledgePreviewAgent(strings.Repeat("x", 32), "tenant-a", token); got != "" {
		t.Fatalf("token signed by another key resolved as %q", got)
	}

	invalidAgent, err := signKnowledgePreviewToken(knowledgePreviewTestKey, "tenant-a", "admin")
	if err != nil {
		t.Fatal(err)
	}
	if got := resolveKnowledgePreviewAgent(knowledgePreviewTestKey, "tenant-a", invalidAgent); got != "" {
		t.Fatalf("unsupported preview agent resolved as %q", got)
	}
}

func TestKnowledgePreviewSessionIsScopedAndStable(t *testing.T) {
	const id = "84e7d2e2-f973-40ba-8665-74ac77dd45fd"
	first, err := knowledgePreviewSession("tenant-a", "user-a", "public", id)
	if err != nil {
		t.Fatal(err)
	}
	second, err := knowledgePreviewSession("tenant-a", "user-a", "public", id)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("same preview scope produced different sessions: %#v %#v", first, second)
	}
	otherUser, _ := knowledgePreviewSession("tenant-a", "user-b", "public", id)
	otherAgent, _ := knowledgePreviewSession("tenant-a", "user-a", "private", id)
	if first.flowSessionID == otherUser.flowSessionID || first.flowSessionID == otherAgent.flowSessionID {
		t.Fatal("preview flow session was reused across user or agent scope")
	}
	if _, err := knowledgePreviewSession("tenant-a", "user-a", "public", "not-a-uuid"); err == nil {
		t.Fatal("invalid client session ID was accepted")
	}
}

func TestKnowledgePreviewMCPExposesOnlyKnowledgeSearch(t *testing.T) {
	gin.SetMode(gin.TestMode)
	previousDB := db.DB
	db.DB = nil
	t.Cleanup(func() { db.DB = previousDB })
	t.Setenv("JWT_SECRET", knowledgePreviewTestKey)
	t.Setenv("ENCRYPTION_KEY", knowledgePreviewTestKey)
	t.Setenv("DB_PASSWORD", "fixture")

	token, err := signKnowledgePreviewToken(knowledgePreviewTestKey, "tenant-a", "public")
	if err != nil {
		t.Fatal(err)
	}

	list := callKnowledgePreviewMCP(t, "tenant-a", token, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	result, ok := list["result"].(map[string]any)
	if !ok {
		t.Fatalf("tools/list missing result: %v", list)
	}
	tools, ok := result["tools"].([]any)
	if !ok || len(tools) != 1 {
		t.Fatalf("preview tools/list = %v, want exactly one tool", result["tools"])
	}
	tool, _ := tools[0].(map[string]any)
	if tool["name"] != knowledgeMCPToolName {
		t.Fatalf("preview tool = %v, want %s", tool["name"], knowledgeMCPToolName)
	}

	for _, name := range []string{"search_products", "search_product_variants", "inventory_by_sku", "inventory_by_line", "lookup_order", "lookup_debt", "order_cart_checkout", "complaint_record"} {
		blocked := callKnowledgePreviewMCP(t, "tenant-a", token, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"`+name+`","arguments":{}}}`)
		errorBody, ok := blocked["error"].(map[string]any)
		if !ok || errorBody["code"] != float64(-32602) || errorBody["message"] != "Tool unavailable in knowledge preview" {
			t.Fatalf("knowledge-only credential called %s: %v", name, blocked)
		}
	}
}

func TestKnowledgePreviewMCPRejectsTokenForAnotherTenant(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("JWT_SECRET", knowledgePreviewTestKey)
	t.Setenv("ENCRYPTION_KEY", knowledgePreviewTestKey)
	t.Setenv("DB_PASSWORD", "fixture")

	token, err := signKnowledgePreviewToken(knowledgePreviewTestKey, "tenant-a", "public")
	if err != nil {
		t.Fatal(err)
	}
	got := callKnowledgePreviewMCP(t, "tenant-b", token, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	if got["error"] != "unauthorized" {
		t.Fatalf("cross-tenant preview MCP response = %v, want unauthorized", got)
	}
}

func TestKnowledgeChatPreviewRunsConfiguredPublicFlowWithPreviewCredential(t *testing.T) {
	gin.SetMode(gin.TestMode)
	const imageID = "cea6c2f1-40bc-4c42-a256-e2f676d24e9d"
	database, err := gorm.Open(mysql.New(mysql.Config{
		DSN:                       "fixture@tcp(127.0.0.1:3306)/fixture",
		SkipInitializeWithVersion: true,
	}), &gorm.Config{DryRun: true, DisableAutomaticPing: true, SkipDefaultTransaction: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := database.Callback().Query().After("gorm:query").Register("fixture:preview", func(q *gorm.DB) {
		switch rows := q.Statement.Dest.(type) {
		case *[]models.AppSetting:
			if q.Statement.Vars[0] == "tenant-a" {
				*rows = []models.AppSetting{
					{SettingKey: "ai_engine_langflow_token", ValuePlain: "tenant-key"},
					{SettingKey: "ai_engine_langflow_public_flow_id", ValuePlain: "flow-public"},
				}
			}
		case *[]models.KnowledgeDocumentImage:
			if !strings.Contains(q.Statement.SQL.String(), "d.audience = ?") {
				t.Error("customer preview image lookup lacks audience guard")
			}
			*rows = []models.KnowledgeDocumentImage{{ID: imageID, DocumentID: "doc-a", Caption: "Bảng hướng dẫn", Enabled: true}}
		}
	}); err != nil {
		t.Fatal(err)
	}
	previousDB := db.DB
	db.DB = database
	t.Cleanup(func() {
		db.DB = previousDB
		pool, _ := database.DB()
		_ = pool.Close()
	})

	type langflowRequest struct {
		Path            string
		APIKey          string
		AgentToken      string
		PermissionToken string
		Payload         map[string]any
	}
	received := make(chan langflowRequest, 1)
	redirected := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/redirect-target" {
			redirected <- struct{}{}
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		var payload map[string]any
		_ = json.NewDecoder(r.Body).Decode(&payload)
		if input, _ := payload["input_value"].(string); strings.HasPrefix(input, "redirect\n") {
			http.Redirect(w, r, "/redirect-target", http.StatusTemporaryRedirect)
			return
		}
		received <- langflowRequest{
			Path:            r.URL.Path,
			APIKey:          r.Header.Get("x-api-key"),
			AgentToken:      r.Header.Get("X-Langflow-Global-Var-CQA_ERP_AGENT_TOKEN"),
			PermissionToken: r.Header.Get("X-Langflow-Global-Var-ERP_PERMISSION_TOKEN"),
			Payload:         payload,
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"outputs":[{"outputs":[{"results":{"message":{"text":"EGO là thương hiệu mũ bảo hiểm.\n[KB_IMAGE:` + imageID + `]"}}}]}]}`))
	}))
	t.Cleanup(server.Close)

	cfg := &config.Config{
		EncryptionKey:        knowledgePreviewTestKey,
		LangflowAPIURL:       server.URL,
		LangflowAPIKey:       "deployment-key",
		LangflowPublicFlowID: "deployment-public",
		LangflowFlowID:       "deployment-private",
	}
	handler := NewKnowledgeChatPreview(cfg)
	call := func(tenant, query string) *httptest.ResponseRecorder {
		recorder := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(recorder)
		ctx.Set("tenant_id", tenant)
		ctx.Set("user_id", "user-a")
		ctx.Set("tenant_role", "member")
		payload, _ := json.Marshal(map[string]string{"question": query, "agent_type": "public"})
		ctx.Request = httptest.NewRequest(http.MethodPost, "/knowledge/chat-preview", strings.NewReader(string(payload)))
		ctx.Request.Header.Set("Content-Type", "application/json")
		handler(ctx)
		return recorder
	}
	recorder := call("tenant-a", "EGO là brand gì?")

	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), "EGO là thương hiệu") ||
		strings.Contains(recorder.Body.String(), "[KB_IMAGE:") || !strings.Contains(recorder.Body.String(), "/knowledge/doc-a/images/"+imageID+"/content") {
		t.Fatalf("preview response status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	request := <-received
	if request.Path != "/api/v1/run/flow-public" {
		t.Fatalf("Langflow path = %q", request.Path)
	}
	if request.APIKey != "tenant-key" {
		t.Fatalf("Langflow did not receive the configured tenant key")
	}
	if request.PermissionToken != "" {
		t.Fatalf("preview sent ERP permission token %q", request.PermissionToken)
	}
	if got := resolveKnowledgePreviewAgent(knowledgePreviewTestKey, "tenant-a", request.AgentToken); got != "public" {
		t.Fatalf("Langflow preview agent token resolved as %q", got)
	}
	if input, _ := request.Payload["input_value"].(string); !strings.HasPrefix(input, "EGO là brand gì?\n\n") || !strings.Contains(input, "search_knowledge") {
		t.Fatalf("Langflow input = %v", request.Payload["input_value"])
	}
	sessionID, _ := request.Payload["session_id"].(string)
	if !strings.HasPrefix(sessionID, "kb-preview-") {
		t.Fatalf("Langflow session = %q", sessionID)
	}
	if got := call("tenant-b", "EGO là brand gì?"); got.Code != http.StatusServiceUnavailable {
		t.Fatalf("unconfigured tenant used deployment credentials: status=%d body=%s", got.Code, got.Body.String())
	}
	if got := call("tenant-a", "redirect"); got.Code != http.StatusBadGateway {
		t.Fatalf("Langflow redirect status=%d body=%s", got.Code, got.Body.String())
	}
	select {
	case <-redirected:
		t.Fatal("preview followed a Langflow API redirect")
	default:
	}
}

func callKnowledgePreviewMCP(t *testing.T, tenantID, token, body string) map[string]any {
	t.Helper()
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Params = gin.Params{{Key: "tenantId", Value: tenantID}}
	ctx.Request = httptest.NewRequest(http.MethodPost, "/mcp/erp/"+tenantID, strings.NewReader(body))
	ctx.Request.Header.Set("Content-Type", "application/json")
	ctx.Request.Header.Set("X-Agent-Token", token)
	ERPMCP(ctx)

	var response map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode MCP response %q: %v", recorder.Body.String(), err)
	}
	return response
}
