package agents

import (
	"reflect"
	"strings"
	"testing"

	"github.com/vietbui/chat-quality-agent/db/models"
	"github.com/vietbui/chat-quality-agent/engine"
)

func TestNormalizeAgent(t *testing.T) {
	temp := 2.5
	base := Agent{Key: " Cong-No ", Name: " Bot công nợ ", Audience: AudienceCustomer,
		Tools: []string{"lookup_debt", "find_customer", "lookup_debt", " "}, KnowledgeTags: []string{"công nợ", "Công nợ", ""}}
	got, err := Normalize(base)
	if err != nil {
		t.Fatal(err)
	}
	if got.Key != "cong-no" || got.Name != "Bot công nợ" || got.Runtime != RuntimeLangflow {
		t.Errorf("unexpected normalized agent: %+v", got)
	}
	if !reflect.DeepEqual(got.Tools, []string{"find_customer", "lookup_debt"}) {
		t.Errorf("tools = %v", got.Tools)
	}
	if !reflect.DeepEqual(got.KnowledgeTags, []string{"công nợ"}) {
		t.Errorf("knowledge tags = %v", got.KnowledgeTags)
	}

	langflowModel := base
	langflowModel.Provider, langflowModel.Model = "openai", "gpt-x"
	if got, err := Normalize(langflowModel); err != nil || got.Provider != "" || got.Model != "" {
		t.Errorf("langflow agent kept a model it does not use: %+v %v", got, err)
	}

	for name, tc := range map[string]struct {
		mutate func(*Agent)
		field  string
	}{
		"bad key":           {func(a *Agent) { a.Key = "Công nợ" }, "key"},
		"empty name":        {func(a *Agent) { a.Name = " " }, "name"},
		"bad audience":      {func(a *Agent) { a.Audience = "vip" }, "audience"},
		"bad runtime":       {func(a *Agent) { a.Runtime = "python" }, "runtime"},
		"unknown tool":      {func(a *Agent) { a.Tools = []string{"drop_tables"} }, "tools"},
		"native no model":   {func(a *Agent) { a.Runtime, a.Provider = RuntimeNative, "openai" }, "model"},
		"native bad vendor": {func(a *Agent) { a.Runtime, a.Provider, a.Model = RuntimeNative, "acme", "m1" }, "provider"},
		"bad flow":          {func(a *Agent) { a.LangflowFlowID = "../../etc" }, "langflow_flow_id"},
		"bad tag":           {func(a *Agent) { a.KnowledgeTags = []string{"#tag"} }, "knowledge_tags"},
		"temperature":       {func(a *Agent) { a.Options.Temperature = &temp }, "options.temperature"},
		"tool calls":        {func(a *Agent) { a.Options.MaxToolCalls = 99 }, "options.max_tool_calls"},
		"long prompt":       {func(a *Agent) { a.SystemPrompt = strings.Repeat("x", MaxPromptChars+1) }, "system_prompt"},
	} {
		a := base
		tc.mutate(&a)
		_, err := Normalize(a)
		verr, ok := err.(*ValidationError)
		if !ok || verr.Field != tc.field {
			t.Errorf("%s: got %v, want error on %s", name, err, tc.field)
		}
	}

	native := base
	native.Runtime, native.Provider, native.Model = RuntimeNative, "OpenRouter", "vendor/model:free"
	if got, err := Normalize(native); err != nil || got.Provider != "openrouter" {
		t.Errorf("native agent: %+v %v", got, err)
	}
}

func TestFromModelBadJSONHasNoTools(t *testing.T) {
	a := FromModel(models.AIAgent{Key: "x", Tools: "{not json", KnowledgeTags: "null"})
	if a.Tools == nil || len(a.Tools) != 0 || a.KnowledgeTags == nil {
		t.Errorf("malformed columns must decode as empty lists: %+v", a)
	}
	var stored models.AIAgent
	Agent{Key: "k", Tools: []string{"lookup_debt"}, Options: Options{LegacySource: LegacyPublicPromptKey}}.ApplyTo(&stored)
	back := FromModel(stored)
	if !reflect.DeepEqual(back.Tools, []string{"lookup_debt"}) || back.Options.LegacySource != LegacyPublicPromptKey {
		t.Errorf("round trip lost data: %+v", back)
	}
}

func TestCatalogToolsServeBothTiers(t *testing.T) {
	// Every tool today serves both tiers, so the legacy agents (all tools,
	// customer and staff audiences) must normalize cleanly.
	for _, audience := range []string{AudienceCustomer, AudienceStaff, AudienceAll} {
		if _, err := normalizeTools(AllToolNames(), audience); err != nil {
			t.Errorf("%s: %v", audience, err)
		}
	}
	if !ToolWrites("not_a_tool") || ToolWrites("lookup_debt") || !ToolWrites("order_cart_checkout") {
		t.Error("ToolWrites misclassifies tools")
	}
	c := Catalog()
	c[0].Tiers[0] = "changed"
	if Catalog()[0].Tiers[0] == "changed" {
		t.Error("Catalog must return a copy")
	}
}

func teamFixture() Team {
	return Team{
		ID: "team", PersonaPrompt: "Xưng em, gọi anh/chị.", DefaultAgentID: "b",
		Members: []Agent{
			{ID: "a", Key: "san-pham", Audience: AudienceCustomer, IsActive: true, SystemPrompt: "Tư vấn sản phẩm."},
			{ID: "b", Key: "cong-no", Audience: AudienceAll, IsActive: true, SystemPrompt: "Tra công nợ."},
			{ID: "c", Key: "tat", Audience: AudienceStaff, IsActive: false},
		},
	}
}

func TestTeamPick(t *testing.T) {
	team := teamFixture()
	sel, ok := team.Pick(AudienceCustomer)
	if !ok || sel.Agent.ID != "b" || sel.Reason != "team_default" || len(sel.Candidates) != 2 {
		t.Errorf("customer pick = %+v %v", sel, ok)
	}
	sel, ok = team.Pick(AudienceStaff)
	if !ok || sel.Agent.ID != "b" || sel.Reason != "only_agent" {
		t.Errorf("staff pick (inactive member skipped) = %+v %v", sel, ok)
	}
	team.DefaultAgentID = "c"
	if sel, _ := team.Pick(AudienceCustomer); sel.Agent.ID != "a" || sel.Reason != "first_member" {
		t.Errorf("fallback pick = %+v", sel)
	}
	team.Members = team.Members[:1]
	if _, ok := team.Pick(AudienceStaff); ok {
		t.Error("no staff member must not pick")
	}
}

func TestSelectionSystemPrompt(t *testing.T) {
	sel, _ := teamFixture().Pick(AudienceCustomer)
	if got := sel.SystemPrompt(); got != "Xưng em, gọi anh/chị.\n\nTra công nợ." {
		t.Errorf("composed prompt = %q", got)
	}
	sel.Team.PersonaPrompt = "  "
	// A migrated agent with no team voice sends exactly the old prompt.
	if got := sel.SystemPrompt(); got != "Tra công nợ." {
		t.Errorf("prompt without persona = %q", got)
	}
	sel.Team.PersonaPrompt, sel.Agent.SystemPrompt = "Giọng chung.", ""
	if got := sel.SystemPrompt(); got != "Giọng chung." {
		t.Errorf("persona only = %q", got)
	}
}

func TestPlan(t *testing.T) {
	legacy := Plan{Mode: ModeLegacy}
	if legacy.BasePrompt("cũ") != "cũ" || !legacy.Allows(ToolComplaintRecord) || !legacy.TakesOrders() || !legacy.AllowsGroup(ToolGroupProducts) {
		t.Error("legacy plan must behave like the AI Engine bot")
	}
	if id, own := legacy.Flow("tier-flow"); id != "tier-flow" || own {
		t.Errorf("legacy flow = %s %v", id, own)
	}
	var zero Plan
	if zero.BasePrompt("cũ") != "cũ" || !zero.Allows("lookup_debt") {
		t.Error("zero plan must behave like legacy")
	}
	p := engine.GroupPermissionContext{TenantID: "t"}
	legacy.Sign(&p)
	if p.AgentKey != "" || !p.ToolAllowed("anything") {
		t.Error("legacy plan must not restrict tools")
	}

	team := teamFixture()
	team.Members[1].Tools = []string{"find_customer", "lookup_debt"}
	team.Members[1].LangflowFlowID = "debt-flow"
	team.Members[1].KnowledgeTags = []string{"công nợ"}
	sel, _ := team.Pick(AudienceCustomer)
	plan := Plan{Mode: ModeAgent, Selection: sel}
	if plan.Allows(ToolComplaintRecord) || !plan.Allows("lookup_debt") || plan.TakesOrders() ||
		plan.AllowsGroup(ToolGroupProducts) || !plan.AllowsGroup(ToolGroupDebt) {
		t.Error("agent plan must follow the agent's tools")
	}
	if id, own := plan.Flow("tier-flow"); id != "debt-flow" || !own {
		t.Errorf("agent flow = %s %v", id, own)
	}
	if plan.BasePrompt("cũ") != sel.SystemPrompt() {
		t.Error("agent plan must use the agent prompt")
	}
	plan.Sign(&p)
	if p.AgentKey != "cong-no" || !p.ToolAllowed("lookup_debt") || p.ToolAllowed("order_cart_update") ||
		!reflect.DeepEqual(p.KnowledgeTags, []string{"công nợ"}) {
		t.Errorf("signed agent context = %+v", p)
	}
	// The token holds a copy: editing the agent afterwards does not widen it.
	sel.Agent.Tools[0] = "order_cart_update"
	if p.ToolAllowed("order_cart_update") {
		t.Error("permission context shares the agent's tool slice")
	}
	if !strings.Contains(plan.LogFields(), "agent=cong-no") {
		t.Errorf("log fields = %s", plan.LogFields())
	}

	empty := Plan{Mode: ModeAgent, Selection: Selection{Agent: Agent{Key: "rong"}}}
	p2 := engine.GroupPermissionContext{}
	empty.Sign(&p2)
	if p2.ToolAllowed("lookup_debt") {
		t.Error("an agent with no tools must not get every tool")
	}
}

func TestSilentAudiences(t *testing.T) {
	team := teamFixture()
	links := []models.AITeamMember{{AgentID: "a", Enabled: true}, {AgentID: "b", Enabled: false}, {AgentID: "c", Enabled: true}}
	if got := silentAudiences(team.Members, links); !reflect.DeepEqual(got, []string{AudienceStaff}) {
		t.Errorf("silent = %v", got)
	}
	links[1].Enabled = true
	if got := silentAudiences(team.Members, links); len(got) != 0 {
		t.Errorf("silent = %v", got)
	}
}

func TestRuntimeAvailable(t *testing.T) {
	if !RuntimeAvailable("") || !RuntimeAvailable(RuntimeLangflow) {
		t.Error("langflow is always available")
	}
	if RuntimeAvailable(RuntimeNative) {
		t.Skip("a native runtime is registered in this build")
	}
}
