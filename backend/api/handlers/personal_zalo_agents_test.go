package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/vietbui/chat-quality-agent/agents"
	"github.com/vietbui/chat-quality-agent/config"
	"github.com/vietbui/chat-quality-agent/db/models"
	"github.com/vietbui/chat-quality-agent/engine"
)

// A verified 1:1 personal Zalo turn takes the answering agent's prompt and
// flow; with agents off it keeps the AI Engine prompt for the tier.
func TestPersonalZaloDirectGrantUsesAgent(t *testing.T) {
	ctx := context.Background()
	cfg := &config.Config{EncryptionKey: stockFixtureSigningKey}
	req := personalZaloChatbotRequest{TenantID: "tenant", ChannelID: "channel", SenderID: "456", ThreadType: personalZaloThreadUser}
	settings := map[string]models.AppSetting{
		"ai_engine_system_prompt":          {ValuePlain: "Prompt khách."},
		"ai_engine_system_prompt_internal": {ValuePlain: "Prompt nội bộ."},
	}
	flow := engine.LangflowConfig{
		APIURL: "https://langflow.test", APIKey: "k", FlowID: "private", PublicFlowID: "public",
		APIKeySource: engine.LangflowSourceTenant, FlowIDSource: engine.LangflowSourceTenant, PublicFlowIDSource: engine.LangflowSourceTenant,
	}
	customer := personalZaloERPIdentity{ID: "c1", AgentType: "public"}
	staff := personalZaloERPIdentity{ID: "s1", AgentType: "private"}

	grant, err := preparePersonalZaloDirectGrant(ctx, cfg, req, customer, flow, "s", settings, agents.Plan{Mode: agents.ModeLegacy})
	if err != nil || grant.prompt != "Prompt khách." || grant.flowID != "public" {
		t.Fatalf("legacy customer grant = %+v %v", grant, err)
	}
	grant, err = preparePersonalZaloDirectGrant(ctx, cfg, req, staff, flow, "s", settings, agents.Plan{})
	if err != nil || grant.prompt != "Prompt nội bộ." || grant.flowID != "private" {
		t.Fatalf("legacy staff grant = %+v %v", grant, err)
	}

	plan := func(flowID string) agents.Plan {
		return agents.Plan{Mode: agents.ModeAgent, Selection: agents.Selection{
			Team: agents.Team{PersonaPrompt: "Xưng em."},
			Agent: agents.Agent{Key: "cong-no", Audience: agents.AudienceCustomer, SystemPrompt: "Tra công nợ.", LangflowFlowID: flowID,
				Tools: []string{"search_knowledge"}, KnowledgeTags: []string{"công nợ"}},
		}}
	}
	grant, err = preparePersonalZaloDirectGrant(ctx, cfg, req, customer, flow, "s", settings, plan("debt-flow"))
	if err != nil || grant.prompt != "Xưng em.\n\nTra công nợ." || grant.flowID != "debt-flow" ||
		grant.permission == "" || grant.agentToken == "" || grant.advicePermissions != nil || grant.turnID != "" {
		t.Fatalf("agent grant = %+v %v", grant, err)
	}
	permission, err := engine.VerifyPermissionToken(grant.permission, stockFixtureSigningKey)
	if err != nil || permission.AgentKey != "cong-no" || len(permission.KnowledgeTags) != 1 || permission.KnowledgeTags[0] != "công nợ" ||
		len(permission.Groups) != 0 || permission.CustomerCode != "" {
		t.Fatalf("specialist-only permission = %+v %v", permission, err)
	}
	grant, err = preparePersonalZaloDirectGrant(ctx, cfg, req, customer, flow, "s", settings, plan(""))
	if err != nil || grant.flowID != "public" {
		t.Fatalf("agent without its own flow must use the tier flow: %+v %v", grant, err)
	}
	// A customer turn never runs on the staff flow, whoever configured it.
	grant, err = preparePersonalZaloDirectGrant(ctx, cfg, req, customer, flow, "s", settings, plan("private"))
	if err != nil || grant.flowID != "" {
		t.Fatalf("customer agent on the staff flow = %+v %v", grant, err)
	}
}

func TestPersonalZaloAgentScopeForGroupAndGuestDraft(t *testing.T) {
	cfg := &config.Config{EncryptionKey: stockFixtureSigningKey}
	req := personalZaloChatbotRequest{
		TenantID: "tenant", ChannelID: "channel", ThreadType: personalZaloThreadGroup,
		ThreadID: "group-1", SenderID: "sender-1",
	}
	plan := agents.Plan{Mode: agents.ModeAgent, Selection: agents.Selection{Agent: agents.Agent{
		Key: "bao-hanh", Tools: []string{"search_knowledge", "complaint_record"}, KnowledgeTags: []string{"bảo hành"},
	}}}

	permissionToken, agentToken, err := personalZaloAgentScope(cfg, req, "public", plan, true)
	if err != nil || permissionToken == "" || agentToken == "" {
		t.Fatalf("group/draft scope = %q %q %v", permissionToken, agentToken, err)
	}
	permission, err := engine.VerifyPermissionToken(permissionToken, stockFixtureSigningKey)
	if err != nil || permission.AgentKey != "bao-hanh" || !permission.DryRun || !permission.PersonalZalo ||
		len(permission.Groups) != 0 || permission.CustomerCode != "" || len(permission.KnowledgeTags) != 1 {
		t.Fatalf("group/draft permission = %+v %v", permission, err)
	}
	if legacyPermission, legacyToken, legacyErr := personalZaloAgentScope(cfg, req, "public", agents.Plan{Mode: agents.ModeLegacy}, true); legacyErr != nil || legacyPermission != "" || legacyToken != "" {
		t.Fatalf("legacy scope changed = %q %q %v", legacyPermission, legacyToken, legacyErr)
	}
	if _, _, err := personalZaloAgentScope(&config.Config{}, req, "public", plan, true); err == nil {
		t.Fatal("specialist scope must fail closed without a signing key")
	}

	var sentPermission, sentAgentToken string
	flowServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			_, _ = fmt.Fprint(w, `{"data":{"nodes":[{"id":"Agent-fixture","data":{"node":{"template":{"system_prompt":{}}}}}]}}`)
			return
		}
		var input struct {
			Tweaks map[string]any `json:"tweaks"`
		}
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			t.Errorf("decode draft request: %v", err)
		}
		sentPermission, _ = input.Tweaks["permission_token"].(string)
		sentAgentToken = r.Header.Get("X-Langflow-Global-Var-CQA_ERP_AGENT_TOKEN")
		_, _ = fmt.Fprint(w, `{"outputs":[{"outputs":[{"results":{"message":{"text":"draft ok"}}}]}]}`)
	}))
	t.Cleanup(flowServer.Close)
	flow := engine.LangflowConfig{
		APIURL: flowServer.URL, APIKey: "tenant-key", FlowID: "private", PublicFlowID: "public",
		APIKeySource: engine.LangflowSourceTenant, PublicFlowIDSource: engine.LangflowSourceTenant,
	}
	client := engine.NewLangflowClient(cfg)
	result, err := engine.RunPersonalZaloGuestDraft(context.Background(), client, flow, cfg.EncryptionKey,
		req.TenantID, "group-draft", "soạn câu trả lời", permissionToken, "Viết ngắn gọn.")
	if err != nil || result.Text != "draft ok" || sentPermission != permissionToken ||
		resolveKnowledgePreviewAgent(cfg.EncryptionKey, req.TenantID, sentAgentToken) != "public" {
		t.Fatalf("draft specialist scope was not propagated: result=%+v permission=%q token=%q err=%v",
			result, sentPermission, sentAgentToken, err)
	}
}
