package messengerjourney

import (
	"context"
	"encoding/json"
	"time"

	"github.com/vietbui/chat-quality-agent/db/models"
	"gorm.io/gorm"
)

// MinValidReferralTime is the earliest referral time CQA accepts. An older value
// comes from a Unix time read in the wrong unit (seconds as milliseconds).
var MinValidReferralTime = time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)

// ReferralTimeRepair reports what RepairReferralTimes found or changed.
type ReferralTimeRepair struct {
	Applied     bool     `json:"applied"`
	IntakeRows  int      `json:"intake_rows"`
	Events      int      `json:"events"`
	Receipts    int64    `json:"receipts"`
	Customers   int      `json:"customers"`
	EventIDs    []string `json:"event_ids"`
	SubjectKeys []string `json:"subject_ids"`
	// Times holds the repaired values, so a consumer copy (AdBazooka D1) can be
	// set to exactly what the API now returns.
	Times []RepairedEventTime `json:"times"`
}

type RepairedEventTime struct {
	EventID           string `json:"event_id"`
	OccurredAt        string `json:"occurred_at"`
	PayloadReferredAt string `json:"payload_referred_at,omitempty"`
}

// RepairReferralTimes replaces referral times before MinValidReferralTime with
// RepairedReferralTime (the decoded Meta time, or the time CQA received the
// referral). It changes, in one transaction:
//   - messenger_intake_attributions.captured_at;
//   - each referral event: occurred_at, payload.referred_at (if present),
//     natural_key and the event's own receipt
//     fingerprint recomputed, so a producer retry from the repaired source row
//     is accepted and not reported as a conflict;
//   - first/latest referral of each affected customer, from its referral events.
//
// Without apply it only counts. The event sequence never changes, so consumers
// that already read the event see the same sequence with the repaired time.
func RepairReferralTimes(ctx context.Context, database *gorm.DB, apply bool) (ReferralTimeRepair, error) {
	result := ReferralTimeRepair{Applied: apply, EventIDs: []string{}, SubjectKeys: []string{}, Times: []RepairedEventTime{}}
	err := database.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var intake []models.MessengerIntakeAttribution
		if err := tx.Where("captured_at < ?", MinValidReferralTime).Order("id").Find(&intake).Error; err != nil {
			return err
		}
		result.IntakeRows = len(intake)
		var events []models.MessengerJourneyEvent
		if err := tx.Where("kind = ? AND occurred_at < ?", KindReferral, MinValidReferralTime).Order("tenant_id, sequence").Find(&events).Error; err != nil {
			return err
		}
		result.Events = len(events)
		subjects := map[string]bool{}
		for _, event := range events {
			result.EventIDs = append(result.EventIDs, event.EventID)
			repaired, err := repairedReferralEvent(event)
			if err != nil {
				return err
			}
			change := RepairedEventTime{EventID: event.EventID, OccurredAt: repaired.OccurredAt.UTC().Format("2006-01-02T15:04:05.000Z")}
			var body map[string]any
			if json.Unmarshal([]byte(repaired.Payload), &body) == nil {
				if value, ok := body["referred_at"].(string); ok {
					change.PayloadReferredAt = value
				}
			}
			result.Times = append(result.Times, change)
			if !subjects[event.SubjectKey] {
				subjects[event.SubjectKey] = true
				result.SubjectKeys = append(result.SubjectKeys, event.SubjectKey)
			}
		}
		if !apply {
			return nil
		}
		for _, row := range intake {
			if err := tx.Model(&models.MessengerIntakeAttribution{}).Where("id = ? AND captured_at < ?", row.ID, MinValidReferralTime).
				Update("captured_at", RepairedReferralTime(row.CapturedAt, row.CreatedAt)).Error; err != nil {
				return err
			}
		}
		for _, event := range events {
			repaired, err := repairedReferralEvent(event)
			if err != nil {
				return err
			}
			if err := tx.Model(&models.MessengerJourneyEvent{}).Where("id = ?", event.ID).
				Updates(map[string]any{"occurred_at": repaired.OccurredAt, "payload": repaired.Payload, "natural_key": repaired.NaturalKey}).Error; err != nil {
				return err
			}
			receipts := tx.Model(&models.MessengerJourneyReceipt{}).Where("tenant_id = ? AND event_id = ? AND canonical_sequence = ?", event.TenantID, event.EventID, event.Sequence).
				Update("fingerprint", eventFingerprint(recordInputOf(repaired)))
			if receipts.Error != nil {
				return receipts.Error
			}
			result.Receipts += receipts.RowsAffected
		}
		for _, subject := range result.SubjectKeys {
			changed, err := reprojectReferrals(tx, subject)
			if err != nil {
				return err
			}
			if changed {
				result.Customers++
			}
		}
		return nil
	})
	return result, err
}

func recordInputOf(event models.MessengerJourneyEvent) RecordInput {
	return RecordInput{BaseInput: BaseInput{EventID: event.EventID, TenantID: event.TenantID, PageID: event.PageID, PSID: event.PSID, ChannelID: event.ChannelID,
		ConversationID: event.ConversationID, Source: event.Source, Version: event.Version, OccurredAt: event.OccurredAt, ObservedAt: event.ObservedAt},
		Kind: event.Kind, EntityID: event.EntityID, PolicyVersion: event.PolicyVersion, Payload: json.RawMessage(event.Payload), SubjectKey: event.SubjectKey}
}

// RepairedReferralTime decodes a time that was built from Unix seconds read as
// milliseconds. The decoded time is used only when it lies in the day before
// receivedAt (and at most 5 minutes after it); otherwise receivedAt is used.
func RepairedReferralTime(bad, receivedAt time.Time) time.Time {
	decoded := time.Unix(bad.UnixMilli(), 0).In(receivedAt.Location())
	if !decoded.Before(receivedAt.Add(-24*time.Hour)) && !decoded.After(receivedAt.Add(5*time.Minute)) {
		return decoded
	}
	return receivedAt
}

func repairedReferralEvent(event models.MessengerJourneyEvent) (models.MessengerJourneyEvent, error) {
	event.OccurredAt = RepairedReferralTime(event.OccurredAt, event.ObservedAt)
	var body map[string]any
	if err := json.Unmarshal([]byte(event.Payload), &body); err != nil {
		return event, err
	}
	if _, present := body["referred_at"]; present {
		// Same encoding as the API's occurred_at, so the payload and the event agree.
		body["referred_at"] = event.OccurredAt
		encoded, err := json.Marshal(body)
		if err != nil {
			return event, err
		}
		event.Payload = string(encoded)
	}
	if event.NaturalKey != nil && event.PSID != "" {
		event.NaturalKey = referralNaturalKey(recordInputOf(event))
	}
	return event, nil
}

// reprojectReferrals sets first/latest referral from the subject's referral
// events, with the same order the projection uses (time, then sequence).
func reprojectReferrals(tx *gorm.DB, subject string) (bool, error) {
	var first, latest models.MessengerJourneyEvent
	err := tx.Where("subject_id = ? AND kind = ?", subject, KindReferral).Order("occurred_at ASC, sequence ASC").First(&first).Error
	if errorsIsNotFound(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if err = tx.Where("subject_id = ? AND kind = ?", subject, KindReferral).Order("occurred_at DESC, sequence DESC").First(&latest).Error; err != nil {
		return false, err
	}
	update := tx.Model(&models.MessengerJourneyCustomer{}).Where("subject_id = ?", subject).Updates(map[string]any{
		"first_referral": first.Payload, "first_referral_at": first.OccurredAt,
		"latest_referral": latest.Payload, "latest_referral_at": latest.OccurredAt, "updated_at": time.Now().UTC(),
	})
	return update.RowsAffected > 0, update.Error
}
