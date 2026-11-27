package handlers

import (
	"context"
	"errors"
	"strings"

	"github.com/vietbui/chat-quality-agent/config"
	"github.com/vietbui/chat-quality-agent/db/models"
	"github.com/vietbui/chat-quality-agent/engine"
)

type personalZaloAI struct {
	cfg         *config.Config
	langflow    *engine.LangflowClient
	scoped      *engine.LangflowClient
	settingKeys []string
}

func newPersonalZaloAI(cfg *config.Config) personalZaloAI {
	keys := append([]string{
		"chatbot_active", "ai_engine_system_prompt", "ai_engine_system_prompt_internal",
		"ai_agent_erp_token_public", "ai_agent_erp_token_private", "ai_agent_erp_token",
	}, engine.LangflowSettingKeys...)
	return personalZaloAI{
		cfg: cfg, langflow: engine.NewLangflowClient(cfg), scoped: engine.NewLangflowPreviewClient(cfg),
		settingKeys: append(keys, erpActivationKeys...),
	}
}

type personalZaloAIOptions struct {
	scope               string
	identity            personalZaloERPIdentity
	guest               bool
	complaint           bool
	staffGroupComplaint bool
	canHandover         bool
	draft               bool
}

type personalZaloAIResult struct {
	reply         string
	images        personalZaloImageSelection
	grant         personalZaloDirectGrant
	handover      bool
	complaint     bool
	settingsError bool
}

const personalZaloInternbotDraftGuide = "Đây là bản nháp cho nhân viên. Chỉ dùng công cụ tra cứu được phép. Không tiết lộ mã xác thực, mật khẩu, token hoặc thông tin không cần thiết. Không tuyên bố đã xác thực, chuyển tiếp, tạo phiếu hay thực hiện thao tác thay đổi dữ liệu. Nếu cần nhân viên xử lý, chỉ trả " + personalZaloAIHandoverMarker + "."

func (ai personalZaloAI) generate(ctx, flowCtx context.Context, channel models.Channel, req personalZaloChatbotRequest,
	settings map[string]models.AppSetting, options personalZaloAIOptions) (personalZaloAIResult, error) {
	result := personalZaloAIResult{complaint: options.complaint}
	flow := engine.ResolveTenantLangflowConfig(settings, ai.cfg)

	switch {
	case options.guest:
		var response engine.LangflowResult
		var err error
		if options.draft {
			prompt := strings.TrimSpace(settings["ai_engine_system_prompt"].ValuePlain + "\n\n" + personalZaloInternbotDraftGuide)
			response, err = engine.RunPersonalZaloGuestDraft(flowCtx, ai.scoped, flow, ai.cfg.EncryptionKey,
				req.TenantID, options.scope, req.Text, prompt)
		} else {
			response, err = runPersonalZaloGuest(flowCtx, ai.scoped, ai.cfg, flow, req, options.scope, options.complaint, false)
		}
		if errors.Is(err, engine.ErrGuestKnowledgeNotConfigured) {
			result.reply = engine.GuestKnowledgeUnavailableReply
			return result, nil
		}
		result.reply, result.images.knowledgeIDs = engine.ParsePersonalZaloKnowledgeImageReply(response.Text)
		return result, err
	case options.identity.AgentType != "":
		if personalZaloMatchesKeyword(req.Text, "hello;hi;xin chào;chào;alo") {
			result.reply = personalZaloDirectGreeting(options.identity.AgentType)
			result.complaint = false
			return result, nil
		}

		grant, err := preparePersonalZaloDirectGrant(ctx, ai.cfg, req, options.identity, flow, options.scope, settings)
		if err == nil && options.complaint && !options.draft {
			err = grant.allowComplaints(ctx, ai.cfg.EncryptionKey, req, options.identity)
		}
		if err != nil {
			result.settingsError = true
			return result, err
		}
		if options.draft {
			grant.draftGuide = personalZaloInternbotDraftGuide
		}
		result.grant = grant
		result.reply, result.images, err = grant.run(flowCtx, ai.scoped, channel, req)
		result.handover = (options.draft || personalZaloHasHandoverDestination(channel)) &&
			strings.TrimSpace(result.reply) == personalZaloAIHandoverMarker
		return result, err
	case options.complaint && flow.PublicFlowIDSource == engine.LangflowSourceTenant && flow.APIKeySource == engine.LangflowSourceTenant:
		response, err := runPersonalZaloGuest(flowCtx, ai.scoped, ai.cfg, flow, req, options.scope, true, options.staffGroupComplaint)
		result.reply, _ = engine.ParseKnowledgeImageReply(response.Text, 0)
		return result, err
	default:
		if flow.APIURL == "" || flow.PublicFlowID == "" || strings.EqualFold(flow.PublicFlowID, flow.FlowID) {
			return result, nil
		}
		prompt := strings.TrimSpace(settings["ai_engine_system_prompt"].ValuePlain)
		instruction := ""
		if prompt != "" {
			instruction = personalZaloHandoverPrompt(channel)
			if instruction != "" {
				prompt += "\n\n" + instruction
			}
		}
		input, prompt := engine.AddProductAdviceInstruction(req.Text, prompt)
		reply, err := ai.langflow.RunFlowWithOverrides(flowCtx, options.scope, options.scope,
			input, flow.APIURL, flow.APIKey, flow.PublicFlowID, prompt)
		result.reply, _ = engine.ParseKnowledgeImageReply(reply, 0)
		result.handover = instruction != "" && options.canHandover && strings.TrimSpace(result.reply) == personalZaloAIHandoverMarker
		return result, err
	}
}
