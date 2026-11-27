package handlers

import (
	"context"
	"errors"
	"log"
	"net/http"
	"regexp"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/gin-gonic/gin"
	"github.com/vietbui/chat-quality-agent/channels"
	"github.com/vietbui/chat-quality-agent/config"
	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/db/models"
	"github.com/vietbui/chat-quality-agent/engine"
	"github.com/vietbui/chat-quality-agent/workers"
	"gorm.io/gorm"
)

const personalZaloTextLimit = 2000

var personalZaloUIDPattern = regexp.MustCompile(`^[0-9]{1,64}$`)

type personalZaloChatbotRequest struct {
	SchemaVersion     string    `json:"schema_version" binding:"required,oneof=personal-zalo-chatbot-v1 personal-zalo-chatbot-v2"`
	RequestID         string    `json:"request_id" binding:"required,max=64"`
	TenantID          string    `json:"tenant_id" binding:"required,max=36"`
	ChannelID         string    `json:"channel_id" binding:"required,max=36"`
	AccountExternalID string    `json:"account_external_id" binding:"required"`
	ThreadID          string    `json:"thread_id" binding:"required"`
	ThreadType        string    `json:"thread_type"`
	SenderID          string    `json:"sender_id"`
	MessageID         string    `json:"message_id" binding:"required,max=128"`
	SentAt            time.Time `json:"sent_at" binding:"required"`
	Text              string    `json:"text" binding:"required"`
	DeliveryReceipt   string    `json:"delivery_receipt" binding:"omitempty,uuid4"`
}

// NewPersonalZaloChatbotHandler receives live text turns from the
// authenticated gateway. The gateway owns the Zalo session and sends the reply.
func NewPersonalZaloChatbotHandler(cfg *config.Config) gin.HandlerFunc {
	langflow := engine.NewLangflowClient(cfg)
	scopedLangflow := engine.NewLangflowPreviewClient(cfg)
	settingKeys := append([]string{"chatbot_active", "ai_engine_system_prompt"}, engine.LangflowSettingKeys...)

	return func(c *gin.Context) {
		var req personalZaloChatbotRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request"})
			return
		}
		if c.GetString("internal_request_id") != req.RequestID {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "request_id_mismatch"})
			return
		}
		req.Text = strings.TrimSpace(req.Text)
		age := time.Since(req.SentAt)
		if req.Text == "" || personalZaloTextBoundary(req.Text, personalZaloTextLimit) != len(req.Text) ||
			!personalZaloUIDPattern.MatchString(req.AccountExternalID) || !normalizePersonalZaloChatbotRequest(&req) ||
			(req.DeliveryReceipt == "" && (age > 2*time.Minute || age < -30*time.Second)) ||
			(req.DeliveryReceipt != "" && req.ThreadType != personalZaloThreadGroup) {
			c.Status(http.StatusNoContent)
			return
		}

		ctx, cancel := context.WithTimeout(c.Request.Context(), 45*time.Second)
		defer cancel()
		channel, enabled, err := personalZaloChatbotChannel(ctx, req)
		if err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "chatbot_settings_unavailable"})
			return
		}
		if !enabled {
			c.Status(http.StatusNoContent)
			return
		}
		settings, err := loadPersonalZaloChatbotSettings(ctx, req.TenantID, settingKeys...)
		if err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "chatbot_settings_unavailable"})
			return
		}
		if !personalZaloChatbotMasterEnabled(settings) {
			c.Status(http.StatusNoContent)
			return
		}

		sessionConfig, err := parsePersonalZaloSessionConfig(channel, cfg)
		if err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "chatbot_session_unavailable"})
			return
		}
		if req.DeliveryReceipt != "" {
			confirmPersonalZaloGroupReply(ctx, sessionConfig, req)
			c.Status(http.StatusNoContent)
			return
		}
		addressed := req.ThreadType == personalZaloThreadUser || personalZaloGroupMessageAddressed(req.Text, sessionConfig)
		groupTurn := preparePersonalZaloGroupTurn(ctx, channel, sessionConfig, req, addressed, workers.PersonalZaloGroupNeedsReply)
		handover := personalZaloHandoverRequested(req.Text) && (addressed || (groupTurn.Inferred && !groupTurn.Staff))
		var turn personalZaloTurn
		if !handover {
			turn, err = planPersonalZaloTurn(ctx, sessionConfig, req, groupTurn.Inferred)
			if err != nil {
				c.JSON(http.StatusServiceUnavailable, gin.H{"error": "chatbot_session_unavailable"})
				return
			}
		}
		reply := turn.Reply
		var imageIDs []string
		if turn.RunFlow {
			flow := engine.ResolveTenantLangflowConfig(settings, cfg)
			if flow.APIURL == "" || flow.PublicFlowID == "" {
				c.Status(http.StatusNoContent)
				return
			}
			systemPrompt := strings.TrimSpace(settings["ai_engine_system_prompt"].ValuePlain)
			instruction := ""
			if systemPrompt != "" {
				instruction = personalZaloHandoverPrompt(channel)
			}
			if instruction != "" {
				systemPrompt = strings.TrimSpace(systemPrompt + "\n\n" + instruction)
			}
			// Direct turns can use public knowledge with tenant-owned Langflow
			// credentials. Personal turns carry no OA whitelist or ERP grants.
			imageLimit := 0
			if req.ThreadType == personalZaloThreadUser && cfg.EncryptionKey != "" &&
				flow.PublicFlowIDSource == engine.LangflowSourceTenant && flow.APIKeySource == engine.LangflowSourceTenant {
				token, tokenErr := signKnowledgePreviewToken(cfg.EncryptionKey, req.TenantID, "public")
				if tokenErr != nil {
					c.JSON(http.StatusServiceUnavailable, gin.H{"error": "chatbot_settings_unavailable"})
					return
				}
				input, prompt := engine.AddKnowledgeImageInstruction(req.Text, systemPrompt)
				result, flowErr := scopedLangflow.RunFlowWithCustomerUsage(ctx, turn.FlowScope, turn.FlowScope,
					input, flow.APIURL, flow.APIKey, flow.PublicFlowID, "", "", token, prompt)
				reply, err = result.Text, flowErr
				imageLimit = engine.MaxKnowledgeReplyImages
			} else {
				reply, err = langflow.RunFlowWithOverrides(ctx, turn.FlowScope, turn.FlowScope,
					req.Text, flow.APIURL, flow.APIKey, flow.PublicFlowID, systemPrompt)
			}
			if err != nil {
				c.JSON(http.StatusBadGateway, gin.H{"error": "chatbot_reply_failed"})
				return
			}
			reply, imageIDs = engine.ParseKnowledgeImageReply(reply, imageLimit)
			if req.ThreadType == personalZaloThreadUser && strings.Contains(reply, "[RICH_MESSAGE_SENT]") {
				c.Status(http.StatusNoContent)
				return
			}
			handover = instruction != "" && (addressed || !groupTurn.Staff) && strings.TrimSpace(reply) == personalZaloAIHandoverMarker
			reply = personalZaloStripHandoverMarker(reply)
		}
		reply = strings.TrimSpace(reply)
		if !handover && reply == "" {
			c.Status(http.StatusNoContent)
			return
		}

		// A channel or tenant can be paused while Langflow is generating.
		latestChannel, enabled, err := personalZaloChatbotChannel(ctx, req)
		if err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "chatbot_settings_unavailable"})
			return
		}
		if !enabled {
			c.Status(http.StatusNoContent)
			return
		}
		settings, err = loadPersonalZaloChatbotSettings(ctx, req.TenantID, "chatbot_active")
		if err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "chatbot_settings_unavailable"})
			return
		}
		if !personalZaloChatbotMasterEnabled(settings) {
			c.Status(http.StatusNoContent)
			return
		}
		if groupTurn.Speaker != "" {
			latestSession, err := parsePersonalZaloSessionConfig(latestChannel, cfg)
			if err != nil || (groupTurn.Inferred && !latestSession.GroupAssistantMode) ||
				(groupTurn.Inferred && groupTurn.Staff && latestSession.GroupStaffCallOnly && !sessionConfig.GroupStaffCallOnly) {
				c.Status(http.StatusNoContent)
				return
			}
			sessionConfig = latestSession
		}
		if handover {
			closePersonalZaloSession(ctx, req)
			clearPersonalZaloStaffFollowup(ctx, req)
			reply = sendPersonalZaloHandover(ctx, cfg, latestChannel, req)
		}

		if personalZaloTextBoundary(reply, personalZaloTextLimit) < len(reply) {
			reply = strings.TrimSpace(reply[:personalZaloTextBoundary(reply, personalZaloTextLimit-1)]) + "…"
		}
		response := gin.H{"reply": reply}
		if !handover && len(imageIDs) > 0 {
			if images := personalZaloKnowledgeReplyImages(ctx, req.TenantID, imageIDs); len(images) > 0 {
				response["images"] = images
			}
		}
		if !handover && groupTurn.Speaker != "" && sessionConfig.GroupAssistantMode {
			if receipt := awaitPersonalZaloGroupDelivery(ctx, req, groupTurn, reply); receipt != "" {
				response["delivery_receipt"] = receipt
			}
		}
		c.JSON(http.StatusOK, response)
	}
}

type personalZaloKnowledgeImage struct {
	ID string `json:"id"`
	channels.ZaloImage
}

func personalZaloKnowledgeReplyImages(ctx context.Context, tenantID string, imageIDs []string) []personalZaloKnowledgeImage {
	rows, err := engine.LoadDeliverableKnowledgeImages(ctx, tenantID, "public", imageIDs)
	if err != nil {
		log.Printf("[personal-zalo] failed to load knowledge image tenant=%s: %v", tenantID, err)
		return nil
	}
	var images []personalZaloKnowledgeImage
	for _, row := range rows {
		attachment, err := channels.PrepareZaloImage(row.Data)
		if err != nil {
			log.Printf("[personal-zalo] failed to prepare knowledge image %s: %v", row.ID, err)
			continue
		}
		current, err := engine.LoadDeliverableKnowledgeReplyImageMetadata(ctx, tenantID, "public", row.ID)
		if err != nil {
			log.Printf("[personal-zalo] failed to recheck knowledge image %s: %v", row.ID, err)
		}
		if len(current) == 0 {
			continue
		}
		images = append(images, personalZaloKnowledgeImage{ID: row.ID, ZaloImage: attachment})
	}
	return images
}

func personalZaloChatbotChannel(ctx context.Context, req personalZaloChatbotRequest) (models.Channel, bool, error) {
	var channel models.Channel
	err := db.DB.WithContext(ctx).Select("id", "tenant_id", "channel_type", "external_id", "metadata").
		Where("id = ? AND tenant_id = ? AND channel_type = ? AND is_active = ? AND personal_zalo_auto_reply_enabled = ?",
			req.ChannelID, req.TenantID, "personal_zalo_import", true, true).
		Take(&channel).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return channel, false, nil
	}
	if err != nil {
		return channel, false, err
	}
	if channel.ExternalID != "" && channel.ExternalID != req.AccountExternalID {
		return channel, false, nil
	}
	metadata, err := decodeMetadataObject(channel.Metadata)
	if err != nil {
		return channel, false, err
	}
	scope, _ := metadata["sync_scope"].(string)
	if req.ThreadType == personalZaloThreadGroup {
		enabled, _ := metadata["personal_zalo_group_reply_enabled"].(bool)
		if !enabled || scope == "direct" {
			return channel, false, nil
		}

		// Keep the group with its linked OA even while the OA is paused or the
		// CRM group is archived. The personal bot must not take over.
		var groupIDs []string
		err := db.DB.WithContext(ctx).Model(&models.CRMGroup{}).Unscoped().
			Joins("JOIN channels AS oa ON oa.id = crm_groups.channel_id AND oa.tenant_id = crm_groups.tenant_id").
			Where("crm_groups.tenant_id = ? AND crm_groups.zalo_group_id = ? AND oa.channel_type = ?",
				req.TenantID, req.ThreadID, "zalo_oa").
			Limit(1).Pluck("crm_groups.id", &groupIDs).Error
		return channel, err == nil && len(groupIDs) == 0, err
	}
	return channel, scope != "group", nil
}

func loadPersonalZaloChatbotSettings(ctx context.Context, tenantID string, keys ...string) (map[string]models.AppSetting, error) {
	var rows []models.AppSetting
	err := db.DB.WithContext(ctx).Where("tenant_id = ? AND setting_key IN ?", tenantID, keys).Find(&rows).Error
	if err != nil {
		return nil, err
	}
	settings := make(map[string]models.AppSetting, len(rows))
	for _, row := range rows {
		settings[row.SettingKey] = row
	}
	return settings, nil
}

func personalZaloChatbotMasterEnabled(settings map[string]models.AppSetting) bool {
	setting, exists := settings["chatbot_active"]
	return !exists || setting.ValuePlain == "true"
}

func personalZaloTextBoundary(text string, limit int) int {
	for index, r := range text {
		limit -= utf16.RuneLen(r)
		if limit < 0 {
			return index
		}
	}
	return len(text)
}
