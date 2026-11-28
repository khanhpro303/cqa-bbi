package handlers

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/vietbui/chat-quality-agent/agents"
	"github.com/vietbui/chat-quality-agent/api/middleware"
	"github.com/vietbui/chat-quality-agent/config"
	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/engine"
)

// Specialist agents and the coordinator teams ("Bộ điều phối") that answer
// through each Zalo OA or personal Zalo account. See docs/admin/agents.md.

// agentSettingsResponse is the state of the tenant switch.
type agentSettingsResponse struct {
	Enabled   bool `json:"enabled"`
	HasAgents bool `json:"has_agents"`
	// Drift lists agents copied from an AI Engine prompt that has changed
	// since (POST /settings/ai-agents/migrate with refresh copies it again).
	Drift []string `json:"drift"`
}

func agentSettings(ctx context.Context, tenantID string) (agentSettingsResponse, error) {
	list, err := agents.ListAgents(ctx, tenantID)
	if err != nil {
		return agentSettingsResponse{}, err
	}
	drift, err := agents.LegacyDrift(ctx, tenantID)
	if err != nil {
		return agentSettingsResponse{}, err
	}
	if drift == nil {
		drift = []string{}
	}
	return agentSettingsResponse{Enabled: agents.Enabled(ctx, tenantID), HasAgents: len(list) > 0, Drift: drift}, nil
}

// GetAIAgentSettings returns whether chats are answered by agents.
func GetAIAgentSettings(c *gin.Context) {
	out, err := agentSettings(c.Request.Context(), middleware.GetTenantID(c))
	if err != nil {
		agentServerError(c, "get settings", err)
		return
	}
	c.JSON(http.StatusOK, out)
}

// SaveAIAgentSettings turns agents on or off. Turning on the first time
// copies the AI Engine prompts into two agents and a default team.
func SaveAIAgentSettings(c *gin.Context) {
	var req struct {
		Enabled *bool `json:"enabled"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.Enabled == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request"})
		return
	}
	tenantID := middleware.GetTenantID(c)
	ctx := c.Request.Context()
	if err := agents.SetEnabled(ctx, tenantID, middleware.GetUserID(c), *req.Enabled); err != nil {
		agentServerError(c, "set enabled", err)
		return
	}
	state := "off"
	if *req.Enabled {
		state = "on"
	}
	logAgentActivity(c, "ai.agents_switched", "settings", agents.EnabledSettingKey, "Agents "+state)
	out, err := agentSettings(ctx, tenantID)
	if err != nil {
		agentServerError(c, "get settings", err)
		return
	}
	c.JSON(http.StatusOK, out)
}

// MigrateAIAgents copies the AI Engine prompts into agents without turning
// them on (to split them before switching), or with refresh copies changed
// AI Engine prompts into the agents made from them.
func MigrateAIAgents(c *gin.Context) {
	var req struct {
		Refresh bool `json:"refresh"`
	}
	if c.Request.ContentLength > 0 {
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request"})
			return
		}
	}
	tenantID, actor := middleware.GetTenantID(c), middleware.GetUserID(c)
	ctx := c.Request.Context()
	created, err := agents.MigrateLegacy(ctx, tenantID, actor)
	if err != nil {
		agentServerError(c, "migrate", err)
		return
	}
	refreshed := []string{}
	if req.Refresh && !created {
		keys, err := agents.RefreshLegacyPrompts(ctx, tenantID, actor)
		if err != nil {
			agentServerError(c, "refresh", err)
			return
		}
		refreshed = append(refreshed, keys...)
	}
	if created || len(refreshed) > 0 {
		logAgentActivity(c, "ai.agents_migrated", "ai_agent", "", fmt.Sprintf("created=%t refreshed=%s", created, strings.Join(refreshed, ",")))
	}
	c.JSON(http.StatusOK, gin.H{"created": created, "refreshed": refreshed})
}

// ListAIAgentTools returns the MCP tools an agent can be given.
func ListAIAgentTools(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"tools": agents.Catalog(), "runtimes": agentRuntimes()})
}

func agentRuntimes() []string {
	out := []string{agents.RuntimeLangflow}
	if agents.RuntimeAvailable(agents.RuntimeNative) {
		out = append(out, agents.RuntimeNative)
	}
	return out
}

// ListAIAgents returns the tenant's agents without their prompts.
func ListAIAgents(c *gin.Context) {
	list, err := agents.ListAgents(c.Request.Context(), middleware.GetTenantID(c))
	if err != nil {
		agentServerError(c, "list", err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"agents": list})
}

// GetAIAgent returns one agent with its prompt.
func GetAIAgent(c *gin.Context) {
	a, err := agents.GetAgent(c.Request.Context(), middleware.GetTenantID(c), c.Param("agentId"))
	if err != nil {
		agentError(c, "get", err)
		return
	}
	c.JSON(http.StatusOK, a)
}

// CreateAIAgent adds an agent.
func CreateAIAgent(c *gin.Context) { saveAIAgent(c, "") }

// UpdateAIAgent saves an agent; its key never changes.
func UpdateAIAgent(c *gin.Context) { saveAIAgent(c, c.Param("agentId")) }

func saveAIAgent(c *gin.Context, id string) {
	var in agents.Agent
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request"})
		return
	}
	in.ID = id
	tenantID := middleware.GetTenantID(c)
	ctx := c.Request.Context()
	if verr := checkAgentRuntime(ctx, tenantID, in); verr != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_agent", "field": verr.Field, "message": verr.Message})
		return
	}
	saved, err := agents.SaveAgent(ctx, tenantID, middleware.GetUserID(c), in)
	if err != nil {
		agentError(c, "save", err)
		return
	}
	action := "ai.agent_updated"
	if id == "" {
		action = "ai.agent_created"
	}
	// Never log prompt text: a short hash shows that it changed.
	logAgentActivity(c, action, "ai_agent", saved.ID,
		fmt.Sprintf("%s tools=%s prompt=%s", saved.Key, strings.Join(saved.Tools, ","), shortHash(saved.SystemPrompt)))
	c.JSON(http.StatusOK, saved)
}

// checkAgentRuntime refuses settings the chat path cannot honour: a runtime
// that is not installed, or a customer-facing agent on the staff flow (the
// AI Engine keeps those flows apart and so do agents).
func checkAgentRuntime(ctx context.Context, tenantID string, a agents.Agent) *agents.ValidationError {
	runtime := strings.TrimSpace(a.Runtime)
	if !agents.RuntimeAvailable(runtime) {
		return &agents.ValidationError{Field: "runtime", Message: "runtime " + runtime + " chưa được cài đặt trên máy chủ"}
	}
	flowID := strings.TrimSpace(a.LangflowFlowID)
	audience := strings.TrimSpace(a.Audience)
	if flowID == "" || audience == agents.AudienceStaff {
		return nil
	}
	cfg, err := config.Load()
	if err != nil {
		return nil
	}
	settings, err := loadPersonalZaloChatbotSettings(ctx, tenantID, engine.LangflowSettingKeys...)
	if err != nil {
		return nil
	}
	flow := engine.ResolveTenantLangflowConfig(settings, cfg)
	if flow.FlowID != "" && canonicalAIFlowID(flowID) == canonicalAIFlowID(flow.FlowID) {
		return &agents.ValidationError{Field: "langflow_flow_id", Message: "agent phục vụ khách không được chạy trên flow nội bộ"}
	}
	return nil
}

// DeleteAIAgent removes an agent that no team uses.
func DeleteAIAgent(c *gin.Context) {
	id := c.Param("agentId")
	if err := agents.DeleteAgent(c.Request.Context(), middleware.GetTenantID(c), id); err != nil {
		agentError(c, "delete", err)
		return
	}
	logAgentActivity(c, "ai.agent_deleted", "ai_agent", id, "")
	c.JSON(http.StatusOK, gin.H{"message": "deleted"})
}

// ListAIAgentRevisions returns an agent's saved versions, newest first.
func ListAIAgentRevisions(c *gin.Context) {
	tenantID, id := middleware.GetTenantID(c), c.Param("agentId")
	ctx := c.Request.Context()
	if _, err := agents.GetAgent(ctx, tenantID, id); err != nil {
		agentError(c, "get", err)
		return
	}
	revs, err := agents.ListRevisions(ctx, tenantID, id)
	if err != nil {
		agentServerError(c, "revisions", err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"revisions": revs})
}

// ListAITeams returns the coordinator teams with members and channels.
func ListAITeams(c *gin.Context) {
	teams, err := agents.ListTeams(c.Request.Context(), middleware.GetTenantID(c))
	if err != nil {
		agentServerError(c, "list teams", err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"teams": teams})
}

// CreateAITeam adds a coordinator team.
func CreateAITeam(c *gin.Context) { saveAITeam(c, "") }

// UpdateAITeam saves a team and replaces its members.
func UpdateAITeam(c *gin.Context) { saveAITeam(c, c.Param("teamId")) }

func saveAITeam(c *gin.Context, id string) {
	var in agents.TeamInput
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request"})
		return
	}
	in.ID = id
	saved, err := agents.SaveTeam(c.Request.Context(), middleware.GetTenantID(c), middleware.GetUserID(c), in)
	if err != nil {
		agentError(c, "save team", err)
		return
	}
	action := "ai.team_updated"
	if id == "" {
		action = "ai.team_created"
	}
	keys := make([]string, 0, len(saved.Members))
	for _, m := range saved.Members {
		keys = append(keys, m.Key)
	}
	logAgentActivity(c, action, "ai_team", saved.ID, fmt.Sprintf("%s members=%s persona=%s", saved.Name, strings.Join(keys, ","), shortHash(saved.PersonaPrompt)))
	c.JSON(http.StatusOK, saved)
}

// DeleteAITeam removes a team no channel uses; the default team stays.
func DeleteAITeam(c *gin.Context) {
	id := c.Param("teamId")
	if err := agents.DeleteTeam(c.Request.Context(), middleware.GetTenantID(c), id); err != nil {
		agentError(c, "delete team", err)
		return
	}
	logAgentActivity(c, "ai.team_deleted", "ai_team", id, "")
	c.JSON(http.StatusOK, gin.H{"message": "deleted"})
}

// SetChannelAITeam points a Zalo OA or personal Zalo channel at a team;
// an empty team_id returns it to the default team.
func SetChannelAITeam(c *gin.Context) {
	var req struct {
		TeamID string `json:"team_id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request"})
		return
	}
	channelID := c.Param("channelId")
	teamID := strings.TrimSpace(req.TeamID)
	if err := agents.SetChannelTeam(c.Request.Context(), middleware.GetTenantID(c), channelID, teamID); err != nil {
		agentError(c, "set channel team", err)
		return
	}
	if teamID == "" {
		teamID = "default"
	}
	logAgentActivity(c, "ai.channel_team_set", "channel", channelID, "team="+teamID)
	c.JSON(http.StatusOK, gin.H{"message": "saved"})
}

func agentError(c *gin.Context, op string, err error) {
	var verr *agents.ValidationError
	switch {
	case errors.As(err, &verr):
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_agent", "field": verr.Field, "message": verr.Message})
	case errors.Is(err, agents.ErrNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": "not_found"})
	case errors.Is(err, agents.ErrInUse):
		c.JSON(http.StatusConflict, gin.H{"error": "in_use"})
	default:
		agentServerError(c, op, err)
	}
}

func agentServerError(c *gin.Context, op string, err error) {
	log.Printf("[agents] %s tenant=%s: %v", op, middleware.GetTenantID(c), err)
	c.JSON(http.StatusInternalServerError, gin.H{"error": "agents_unavailable"})
}

func logAgentActivity(c *gin.Context, action, resourceType, resourceID, detail string) {
	db.LogActivity(middleware.GetTenantID(c), middleware.GetUserID(c), middleware.GetUserEmail(c),
		action, resourceType, resourceID, detail, "", c.ClientIP())
}
