package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/vietbui/chat-quality-agent/agents"
	"github.com/vietbui/chat-quality-agent/config"
	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/db/models"
	"github.com/vietbui/chat-quality-agent/engine"
	"github.com/vietbui/chat-quality-agent/pkg"
)

func connectAgentsConsoleMySQL(t *testing.T) string {
	t.Helper()
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
	if err := db.DB.Create(&models.Tenant{ID: tenant, Name: "Console fixture", Slug: "console-" + tenant[:8], Settings: "{}", CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for _, m := range []any{&models.AIUsageLog{}, &models.AITeamMember{}, &models.AITeam{}, &models.AIAgentRevision{}, &models.AIAgent{}, &models.AppSetting{}} {
			db.DB.Where("tenant_id = ?", tenant).Delete(m)
		}
		db.DB.Where("id = ?", tenant).Delete(&models.Tenant{})
	})
	return tenant
}

func agentsTestRequest(t *testing.T, handler gin.HandlerFunc, method, target, tenant, role string, body any) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Handle(method, "/x", func(c *gin.Context) {
		c.Set("tenant_id", tenant)
		c.Set("user_id", "user-1")
		c.Set("tenant_role", role)
		handler(c)
	})
	var payload []byte
	if body != nil {
		payload, _ = json.Marshal(body)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(method, "/x"+target, bytes.NewReader(payload)))
	var out map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return w, out
}

// Chatbot spend splits per answering agent: a deleted agent keeps its key,
// AI Engine turns share one bar, job spend is left out.
func TestAICostByAgentMySQL(t *testing.T) {
	tenant := connectAgentsConsoleMySQL(t)
	ctx := context.Background()
	if _, err := agents.SaveAgent(ctx, tenant, "admin", agents.Agent{Key: "cong-no", Name: "Bot công nợ", Audience: agents.AudienceCustomer, IsActive: true}); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	for _, row := range []struct {
		source, agent string
		cost          float64
	}{
		{"chatbot", "cong-no", 0.5}, {"chatbot", "cong-no", 0.25}, {"chatbot", "da-xoa", 0.1}, {"chatbot", "", 0.2}, {"job", "", 9},
	} {
		if err := db.DB.Create(&models.AIUsageLog{ID: pkg.NewUUID(), TenantID: tenant, Source: row.source, AgentKey: row.agent,
			CostUSD: row.cost, InputTokens: 10, OutputTokens: 1, CreatedAt: now}).Error; err != nil {
			t.Fatal(err)
		}
	}
	w, out := agentsTestRequest(t, GetAICostAnalytics, http.MethodGet, "?group_by=agent", tenant, "owner", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	rows, _ := out["rows"].([]any)
	got := map[string]float64{}
	for _, r := range rows {
		m := r.(map[string]any)
		got[m["label"].(string)] = m["total_cost"].(float64)
	}
	want := map[string]float64{"Bot công nợ": 0.75, "da-xoa": 0.1, legacyAgentCostLabel: 0.2}
	if len(got) != len(want) {
		t.Fatalf("rows = %v", got)
	}
	for label, cost := range want {
		if d := got[label] - cost; d > 1e-9 || d < -1e-9 {
			t.Errorf("%s = %v, want %v (rows %v)", label, got[label], cost, got)
		}
	}
	first := rows[0].(map[string]any)["label"]
	if first != "Bot công nợ" {
		t.Errorf("rows not ordered by cost: %v", rows)
	}
}

// The test console runs a message through the team's coordinator and lets
// the chosen agent answer with a dry-run scope that carries no ERP groups.
func TestAIAgentTestConsoleMySQL(t *testing.T) {
	tenant := connectAgentsConsoleMySQL(t)
	ctx := context.Background()
	stock, err := agents.SaveAgent(ctx, tenant, "admin", agents.Agent{Key: "kho", Name: "Bot kho", Description: "Tồn kho",
		Audience: agents.AudienceCustomer, SystemPrompt: "Bạn là bot kho.", Tools: []string{"inventory_by_sku"}, IsActive: true})
	if err != nil {
		t.Fatal(err)
	}
	// A native agent can only be saved while the runtime is installed.
	runtime := &consoleRuntime{text: "Dạ anh còn nợ 2 triệu ạ."}
	agents.RegisterRuntime(agents.RuntimeNative, runtime)
	t.Cleanup(func() { agents.RegisterRuntime(agents.RuntimeNative, nil) })
	debt, err := agents.SaveAgent(ctx, tenant, "admin", agents.Agent{Key: "cong-no", Name: "Bot công nợ", Description: "Công nợ",
		Audience: agents.AudienceCustomer, Runtime: agents.RuntimeNative, Provider: "openai", Model: "gpt-5-mini",
		SystemPrompt: "Bạn là bot công nợ.", Tools: []string{"lookup_debt", "search_knowledge"}, KnowledgeTags: []string{"công nợ"}, IsActive: true})
	if err != nil {
		t.Fatal(err)
	}
	team, err := agents.SaveTeam(ctx, tenant, "admin", agents.TeamInput{Name: "Bán hàng", PersonaPrompt: "Xưng em.", DefaultAgentID: stock.ID,
		Members: []agents.MemberInput{{AgentID: stock.ID, Enabled: true}, {AgentID: debt.ID, Position: 1, Enabled: true}}})
	if err != nil {
		t.Fatal(err)
	}

	var asked string
	previous := agentRouterAsk
	agentRouterAsk = func(*config.Config, string) agents.AskFunc {
		return func(_ context.Context, systemPrompt, input string) (string, error) {
			asked = input
			return `{"agent":"cong-no","reason":"khách hỏi công nợ"}`, nil
		}
	}
	t.Cleanup(func() { agentRouterAsk = previous })
	handler := NewAIAgentTestConsole(&config.Config{EncryptionKey: stockFixtureSigningKey})

	w, out := agentsTestRequest(t, handler, http.MethodPost, "", tenant, "member",
		map[string]any{"team_id": team.ID, "audience": "customer", "message": "em còn nợ bao nhiêu?"})
	agent, _ := out["agent"].(map[string]any)
	if w.Code != http.StatusOK || agent["key"] != "cong-no" || out["reason"] != agents.ReasonRouted ||
		out["router_reason"] != "khách hỏi công nợ" || out["routed"] != true || asked != "<tin_gan_day>\n</tin_gan_day>\nTin mới: em còn nợ bao nhiêu?" {
		t.Fatalf("routing = %d %v (asked %q)", w.Code, out, asked)
	}
	if _, ok := out["reply"]; ok || len(runtime.inputs) != 0 {
		t.Fatal("routing only must not run the agent")
	}

	w, out = agentsTestRequest(t, handler, http.MethodPost, "", tenant, "member",
		map[string]any{"team_id": team.ID, "audience": "customer", "message": "em còn nợ bao nhiêu?", "current_agent": "kho", "reply": true})
	if w.Code != http.StatusOK || out["reply"] != runtime.text || out["runtime"] != "native" || out["session_id"] == "" || len(runtime.inputs) != 1 {
		t.Fatalf("reply = %d %v", w.Code, out)
	}
	in := runtime.inputs[0]
	if in.Agent.Key != "cong-no" || in.Tier != agents.TierPublic || !strings.HasPrefix(in.SystemPrompt, "Xưng em.\n\nBạn là bot công nợ.") {
		t.Errorf("console run = %+v", in)
	}
	scope, err := engine.VerifyPermissionToken(in.PermissionToken, stockFixtureSigningKey)
	if err != nil || !scope.DryRun || scope.AgentKey != "cong-no" || len(scope.Groups) != 0 || scope.CustomerCode != "" ||
		!scope.ToolAllowed("lookup_debt") || scope.ToolAllowed("inventory_by_sku") || len(scope.KnowledgeTags) != 1 {
		t.Errorf("console scope = %+v %v", scope, err)
	}

	// Staff agents read staff knowledge: owners and admins only.
	if w, _ := agentsTestRequest(t, handler, http.MethodPost, "", tenant, "member",
		map[string]any{"team_id": team.ID, "audience": "staff", "message": "x"}); w.Code != http.StatusForbidden {
		t.Errorf("member staff test = %d", w.Code)
	}
	// The team has no staff agent: the console says the chat stays with people.
	if w, out := agentsTestRequest(t, handler, http.MethodPost, "", tenant, "owner",
		map[string]any{"team_id": team.ID, "audience": "staff", "message": "x"}); w.Code != http.StatusOK || out["silent"] != true {
		t.Errorf("staff test on a customer team = %d %v", w.Code, out)
	}
	if w, _ := agentsTestRequest(t, handler, http.MethodPost, "", tenant, "owner",
		map[string]any{"team_id": team.ID, "audience": "customer", "message": "x", "current_agent": "khong-co"}); w.Code != http.StatusBadRequest {
		t.Errorf("unknown current agent = %d", w.Code)
	}
	other := pkg.NewUUID()
	if w, _ := agentsTestRequest(t, handler, http.MethodPost, "", other, "owner",
		map[string]any{"team_id": team.ID, "audience": "customer", "message": "x"}); w.Code != http.StatusNotFound {
		t.Errorf("cross-tenant team = %d", w.Code)
	}
}

type consoleRuntime struct {
	text   string
	inputs []agents.RunInput
}

func (r *consoleRuntime) Run(_ context.Context, in agents.RunInput) (agents.RunResult, error) {
	r.inputs = append(r.inputs, in)
	return agents.RunResult{Text: r.text, Usage: agents.Usage{Provider: "openai", Model: "gpt-5-mini", InputTokens: 12, OutputTokens: 4, HasUsage: true}}, nil
}
