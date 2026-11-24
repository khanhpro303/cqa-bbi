package workers

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/vietbui/chat-quality-agent/chatmemory"
	"github.com/vietbui/chat-quality-agent/config"
	"github.com/vietbui/chat-quality-agent/customercases"
	"github.com/vietbui/chat-quality-agent/engine"
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
// images are the pictures the customer sent with it, with what the AI read in
// them; the message is then stored as an image so cases can attach them.
func startChatMemoryTurn(ctx context.Context, turn chatmemory.Turn, payload ZaloWebhookPayload, text, senderName string, images ...customercases.ImageAttachment) context.Context {
	if !chatmemory.Enabled() {
		return ctx
	}
	contentType := "text"
	if strings.TrimSpace(payload.Message.Text) == "" && len(payload.Message.Attachments) > 0 {
		contentType = payload.Message.Attachments[0].Type
	}
	var attachments interface{}
	if len(images) > 0 {
		contentType = "image"
		attachments = images
	}
	chatmemory.RecordInbound(ctx, turn, chatmemory.Inbound{
		MessageID:   payload.Message.MsgID,
		Text:        text,
		ContentType: contentType,
		Attachments: attachments,
		SenderName:  senderName,
		SentAt:      webhookTime(payload.Timestamp),
		Raw:         withoutForeignImages(payload),
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
	memo := chatmemory.PromptContext(ctx, turn.TenantID, turn.ChannelID, subjectType, subjectID, turn.CustomerCode)
	if memo == "" {
		return systemPrompt
	}
	return systemPrompt + "\n\n" + memo
}

// appendComplaintContext adds the complaint guide (editable in AI Engine,
// complaint_bot_guide) and the chat's open cases to the system prompt, so
// the bot records complaints with complaint_record and knows what is still
// being handled. Skipped when no system prompt is set, like the other
// blocks, so the flow's own prompt is never replaced by this block alone.
func appendComplaintContext(ctx context.Context, tenantID, channelID, subjectType, subjectID, customerCode string, listCases bool, systemPrompt string) string {
	if strings.TrimSpace(systemPrompt) == "" {
		return systemPrompt
	}
	if !listCases {
		subjectID = "" // no cases listed
	}
	block := customercases.BotContext(ctx, tenantID, channelID, subjectType, subjectID, customerCode, func(cases string) string {
		return engine.BuildTaskPrompt(tenantID, engine.PromptComplaintBot, map[string]string{"cases": cases})
	})
	if strings.TrimSpace(block) == "" {
		return systemPrompt
	}
	return systemPrompt + "\n\n" + block
}

// withoutForeignImages is the payload as stored: image URLs that are not on
// Zalo's CDN are blanked, so no stored copy points elsewhere.
func withoutForeignImages(payload ZaloWebhookPayload) ZaloWebhookPayload {
	atts := make([]ZaloWebhookAttachment, len(payload.Message.Attachments))
	for i, a := range payload.Message.Attachments {
		if a.Type != "image" {
			atts[i] = a
			continue
		}
		if a.Payload.URL != "" && !allowedImageURL(a.Payload.URL) {
			a.Payload.URL = ""
		}
		if a.Payload.Thumbnail != "" && !allowedImageURL(a.Payload.Thumbnail) {
			a.Payload.Thumbnail = ""
		}
		atts[i] = a
	}
	payload.Message.Attachments = atts
	return payload
}
