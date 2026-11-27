package handlers

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log"
	"strings"

	"github.com/google/uuid"
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
	showImages        bool
	advicePermissions *engine.GroupPermissionContext
}

func preparePersonalZaloDirectGrant(ctx context.Context, cfg *config.Config, req personalZaloChatbotRequest, identity personalZaloERPIdentity, flow engine.LangflowConfig, sessionScope string, settings map[string]models.AppSetting) (personalZaloDirectGrant, error) {
	grant := personalZaloDirectGrant{flow: flow}
	if identity.AgentType != "private" && flow.PublicFlowID == "" {
		return grant, nil
	}
	flowID, flowSource := flow.FlowFor(identity.AgentType)
	promptKey := "ai_engine_system_prompt"
	if identity.AgentType == "private" {
		promptKey = "ai_engine_system_prompt_internal"
	}
	if flow.APIURL == "" || flowID == "" || canonicalAIFlowID(flow.PublicFlowID) == canonicalAIFlowID(flow.FlowID) {
		return grant, nil
	}
	grant.flowID = flowID
	grant.prompt = strings.TrimSpace(settings[promptKey].ValuePlain)
	agentToken := strings.TrimSpace(settings["ai_agent_erp_token_"+identity.AgentType].ValuePlain)
	if identity.AgentType == "private" && agentToken == "" {
		agentToken = strings.TrimSpace(settings["ai_agent_erp_token"].ValuePlain)
	}
	var permissions engine.GroupPermissionContext
	if agentToken != "" && erpActiveForRole(settings, identity.AgentType) {
		var err error
		permissions, err = engine.ResolvePersonalZaloERPPermissions(ctx, req.TenantID, req.ChannelID, req.SenderID, identity.AgentType, identity.Customer)
		if err != nil {
			return grant, err
		}
		grant.grantHash = personalZaloPermissionsHash(permissions)
	}
	digest := sha256.Sum256([]byte(strings.Join([]string{sessionScope, identity.ID, identity.AgentType, flowID, grant.grantHash}, "\x00")))
	grant.flowScope = "personal-zalo-" + hex.EncodeToString(digest[:])
	if len(permissions.Groups) > 0 {
		grant.turnID = "personal-zalo-erp:" + uuid.NewString()
		permissions.TurnID = grant.turnID
		permissions.PersonalZaloSessionID = grant.flowScope
		var err error
		grant.permission, err = engine.SignPermissionToken(permissions, cfg.EncryptionKey)
		if err != nil {
			return grant, err
		}
		grant.agentToken = agentToken
		grant.customer = permissions.CustomerCode
		grant.advicePermissions = &permissions
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
	if reply := g.answerPersonalZaloStockPick(ctx, req); reply != "" {
		return reply, personalZaloImageSelection{}, nil
	}
	input, prompt := req.Text, g.prompt
	if prompt != "" {
		prompt += "\n\nKhi người dùng chỉ chào hỏi, chào ngắn gọn và hỏi họ cần hỗ trợ gì. Chỉ xác nhận kết quả tra cứu do công cụ trả về."
		if g.turnID == "" {
			prompt += " Phiên này chưa có quyền ERP. Chỉ tư vấn dựa trên tài liệu được phép dùng."
		}
		if handover := personalZaloHandoverPrompt(channel); handover != "" {
			if g.complaintGuide != "" {
				handover = "Với các tình huống ngoài hướng dẫn ghi nhận khiếu nại: " + handover
			}
			prompt += "\n\n" + handover
		}
	}
	if g.complaintGuide != "" {
		prompt = strings.TrimSpace(prompt + "\n\n" + g.complaintGuide)
	}
	if g.agentToken != "" {
		input, prompt = engine.AddProductAdviceInstruction(input, prompt)
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
		input, prompt = engine.AddPersonalZaloKnowledgeImageInstruction(input, prompt)
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
