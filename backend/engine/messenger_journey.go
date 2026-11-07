package engine

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
	"github.com/vietbui/chat-quality-agent/messengerlabels"
	"github.com/vietbui/chat-quality-agent/servicequality"
	"gorm.io/gorm"
)

type messengerJourneySubject struct {
	TenantID, PageID, PSID, ChannelID, ConversationID string
}

func engineJourneyHash(parts ...string) string {
	hash := sha256.New()
	for _, part := range parts {
		_, _ = hash.Write([]byte(part))
		_, _ = hash.Write([]byte{0})
	}
	return hex.EncodeToString(hash.Sum(nil)[:16])
}

func loadMessengerJourneySubject(tx *gorm.DB, tenantID, conversationID string) (messengerJourneySubject, bool, error) {
	var conversation models.Conversation
	if err := tx.Select("id,tenant_id,channel_id,external_user_id,metadata").Where("id = ? AND tenant_id = ?", conversationID, tenantID).First(&conversation).Error; err != nil {
		return messengerJourneySubject{}, false, err
	}
	var channel models.Channel
	if err := tx.Select("id,tenant_id,channel_type,external_id").Where("id = ? AND tenant_id = ?", conversation.ChannelID, tenantID).First(&channel).Error; err != nil {
		return messengerJourneySubject{}, false, err
	}
	if channel.ChannelType != "facebook" || strings.TrimSpace(channel.ExternalID) == "" {
		return messengerJourneySubject{}, false, nil
	}
	psid := ""
	var snapshot models.MessengerLabelSnapshot
	if err := tx.Select("psid").Where("conversation_id = ? AND tenant_id = ?", conversationID, tenantID).First(&snapshot).Error; err == nil {
		psid = snapshot.PSID
	} else if err != gorm.ErrRecordNotFound {
		return messengerJourneySubject{}, false, err
	}
	if psid == "" {
		var intake models.MessengerIntakeAttribution
		if err := tx.Select("psid").Where("conversation_id = ? AND tenant_id = ?", conversationID, tenantID).First(&intake).Error; err == nil {
			psid = intake.PSID
		} else if err != gorm.ErrRecordNotFound {
			return messengerJourneySubject{}, false, err
		}
	}
	if psid == "" {
		psid = messengerlabels.ParticipantPSID(conversation, channel.ExternalID)
	}
	if psid == "" {
		return messengerJourneySubject{}, false, nil
	}
	return messengerJourneySubject{TenantID: tenantID, PageID: channel.ExternalID, PSID: psid, ChannelID: channel.ID, ConversationID: conversationID}, true, nil
}

func loadDurableOrderOutcomes(database *gorm.DB, tenantID, conversationID string) ([]persistedOrderOutcome, error) {
	var rows []models.MessengerJourneyOrder
	if err := database.Where("tenant_id = ? AND conversation_id = ?", tenantID, conversationID).Order("order_id ASC").Find(&rows).Error; err != nil {
		return nil, err
	}
	outcomes := make([]persistedOrderOutcome, 0, len(rows))
	for _, row := range rows {
		var outcome persistedOrderOutcome
		if json.Unmarshal([]byte(row.Payload), &outcome) == nil && outcome.OrderID != "" {
			outcomes = append(outcomes, outcome)
		}
	}
	return outcomes, nil
}

func recordMessengerInsightJourney(ctx context.Context, tx *gorm.DB, subject messengerJourneySubject, resultID, sourceJobID string, outcomes []persistedOrderOutcome, metadata analysisSnapshotMetadata, observedAt time.Time) error {
	for _, outcome := range outcomes {
		semanticPayload, err := json.Marshal(outcome)
		if err != nil {
			return err
		}
		version := engineJourneyHash(string(semanticPayload))
		var payload map[string]interface{}
		if err := json.Unmarshal(semanticPayload, &payload); err != nil {
			return err
		}
		payload["source_result_id"] = resultID
		if sourceJobID != "" {
			payload["source_job_id"] = sourceJobID
		}
		storedPayload, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		occurredAt, err := time.Parse(time.RFC3339Nano, outcome.OrderedAt)
		if err != nil {
			occurredAt = observedAt
		}
		_, err = messengerjourney.RecordInTransaction(ctx, tx, messengerjourney.RecordInput{
			BaseInput: messengerjourney.BaseInput{
				EventID:  "cqa_order_" + engineJourneyHash(subject.TenantID, outcome.OrderID, version),
				TenantID: subject.TenantID, PageID: subject.PageID, PSID: subject.PSID, ChannelID: subject.ChannelID,
				ConversationID: subject.ConversationID, Source: "cqa_ai_validated", Version: version,
				OccurredAt: occurredAt, ObservedAt: observedAt,
			}, Kind: messengerjourney.KindOrder, EntityID: outcome.OrderID, Payload: storedPayload,
		})
		if err != nil {
			return err
		}
	}
	coveragePayload, _ := json.Marshal(map[string]interface{}{
		"status": "complete", "source_last_message_at": metadata.SourceLastMessageAt,
		"source_last_message_id": metadata.SourceLastMessageID, "source_message_count": metadata.SourceMessageCount,
	})
	_, err := messengerjourney.RecordInTransaction(ctx, tx, messengerjourney.RecordInput{
		BaseInput: messengerjourney.BaseInput{
			EventID:  "cqa_coverage_" + engineJourneyHash(subject.TenantID, subject.ConversationID, resultID),
			TenantID: subject.TenantID, PageID: subject.PageID, PSID: subject.PSID, ChannelID: subject.ChannelID,
			ConversationID: subject.ConversationID, Source: "cqa_ai_analysis", OccurredAt: observedAt, ObservedAt: observedAt,
		}, Kind: messengerjourney.KindCoverage, Payload: coveragePayload,
	})
	return err
}

func saveConversationInsightWithJourney(database *gorm.DB, result models.JobResult, outcomes []persistedOrderOutcome, metadata analysisSnapshotMetadata, enabled bool) error {
	return database.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&result).Error; err != nil {
			return err
		}
		if !enabled {
			return nil
		}
		subject, ok, err := loadMessengerJourneySubject(tx, result.TenantID, result.ConversationID)
		if err != nil {
			return err
		}
		if !ok {
			// Non-Facebook conversations and legacy rows without a trustworthy
			// Page-scoped identity keep their existing insight result. They are
			// deliberately absent from the AdBazooka completeness projection.
			return nil
		}
		sourceJobID := ""
		if result.JobRunID != "" {
			var run models.JobRun
			if err := tx.Select("job_id").Where("id = ?", result.JobRunID).First(&run).Error; err == nil {
				sourceJobID = run.JobID
			} else if err != gorm.ErrRecordNotFound {
				return err
			}
		}
		if err := recordMessengerInsightJourney(context.Background(), tx, subject, result.ID, sourceJobID, outcomes, metadata, result.CreatedAt); err != nil {
			return err
		}
		return recordNewInsightEvent(tx, subject, result)
	})
}

// recordNewInsightEvent exports the insight just saved. An unreadable detail is
// skipped so the insight and its coverage are still stored.
func recordNewInsightEvent(tx *gorm.DB, subject messengerJourneySubject, result models.JobResult) error {
	policy, err := servicequality.LoadPolicy(tx, result.TenantID)
	if err != nil {
		policy = servicequality.DefaultPolicy()
	}
	messages, err := loadConversationMessages(tx, result.TenantID, []string{result.ConversationID})
	if err != nil {
		return err
	}
	err = recordInsightEvent(context.Background(), tx, subject, result.ID, result.Detail, messages[result.ConversationID], policy, result.CreatedAt, result.CreatedAt, insightEventSourceAnalysis)
	if errors.Is(err, errInsightDetail) {
		return nil
	}
	return err
}
