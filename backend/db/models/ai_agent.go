package models

import "time"

// AIAgent is a specialist chatbot agent: its own prompt, model, MCP tools and
// knowledge. Agents answer through an AITeam ("Bộ điều phối"), which channels
// point at. The security tier of a turn (public/private) still comes from who
// is chatting and is signed into the permission token; an agent can only
// narrow what that tier allows, never widen it. See backend/agents.
type AIAgent struct {
	ID       string `gorm:"type:char(36);primaryKey" json:"id"`
	TenantID string `gorm:"type:char(36);not null;uniqueIndex:idx_ai_agent_tenant_key,priority:1" json:"tenant_id"`
	// Key is the agent's stable slug inside the tenant: the coordinator
	// answers with it and handoff markers name it.
	Key  string `gorm:"type:varchar(64);not null;uniqueIndex:idx_ai_agent_tenant_key,priority:2" json:"key"`
	Name string `gorm:"type:varchar(255);not null" json:"name"`
	// Description tells the coordinator when to pick this agent.
	Description string `gorm:"type:text" json:"description"`
	// Audience: customer | staff | all. Who the agent serves; the
	// coordinator only picks agents that fit the sender.
	Audience string `gorm:"type:varchar(16);not null;default:'customer'" json:"audience"`
	// Runtime: langflow | native.
	Runtime string `gorm:"type:varchar(16);not null;default:'langflow'" json:"runtime"`
	// LangflowFlowID overrides the tier's AI Engine flow for this agent.
	// Empty uses the tenant flow for the turn's tier.
	LangflowFlowID string `gorm:"type:varchar(64);not null;default:''" json:"langflow_flow_id"`
	Provider       string `gorm:"type:varchar(32);not null;default:''" json:"provider"`
	Model          string `gorm:"type:varchar(128);not null;default:''" json:"model"`
	SystemPrompt   string `gorm:"type:mediumtext" json:"system_prompt,omitempty"`
	// Tools is a JSON array of MCP tool names the agent may call. Empty
	// array means none; the legacy agents carry every tool of their tier.
	Tools string `gorm:"type:json" json:"-"`
	// KnowledgeTags is a JSON array of knowledge document tags the agent
	// searches. Empty means every document its tier may read.
	KnowledgeTags string    `gorm:"type:json" json:"-"`
	Options       string    `gorm:"type:json" json:"-"`
	IsActive      bool      `gorm:"not null;default:true" json:"is_active"`
	UpdatedBy     string    `gorm:"type:char(36);not null;default:''" json:"updated_by"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

func (AIAgent) TableName() string { return "ai_agents" }

// AIAgentRevision is a saved copy of an agent's editable fields, written on
// every save so staff can compare and restore prompts.
type AIAgentRevision struct {
	ID           string    `gorm:"type:char(36);primaryKey" json:"id"`
	TenantID     string    `gorm:"type:char(36);not null;index" json:"tenant_id"`
	AgentID      string    `gorm:"type:char(36);not null;index:idx_ai_agent_rev_agent,priority:1" json:"agent_id"`
	Name         string    `gorm:"type:varchar(255);not null;default:''" json:"name"`
	Description  string    `gorm:"type:text" json:"description"`
	SystemPrompt string    `gorm:"type:mediumtext" json:"system_prompt"`
	Tools        string    `gorm:"type:json" json:"-"`
	EditedBy     string    `gorm:"type:char(36);not null;default:''" json:"edited_by"`
	CreatedAt    time.Time `gorm:"index:idx_ai_agent_rev_agent,priority:2" json:"created_at"`
}

func (AIAgentRevision) TableName() string { return "ai_agent_revisions" }

// AITeam is a coordinator ("Bộ điều phối"): the agents that answer behind
// one or more channels with one voice. With a single agent for the sender's
// audience it goes straight to that agent; with several, the coordinator
// model picks one per turn.
type AITeam struct {
	ID       string `gorm:"type:char(36);primaryKey" json:"id"`
	TenantID string `gorm:"type:char(36);not null;index" json:"tenant_id"`
	Name     string `gorm:"type:varchar(255);not null" json:"name"`
	// PersonaPrompt is the shared voice placed before every member's prompt.
	PersonaPrompt string `gorm:"type:mediumtext" json:"persona_prompt"`
	// RouterPrompt overrides the default coordinator instructions.
	RouterPrompt   string    `gorm:"type:mediumtext" json:"router_prompt"`
	DefaultAgentID string    `gorm:"type:char(36);not null;default:''" json:"default_agent_id"`
	Options        string    `gorm:"type:json" json:"-"`
	IsDefault      bool      `gorm:"not null;default:false" json:"is_default"`
	IsActive       bool      `gorm:"not null;default:true" json:"is_active"`
	UpdatedBy      string    `gorm:"type:char(36);not null;default:''" json:"updated_by"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

func (AITeam) TableName() string { return "ai_teams" }

// AITeamMember places an agent in a team.
type AITeamMember struct {
	TeamID   string `gorm:"type:char(36);primaryKey" json:"team_id"`
	AgentID  string `gorm:"type:char(36);primaryKey;index" json:"agent_id"`
	TenantID string `gorm:"type:char(36);not null;index" json:"tenant_id"`
	Position int    `gorm:"not null;default:0" json:"position"`
	Enabled  bool   `gorm:"not null;default:true" json:"enabled"`
}

func (AITeamMember) TableName() string { return "ai_team_members" }
