package agents

import (
	"encoding/json"
	"strings"

	"github.com/vietbui/chat-quality-agent/db/models"
)

// TeamOptions are per-team knobs stored as JSON.
type TeamOptions struct {
	// StickyMinutes keeps the agent answering a chat for this long after
	// its last turn (0 = the chat session's own timeout).
	StickyMinutes int `json:"sticky_minutes,omitempty"`
	// DisableHandoff stops agents from passing a turn to a teammate.
	DisableHandoff bool `json:"disable_handoff,omitempty"`
}

// Team is a decoded models.AITeam with its active members in order.
type Team struct {
	ID             string      `json:"id"`
	TenantID       string      `json:"tenant_id"`
	Name           string      `json:"name"`
	PersonaPrompt  string      `json:"persona_prompt"`
	RouterPrompt   string      `json:"router_prompt"`
	DefaultAgentID string      `json:"default_agent_id"`
	IsDefault      bool        `json:"is_default"`
	IsActive       bool        `json:"is_active"`
	Options        TeamOptions `json:"options"`
	Members        []Agent     `json:"members"`
}

// TeamFromModel decodes a stored team; members are filled by the caller.
func TeamFromModel(m models.AITeam) Team {
	t := Team{
		ID: m.ID, TenantID: m.TenantID, Name: m.Name, PersonaPrompt: m.PersonaPrompt,
		RouterPrompt: m.RouterPrompt, DefaultAgentID: m.DefaultAgentID,
		IsDefault: m.IsDefault, IsActive: m.IsActive,
	}
	if m.Options != "" {
		_ = json.Unmarshal([]byte(m.Options), &t.Options)
	}
	return t
}

// Candidates returns the active members that may answer someone of
// audience, in team order.
func (t Team) Candidates(audience string) []Agent {
	out := make([]Agent, 0, len(t.Members))
	for _, a := range t.Members {
		if a.IsActive && a.Serves(audience) {
			out = append(out, a)
		}
	}
	return out
}

// Selection is the agent chosen to answer a turn.
type Selection struct {
	Team  Team
	Agent Agent
	// Candidates are the team's agents for the sender's audience; more than
	// one means the coordinator chose among them.
	Candidates []Agent
	// Reason is a short note for logs and the test console.
	Reason string
}

// Pick chooses the agent for a turn without asking a model: the only
// candidate, else the team default when it fits, else the first candidate.
// ok is false when no member serves the audience.
func (t Team) Pick(audience string) (Selection, bool) {
	candidates := t.Candidates(audience)
	if len(candidates) == 0 {
		return Selection{}, false
	}
	sel := Selection{Team: t, Candidates: candidates}
	if len(candidates) == 1 {
		sel.Agent, sel.Reason = candidates[0], "only_agent"
		return sel, true
	}
	for _, a := range candidates {
		if a.ID == t.DefaultAgentID {
			sel.Agent, sel.Reason = a, "team_default"
			return sel, true
		}
	}
	sel.Agent, sel.Reason = candidates[0], "first_member"
	return sel, true
}

// SystemPrompt is the agent's base prompt for this team: the team's shared
// voice, then the agent's own prompt. Context sections (customer card, cart,
// complaints...) are appended by the caller as before. With no persona the
// result is the agent prompt unchanged, so an agent migrated from the AI
// Engine prompt sends Langflow exactly what that prompt sent.
func (s Selection) SystemPrompt() string {
	persona := strings.TrimSpace(s.Team.PersonaPrompt)
	prompt := s.Agent.SystemPrompt
	if persona == "" {
		return prompt
	}
	if strings.TrimSpace(prompt) == "" {
		return persona
	}
	return persona + "\n\n" + prompt
}

// FlowFor returns the Langflow flow the agent runs on for tier: its own
// flow when set, else the tenant's AI Engine flow for the tier.
func (a Agent) FlowFor(tierFlowID string) string {
	if a.LangflowFlowID != "" {
		return a.LangflowFlowID
	}
	return tierFlowID
}
