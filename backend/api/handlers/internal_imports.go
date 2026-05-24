package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/vietbui/chat-quality-agent/api/contracts"
	"github.com/vietbui/chat-quality-agent/channels"
	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/db/models"
	"github.com/vietbui/chat-quality-agent/engine"
)

func NewPersonalZaloImportHandler(ingestor engine.ConversationIngester) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req contracts.PersonalZaloImportRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request", "details": err.Error()})
			return
		}
		if requestID, _ := c.Get("internal_request_id"); requestID != nil && requestID.(string) != req.RequestID {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "request_id_mismatch"})
			return
		}

		var channel models.Channel
		if err := db.DB.Where("id = ? AND tenant_id = ?", req.ChannelID, req.TenantID).First(&channel).Error; err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "channel_not_found"})
			return
		}
		if !channel.IsActive {
			c.JSON(http.StatusForbidden, gin.H{
				"error":   "channel_is_inactive",
				"message": "Kênh chat đã tạm dừng hoạt động.",
			})
			return
		}
		if channel.ChannelType != "personal_zalo_import" {
			c.JSON(http.StatusConflict, gin.H{"error": "channel_type_not_supported"})
			return
		}

		if req.AccountExternalID != "" && channel.ExternalID != "" && channel.ExternalID != req.AccountExternalID {
			c.JSON(http.StatusConflict, gin.H{"error": "channel_account_mismatch"})
			return
		}

		resolvedOwner, updatedMetadata, err := resolvePersonalZaloAccountOwner(req.TenantID, &channel, req.AccountExternalID)
		if err != nil {
			c.JSON(http.StatusConflict, gin.H{"error": "account_owner_resolution_failed", "details": err.Error()})
			return
		}
		if updatedMetadata != "" && updatedMetadata != channel.Metadata {
			if err := db.DB.Model(&models.Channel{}).
				Where("id = ? AND tenant_id = ?", channel.ID, channel.TenantID).
				Update("metadata", updatedMetadata).Error; err == nil {
				channel.Metadata = updatedMetadata
			}
		}

		var metadata struct {
			SyncScope string `json:"sync_scope"`
		}
		if channel.Metadata != "" {
			_ = json.Unmarshal([]byte(channel.Metadata), &metadata)
		}

		totalProcessed := 0
		totalInserted := 0
		totalDeduplicated := 0
		for _, bundle := range req.Conversations {
			threadType := bundle.Conversation.ThreadType
			if threadType == "" {
				threadType = "user"
			}
			if metadata.SyncScope == "direct" && threadType == "group" {
				continue
			}
			if metadata.SyncScope == "group" && threadType == "user" {
				continue
			}

			result, err := ingestor.IngestConversationBatch(
				req.TenantID,
				req.ChannelID,
				toSyncedConversation(bundle.Conversation, req.AccountExternalID, resolvedOwner),
				toSyncedMessages(bundle.Messages),
			)
			if err != nil {
				db.LogActivity(
					req.TenantID,
					"",
					"system",
					"import.personal_zalo.error",
					"channel",
					req.ChannelID,
					"Lỗi đồng bộ Zalo cá nhân",
					err.Error(),
					c.ClientIP(),
				)
				c.JSON(http.StatusInternalServerError, gin.H{"error": "import_failed", "details": err.Error()})
				return
			}
			totalProcessed += result.MessagesProcessed
			totalInserted += result.MessagesInserted
			totalDeduplicated += result.MessagesDeduplicated
		}

		if totalInserted > 0 {
			db.LogActivity(
				req.TenantID,
				"",
				"system",
				"import.personal_zalo",
				"channel",
				req.ChannelID,
				fmt.Sprintf("Zalo import: %d conversations, %d new, %d dedup",
					len(req.Conversations), totalInserted, totalDeduplicated),
				"",
				c.ClientIP(),
			)
		}
		updates := map[string]interface{}{
			"last_sync_at":     req.ImportedAt.UTC(),
			"last_sync_status": "success",
			"last_sync_error":  "",
			"updated_at":       time.Now(),
		}
		if req.AccountExternalID != "" && channel.ExternalID == "" {
			updates["external_id"] = req.AccountExternalID
		}
		db.DB.Model(&models.Channel{}).Where("id = ?", req.ChannelID).Updates(updates)

		c.JSON(http.StatusAccepted, gin.H{
			"message":               "imported",
			"request_id":            req.RequestID,
			"conversations":         len(req.Conversations),
			"messages_processed":    totalProcessed,
			"messages_inserted":     totalInserted,
			"messages_deduplicated": totalDeduplicated,
		})
	}
}

func toSyncedConversation(conv contracts.ImportConversation, accountExternalID string, owner *contracts.PersonalZaloAccountOwner) channels.SyncedConversation {
	metadata := make(map[string]interface{}, len(conv.Metadata)+5)
	for k, v := range conv.Metadata {
		metadata[k] = v
	}
	threadType := conv.ThreadType
	if threadType == "" {
		threadType = "user"
	}
	metadata["thread_type"] = threadType
	if accountExternalID != "" {
		metadata["account_external_id"] = accountExternalID
	}
	if owner != nil {
		metadata["owner_user_id"] = owner.UserID
		if owner.UserName != "" {
			metadata["owner_name"] = owner.UserName
		}
		if owner.UserEmail != "" {
			metadata["owner_email"] = owner.UserEmail
		}
	}

	return channels.SyncedConversation{
		ExternalID:     composeImportConversationID(threadType, conv.ExternalID),
		ExternalUserID: conv.ExternalUserID,
		CustomerName:   conv.CustomerName,
		LastMessageAt:  conv.LastMessageAt,
		Metadata:       metadata,
	}
}

func toSyncedMessages(messages []contracts.ImportMessage) []channels.SyncedMessage {
	result := make([]channels.SyncedMessage, 0, len(messages))
	for _, msg := range messages {
		result = append(result, channels.SyncedMessage{
			ExternalID:  msg.ExternalID,
			SenderType:  msg.SenderType,
			SenderName:  msg.SenderName,
			Content:     msg.Content,
			ContentType: msg.ContentType,
			Attachments: toAttachments(msg.Attachments),
			SentAt:      msg.SentAt,
			RawData:     msg.RawData,
		})
	}
	return result
}

func toAttachments(attachments []contracts.ImportAttachment) []channels.Attachment {
	result := make([]channels.Attachment, 0, len(attachments))
	for _, att := range attachments {
		result = append(result, channels.Attachment{
			Type:      att.Type,
			URL:       att.URL,
			Name:      att.Name,
			LocalPath: att.LocalPath,
		})
	}
	return result
}

func composeImportConversationID(threadType, externalID string) string {
	if threadType == "" {
		threadType = "user"
	}
	return threadType + ":" + externalID
}
