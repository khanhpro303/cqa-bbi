package handlers

import (
	"context"
	"errors"
	"log"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/vietbui/chat-quality-agent/agents"
	"github.com/vietbui/chat-quality-agent/ai"
	"github.com/vietbui/chat-quality-agent/api/middleware"
	"github.com/vietbui/chat-quality-agent/config"
	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/db/models"
	"github.com/vietbui/chat-quality-agent/engine"
)

// agentConsoleBrief is an agent as the test console shows it.
type agentConsoleBrief struct {
	ID   string `json:"id"`
	Key  string `json:"key"`
	Name string `json:"name"`
}

func briefAgent(a agents.Agent) agentConsoleBrief {
	return agentConsoleBrief{ID: a.ID, Key: a.Key, Name: a.Name}
}

// agentConsoleUsage is what the console's reply cost.
type agentConsoleUsage struct {
	Provider     string  `json:"provider"`
	Model        string  `json:"model"`
	InputTokens  int     `json:"input_tokens"`
	OutputTokens int     `json:"output_tokens"`
	Estimated    bool    `json:"estimated"`
	CostUSD      float64 `json:"cost_usd"`
}

// agentRouterAsk is the model the coordinator asks on personal Zalo turns
// and in the test console (a seam for tests).
var agentRouterAsk = agents.TenantAsk

// errAgentConsoleFlow means the agent has no flow the console may run.
var errAgentConsoleFlow = errors.New("agent console flow not configured")

// NewAIAgentTestConsole runs one message through a team's coordinator, as a
// chat turn would, and with reply=true lets the chosen agent answer it.
//
// POST /tenants/:tenantId/ai-agents/test
//
//	{"team_id", "audience": "customer"|"staff", "message",
//	 "current_agent": key of the agent already in the chat (optional),
//	 "reply": bool, "session_id": uuid from an earlier reply (optional)}
//
// The reply runs with the agent's prompt, flow (or native runtime) and
// tool list, but its signed scope is a dry run with no ERP groups and no
// customer: knowledge search works, ERP data and every write are refused by
// the MCP server. Nothing is sent to a chat and nothing is written.
func NewAIAgentTestConsole(cfg *config.Config) gin.HandlerFunc {
	client := engine.NewLangflowPreviewClient(cfg)
	return func(c *gin.Context) {
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 32<<10)
		var req struct {
			TeamID       string `json:"team_id"`
			Audience     string `json:"audience"`
			Message      string `json:"message"`
			CurrentAgent string `json:"current_agent"`
			Reply        bool   `json:"reply"`
			SessionID    string `json:"session_id"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request"})
			return
		}
		message := strings.TrimSpace(req.Message)
		if message == "" || utf8.RuneCountInString(message) > 2000 || strings.TrimSpace(req.TeamID) == "" ||
			(req.Audience != agents.AudienceCustomer && req.Audience != agents.AudienceStaff) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request"})
			return
		}
		tenantID, userID := middleware.GetTenantID(c), middleware.GetUserID(c)
		role := middleware.GetTenantRole(c)
		// Staff agents read staff-only knowledge: same rule as the
		// knowledge chat preview.
		if tenantID == "" || userID == "" || (req.Audience == agents.AudienceStaff && role != "owner" && role != "admin") {
			c.JSON(http.StatusForbidden, gin.H{"error": "console_forbidden"})
			return
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), 90*time.Second)
		defer cancel()

		team, err := agents.TeamByID(ctx, tenantID, strings.TrimSpace(req.TeamID))
		if err != nil {
			agentError(c, "test console team", err)
			return
		}
		sel, ok := team.Pick(req.Audience)
		if !ok {
			c.JSON(http.StatusOK, gin.H{"silent": true, "candidates": []agentConsoleBrief{}})
			return
		}
		plan := agents.Plan{Mode: agents.ModeAgent, Selection: sel, Reason: sel.Reason}
		if key := strings.TrimSpace(req.CurrentAgent); key != "" && !plan.StickTo(key) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "current_agent_not_in_team"})
			return
		}
		routed := plan.NeedsRouting() && !plan.Stuck()
		routerReason := plan.Route(ctx, tenantID, agentRouterAsk(cfg, tenantID), agents.RouterInput(message))
		plan.Reason = plan.Selection.Reason

		candidates := make([]agentConsoleBrief, len(plan.Selection.Candidates))
		for i, a := range plan.Selection.Candidates {
			candidates[i] = briefAgent(a)
		}
		out := gin.H{
			"agent": briefAgent(plan.Selection.Agent), "reason": plan.Selection.Reason,
			"router_reason": routerReason, "routed": routed, "candidates": candidates,
		}
		if !req.Reply {
			c.JSON(http.StatusOK, out)
			return
		}

		tier := agents.TierPublic
		if req.Audience == agents.AudienceStaff {
			tier = agents.TierPrivate
		}
		session, err := knowledgePreviewSession(tenantID, userID, "agent-console:"+tier, req.SessionID)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_console_session"})
			return
		}
		result, runtimeName, err := runAgentConsoleReply(ctx, cfg, client, tenantID, tier, plan, session.flowSessionID, message)
		switch {
		case errors.Is(err, errAgentConsoleFlow):
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "console_flow_not_configured", "routing": out})
			return
		case err != nil:
			log.Printf("[agents] test console tenant=%s agent=%s runtime=%s failed: %v", tenantID, plan.Selection.Agent.Key, runtimeName, err)
			c.JSON(http.StatusBadGateway, gin.H{"error": "console_reply_failed", "routing": out})
			return
		}
		answer, _ := engine.ParseKnowledgeImageReply(result.Text, engine.MaxKnowledgeReplyImages)
		usage := agentConsoleUsage{
			Provider: result.Usage.Provider, Model: result.Usage.Model,
			InputTokens: result.Usage.InputTokens, OutputTokens: result.Usage.OutputTokens,
			Estimated: !result.Usage.HasUsage,
		}
		if usage.Estimated {
			usage.InputTokens = ai.EstimateTokens(message)
			usage.OutputTokens = ai.EstimateTokens(result.Text)
		}
		usage.CostUSD = (ai.AIResponse{Provider: usage.Provider, Model: usage.Model, InputTokens: usage.InputTokens,
			OutputTokens: usage.OutputTokens, ReportedCostUSD: result.Usage.ReportedCostUSD}).UsageCostUSD()
		out["reply"] = strings.TrimSpace(answer)
		out["runtime"] = runtimeName
		out["usage"] = usage
		out["session_id"] = session.id
		c.JSON(http.StatusOK, out)
	}
}

// runAgentConsoleReply lets the plan's agent answer message on its runtime
// with a dry-run, knowledge-only scope (see NewAIAgentTestConsole). It
// returns the runtime used: "native", or the Langflow flow source
// ("agent_flow" or "tier_flow").
func runAgentConsoleReply(ctx context.Context, cfg *config.Config, client *engine.LangflowClient, tenantID, tier string,
	plan agents.Plan, sessionID, message string) (agents.RunResult, string, error) {
	if cfg == nil || cfg.EncryptionKey == "" {
		return agents.RunResult{}, "", errAgentConsoleFlow
	}
	scope := engine.GroupPermissionContext{TenantID: tenantID, ZaloUserID: sessionID, AgentType: tier, DryRun: true}
	plan.Sign(&scope)
	permission, err := engine.SignPermissionToken(scope, cfg.EncryptionKey)
	if err != nil {
		return agents.RunResult{}, "", err
	}
	agentToken, err := signKnowledgePreviewToken(cfg.EncryptionKey, tenantID, tier)
	if err != nil {
		return agents.RunResult{}, "", err
	}
	input, prompt := engine.AddKnowledgeImageInstruction(tenantID, message, plan.BasePrompt(""))
	in := agents.RunInput{
		Agent: plan.Selection.Agent, Tier: tier, SessionID: sessionID, SenderID: sessionID,
		Input: input, SystemPrompt: prompt, PermissionToken: permission, AgentToken: agentToken,
		Deadline: time.Now().Add(75 * time.Second),
	}
	if rt, ok := plan.NativeRuntime(); ok {
		res, err := rt.Run(ctx, in)
		return res, "native", err
	}

	var rows []models.AppSetting
	if db.DB == nil || db.DB.WithContext(ctx).Where("tenant_id = ? AND setting_key IN ?", tenantID, engine.LangflowSettingKeys).Find(&rows).Error != nil {
		return agents.RunResult{}, "", errors.New("load Langflow settings")
	}
	settings := make(map[string]models.AppSetting, len(rows))
	for _, row := range rows {
		settings[row.SettingKey] = row
	}
	lf := engine.ResolveTenantLangflowConfig(settings, cfg)
	tierFlow, tierSource := lf.PublicFlowID, lf.PublicFlowIDSource
	if tier == agents.TierPrivate {
		tierFlow, tierSource = lf.FlowID, lf.FlowIDSource
	}
	flowID, agentFlow := plan.Flow(tierFlow)
	source := "tier_flow"
	if agentFlow {
		source = "agent_flow"
	}
	// Only the tenant's own Langflow key, and only a flow the tenant chose
	// (the agent's, or its saved AI Engine flow): environment defaults may
	// belong to another tenant. A customer agent never runs on the staff
	// flow.
	if lf.APIURL == "" || lf.APIKeySource != engine.LangflowSourceTenant || flowID == "" ||
		(!agentFlow && tierSource != engine.LangflowSourceTenant) ||
		(tier == agents.TierPublic && lf.FlowID != "" && flowID == lf.FlowID) {
		return agents.RunResult{}, source, errAgentConsoleFlow
	}
	in.LangflowURL, in.LangflowAPIKey, in.FlowID = lf.APIURL, lf.APIKey, flowID
	res, err := agents.LangflowRuntime{Client: client}.Run(ctx, in)
	return res, source, err
}
