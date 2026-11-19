package workers

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/vietbui/chat-quality-agent/chatmemory"
	"github.com/vietbui/chat-quality-agent/config"
)

// chatMemoryTurn describes the chat a webhook belongs to for chat memory.
// agentType follows the routing rule used for the Langflow call: "private"
// only for whitelisted staff outside a GMF group.
func chatMemoryTurn(tenantID, channelID, sessionID, zaloUserID, zaloGroupID, groupName, customerName, customerCode string, staffOutsideGroup bool) chatmemory.Turn {
	agentType := "public"
	if staffOutsideGroup {
		agentType = "private"
	}
	return chatmemory.Turn{
		TenantID:     tenantID,
		ChannelID:    channelID,
		SessionID:    sessionID,
		ZaloUserID:   zaloUserID,
		ZaloGroupID:  zaloGroupID,
		GroupName:    groupName,
		CustomerName: customerName,
		CustomerCode: customerCode,
		AgentType:    agentType,
	}
}

// startChatMemoryTurn records the received message and returns ctx carrying
// the turn, so the replies sent with it are recorded in the same session.
func startChatMemoryTurn(ctx context.Context, turn chatmemory.Turn, payload ZaloWebhookPayload, text, senderName string) context.Context {
	if !chatmemory.Enabled() {
		return ctx
	}
	contentType := "text"
	if strings.TrimSpace(payload.Message.Text) == "" && len(payload.Message.Attachments) > 0 {
		contentType = payload.Message.Attachments[0].Type
	}
	chatmemory.RecordInbound(ctx, turn, chatmemory.Inbound{
		MessageID:   payload.Message.MsgID,
		Text:        text,
		ContentType: contentType,
		SenderName:  senderName,
		SentAt:      webhookTime(payload.Timestamp),
		Raw:         payload,
	})
	return chatmemory.WithTurn(ctx, turn)
}

// webhookTime parses Zalo's millisecond timestamp, falling back to now.
func webhookTime(ts string) time.Time {
	ms, err := strconv.ParseInt(strings.TrimSpace(ts), 10, 64)
	if err != nil || ms <= 0 {
		return time.Now()
	}
	t := time.UnixMilli(ms)
	// Reject clocks far off from ours (bad payloads, replays of old events).
	if d := time.Since(t); d > 24*time.Hour || d < -time.Hour {
		return time.Now()
	}
	return t
}

// appendChatMemoryContext adds what earlier sessions taught about this chat to
// the system prompt, when the channel turns on "bot uses customer profiles".
// An empty base prompt means the flow uses its own prompt, which this tweak
// would replace, so nothing is added then.
func appendChatMemoryContext(ctx context.Context, cfg *config.Config, channelMetadata, systemPrompt string, turn chatmemory.Turn) string {
	if cfg == nil || !cfg.ChatMemory.Enabled || strings.TrimSpace(systemPrompt) == "" {
		return systemPrompt
	}
	if !chatmemory.ChannelSettingsFrom(channelMetadata).ProfileInPrompt {
		return systemPrompt
	}
	subjectType, subjectID := turn.Subject()
	memo := chatmemory.PromptContext(ctx, turn.TenantID, turn.ChannelID, subjectType, subjectID)
	if memo == "" {
		return systemPrompt
	}
	return systemPrompt + "\n\n" + memo
}
