package messengerlabels

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"strings"
	"time"

	"github.com/vietbui/chat-quality-agent/channels"
	"github.com/vietbui/chat-quality-agent/db/models"
	"github.com/vietbui/chat-quality-agent/messengerjourney"
	"gorm.io/gorm"
)

func journeyHash(parts ...string) string {
	hash := sha256.New()
	for _, part := range parts {
		_, _ = hash.Write([]byte(part))
		_, _ = hash.Write([]byte{0})
	}
	return hex.EncodeToString(hash.Sum(nil)[:16])
}

func journeyHistoryEnabled() bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv("CQA_MESSENGER_JOURNEY_ENABLED")), "true")
}

func policyVersion(rules []Rule) string {
	data, _ := json.Marshal(rules)
	return journeyHash(string(data))
}

func recordLabelJourney(tx *gorm.DB, channel models.Channel, state models.MessengerLabelState, snapshot models.MessengerLabelSnapshot, previous *models.MessengerLabelSnapshot) error {
	if !journeyHistoryEnabled() {
		return nil
	}
	if snapshot.Status != "success" || snapshot.CheckedAt == nil || snapshot.PSID == "" || channel.ExternalID == "" {
		return nil
	}
	if previous != nil && previous.CheckedAt != nil && previous.Labels == snapshot.Labels {
		return nil
	}
	observedAt := snapshot.CheckedAt.UTC()
	base := messengerjourney.BaseInput{
		TenantID: channel.TenantID, PageID: channel.ExternalID, PSID: snapshot.PSID,
		ChannelID: channel.ID, ConversationID: snapshot.ConversationID,
		Source: "meta_label_sync", OccurredAt: observedAt, ObservedAt: observedAt,
	}
	base.EventID = "cqa_labels_" + journeyHash(channel.TenantID, channel.ExternalID, snapshot.PSID, snapshot.Labels, observedAt.Format(time.RFC3339Nano))
	if _, err := messengerjourney.RecordInTransaction(context.Background(), tx, messengerjourney.RecordInput{
		BaseInput: base, Kind: messengerjourney.KindLabels, Payload: json.RawMessage(snapshot.Labels),
	}); err != nil {
		return err
	}
	return recordClassificationJourney(tx, channel, state, snapshot, observedAt)
}

func recordClassificationJourney(tx *gorm.DB, channel models.Channel, state models.MessengerLabelState, snapshot models.MessengerLabelSnapshot, observedAt time.Time) error {
	if snapshot.PSID == "" || channel.ExternalID == "" {
		return nil
	}
	var labels []channels.FacebookLabel
	var rules []Rule
	if json.Unmarshal([]byte(snapshot.Labels), &labels) != nil || labels == nil || json.Unmarshal([]byte(state.Rules), &rules) != nil {
		return ErrConfiguration
	}
	version := policyVersion(rules)
	classification := Classify(labels, nil, snapshot.Status, snapshot.CheckedAt, rules, state.Enabled, observedAt)
	var current models.MessengerJourneyCustomer
	subjectKey := messengerjourney.SubjectKey(channel.TenantID, channel.ExternalID, snapshot.PSID)
	if err := tx.Select("classification,classification_policy_version").Where("subject_id = ?", subjectKey).First(&current).Error; err == nil {
		if current.Classification == classification && current.ClassificationPolicyVersion == version {
			return nil
		}
	} else if err != gorm.ErrRecordNotFound {
		return err
	}
	payload := map[string]string{"classification": classification}
	// A policy-only reclassification is not new customer activity. Preserve the
	// label observation clock so downstream retention cannot be extended merely
	// by changing CQA's mapping rules.
	if snapshot.CheckedAt != nil {
		payload["source_activity_at"] = snapshot.CheckedAt.UTC().Format(time.RFC3339Nano)
	}
	payloadJSON, _ := json.Marshal(payload)
	eventID := "cqa_classification_" + journeyHash(channel.TenantID, channel.ExternalID, snapshot.PSID, version, classification, observedAt.UTC().Format(time.RFC3339Nano))
	_, err := messengerjourney.RecordInTransaction(context.Background(), tx, messengerjourney.RecordInput{
		BaseInput: messengerjourney.BaseInput{
			EventID: eventID, TenantID: channel.TenantID, PageID: channel.ExternalID, PSID: snapshot.PSID,
			ChannelID: channel.ID, ConversationID: snapshot.ConversationID, Source: "cqa_label_policy",
			OccurredAt: observedAt, ObservedAt: observedAt,
		},
		Kind: messengerjourney.KindClassification, PolicyVersion: version, Payload: payloadJSON,
	})
	return err
}

func recordPolicyJourney(tx *gorm.DB, channel models.Channel, state models.MessengerLabelState, now time.Time) error {
	if !journeyHistoryEnabled() {
		return nil
	}
	var snapshots []models.MessengerLabelSnapshot
	if err := tx.Where("tenant_id = ? AND channel_id = ? AND status = ? AND checked_at IS NOT NULL", channel.TenantID, channel.ID, "success").Find(&snapshots).Error; err != nil {
		return err
	}
	for _, snapshot := range snapshots {
		if err := recordClassificationJourney(tx, channel, state, snapshot, now); err != nil {
			return err
		}
	}
	return nil
}
