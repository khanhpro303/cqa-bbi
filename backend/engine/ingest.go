package engine

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/vietbui/chat-quality-agent/channels"
	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/db/models"
	"github.com/vietbui/chat-quality-agent/messengerintake"
	"github.com/vietbui/chat-quality-agent/pkg"
	"gorm.io/gorm"
)

type ConversationIngester interface {
	EnsureConversation(tenantID, channelID string, conv channels.SyncedConversation) (string, error)
	IngestMessages(tenantID, conversationID string, messages []channels.SyncedMessage) (IngestResult, error)
	RecountConversationMessages(conversationID string) error
	IngestConversationBatch(tenantID, channelID string, conv channels.SyncedConversation, messages []channels.SyncedMessage) (IngestResult, error)
}

type IngestResult struct {
	ConversationID       string
	MessagesProcessed    int
	MessagesInserted     int
	MessagesDeduplicated int
}

type IngestService struct{}

func NewIngestService() *IngestService {
	return &IngestService{}
}

func (s *IngestService) EnsureConversation(tenantID, channelID string, conv channels.SyncedConversation) (string, error) {
	return s.upsertConversation(db.DB, tenantID, channelID, conv)
}

func (s *IngestService) IngestMessages(tenantID, conversationID string, messages []channels.SyncedMessage) (IngestResult, error) {
	inserted := 0
	deduped := 0
	for _, msg := range messages {
		wasInserted, err := s.upsertMessage(db.DB, tenantID, conversationID, msg)
		if err != nil {
			return IngestResult{}, err
		}
		if wasInserted {
			inserted++
		} else {
			deduped++
		}
	}

	return IngestResult{
		ConversationID:       conversationID,
		MessagesProcessed:    len(messages),
		MessagesInserted:     inserted,
		MessagesDeduplicated: deduped,
	}, nil
}

func (s *IngestService) RecountConversationMessages(conversationID string) error {
	var count int64
	if err := db.DB.Model(&models.Message{}).Where("conversation_id = ?", conversationID).Count(&count).Error; err != nil {
		return err
	}
	return db.DB.Model(&models.Conversation{}).Where("id = ?", conversationID).Update("message_count", count).Error
}

func (s *IngestService) IngestConversationBatch(tenantID, channelID string, conv channels.SyncedConversation, messages []channels.SyncedMessage) (IngestResult, error) {
	if tenantID == "" || channelID == "" {
		return IngestResult{}, errors.New("tenantID and channelID are required")
	}

	var result IngestResult
	err := db.DB.Transaction(func(tx *gorm.DB) error {
		convID, err := s.upsertConversation(tx, tenantID, channelID, conv)
		if err != nil {
			return err
		}

		inserted := 0
		deduped := 0
		for _, msg := range messages {
			wasInserted, err := s.upsertMessage(tx, tenantID, convID, msg)
			if err != nil {
				return err
			}
			if wasInserted {
				inserted++
			} else {
				deduped++
			}
		}

		if err := s.recountConversationMessagesTx(tx, convID); err != nil {
			return err
		}

		result = IngestResult{
			ConversationID:       convID,
			MessagesProcessed:    len(messages),
			MessagesInserted:     inserted,
			MessagesDeduplicated: deduped,
		}
		return nil
	})
	if err != nil {
		return IngestResult{}, err
	}

	return result, nil
}

func (s *IngestService) recountConversationMessagesTx(tx *gorm.DB, conversationID string) error {
	var count int64
	if err := tx.Model(&models.Message{}).Where("conversation_id = ?", conversationID).Count(&count).Error; err != nil {
		return err
	}
	return tx.Model(&models.Conversation{}).Where("id = ?", conversationID).Update("message_count", count).Error
}

func (s *IngestService) upsertConversation(tx *gorm.DB, tenantID, channelID string, conv channels.SyncedConversation) (string, error) {
	var existing models.Conversation
	result := tx.Where("tenant_id = ? AND channel_id = ? AND external_conversation_id = ?",
		tenantID, channelID, conv.ExternalID).First(&existing)

	metadataJSON, _ := json.Marshal(conv.Metadata)

	if result.Error == nil {
		updates := map[string]interface{}{
			"customer_name":   conv.CustomerName,
			"last_message_at": conv.LastMessageAt,
			"metadata":        string(metadataJSON),
			"updated_at":      time.Now(),
		}
		if conv.ExternalUserID != "" {
			updates["external_user_id"] = conv.ExternalUserID
		}
		if err := tx.Model(&existing).Updates(updates).Error; err != nil {
			return "", err
		}
		if err := messengerintake.BindConversation(tx, tenantID, channelID, conv.ExternalUserID, existing.ID); err != nil {
			return "", err
		}
		return existing.ID, nil
	}
	if !errors.Is(result.Error, gorm.ErrRecordNotFound) {
		return "", result.Error
	}

	newConv := models.Conversation{
		ID:                     pkg.NewUUID(),
		TenantID:               tenantID,
		ChannelID:              channelID,
		ExternalConversationID: conv.ExternalID,
		ExternalUserID:         conv.ExternalUserID,
		CustomerName:           conv.CustomerName,
		LastMessageAt:          &conv.LastMessageAt,
		MessageCount:           0,
		Metadata:               string(metadataJSON),
		CreatedAt:              time.Now(),
		UpdatedAt:              time.Now(),
	}
	if err := tx.Create(&newConv).Error; err != nil {
		return "", err
	}
	if err := messengerintake.BindConversation(tx, tenantID, channelID, conv.ExternalUserID, newConv.ID); err != nil {
		return "", err
	}
	return newConv.ID, nil
}

func (s *IngestService) upsertMessage(tx *gorm.DB, tenantID, conversationID string, msg channels.SyncedMessage) (bool, error) {
	var existing models.Message
	result := tx.Where("tenant_id = ? AND conversation_id = ? AND external_message_id = ?",
		tenantID, conversationID, msg.ExternalID).First(&existing)
	if result.Error == nil {
		hasLocalPath := false
		for _, att := range msg.Attachments {
			if att.LocalPath != "" {
				hasLocalPath = true
				break
			}
		}
		if hasLocalPath {
			attachmentsJSON, _ := json.Marshal(msg.Attachments)
			if err := tx.Model(&existing).Update("attachments", string(attachmentsJSON)).Error; err != nil {
				return false, err
			}
		}
		return false, nil
	}
	if !errors.Is(result.Error, gorm.ErrRecordNotFound) {
		return false, result.Error
	}

	attachmentsJSON, _ := json.Marshal(msg.Attachments)
	rawDataJSON, _ := json.Marshal(msg.RawData)

	message := models.Message{
		ID:                pkg.NewUUID(),
		TenantID:          tenantID,
		ConversationID:    conversationID,
		ExternalMessageID: msg.ExternalID,
		SenderType:        msg.SenderType,
		SenderName:        msg.SenderName,
		Content:           msg.Content,
		ContentType:       msg.ContentType,
		Attachments:       string(attachmentsJSON),
		SentAt:            msg.SentAt,
		RawData:           string(rawDataJSON),
		CreatedAt:         time.Now(),
	}
	if err := tx.Create(&message).Error; err != nil {
		return false, err
	}
	return true, nil
}
