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

// A turn signed for a specialist agent with knowledge tags searches only the
// documents carrying one of them; a turn without an agent searches them all.
func TestSearchKnowledgeAgentTagsMySQL(t *testing.T) {
	dsn := os.Getenv("CQA_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("CQA_TEST_MYSQL_DSN not set")
	}
	t.Setenv("JWT_SECRET", stockFixtureSigningKey)
	t.Setenv("ENCRYPTION_KEY", stockFixtureSigningKey)
	t.Setenv("DB_PASSWORD", "fixture")
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
	db.DB.Create(&models.AppSetting{ID: pkg.NewUUID(), TenantID: tenant, SettingKey: "ai_agent_erp_token_public", ValuePlain: "pub-token", CreatedAt: now, UpdatedAt: now})
	svc := knowledge.NewService(nil, nil)
	knowledge.SetDefault(svc)
	debt, _ := svc.CreateDocument(knowledge.NewDocumentInput{TenantID: tenant, Title: "Công nợ", Tags: []string{"công nợ"}, Text: "Hạn thanh toán công nợ 30 ngày."})
	other, _ := svc.CreateDocument(knowledge.NewDocumentInput{TenantID: tenant, Title: "Khuyến mãi", Tags: []string{"marketing"}, Text: "Thanh toán sớm trong 30 ngày được tặng quà."})
	for i := 0; i < 100; i++ {
		var n int64
		db.DB.Model(&models.KnowledgeDocument{}).Where("id IN ? AND status = ?", []string{debt.ID, other.ID}, knowledge.StatusReady).Count(&n)
		if n == 2 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	sign := func(agentKey string, tags []string) string {
		token, err := engine.SignPermissionToken(engine.GroupPermissionContext{
			TenantID: tenant, AgentType: "public", ZaloUserID: "fixture-zalo",
			AgentKey: agentKey, AgentTools: []string{knowledgeMCPToolName}, KnowledgeTags: tags,
		}, stockFixtureSigningKey)
		if err != nil {
			t.Fatal(err)
		}
		return token
	}
	gin.SetMode(gin.TestMode)
	search := func(permission string) string {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Params = gin.Params{{Key: "tenantId", Value: tenant}}
		c.Request = httptest.NewRequest(http.MethodPost, "/mcp/erp/"+tenant, strings.NewReader(
			`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"search_knowledge","arguments":{"query":"thanh toán 30 ngày"}}}`))
		c.Request.Header.Set("Content-Type", "application/json")
		c.Request.Header.Set("X-Agent-Token", "pub-token")
		c.Request.Header.Set("X-Permission-Token", permission)
		ERPMCP(c)
		var out struct {
			Result struct {
				Content []struct{ Text string }
			}
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || len(out.Result.Content) == 0 {
			t.Fatalf("bad response %s", rec.Body.String())
		}
		return out.Result.Content[0].Text
	}

	if got := search(sign("cong-no", []string{"Công nợ"})); !strings.Contains(got, "Hạn thanh toán") || strings.Contains(got, "tặng quà") {
		t.Fatalf("debt agent result: %s", got)
	}
	if got := search(sign("cong-no", nil)); !strings.Contains(got, "Hạn thanh toán") || !strings.Contains(got, "tặng quà") {
		t.Fatalf("agent without tags must search every document: %s", got)
	}
	// Tags without an agent key are not an agent turn: no filter.
	if got := search(sign("", []string{"marketing"})); !strings.Contains(got, "Hạn thanh toán") {
		t.Fatalf("legacy turn was filtered: %s", got)
	}
	// A token for another tenant does not apply its tags here.
	foreign, _ := engine.SignPermissionToken(engine.GroupPermissionContext{
		TenantID: "other", AgentType: "public", AgentKey: "x", AgentTools: []string{knowledgeMCPToolName}, KnowledgeTags: []string{"marketing"},
	}, stockFixtureSigningKey)
	if got := search(foreign); !strings.Contains(got, "Hạn thanh toán") {
		t.Fatalf("foreign token filtered the search: %s", got)
	}
}
