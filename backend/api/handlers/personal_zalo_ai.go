package handlers

import (
	"context"
	"errors"
	"log"
	"strings"
	"time"

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
		log.Printf("[agents] personal Zalo channel %s: %v; staying silent until routing recovers", req.ChannelID, err)
	}
	if sel, ok := plan.Agent(); ok && sel.Agent.Runtime == agents.RuntimeNative {
		// Personal Zalo turns keep their memory in Langflow (its chat log is
		// imported minutes later), so a native agent answers here on the
		// tier's Langflow flow with its prompt and tools.
		log.Printf("[agents] personal Zalo channel %s: native agent %s runs on Langflow here", req.ChannelID, sel.Agent.Key)
	}
	return plan
}

// routePersonalZaloAgent lets the channel's coordinator pick the agent for a
// personal Zalo message addressed to the bot when the team has more than one
// for the sender, leaning towards the agent already in the chat. Personal
// Zalo keeps its chat log in Langflow, so the coordinator reads the message
// alone; a turn without text (an image) stays with the current agent.
func routePersonalZaloAgent(ctx context.Context, cfg *config.Config, req personalZaloChatbotRequest, plan agents.Plan, sessionMinutes int) agents.Plan {
	if !plan.NeedsRouting() {
		return plan
	}
	chatKey := personalZaloSessionKey(req)
	plan.Stick(ctx, chatKey)
	if text := strings.TrimSpace(req.Text); text != "" {
		from := plan.Selection.Agent.Key
		reason := plan.Route(ctx, req.TenantID, agentRouterAsk(cfg, req.TenantID), agents.RouterInput(text))
		log.Printf("[agents] personal Zalo channel %s: coordinator %s -> %s (%s) %s",
			req.ChannelID, from, plan.Selection.Agent.Key, plan.Selection.Reason, reason)
	}
	plan.Remember(ctx, chatKey, time.Duration(sessionMinutes)*time.Minute)
	return plan
}

// personalZaloAgentScope signs only the selected specialist's tool and
// knowledge scope. It deliberately carries no ERP groups or customer code.
// Legacy turns return empty values and keep their existing preview behavior.
func personalZaloAgentScope(cfg *config.Config, req personalZaloChatbotRequest, agentType string, plan agents.Plan, dryRun bool) (permission, agentToken string, err error) {
	if plan.Mode != agents.ModeAgent {
		return "", "", nil
	}
	if cfg == nil || cfg.EncryptionKey == "" {
		return "", "", errors.New("specialist permission signing is unavailable")
	}
	p := engine.GroupPermissionContext{
		TenantID: req.TenantID, ChannelID: req.ChannelID, ZaloUserID: req.SenderID,
		AgentType: agentType, PersonalZalo: true, DryRun: dryRun,
	}
	plan.Sign(&p)
	permission, err = engine.SignPermissionToken(p, cfg.EncryptionKey)
	if err != nil {
		return "", "", err
	}
	agentToken, err = signKnowledgePreviewToken(cfg.EncryptionKey, req.TenantID, agentType)
	if err != nil {
		return "", "", err
	}
	return permission, agentToken, nil
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
			var permission, prompt string
			if plan.KeepGuestCandidates(false) {
				var scopeErr error
				permission, _, scopeErr = personalZaloAgentScope(ai.cfg, req, "public", plan, true)
				if scopeErr != nil {
					result.settingsError = true
					return result, scopeErr
				}
				prompt = strings.TrimSpace(plan.BasePrompt(settings["ai_engine_system_prompt"].ValuePlain) + "\n\n" +
					engine.TaskPromptText(req.TenantID, engine.PromptPZInternbotDraft))
			}
			response, err = engine.RunPersonalZaloGuestDraft(flowCtx, ai.scoped, flow, ai.cfg.EncryptionKey,
				req.TenantID, options.scope, req.Text, permission, prompt, plan.GuestKnowledgeRules())
		} else {
			response, err = runPersonalZaloGuest(flowCtx, ai.scoped, ai.cfg, flow, req, options.scope, options.complaint, false, plan)
		}
		if errors.Is(err, engine.ErrGuestKnowledgeNotConfigured) {
			result.reply = engine.GuestKnowledgeUnavailableReply
			return result, nil
		}
		if plan.UsesRule(engine.PromptPZKnowledgeImages) {
			result.reply, result.images.knowledgeIDs = engine.ParsePersonalZaloKnowledgeImageReply(response.Text)
		} else {
			result.reply, _ = engine.ParseKnowledgeImageReply(response.Text, 0)
		}
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
			grant.draftGuide = engine.TaskPromptText(req.TenantID, engine.PromptPZInternbotDraft)
		}
		if target.raw != "" {
			result.caseTurnID = target.pending.TurnID
			result.caseText = target.pending.Text
			grant.complaintGuide += "\n" + engine.TaskPromptText(req.TenantID, engine.PromptPZStaffConfirmed)
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
		response, err := runPersonalZaloGuest(flowCtx, ai.scoped, ai.cfg, flow, req, options.scope, true, options.staffGroupComplaint, plan)
		result.reply, _ = engine.ParseKnowledgeImageReply(response.Text, 0)
		return result, err
	default:
		flowID, _ := plan.Flow(flow.PublicFlowID)
		if flow.APIURL == "" || flow.PublicFlowID == "" || canonicalAIFlowID(flow.PublicFlowID) == canonicalAIFlowID(flow.FlowID) ||
			canonicalAIFlowID(flowID) == canonicalAIFlowID(flow.FlowID) {
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
		input := req.Text
		if plan.UsesRule(engine.PromptProductAdvice) {
			input, prompt = engine.AddProductAdviceInstruction(req.TenantID, input, prompt)
		}
		var reply string
		var err error
		if plan.Mode == agents.ModeAgent {
			permission, agentToken, scopeErr := personalZaloAgentScope(ai.cfg, req, "public", plan, false)
			if scopeErr != nil {
				result.settingsError = true
				return result, scopeErr
			}
			response, runErr := ai.scoped.RunFlowWithCustomerUsage(flowCtx, options.scope, options.scope,
				input, flow.APIURL, flow.APIKey, flowID, "", permission, agentToken, prompt)
			reply, err = response.Text, runErr
		} else {
			reply, err = ai.langflow.RunFlowWithOverrides(flowCtx, options.scope, options.scope,
				input, flow.APIURL, flow.APIKey, flowID, prompt)
		}
		result.reply, _ = engine.ParseKnowledgeImageReply(reply, 0)
		result.handover = instruction != "" && options.canHandover && strings.TrimSpace(result.reply) == personalZaloAIHandoverMarker
		return result, err
	}
}
