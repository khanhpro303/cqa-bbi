package workers

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"

	"github.com/hibiken/asynq"
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
	sessionScope := strings.Join([]string{"zalo-oa", channel.ID, payload.Sender.ID}, "\x00")
	result, err := engine.RunGuestKnowledge(ctx, langflow, flow, cfg.EncryptionKey, channel.TenantID, sessionScope, text)
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

	if reply != engine.GuestKnowledgeVerificationReply || result.HasUsage {
		logChatbotAIUsage(channel.TenantID, channel.ID, payload.Sender.ID, "", "public", "", result, text, result.Text)
	}
	return nil
}
