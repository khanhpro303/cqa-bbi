package handlers

import (
	"context"
	"errors"
	"log"
	"strings"

	"github.com/vietbui/chat-quality-agent/agents"
	"github.com/vietbui/chat-quality-agent/config"
	"github.com/vietbui/chat-quality-agent/customercases"
	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/db/models"
	"github.com/vietbui/chat-quality-agent/engine"
	"github.com/vietbui/chat-quality-agent/workers"
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
	// plan is who answers (see personalZaloAgentPlan); zero means decide here.
	plan agents.Plan
}

// personalZaloAgentPlan decides who answers a personal Zalo turn. Staff in a
// 1:1 chat are the private tier; everyone else (customers, guests, groups) is
// the public tier, as on the OA.
func personalZaloAgentPlan(ctx context.Context, req personalZaloChatbotRequest, identity personalZaloERPIdentity) agents.Plan {
	tier := agents.TierPublic
	if identity.AgentType == "private" && req.ThreadType == personalZaloThreadUser {
		tier = agents.TierPrivate
	}
	plan, err := agents.PlanTurn(ctx, req.TenantID, req.ChannelID, tier)
	if err != nil {
		log.Printf("[agents] personal Zalo channel %s: %v; answering with the AI Engine prompt", req.ChannelID, err)
	}
	return plan
}

type personalZaloAIResult struct {
	reply         string
	images        personalZaloImageSelection
	grant         personalZaloDirectGrant
	handover      bool
	complaint     bool
	settingsError bool
	caseTurnID    string
	caseText      string
}

const personalZaloInternbotDraftGuide = "Đây là bản nháp cho nhân viên. Chỉ dùng công cụ tra cứu được phép. Không tiết lộ mã xác thực, mật khẩu, token hoặc thông tin không cần thiết. Không tuyên bố đã xác thực, chuyển tiếp, tạo phiếu hay thực hiện thao tác thay đổi dữ liệu. Nếu cần nhân viên xử lý, chỉ trả " + personalZaloAIHandoverMarker + "."

func (ai personalZaloAI) generate(ctx, flowCtx context.Context, channel models.Channel, req personalZaloChatbotRequest,
	settings map[string]models.AppSetting, options personalZaloAIOptions) (personalZaloAIResult, error) {
	result := personalZaloAIResult{complaint: options.complaint}
	flow := engine.ResolveTenantLangflowConfig(settings, ai.cfg)
	plan := options.plan
	if plan.Mode == "" {
		plan = personalZaloAgentPlan(ctx, req, options.identity)
	}
	if plan.Mode == agents.ModeSilent {
		// No agent of this channel's team serves the sender: people answer.
		return personalZaloAIResult{}, nil
	}

	switch {
	case options.guest:
		var response engine.LangflowResult
		var err error
		if options.draft {
			prompt := strings.TrimSpace(plan.BasePrompt(settings["ai_engine_system_prompt"].ValuePlain) + "\n\n" + personalZaloInternbotDraftGuide)
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

		var target personalZaloComplaintTarget
		staffComplaint := options.complaint && !options.draft && options.identity.AgentType == "private" && req.ThreadType == personalZaloThreadUser
		if staffComplaint {
			target = resolvePersonalZaloComplaintTarget(flowCtx, req, options.identity.ID, options.scope)
			if target.recorded {
				return personalZaloAIResult{reply: personalZaloCaseRecorded}, nil
			}
			if target.reply != "" && target.raw != "" {
				return personalZaloAIResult{reply: target.ask(ctx)}, nil
			}
			if target.raw == "" && target.reply == personalZaloComplaintNeedCode && personalZaloHandoverRequested(req.Text) &&
				!explicitComplaintProblem.MatchString(foldDebtSearch(req.Text)) {
				return personalZaloAIResult{handover: true}, nil
			}
		}
		grant, err := preparePersonalZaloDirectGrant(ctx, ai.cfg, req, options.identity, flow, options.scope, settings, plan)
		if err == nil && options.complaint && !options.draft {
			err = grant.allowComplaints(ctx, ai.cfg.EncryptionKey, req, options.identity, target)
		}
		if err != nil {
			result.settingsError = true
			return result, err
		}
		if options.draft {
			grant.draftGuide = personalZaloInternbotDraftGuide
		}
		if target.raw != "" {
			result.caseTurnID = target.pending.TurnID
			result.caseText = target.pending.Text
			grant.complaintGuide += "\nNhân viên đã trả lời xác nhận mã cho nội dung khiếu nại sau. Hãy ghi nhận ngay bằng complaint_record."
			req.Text = target.pending.Text + "\n\nMã khách đã xác nhận: " + target.code
		}
		result.grant = grant
		result.reply, result.images, err = grant.run(flowCtx, ai.scoped, channel, req)
		if staffComplaint {
			turnID := personalZaloComplaintTurnID(req)
			if result.caseTurnID != "" {
				turnID = result.caseTurnID
			}
			var refusal string
			if db.RedisClient != nil {
				refusal, _ = db.RedisClient.GetDel(ctx, personalZaloComplaintRefusalKey(turnID)).Result()
			}
			if target.reply != "" {
				if refusal != "" || strings.Contains(result.reply, personalZaloComplaintCodeMarker) ||
					workers.PersonalZaloComplaintNeedsHandover(flowCtx, req.TenantID, req.Text) {
					return personalZaloAIResult{reply: target.ask(ctx)}, nil
				}
				result.complaint = false
			} else if refusal != "" {
				if recorded, _ := customercases.TurnCaseState(ctx, req.TenantID, turnID); !recorded {
					return personalZaloAIResult{reply: refusal}, nil
				}
			}
		}
		if target.raw != "" {
			if recorded, _ := customercases.TurnCaseState(ctx, req.TenantID, result.caseTurnID); recorded {
				_ = completePersonalZaloComplaintConfirmation.Run(ctx, db.RedisClient, []string{target.key, target.receiptKey},
					target.raw, target.code, personalZaloComplaintConfirmTTL.Milliseconds()).Err()
			} else {
				return personalZaloAIResult{reply: "Dạ, em chưa ghi nhận được khiếu nại. Anh/chị thử nhắn lại mã khách đã xác nhận giúp em ạ."}, nil
			}
		}
		result.handover = (options.draft || personalZaloHasHandoverDestination(channel)) &&
			strings.TrimSpace(result.reply) == personalZaloAIHandoverMarker
		return result, err
	case options.complaint && flow.PublicFlowIDSource == engine.LangflowSourceTenant && flow.APIKeySource == engine.LangflowSourceTenant:
		response, err := runPersonalZaloGuest(flowCtx, ai.scoped, ai.cfg, flow, req, options.scope, true, options.staffGroupComplaint)
		result.reply, _ = engine.ParseKnowledgeImageReply(response.Text, 0)
		return result, err
	default:
		flowID, _ := plan.Flow(flow.PublicFlowID)
		if flow.APIURL == "" || flow.PublicFlowID == "" || strings.EqualFold(flow.PublicFlowID, flow.FlowID) ||
			strings.EqualFold(flowID, flow.FlowID) {
			return result, nil
		}
		prompt := strings.TrimSpace(plan.BasePrompt(settings["ai_engine_system_prompt"].ValuePlain))
		instruction := ""
		if prompt != "" {
			instruction = personalZaloHandoverPrompt(channel)
			if instruction != "" {
				prompt += "\n\n" + instruction
			}
		}
		input, prompt := engine.AddProductAdviceInstruction(req.Text, prompt)
		reply, err := ai.langflow.RunFlowWithOverrides(flowCtx, options.scope, options.scope,
			input, flow.APIURL, flow.APIKey, flowID, prompt)
		result.reply, _ = engine.ParseKnowledgeImageReply(reply, 0)
		result.handover = instruction != "" && options.canHandover && strings.TrimSpace(result.reply) == personalZaloAIHandoverMarker
		return result, err
	}
}
