package handlers

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log"
	"strings"

	"github.com/google/uuid"
	"github.com/vietbui/chat-quality-agent/agents"
	"github.com/vietbui/chat-quality-agent/config"
	"github.com/vietbui/chat-quality-agent/db/models"
	"github.com/vietbui/chat-quality-agent/engine"
	"github.com/vietbui/chat-quality-agent/workers"
)

const personalZaloERPUnavailable = "Dạ em chưa thể thực hiện tra cứu này trên kênh Zalo này. Anh/chị vui lòng thử lại hoặc liên hệ nhân viên phụ trách ạ."

func personalZaloDirectGreeting(agentType string) string {
	if agentType == "private" {
		return "Chào anh/chị, em là trợ lý nội bộ. Anh/chị cần em hỗ trợ tra cứu gì ạ?"
	}
	return "Dạ em chào anh/chị. Anh/chị cần em hỗ trợ thông tin gì ạ?"
}

type personalZaloDirectGrant struct {
	flow              engine.LangflowConfig
	flowID            string
	flowScope         string
	permission        string
	customer          string
	agentToken        string
	grantHash         string
	turnID            string
	prompt            string
	complaintGuide    string
	draftGuide        string
	showImages        bool
	skipStockPick     bool
	advicePermissions *engine.GroupPermissionContext
}

// preparePersonalZaloDirectGrant builds a verified 1:1 turn. plan says who
// answers it: an agent's prompt, flow and tools, or the AI Engine prompt.
func preparePersonalZaloDirectGrant(ctx context.Context, cfg *config.Config, req personalZaloChatbotRequest, identity personalZaloERPIdentity, flow engine.LangflowConfig, sessionScope string, settings map[string]models.AppSetting, plan agents.Plan) (personalZaloDirectGrant, error) {
	grant := personalZaloDirectGrant{flow: flow, skipStockPick: !plan.Allows("inventory_by_line")}
	if identity.AgentType != "private" && flow.PublicFlowID == "" {
		return grant, nil
	}
	flowID, flowSource := flow.FlowFor(identity.AgentType)
	flowID, _ = plan.Flow(flowID)
	promptKey := "ai_engine_system_prompt"
	if identity.AgentType == "private" {
		promptKey = "ai_engine_system_prompt_internal"
	}
	if flow.APIURL == "" || flowID == "" || canonicalAIFlowID(flow.PublicFlowID) == canonicalAIFlowID(flow.FlowID) ||
		(identity.AgentType != "private" && canonicalAIFlowID(flowID) == canonicalAIFlowID(flow.FlowID)) {
		return grant, nil
	}
	grant.flowID = flowID
	grant.prompt = strings.TrimSpace(plan.BasePrompt(settings[promptKey].ValuePlain))
	agentToken := strings.TrimSpace(settings["ai_agent_erp_token_"+identity.AgentType].ValuePlain)
	if identity.AgentType == "private" && agentToken == "" {
		agentToken = strings.TrimSpace(settings["ai_agent_erp_token"].ValuePlain)
	}
	permissions := engine.GroupPermissionContext{
		TenantID: req.TenantID, ChannelID: req.ChannelID, ZaloUserID: req.SenderID,
		AgentType: identity.AgentType, PersonalZalo: true,
	}
	erpAgentToken := ""
	if agentToken != "" && erpActiveForRole(settings, identity.AgentType) {
		var err error
		permissions, err = engine.ResolvePersonalZaloERPPermissions(ctx, req.TenantID, req.ChannelID, req.SenderID, identity.AgentType, identity.Customer)
		if err != nil {
			return grant, err
		}
		grant.grantHash = personalZaloPermissionsHash(permissions)
		erpAgentToken = agentToken
	}
	digest := sha256.Sum256([]byte(strings.Join([]string{sessionScope, identity.ID, identity.AgentType, flowID, grant.grantHash}, "\x00")))
	grant.flowScope = "personal-zalo-" + hex.EncodeToString(digest[:])
	hasERPGrant := len(permissions.Groups) > 0
	if hasERPGrant || plan.Mode == agents.ModeAgent {
		if cfg.EncryptionKey == "" {
			return grant, errors.New("specialist permission signing is unavailable")
		}
		permissions.PersonalZaloSessionID = grant.flowScope
		// After grantHash: stillAuthorized compares it with freshly resolved
		// ERP permissions, which carry no agent.
		plan.Sign(&permissions)
		if hasERPGrant {
			grant.turnID = "personal-zalo-erp:" + uuid.NewString()
			permissions.TurnID = grant.turnID
		}
		var err error
		grant.permission, err = engine.SignPermissionToken(permissions, cfg.EncryptionKey)
		if err != nil {
			return grant, err
		}
		grant.agentToken = erpAgentToken
		if hasERPGrant {
			grant.customer = permissions.CustomerCode
		}
		if hasERPGrant && plan.AllowsGroup(agents.ToolGroupProducts) {
			grant.advicePermissions = &permissions
		}
	}
	if cfg.EncryptionKey != "" && flowSource == engine.LangflowSourceTenant && flow.APIKeySource == engine.LangflowSourceTenant {
		if grant.agentToken == "" {
			token, err := signKnowledgePreviewToken(cfg.EncryptionKey, req.TenantID, identity.AgentType)
			if err != nil {
				return grant, err
			}
			grant.agentToken = token
		}
		grant.showImages = true
	}
	if plan.Mode == agents.ModeAgent && grant.agentToken == "" {
		return grant, errors.New("specialist scoped agent token is unavailable")
	}
	return grant, nil
}

func personalZaloPermissionsHash(permissions engine.GroupPermissionContext) string {
	// The DTO holds only serializable fields. Hash the resolved grants without
	// the one-use turn identifier so conversation history follows the same scope.
	data, _ := json.Marshal(permissions)
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

func (g personalZaloDirectGrant) stillAuthorized(ctx context.Context, req personalZaloChatbotRequest, identity personalZaloERPIdentity, settings map[string]models.AppSetting) bool {
	if g.grantHash == "" {
		return true
	}
	if !erpActiveForRole(settings, identity.AgentType) {
		return false
	}
	current, err := engine.ResolvePersonalZaloERPPermissions(ctx, req.TenantID, req.ChannelID, req.SenderID, identity.AgentType, identity.Customer)
	return err == nil && g.grantHash == personalZaloPermissionsHash(current)
}

type personalZaloImageSelection struct {
	knowledgeIDs []string
	products     []models.ProductAdvisoryImage
}

func (g personalZaloDirectGrant) run(ctx context.Context, langflow *engine.LangflowClient, channel models.Channel, req personalZaloChatbotRequest) (string, personalZaloImageSelection, error) {
	if g.flowID == "" {
		return personalZaloERPUnavailable, personalZaloImageSelection{}, nil
	}
	if !g.skipStockPick {
		if reply := g.answerPersonalZaloStockPick(ctx, req); reply != "" {
			return reply, personalZaloImageSelection{}, nil
		}
	}
	input, prompt := req.Text, g.prompt
	if prompt != "" {
		prompt += "\n\n" + engine.TaskPromptText(req.TenantID, engine.PromptPZDirectTurn)
		if g.turnID == "" {
			prompt += " " + engine.TaskPromptText(req.TenantID, engine.PromptPZNoERP)
		}
		if handover := personalZaloHandoverPrompt(channel); handover != "" {
			if g.complaintGuide != "" {
				handover = engine.TaskPromptText(req.TenantID, engine.PromptPZHandoverComplaintLead) + " " + handover
			}
			prompt += "\n\n" + handover
		}
	}
	if g.complaintGuide != "" {
		prompt = strings.TrimSpace(prompt + "\n\n" + g.complaintGuide)
	}
	if g.draftGuide != "" {
		if prompt == "" {
			input = g.draftGuide + "\n\nTin nhắn của khách:\n" + input
		} else {
			prompt += "\n\n" + g.draftGuide
		}
	}
	if g.agentToken != "" {
		input, prompt = engine.AddProductAdviceInstruction(req.TenantID, input, prompt)
	}
	var productCandidates []models.ProductAdvisoryImage
	if g.advicePermissions != nil {
		adviceContext, images, err := workers.PersonalZaloProductAdvice(ctx, g.advicePermissions, req.Text)
		productCandidates = images
		switch {
		case err != nil:
			log.Printf("[product-advisory] catalog lookup for personal Zalo channel %s failed: %v", channel.ID, err)
		case adviceContext != "" && prompt != "":
			prompt += "\n\n" + adviceContext
		case adviceContext != "":
			input += "\n\n" + adviceContext
		}
	}
	if g.showImages {
		input, prompt = engine.AddPersonalZaloKnowledgeImageInstruction(req.TenantID, input, prompt)
	}
	result, err := langflow.RunFlowWithCustomerUsage(ctx, g.flowScope, g.flowScope, input, g.flow.APIURL, g.flow.APIKey, g.flowID,
		g.customer, g.permission, g.agentToken, prompt)
	if err != nil {
		return "", personalZaloImageSelection{}, err
	}
	if menu := g.publishPersonalZaloStockMenu(ctx); menu != "" {
		return menu, personalZaloImageSelection{}, nil
	}
	if _, text, _ := engine.TakePromptDelivery(ctx, g.turnID); text != "" {
		return text, personalZaloImageSelection{}, nil
	}
	if strings.Contains(result.Text, "[RICH_MESSAGE_SENT]") {
		return personalZaloERPUnavailable, personalZaloImageSelection{}, nil
	}
	text, productImages := workers.ParsePersonalZaloProductAdviceImages(result.Text, productCandidates)
	images := personalZaloImageSelection{products: productImages}
	if g.showImages {
		text, images.knowledgeIDs = engine.ParsePersonalZaloKnowledgeImageReply(text)
	} else {
		text, _ = engine.ParseKnowledgeImageReply(text, 0)
	}
	return text, images, nil
}
