package messengerlabels

import (
	"context"
	"errors"
	"time"

	"github.com/vietbui/chat-quality-agent/db/models"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// CaptureMissingIntakeLabels records the first complete label observation for
// each conversation, even when workflow tracking is disabled. The observation
// is immutable and is used only as an exclusion boundary, never as a stage.
func CaptureMissingIntakeLabels(ctx context.Context, database *gorm.DB, channel models.Channel, pageID string, reader Reader) (int, int, error) {
	database = database.WithContext(ctx)
	var capturedIDs []string
	if err := database.Model(&models.MessengerLabelSnapshot{}).
		Where("tenant_id = ? AND channel_id = ? AND intake_labels_captured_at IS NOT NULL", channel.TenantID, channel.ID).
		Pluck("conversation_id", &capturedIDs).Error; err != nil {
		return 0, 0, err
	}

	query := database.Select("id,tenant_id,channel_id,external_conversation_id,metadata").
		Where("tenant_id = ? AND channel_id = ?", channel.TenantID, channel.ID)
	if len(capturedIDs) > 0 {
		query = query.Where("id NOT IN ?", capturedIDs)
	}
	var conversations []models.Conversation
	if err := query.Order("id ASC").Limit(MaxConversations + 1).Find(&conversations).Error; err != nil {
		return 0, 0, err
	}
	if len(conversations) > MaxConversations {
		return 0, 0, ErrTooLarge
	}
	if len(conversations) == 0 {
		return 0, 0, nil
	}

	return Observe(ctx, reader, conversations, pageID, func(snapshot models.MessengerLabelSnapshot) error {
		if snapshot.Status != "success" || snapshot.IntakeLabels == nil || snapshot.IntakeLabelsCapturedAt == nil {
			return nil
		}
		return saveIntakeLabelSnapshot(database, snapshot)
	})
}

func saveIntakeLabelSnapshot(database *gorm.DB, snapshot models.MessengerLabelSnapshot) error {
	if snapshot.ConversationID == "" || snapshot.TenantID == "" || snapshot.ChannelID == "" || snapshot.IntakeLabels == nil || snapshot.IntakeLabelsCapturedAt == nil {
		return errors.New("invalid messenger intake label snapshot")
	}

	channel := models.Channel{ID: snapshot.ChannelID, TenantID: snapshot.TenantID}
	return database.Transaction(func(tx *gorm.DB) error {
		state, err := lockMessengerLabelState(tx, channel)
		if err != nil {
			return err
		}
		var previous models.MessengerLabelSnapshot
		err = tx.Select("conversation_id,intake_labels,intake_labels_captured_at").Where("conversation_id = ? AND tenant_id = ? AND channel_id = ?", snapshot.ConversationID, snapshot.TenantID, snapshot.ChannelID).First(&previous).Error
		if err == nil && previous.IntakeLabelsCapturedAt != nil {
			return nil
		}
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		seed := snapshot
		seed.Labels = "[]"
		seed.Status = "never"
		seed.ErrorKind = ""
		seed.CheckedAt = nil
		seed.AttemptedAt = time.Now()
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&seed).Error; err != nil {
			return err
		}
		result := tx.Model(&models.MessengerLabelSnapshot{}).
			Where("conversation_id = ? AND tenant_id = ? AND channel_id = ? AND intake_labels_captured_at IS NULL", snapshot.ConversationID, snapshot.TenantID, snapshot.ChannelID).
			Updates(map[string]interface{}{
				"psid":                      snapshot.PSID,
				"intake_labels":             *snapshot.IntakeLabels,
				"intake_labels_captured_at": *snapshot.IntakeLabelsCapturedAt,
			})
		if result.Error != nil {
			return result.Error
		}
		var stored models.MessengerLabelSnapshot
		if err := tx.Select("conversation_id,intake_labels,intake_labels_captured_at").Where("conversation_id = ? AND tenant_id = ? AND channel_id = ?", snapshot.ConversationID, snapshot.TenantID, snapshot.ChannelID).First(&stored).Error; err != nil {
			return err
		}
		return pruneNewIntakeRules(tx, &state, []models.MessengerLabelSnapshot{stored})
	})
}
