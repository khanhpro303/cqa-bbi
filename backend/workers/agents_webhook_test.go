package workers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/vietbui/chat-quality-agent/agents"
	"github.com/vietbui/chat-quality-agent/config"
	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/db/models"
	"github.com/vietbui/chat-quality-agent/engine"
	"github.com/vietbui/chat-quality-agent/pkg"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

// With agents on, a group turn on the OA is answered by the team's customer
// agent: its flow, the team voice plus its prompt, and a token that names it
// and its tools. A team without a customer agent leaves the chat to people.
func TestZaloWebhookAnswersWithAgent(t *testing.T) {
	const (
		key      = "0123456789abcdef0123456789abcdef"
		question = "Bull ơi, giày LS2 Zoe Man size 44 còn hàng không?"
		answer   = "Dạ mẫu này còn hàng ạ."
	)
	database, err := gorm.Open(mysql.New(mysql.Config{
		DSN:                       "fixture@tcp(127.0.0.1:3306)/fixture",
		SkipInitializeWithVersion: true,
	}), &gorm.Config{DryRun: true, DisableAutomaticPing: true, SkipDefaultTransaction: true})
	if err != nil {
		t.Fatal(err)
	}
	pool, err := database.DB()
	if err != nil {
		t.Fatal(err)
	}
	previousDB, previousRedis := db.DB, db.RedisClient
	db.DB, db.RedisClient = database, nil
	t.Cleanup(func() {
		db.DB, db.RedisClient = previousDB, previousRedis
		_ = pool.Close()
	})

	creds, err := pkg.Encrypt([]byte(`{"access_token":"fixture","oa_id":"oa"}`), key)
	if err != nil {
		t.Fatal(err)
	}
	group := models.CRMGroup{ID: "crm", TenantID: "tenant", ChannelID: "channel", ZaloGroupID: "group", CustomerCode: "S084"}
	channel := models.Channel{ID: group.ChannelID, TenantID: group.TenantID, AutoReplyEnabled: true, CredentialsEncrypted: creds,
		Metadata: `{"session_keyword":"Chào Bull","group_staff_call_only":true,"group_bot_names":"Bull"}`}
	team := models.AITeam{ID: "team", TenantID: group.TenantID, Name: "Mặc định", PersonaPrompt: "Xưng em, gọi anh/chị.",
		DefaultAgentID: "agent-kho", IsDefault: true, IsActive: true}
	var stock, staff models.AIAgent
	agents.Agent{Key: "kho", Name: "Bot kho", Audience: agents.AudienceCustomer, LangflowFlowID: "agent-flow",
		SystemPrompt: "Bạn là bot kho.", Tools: []string{"inventory_by_sku", "search_products"}, IsActive: true}.ApplyTo(&stock)
	stock.ID, stock.TenantID = "agent-kho", group.TenantID
	agents.Agent{Key: "noi-bo", Name: "Nội bộ", Audience: agents.AudienceStaff, SystemPrompt: "Nội bộ.",
		Tools: []string{"lookup_debt"}, IsActive: true}.ApplyTo(&staff)
	staff.ID, staff.TenantID = "agent-noi-bo", group.TenantID
	members := []models.AIAgent{stock, staff}

	err = database.Callback().Query().After("gorm:query").Register("test:agents", func(tx *gorm.DB) {
		switch row := tx.Statement.Dest.(type) {
		case *[]models.Channel:
			*row = []models.Channel{channel}
		case *models.Channel:
			*row = channel
		case *models.CRMGroup:
			*row = group
		case *[]models.CRMGroup:
			*row = []models.CRMGroup{group}
		case *models.ZaloWhitelist:
			*row = models.ZaloWhitelist{ID: "whitelist", Name: "Fixture Staff"}
		case *models.ZaloCustomer:
			_ = tx.AddError(gorm.ErrRecordNotFound)
		case *int64:
			if tx.Statement.Table == "crm_group_employees" {
				*row = 1
			}
		case *[]models.ERPEndpoint:
			*row = []models.ERPEndpoint{{Resource: "inventory", IsEnabled: true, ScopeType: "own"}}
		case *models.AppSetting:
			for _, v := range tx.Statement.Vars {
				if v == agents.EnabledSettingKey {
					row.ValuePlain = "true"
				}
			}
		case *models.AITeam:
			*row = team
		case *[]models.AITeamMember:
			links := make([]models.AITeamMember, 0, len(members))
			for i, m := range members {
				links = append(links, models.AITeamMember{TeamID: team.ID, AgentID: m.ID, TenantID: team.TenantID, Position: i, Enabled: true})
			}
			*row = links
		case *[]models.AIAgent:
			*row = members
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	usageDone := make(chan struct{}, 1)
	err = database.Callback().Create().After("gorm:create").Register("test:agents-usage", func(tx *gorm.DB) {
		if _, ok := tx.Statement.Dest.(*models.AIUsageLog); ok {
			usageDone <- struct{}{}
		}
	})
	if err != nil {
		t.Fatal(err)
	}

	var replies []string
	flowCalls := 0
	transport := http.DefaultTransport
	http.DefaultTransport = stockTestTransport(func(req *http.Request) (*http.Response, error) {
		var body string
		switch req.URL.Host + req.URL.Path {
		case "langflow.test/api/v1/run/agent-flow":
			flowCalls++
			var input struct {
				Text   string         `json:"input_value"`
				Tweaks map[string]any `json:"tweaks"`
			}
			if err := json.NewDecoder(req.Body).Decode(&input); err != nil || !strings.HasPrefix(input.Text, question) {
				t.Errorf("agent turn payload: text %q, decode error %v", input.Text, err)
			}
			prompts := 0
			for _, value := range input.Tweaks {
				tweak, _ := value.(map[string]any)
				if prompt, ok := tweak["system_prompt"].(string); ok {
					prompts++
					if !strings.HasPrefix(prompt, "Xưng em, gọi anh/chị.\n\nBạn là bot kho.") {
						t.Errorf("agent prompt = %q", prompt)
					}
				}
			}
			if prompts == 0 {
				t.Errorf("no system prompt tweak in %v", input.Tweaks)
			}
			permissions, err := engine.VerifyPermissionToken(req.Header.Get("X-Langflow-Global-Var-ERP_PERMISSION_TOKEN"), key)
			if err != nil || permissions.AgentType != "public" || permissions.AgentKey != "kho" ||
				strings.Join(permissions.AgentTools, ",") != "inventory_by_sku,search_products" ||
				!permissions.ToolAllowed("inventory_by_sku") || permissions.ToolAllowed("lookup_debt") {
				t.Errorf("agent turn permissions = %+v, %v", permissions, err)
			}
			body = fmt.Sprintf(`{"outputs":[{"outputs":[{"results":{"message":{"text":%q}}}]}]}`, answer)
		case "langflow.test/api/v1/run/public":
			t.Error("the AI Engine flow ran instead of the agent's")
			body = `{"outputs":[]}`
		case "openapi.zalo.me/v3.0/oa/group/message":
			var reply struct {
				Message struct{ Text string }
			}
			if err := json.NewDecoder(req.Body).Decode(&reply); err != nil {
				return nil, err
			}
			replies = append(replies, reply.Message.Text)
			body = `{"error":0}`
		default:
			return nil, fmt.Errorf("unexpected fixture request: %s %s", req.Method, req.URL)
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body))}, nil
	})
	t.Cleanup(func() { http.DefaultTransport = transport })
	cfg := &config.Config{EncryptionKey: key, LangflowAPIURL: "https://langflow.test",
		LangflowPublicFlowID: "public", ChatbotSessionWelcomeMessage: "Xin chào."}
	handle := HandleZaloWebhookTask(cfg, engine.NewLangflowClient(cfg), nil)
	send := func() error {
		payload := ZaloWebhookPayload{EventName: "user_send_group_text", OAID: "oa", SignatureVerified: true}
		payload.Sender.ID, payload.Recipient.ID, payload.Message.Text = "sender", group.ZaloGroupID, question
		task, err := NewZaloWebhookTask(payload)
		if err != nil {
			t.Fatal(err)
		}
		return handle(context.Background(), task)
	}

	if err := send(); err != nil || flowCalls != 1 || len(replies) != 1 || replies[0] != answer {
		t.Fatalf("agent turn: replies %v, flow calls %d, error %v", replies, flowCalls, err)
	}
	select {
	case <-usageDone:
	case <-time.After(3 * time.Second):
		t.Fatal("chatbot usage write did not finish")
	}

	// The same agent on the native runtime: its own model answers through
	// the runtime with the same prompt and agent token; Langflow is not
	// called.
	var nativeStock models.AIAgent
	agents.Agent{Key: "kho", Name: "Bot kho", Audience: agents.AudienceCustomer, Runtime: agents.RuntimeNative,
		Provider: "openai", Model: "gpt-5-mini", SystemPrompt: "Bạn là bot kho.",
		Tools: []string{"inventory_by_sku", "search_products"}, IsActive: true}.ApplyTo(&nativeStock)
	nativeStock.ID, nativeStock.TenantID = "agent-kho", group.TenantID
	members = []models.AIAgent{nativeStock, staff}
	runtime := &recordingRuntime{text: "Dạ mẫu này còn 2 đôi ạ."}
	agents.RegisterRuntime(agents.RuntimeNative, runtime)
	t.Cleanup(func() { agents.RegisterRuntime(agents.RuntimeNative, nil) })
	replies, flowCalls = nil, 0
	if err := send(); err != nil || flowCalls != 0 || len(replies) != 1 || replies[0] != runtime.text {
		t.Fatalf("native turn: replies %v, flow calls %d, error %v", replies, flowCalls, err)
	}
	if len(runtime.inputs) != 1 {
		t.Fatalf("runtime runs = %d", len(runtime.inputs))
	}
	in := runtime.inputs[0]
	if in.Agent.Key != "kho" || in.Tier != agents.TierPublic || !strings.HasPrefix(in.Input, question) ||
		!strings.HasPrefix(in.SystemPrompt, "Xưng em, gọi anh/chị.\n\nBạn là bot kho.") {
		t.Errorf("native run input = %+v", in)
	}
	permissions, err := engine.VerifyPermissionToken(in.PermissionToken, key)
	if err != nil || permissions.AgentKey != "kho" || !permissions.ToolAllowed("inventory_by_sku") || permissions.ToolAllowed("lookup_debt") {
		t.Errorf("native turn permissions = %+v, %v", permissions, err)
	}
	select {
	case <-usageDone:
	case <-time.After(3 * time.Second):
		t.Fatal("native turn usage write did not finish")
	}

	// Only a staff agent left: customers in the group get no bot reply.
	members = []models.AIAgent{staff}
	team.DefaultAgentID = staff.ID
	replies, flowCalls = nil, 0
	if err := send(); err != nil || flowCalls != 0 || len(replies) != 0 || len(runtime.inputs) != 1 {
		t.Fatalf("silent turn: replies %v, flow calls %d, error %v", replies, flowCalls, err)
	}
}

type recordingRuntime struct {
	text   string
	inputs []agents.RunInput
}

func (r *recordingRuntime) Run(_ context.Context, in agents.RunInput) (agents.RunResult, error) {
	r.inputs = append(r.inputs, in)
	return agents.RunResult{Text: r.text, Usage: agents.Usage{Provider: "openai", Model: "gpt-5-mini", InputTokens: 10, OutputTokens: 2, HasUsage: true}}, nil
}

func TestAgentTurnFlow(t *testing.T) {
	lf := engine.LangflowConfig{FlowID: "staff", PublicFlowID: "public", FlowIDSource: engine.LangflowSourceTenant, PublicFlowIDSource: engine.LangflowSourceTenant}
	agentPlan := func(flow string) agents.Plan {
		return agents.Plan{Mode: agents.ModeAgent, Selection: agents.Selection{Agent: agents.Agent{Key: "a", LangflowFlowID: flow}}}
	}
	for _, tc := range []struct {
		plan       agents.Plan
		tier, want string
	}{
		{agents.Plan{Mode: agents.ModeLegacy}, agents.TierPublic, "public"},
		{agents.Plan{Mode: agents.ModeLegacy}, agents.TierPrivate, "staff"},
		{agentPlan(""), agents.TierPublic, "public"},
		{agentPlan("debt"), agents.TierPublic, "debt"},
		{agentPlan("debt"), agents.TierPrivate, "debt"},
		{agentPlan("STAFF "), agents.TierPublic, "public"},
		{agentPlan("staff"), agents.TierPrivate, "staff"},
	} {
		if got, _ := agentTurnFlow(tc.plan, lf, tc.tier); got != tc.want {
			t.Errorf("%s %+v: flow %s, want %s", tc.tier, tc.plan.Selection.Agent, got, tc.want)
		}
	}
	lf.PublicFlowID = ""
	if got, _ := agentTurnFlow(agentPlan(""), lf, agents.TierPublic); got != "" {
		t.Errorf("public specialist used the missing public flow's staff fallback %q", got)
	}
	if got, _ := agentTurnFlow(agents.Plan{Mode: agents.ModeLegacy}, lf, agents.TierPublic); got != lf.FlowID {
		t.Errorf("legacy staff fallback = %q, want %q", got, lf.FlowID)
	}
	lf.FlowID, lf.PublicFlowID = "37593e48-052f-4be3-abcc-94a21465b3aa", "{37593E48-052F-4BE3-ABCC-94A21465B3AA}"
	if got, _ := agentTurnFlow(agentPlan(lf.PublicFlowID), lf, agents.TierPublic); got != "" {
		t.Errorf("public specialist used the staff flow by its equivalent UUID %q", got)
	}
}
