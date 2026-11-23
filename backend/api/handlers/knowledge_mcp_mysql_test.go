package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/db/models"
	"github.com/vietbui/chat-quality-agent/engine"
	"github.com/vietbui/chat-quality-agent/knowledge"
	"github.com/vietbui/chat-quality-agent/pkg"
)

// CQA_TEST_MYSQL_DSN='root:root@tcp(127.0.0.1:3307)/cqa?parseTime=true&loc=UTC&charset=utf8mb4'
func TestSearchKnowledgeMCPMySQL(t *testing.T) {
	dsn := os.Getenv("CQA_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("CQA_TEST_MYSQL_DSN not set")
	}
	saved := db.DB
	if err := db.Connect(dsn, false); err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.DB = saved })

	tenant := pkg.NewUUID()
	now := time.Now()
	for agent, tok := range map[string]string{"public": "pub-token", "private": "priv-token"} {
		db.DB.Create(&models.AppSetting{ID: pkg.NewUUID(), TenantID: tenant, SettingKey: "ai_agent_erp_token_" + agent, ValuePlain: tok, CreatedAt: now, UpdatedAt: now})
	}
	db.DB.Create(&models.AppSetting{ID: pkg.NewUUID(), TenantID: tenant, SettingKey: "ai_prompt_" + engine.PromptKnowledgeTool, ValuePlain: "Mô tả do tenant sửa", CreatedAt: now, UpdatedAt: now})

	svc := knowledge.NewService(nil, nil) // keyword search
	knowledge.SetDefault(svc)
	pub, _ := svc.CreateDocument(knowledge.NewDocumentInput{TenantID: tenant, Title: "Đổi trả", Text: "Đổi trả trong 7 ngày nếu còn tem."})
	internal, _ := svc.CreateDocument(knowledge.NewDocumentInput{TenantID: tenant, Title: "Giá vốn", Audience: "internal", Text: "Giá vốn đổi trả nội bộ 7 ngày."})
	for i := 0; i < 100; i++ {
		var n int64
		db.DB.Model(&models.KnowledgeDocument{}).Where("id IN ? AND status = ?", []string{pub.ID, internal.ID}, knowledge.StatusReady).Count(&n)
		if n == 2 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	gin.SetMode(gin.TestMode)
	call := func(token, body string) map[string]any {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Params = gin.Params{{Key: "tenantId", Value: tenant}}
		c.Request = httptest.NewRequest(http.MethodPost, "/mcp/erp/"+tenant, strings.NewReader(body))
		c.Request.Header.Set("Content-Type", "application/json")
		c.Request.Header.Set("X-Agent-Token", token)
		ERPMCP(c)
		var out map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return out
	}

	list := call("pub-token", `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	raw, _ := json.Marshal(list)
	if !strings.Contains(string(raw), `"name":"search_knowledge"`) || !strings.Contains(string(raw), "Mô tả do tenant sửa") {
		t.Fatalf("tools/list missing search_knowledge with tenant description: %s", raw)
	}

	text := func(out map[string]any) string {
		res, _ := out["result"].(map[string]any)
		content, _ := res["content"].([]any)
		if len(content) == 0 {
			t.Fatalf("no content: %v", out)
		}
		return content[0].(map[string]any)["text"].(string)
	}
	q := `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"search_knowledge","arguments":{"query":"đổi trả mấy ngày"}}}`
	got := text(call("pub-token", q))
	if !strings.Contains(got, "còn tem") || strings.Contains(got, "Giá vốn") {
		t.Fatalf("customer bot result: %s", got)
	}
	got = text(call("priv-token", q))
	if !strings.Contains(got, "Giá vốn") {
		t.Fatalf("staff bot must see internal doc: %s", got)
	}
	bad := call("pub-token", `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"search_knowledge","arguments":{"query":"x","tenant_id":"other"}}}`)
	if res, _ := bad["result"].(map[string]any); res["isError"] != true {
		t.Fatalf("unknown argument must be rejected: %v", bad)
	}
	if out := call("wrong", q); out["error"] == nil {
		t.Fatalf("bad agent token accepted: %v", out)
	}
}
