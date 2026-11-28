package agents

import "github.com/vietbui/chat-quality-agent/engine"

// Every task prompt in the AI Engine registry (engine.TaskPrompts) falls in
// one of three kinds, and a test keeps the lists below complete:
//
//   - standalonePrompts run their own AI call (classifiers, summaries,
//     matchers). Agents do not change them.
//   - sharedRules are added to the answering bot's prompt on every turn
//     their condition holds, whatever the agent: they are about the chat
//     (handover, group, customer card, session), not about a tool.
//   - toolRules are added only when the answering agent has a tool they
//     are about, so a debt agent is not taught stock rules it cannot use.
//
// The AI Engine bot (agents off) has every tool, so it gets every rule.

// ruleNeed is the tools that make a rule relevant: any tool named, or any
// tool of a catalog group.
type ruleNeed struct {
	tools  []string
	groups []string
}

const toolSearchKnowledge = "search_knowledge"

var (
	needProducts   = ruleNeed{groups: []string{ToolGroupProducts}}
	needKnowledge  = ruleNeed{tools: []string{toolSearchKnowledge}}
	needComplaints = ruleNeed{tools: []string{ToolComplaintRecord}}
	needCart       = ruleNeed{tools: []string{"order_cart_update", "order_cart_view", "order_cart_checkout", "order_cart_clear"}}
)

var toolRules = map[string]ruleNeed{
	engine.PromptMissingDataRules:  needProducts,
	engine.PromptPublicStockRules:  needProducts,
	engine.PromptProductAdvice:     needProducts,
	engine.PromptPublicOrderLookup: {tools: []string{ToolLookupOrder}},
	engine.PromptOrderCart:         needCart,

	engine.PromptKnowledgeImages:   needKnowledge,
	engine.PromptPZKnowledgeImages: needKnowledge,
	engine.PromptGuestKnowledge:    needKnowledge,
	// The search_knowledge tool description: the MCP server lists the tool
	// only to agents that have it.
	engine.PromptKnowledgeTool: needKnowledge,

	engine.PromptComplaintBot:            needComplaints,
	engine.PromptPZComplaintRules:        needComplaints,
	engine.PromptPZComplaintGroup:        needComplaints,
	engine.PromptPZHandoverComplaintLead: needComplaints,
	engine.PromptPZGuestComplaint:        needComplaints,
	engine.PromptPZStaffNoCode:           needComplaints,
	engine.PromptPZStaffCode:             needComplaints,
	engine.PromptPZStaffConfirmed:        needComplaints,
	engine.PromptPZStaffGroup:            needComplaints,
}

var sharedRules = []string{
	engine.PromptUnansweredHandover,
	engine.PromptGroupAssistant,
	engine.PromptChatMemoryContext,
	engine.PromptPZDirectTurn,
	engine.PromptPZNoERP,
	engine.PromptPZHandover,
	engine.PromptPZInternbotDraft,
	engine.PromptPZGuestChannelLead,
}

var standalonePrompts = []string{
	engine.PromptChatMemorySummary,
	engine.PromptChatMemoryProfile,
	engine.PromptChatMemoryIssues,
	engine.PromptIntentClassifier,
	engine.PromptBusinessScope, // added to the classifiers' prompts
	engine.PromptAgentRouter,
	engine.PromptGroupClassifier,
	engine.PromptCasualReply,
	engine.PromptInventoryKeyword,
	engine.PromptImageReader,
	engine.PromptComplaintMatch,
	engine.PromptProductCodeMatch,
	engine.PromptAttributeMatch,
	engine.PromptOrderApproval,
}

// UsesRule reports whether the task prompt key belongs in the answering
// bot's prompt this turn as far as tools go: shared rules always, tool rules
// when the agent has a tool they are about. Callers still check the rule's
// own condition (a customer turn, a verified sender, a channel setting).
func (p Plan) UsesRule(key string) bool {
	need, ok := toolRules[key]
	if !ok || p.Mode != ModeAgent {
		return true
	}
	a := p.Selection.Agent
	for _, t := range need.tools {
		if a.HasTool(t) {
			return true
		}
	}
	for _, g := range need.groups {
		if a.HasToolGroup(g) {
			return true
		}
	}
	return false
}

// KeepCandidates narrows the agents the coordinator may pick to those keep
// accepts, for a turn only some of them can serve (an unverified guest can
// only be answered from documents). The agent the plan holds stays when
// kept, else the team default when kept, else the first kept candidate. When
// no candidate is kept the plan is left as it was and false is returned.
func (p *Plan) KeepCandidates(keep func(Agent) bool) bool {
	if p.Mode != ModeAgent {
		return true
	}
	var kept []Agent
	for _, a := range p.Selection.Candidates {
		if keep(a) {
			kept = append(kept, a)
		}
	}
	if len(kept) == 0 {
		return false
	}
	p.Selection.Candidates = kept
	if keep(p.Selection.Agent) {
		return true
	}
	p.Selection.Agent, p.Selection.Reason = kept[0], "first_member"
	for _, a := range kept {
		if a.ID == p.Selection.Team.DefaultAgentID {
			p.Selection.Agent, p.Selection.Reason = a, "team_default"
			break
		}
	}
	return true
}

// AnswersFromDocuments reports whether an agent can answer from the
// knowledge base (search_knowledge).
func AnswersFromDocuments(a Agent) bool {
	return a.HasTool(toolSearchKnowledge)
}

// KeepGuestCandidates limits a guest turn to document search and, when enabled
// for Personal Zalo, complaint recording.
func (p *Plan) KeepGuestCandidates(complaints bool) bool {
	return p.KeepCandidates(func(a Agent) bool {
		return AnswersFromDocuments(a) || (complaints && a.HasTool(ToolComplaintRecord))
	})
}

// GuestKnowledgeRules gives the guest flow the same tool rules as other turns.
func (p Plan) GuestKnowledgeRules() engine.GuestKnowledgeRules {
	return engine.GuestKnowledgeRules{
		Knowledge:     p.UsesRule(engine.PromptGuestKnowledge),
		ProductAdvice: p.UsesRule(engine.PromptProductAdvice),
	}
}
