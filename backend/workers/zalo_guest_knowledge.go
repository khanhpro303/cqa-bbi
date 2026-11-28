package workers

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/hibiken/asynq"
	"github.com/vietbui/chat-quality-agent/agents"
	"github.com/vietbui/chat-quality-agent/channels"
	"github.com/vietbui/chat-quality-agent/config"
	"github.com/vietbui/chat-quality-agent/db/models"
	"github.com/vietbui/chat-quality-agent/engine"
)

func handleZaloOAGuestKnowledge(
	ctx context.Context,
	cfg *config.Config,
	langflow *engine.LangflowClient,
	adapter *channels.ZaloOAAdapter,
	channel models.Channel,
	payload ZaloWebhookPayload,
	flow engine.LangflowConfig,
	text string,
) error {
	plan, err := agents.PlanTurn(ctx, channel.TenantID, channel.ID, agents.TierPublic)
	if err != nil {
		log.Printf("[agents] OA guest channel %s: %v; staying silent (%s)", channel.ID, err, plan.LogFields())
		return nil
	}
	if plan.Mode == agents.ModeSilent {
		return nil
	}
	// A guest is answered from documents only: an agent without
	// search_knowledge could not answer them at all.
	canAnswer := plan.KeepGuestCandidates(false)
	if !canAnswer {
		log.Printf("[agents] OA guest channel %s: no agent of team %s can search documents", channel.ID, plan.Selection.Team.ID)
	}
	if canAnswer && plan.NeedsRouting() {
		// Same coordinator as the main turn: the guest's question goes to
		// the customer agent whose knowledge it is.
		chatKey := engine.BuildSessionKey(channel.ID, payload.Sender.ID, "")
		plan.Stick(ctx, chatKey)
		from := plan.Selection.Agent.Key
		reason := plan.Route(ctx, channel.TenantID, agentRouterAsk(cfg, channel.TenantID), agents.RouterInput(text))
		log.Printf("[agents] OA guest channel %s: coordinator %s -> %s (%s) %s", channel.ID, from, plan.Selection.Agent.Key, plan.Selection.Reason, reason)
		plan.Remember(ctx, chatKey, time.Duration(cfg.ChatbotSessionTimeout)*time.Minute)
	}
	var permission, prompt string
	if canAnswer && plan.Mode == agents.ModeAgent {
		scope := engine.GroupPermissionContext{TenantID: channel.TenantID, ChannelID: channel.ID, AgentType: agents.TierPublic, DryRun: true}
		plan.Sign(&scope)
		permission, err = engine.SignPermissionToken(scope, cfg.EncryptionKey)
		if err != nil {
			return fmt.Errorf("sign OA guest agent scope: %w", err)
		}
		prompt = plan.BasePrompt("")
	}
	sessionScope := strings.Join([]string{"zalo-oa", channel.ID, payload.Sender.ID}, "\x00")
	result, err := engine.RunGuestKnowledgeWithScope(ctx, langflow, flow, cfg.EncryptionKey, channel.TenantID, sessionScope, text, permission, prompt, plan.GuestKnowledgeRules())
	if errors.Is(err, engine.ErrGuestKnowledgeNotConfigured) {
		return adapter.SendTextMessage(ctx, payload.Sender.ID, engine.GuestKnowledgeUnavailableReply)
	}
	if err != nil {
		if isLastAttempt(ctx) || engine.IsPermanentLangflowError(err) {
			if sendErr := adapter.SendMessage(ctx, payload.Sender.ID, botUnavailableMessage); sendErr != nil {
				log.Printf("[worker] failed to send guest knowledge fallback: %v", sendErr)
			}
			return fmt.Errorf("guest knowledge error: %w: %w", err, asynq.SkipRetry)
		}
		return fmt.Errorf("guest knowledge error: %w", err)
	}

	reply, imageIDs := engine.ParseKnowledgeImageReply(result.Text, engine.MaxKnowledgeReplyImages)
	reply = strings.TrimSpace(normalizeLangflowReply(reply))
	if reply == "" {
		reply = botEmptyAnswerMessage
		imageIDs = nil
	}
	if err := adapter.SendTextMessage(ctx, payload.Sender.ID, reply); err != nil {
		return fmt.Errorf("send guest knowledge reply: %w", err)
	}
	if reply != botEmptyAnswerMessage && len(imageIDs) > 0 {
		if _, err := sendKnowledgeImage(ctx, adapter, channel.TenantID, "public", payload.Sender.ID, "", imageIDs); err != nil {
			log.Printf("[knowledge] guest image delivery failed for channel %s: %v", channel.ID, err)
		}
	}

	if canAnswer && (reply != engine.GuestKnowledgeVerificationReply || result.HasUsage) {
		logChatbotAIUsage(channel.TenantID, channel.ID, payload.Sender.ID, "", "public", "", plan.AgentKey(), result, text, result.Text)
	}
	return nil
}
