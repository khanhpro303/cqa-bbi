package agents

import (
	"testing"

	"github.com/vietbui/chat-quality-agent/engine"
)

// Every AI Engine task prompt is classified exactly once: run on its own,
// shared by every agent, or tied to tools that exist in the catalog. A new
// prompt that is added to the bot's prompt must be placed here, or this
// test fails.
func TestEveryTaskPromptIsClassified(t *testing.T) {
	seen := map[string]string{}
	mark := func(key, kind string) {
		if prev, ok := seen[key]; ok {
			t.Errorf("%s is both %s and %s", key, prev, kind)
		}
		seen[key] = kind
		if _, ok := engine.LookupTaskPrompt(key); !ok {
			t.Errorf("%s (%s) is not in the task prompt registry", key, kind)
		}
	}
	for _, k := range standalonePrompts {
		mark(k, "standalone")
	}
	for _, k := range sharedRules {
		mark(k, "shared")
	}
	for k, need := range toolRules {
		mark(k, "tool rule")
		if len(need.tools)+len(need.groups) == 0 {
			t.Errorf("%s needs no tool", k)
		}
		for _, tool := range need.tools {
			if _, ok := LookupTool(tool); !ok {
				t.Errorf("%s needs unknown tool %s", k, tool)
			}
		}
		for _, g := range need.groups {
			found := false
			for _, info := range Catalog() {
				found = found || info.Group == g
			}
			if !found {
				t.Errorf("%s needs unknown tool group %s", k, g)
			}
		}
	}
	for _, p := range engine.TaskPrompts() {
		if _, ok := seen[p.Key]; !ok {
			t.Errorf("task prompt %s (%s) is not classified in agents/rules.go", p.Key, p.Title)
		}
	}
}

func TestUsesRule(t *testing.T) {
	agent := func(tools ...string) Plan {
		return Plan{Mode: ModeAgent, Selection: Selection{Agent: Agent{Key: "a", Tools: tools}}}
	}
	debt := agent("lookup_debt", "find_customer")
	stock := agent("inventory_by_sku")
	docs := agent("search_knowledge")
	sales := agent("order_cart_view", "lookup_order", "complaint_record")
	legacy := Plan{Mode: ModeLegacy}
	for _, tc := range []struct {
		name string
		plan Plan
		rule string
		want bool
	}{
		{"debt agent, stock rules", debt, engine.PromptPublicStockRules, false},
		{"debt agent, missing ERP data rules", debt, engine.PromptMissingDataRules, false},
		{"debt agent, product advice", debt, engine.PromptProductAdvice, false},
		{"debt agent, document images", debt, engine.PromptKnowledgeImages, false},
		{"debt agent, cart", debt, engine.PromptOrderCart, false},
		{"debt agent, complaints", debt, engine.PromptComplaintBot, false},
		{"debt agent, handover (shared)", debt, engine.PromptUnansweredHandover, true},
		{"debt agent, customer card (shared)", debt, engine.PromptChatMemoryContext, true},
		{"stock agent, stock rules", stock, engine.PromptPublicStockRules, true},
		{"stock agent, missing ERP data rules", stock, engine.PromptMissingDataRules, true},
		{"stock agent, order lookup", stock, engine.PromptPublicOrderLookup, false},
		{"docs agent, OA images", docs, engine.PromptKnowledgeImages, true},
		{"docs agent, personal Zalo images", docs, engine.PromptPZKnowledgeImages, true},
		{"docs agent, stock rules", docs, engine.PromptPublicStockRules, false},
		{"sales agent, cart", sales, engine.PromptOrderCart, true},
		{"sales agent, order lookup", sales, engine.PromptPublicOrderLookup, true},
		{"sales agent, staff complaint", sales, engine.PromptPZStaffCode, true},
		{"AI Engine bot, stock rules", legacy, engine.PromptPublicStockRules, true},
		{"AI Engine bot, complaints", legacy, engine.PromptComplaintBot, true},
	} {
		if got := tc.plan.UsesRule(tc.rule); got != tc.want {
			t.Errorf("%s: UsesRule = %t, want %t", tc.name, got, tc.want)
		}
	}
	// The older helpers agree with the rule table.
	for _, p := range []Plan{debt, stock, docs, sales, legacy} {
		if p.UsesRule(engine.PromptOrderCart) != p.TakesOrders() ||
			p.UsesRule(engine.PromptComplaintBot) != p.Allows(ToolComplaintRecord) ||
			p.UsesRule(engine.PromptPublicStockRules) != p.AllowsGroup(ToolGroupProducts) {
			t.Errorf("rule table and plan helpers disagree for %v", p.Selection.Agent.Tools)
		}
	}
}

func TestKeepCandidatesForGuests(t *testing.T) {
	team := Team{ID: "t", DefaultAgentID: "a-docs", Members: []Agent{
		{ID: "a-debt", Key: "cong-no", Audience: AudienceCustomer, IsActive: true, Tools: []string{"lookup_debt"}},
		{ID: "a-sales", Key: "ban-hang", Audience: AudienceCustomer, IsActive: true, Tools: []string{"search_knowledge", "search_products"}},
		{ID: "a-docs", Key: "tai-lieu", Audience: AudienceCustomer, IsActive: true, Tools: []string{"search_knowledge"}},
	}}
	sel, _ := team.Pick(AudienceCustomer)
	plan := Plan{Mode: ModeAgent, Selection: sel}
	plan.StickTo("cong-no")
	if !plan.KeepCandidates(AnswersFromDocuments) || plan.Selection.Agent.Key != "tai-lieu" || len(plan.Selection.Candidates) != 2 {
		t.Fatalf("guest plan = %+v", plan.Selection)
	}
	// A sticky agent that can search documents keeps the guest.
	plan = Plan{Mode: ModeAgent, Selection: sel}
	plan.StickTo("ban-hang")
	if !plan.KeepCandidates(AnswersFromDocuments) || plan.Selection.Agent.Key != "ban-hang" || plan.Selection.Reason != ReasonSticky {
		t.Fatalf("sticky guest plan = %+v", plan.Selection)
	}
	// No agent can: nothing changes, and the caller is told.
	only := Plan{Mode: ModeAgent, Selection: Selection{Agent: team.Members[0], Candidates: team.Members[:1]}}
	if only.KeepCandidates(AnswersFromDocuments) || only.Selection.Agent.Key != "cong-no" {
		t.Fatalf("no-docs plan = %+v", only.Selection)
	}
	if legacy := (Plan{Mode: ModeLegacy}); !legacy.KeepCandidates(AnswersFromDocuments) {
		t.Fatal("the AI Engine bot always answers guests")
	}
}
