package messengerintake

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/vietbui/chat-quality-agent/db/models"
	"github.com/vietbui/chat-quality-agent/messengerjourney"
	"github.com/vietbui/chat-quality-agent/pkg"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type Referral struct {
	PageID        string
	PSID          string
	AdID          string
	Ref           string
	Source        string
	ReferralType  string
	MessageID     string
	RawJSON       string
	CapturedAt    time.Time
	RecordJourney bool
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

	now := time.Now()
	return database.Transaction(func(tx *gorm.DB) error {
		conversationID, err := captureFirstReferralInTransaction(tx, channel, referral, now)
		if err != nil {
			return err
		}
		if !referral.RecordJourney {
			return nil
		}
		payloadFields := map[string]interface{}{"referral": json.RawMessage(referral.RawJSON)}
		for key, value := range map[string]string{
			"ad_id": referral.AdID, "ref": referral.Ref, "source": referral.Source,
			"type": referral.ReferralType, "message_id": referral.MessageID,
		} {
			if value != "" {
				payloadFields[key] = value
			}
		}
		payload, err := json.Marshal(payloadFields)
		if err != nil {
			return err
		}
		eventSeed := strings.Join([]string{channel.ID, referral.PageID, referral.PSID, referral.AdID, referral.Ref, referral.ReferralType, referral.MessageID, referral.CapturedAt.UTC().Format(time.RFC3339Nano), referral.RawJSON}, "\x00")
		digest := sha256.Sum256([]byte(eventSeed))
		eventID := "cqa_referral_" + hex.EncodeToString(digest[:16])
		_, err = messengerjourney.RecordInTransaction(context.Background(), tx, messengerjourney.RecordInput{
			BaseInput: messengerjourney.BaseInput{
				EventID: eventID, TenantID: channel.TenantID, PageID: referral.PageID, PSID: referral.PSID,
				ChannelID: channel.ID, ConversationID: conversationID, Source: "cqa_webhook", OccurredAt: referral.CapturedAt, ObservedAt: now,
			},
			Kind: messengerjourney.KindReferral, Payload: payload,
		})
		return err
	})
}

// CaptureFirstReferralInTransaction preserves the legacy immutable first-source
// projection. Callers that also append the journey event can keep both writes in
// one transaction without recursively starting another transaction.
func CaptureFirstReferralInTransaction(tx *gorm.DB, channel models.Channel, referral Referral) error {
	if tx == nil || channel.ID == "" || channel.TenantID == "" || referral.PageID == "" || referral.PSID == "" || referral.RawJSON == "" {
		return errors.New("invalid messenger intake referral")
	}
	if referral.CapturedAt.IsZero() {
		referral.CapturedAt = time.Now()
	}
	_, err := captureFirstReferralInTransaction(tx, channel, referral, time.Now())
	return err
}

func captureFirstReferralInTransaction(tx *gorm.DB, channel models.Channel, referral Referral, now time.Time) (string, error) {
	subjectKey := messengerjourney.SubjectKey(channel.TenantID, referral.PageID, referral.PSID)
	var erased int64
	if err := tx.Model(&models.MessengerJourneyErasure{}).Where("subject_id = ?", subjectKey).Count(&erased).Error; err != nil {
		return "", err
	}
	if erased > 0 {
		return "", messengerjourney.ErrSubjectErased
	}
	var conversationID *string
	var conversation models.Conversation
	result := tx.Select("id").Where(
		"tenant_id = ? AND channel_id = ? AND external_user_id = ?",
		channel.TenantID, channel.ID, referral.PSID,
	).First(&conversation)
	if result.Error == nil {
		conversationID = &conversation.ID
	} else if !errors.Is(result.Error, gorm.ErrRecordNotFound) {
		return "", result.Error
	} else {
		var snapshot models.MessengerLabelSnapshot
		result = tx.Select("conversation_id").Where(
			"tenant_id = ? AND channel_id = ? AND psid = ?",
			channel.TenantID, channel.ID, referral.PSID,
		).First(&snapshot)
		if result.Error == nil {
			conversationID = &snapshot.ConversationID
		} else if !errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return "", result.Error
		}
	}
	conversationIDValue := ""
	if conversationID != nil {
		conversationIDValue = *conversationID
	}
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
	if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&attribution).Error; err != nil {
		return "", err
	}
	return conversationIDValue, nil
}

// BindConversation links a previously received webhook referral after pull
// sync creates or refreshes the corresponding local conversation.
func BindConversation(database *gorm.DB, tenantID, channelID, psid, conversationID string) error {
	if database == nil || tenantID == "" || channelID == "" || psid == "" || conversationID == "" {
		return nil
	}
	return database.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&models.MessengerIntakeAttribution{}).
			Where("tenant_id = ? AND channel_id = ? AND psid = ? AND conversation_id IS NULL", tenantID, channelID, psid).
			Updates(map[string]interface{}{"conversation_id": conversationID, "updated_at": time.Now()}).Error; err != nil {
			return err
		}
		var channel models.Channel
		if err := tx.Select("external_id").Where("id = ? AND tenant_id = ?", channelID, tenantID).First(&channel).Error; err != nil {
			return err
		}
		return messengerjourney.BindConversation(context.Background(), tx, tenantID, channelID, channel.ExternalID, psid, conversationID)
	})
}
