package agents

import (
	"context"
	"fmt"

	"github.com/vietbui/chat-quality-agent/engine"
)

// How a turn is answered.
const (
	// ModeLegacy answers with the AI Engine prompts: agents are off, or on
	// without any team (a setup error, logged by the caller).
	ModeLegacy = "legacy"
	// ModeAgent answers with Plan.Selection.
	ModeAgent = "agent"
	// ModeSilent leaves the chat to people: agents are on and the channel's
	// team has no agent for the sender's audience (for example a staff-only
	// account receiving a customer message). This is a setting, not an
	// error, so the AI Engine bot does not step in.
	ModeSilent = "silent"
)

// Plan says who answers a turn.
type Plan struct {
	Mode      string
	Selection Selection
	// Reason is a short note for logs.
	Reason string
}

// PlanTurn decides who answers a turn on channelID for tier. On a database
// error it returns ModeLegacy with the error, so a failed lookup degrades to
// the pre-agent bot (same tier, same data scope) instead of no reply.
func PlanTurn(ctx context.Context, tenantID, channelID, tier string) (Plan, error) {
	if !Enabled(ctx, tenantID) {
		return Plan{Mode: ModeLegacy, Reason: "agents_off"}, nil
	}
	team, ok, err := TeamForChannel(ctx, tenantID, channelID)
	if err != nil {
		return Plan{Mode: ModeLegacy, Reason: "team_lookup_failed"}, err
	}
	if !ok {
		return Plan{Mode: ModeLegacy, Reason: "no_team"}, nil
	}
	sel, ok := team.Pick(AudienceForTier(tier))
	if !ok {
		return Plan{Mode: ModeSilent, Selection: Selection{Team: team}, Reason: "no_agent_for_" + AudienceForTier(tier)}, nil
	}
	return Plan{Mode: ModeAgent, Selection: sel, Reason: sel.Reason}, nil
}

// Agent reports whether an agent answers, and which.
func (p Plan) Agent() (Selection, bool) {
	return p.Selection, p.Mode == ModeAgent
}

// BasePrompt is the prompt the turn starts from before context is added:
// the agent's (with the team voice) in agent mode, else legacyPrompt.
func (p Plan) BasePrompt(legacyPrompt string) string {
	if p.Mode == ModeAgent {
		return p.Selection.SystemPrompt()
	}
	return legacyPrompt
}

// Allows reports whether the answering bot may call tool. The AI Engine bot
// has every tool of its tier.
func (p Plan) Allows(tool string) bool {
	if p.Mode != ModeAgent {
		return true
	}
	return p.Selection.Agent.HasTool(tool)
}

// AllowsGroup reports whether the answering bot has any tool of a catalog
// group (ToolGroupProducts...). The worker uses it for work it does on the
// bot's behalf, such as adding product notes to the prompt.
func (p Plan) AllowsGroup(group string) bool {
	if p.Mode != ModeAgent {
		return true
	}
	return p.Selection.Agent.HasToolGroup(group)
}

// TakesOrders reports whether the answering bot runs the chat order cart.
func (p Plan) TakesOrders() bool {
	if p.Mode != ModeAgent {
		return true
	}
	return p.Selection.Agent.HasCartTools()
}

// Flow returns the Langflow flow for the turn: the agent's own flow when it
// has one, else tierFlowID. agentFlow is true when the agent's flow is used.
func (p Plan) Flow(tierFlowID string) (flowID string, agentFlow bool) {
	if p.Mode == ModeAgent && p.Selection.Agent.LangflowFlowID != "" {
		return p.Selection.Agent.LangflowFlowID, true
	}
	return tierFlowID, false
}

// Sign applies the agent to the permission context before it is signed (see
// Selection.ApplyTo). Legacy turns are left as they are.
func (p Plan) Sign(perm *engine.GroupPermissionContext) {
	if p.Mode == ModeAgent {
		p.Selection.ApplyTo(perm)
	}
}

// LogFields is "mode=… agent=… team=… reason=…" for routing log lines.
func (p Plan) LogFields() string {
	return fmt.Sprintf("agent_mode=%s agent=%s team=%s agent_reason=%s",
		p.Mode, p.Selection.Agent.Key, p.Selection.Team.ID, p.Reason)
}
