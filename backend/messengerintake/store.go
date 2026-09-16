package messengerintake

import (
	"errors"
	"time"

	"github.com/vietbui/chat-quality-agent/db/models"
	"github.com/vietbui/chat-quality-agent/pkg"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type Referral struct {
	PageID       string
	PSID         string
	AdID         string
	Ref          string
	Source       string
	ReferralType string
	MessageID    string
	RawJSON      string
	CapturedAt   time.Time
}

// CaptureReferral persists only the first referral for a Page-scoped user.
// Later messages or ads must never rewrite the intake source.
func CaptureReferral(database *gorm.DB, channel models.Channel, referral Referral) error {
	if database == nil || channel.ID == "" || channel.TenantID == "" || referral.PageID == "" || referral.PSID == "" || referral.RawJSON == "" {
		return errors.New("invalid messenger intake referral")
	}
	if referral.CapturedAt.IsZero() {
		referral.CapturedAt = time.Now()
	}

	var conversationID *string
	var conversation models.Conversation
	result := database.Select("id").Where(
		"tenant_id = ? AND channel_id = ? AND external_user_id = ?",
		channel.TenantID, channel.ID, referral.PSID,
	).First(&conversation)
	if result.Error == nil {
		conversationID = &conversation.ID
	} else if !errors.Is(result.Error, gorm.ErrRecordNotFound) {
		return result.Error
	} else {
		var snapshot models.MessengerLabelSnapshot
		result = database.Select("conversation_id").Where(
			"tenant_id = ? AND channel_id = ? AND psid = ?",
			channel.TenantID, channel.ID, referral.PSID,
		).First(&snapshot)
		if result.Error == nil {
			conversationID = &snapshot.ConversationID
		} else if !errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return result.Error
		}
	}

	now := time.Now()
	attribution := models.MessengerIntakeAttribution{
		ID:             pkg.NewUUID(),
		TenantID:       channel.TenantID,
		ChannelID:      channel.ID,
		ConversationID: conversationID,
		PageID:         referral.PageID,
		PSID:           referral.PSID,
		AdID:           referral.AdID,
		Ref:            referral.Ref,
		Source:         referral.Source,
		ReferralType:   referral.ReferralType,
		MessageID:      referral.MessageID,
		RawReferral:    referral.RawJSON,
		CapturedAt:     referral.CapturedAt,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	return database.Clauses(clause.OnConflict{DoNothing: true}).Create(&attribution).Error
}

// BindConversation links a previously received webhook referral after pull
// sync creates or refreshes the corresponding local conversation.
func BindConversation(database *gorm.DB, tenantID, channelID, psid, conversationID string) error {
	if database == nil || tenantID == "" || channelID == "" || psid == "" || conversationID == "" {
		return nil
	}
	return database.Model(&models.MessengerIntakeAttribution{}).
		Where("tenant_id = ? AND channel_id = ? AND psid = ? AND conversation_id IS NULL", tenantID, channelID, psid).
		Updates(map[string]interface{}{"conversation_id": conversationID, "updated_at": time.Now()}).Error
}
