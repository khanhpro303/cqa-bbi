package handlers

import (
	"context"
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
	cfg := &config.Config{}
	req := personalZaloChatbotRequest{TenantID: "tenant", ChannelID: "channel", SenderID: "456", ThreadType: personalZaloThreadUser}
	settings := map[string]models.AppSetting{
		"ai_engine_system_prompt":          {ValuePlain: "Prompt khách."},
		"ai_engine_system_prompt_internal": {ValuePlain: "Prompt nội bộ."},
	}
	flow := engine.LangflowConfig{APIURL: "https://langflow.test", APIKey: "k", FlowID: "private", PublicFlowID: "public"}
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
			Team:  agents.Team{PersonaPrompt: "Xưng em."},
			Agent: agents.Agent{Key: "cong-no", Audience: agents.AudienceCustomer, SystemPrompt: "Tra công nợ.", LangflowFlowID: flowID, Tools: []string{"lookup_debt"}},
		}}
	}
	grant, err = preparePersonalZaloDirectGrant(ctx, cfg, req, customer, flow, "s", settings, plan("debt-flow"))
	if err != nil || grant.prompt != "Xưng em.\n\nTra công nợ." || grant.flowID != "debt-flow" || grant.advicePermissions != nil {
		t.Fatalf("agent grant = %+v %v", grant, err)
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
