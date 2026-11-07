package engine

import (
	"context"
	"encoding/json"
	"time"

	"github.com/vietbui/chat-quality-agent/db/models"
	"github.com/vietbui/chat-quality-agent/messengerjourney"
	"gorm.io/gorm"
)

// maxCoverageReconcilePerRun bounds one pass so a large backlog is drained over
// several after-sync runs without a long-running transaction burst.
const maxCoverageReconcilePerRun = 1000

// maxCoverageAnalysisPerRun caps conversations added to one AI run because their
// insight cannot prove coverage.
const maxCoverageAnalysisPerRun = 50

type insightCoverageCandidate struct {
	ConversationID string    `gorm:"column:conversation_id"`
	InsightID      string    `gorm:"column:insight_id"`
	InsightAt      time.Time `gorm:"column:insight_at"`
	Detail         string    `gorm:"column:detail"`
}

// CoverageReconcileResult reports one reconciliation pass.
//   - Written: coverage recorded from an existing insight, without an AI call.
//   - NeedsAnalysis: the insight cannot prove it read the current messages, so
//     the AI must analyze the conversation again.
type CoverageReconcileResult struct {
	Written       int
	NeedsAnalysis []string
}

// insightProvesCursor is the rule for reusing an insight as journey coverage.
// The insight must name the newest message it read, that message must still be
// the newest stored message, and every current message must have been stored
// before the insight was saved. A message that arrives later, even with an older
// sent_at, therefore requires a new analysis instead of being silently covered.
func insightProvesCursor(detail string, insightAt time.Time, current messengerjourney.MessageCursor) bool {
	if current.SourceMessageCount <= 0 || current.SourceLastMessageAt == nil || current.LastStoredAt == nil {
		return false
	}
	var source struct {
		LastMessageAt *time.Time `json:"source_last_message_at"`
	}
	if json.Unmarshal([]byte(detail), &source) != nil || source.LastMessageAt == nil {
		return false
	}
	if current.SourceLastMessageAt.After(*source.LastMessageAt) {
		return false
	}
	return !current.LastStoredAt.After(insightAt)
}

// ReconcileInsightCoverage records journey coverage for conversations whose
// latest Messenger insight already covers every stored message. Coverage was
// only written for insights created after journey writes were enabled, so older
// insights otherwise stay "waiting for AI" forever while the insight job, which
// selects work from job_results, never re-analyzes them.
func ReconcileInsightCoverage(ctx context.Context, database *gorm.DB, tenantID string, channelIDs []string, limit int, now time.Time) (CoverageReconcileResult, error) {
	var result CoverageReconcileResult
	if len(channelIDs) == 0 {
		return result, nil
	}
	if limit <= 0 || limit > maxCoverageReconcilePerRun {
		limit = maxCoverageReconcilePerRun
	}
	var candidates []insightCoverageCandidate
	err := database.WithContext(ctx).Table("conversations AS c").
		Select("c.id AS conversation_id, j.id AS insight_id, j.created_at AS insight_at, j.detail").
		Joins(`JOIN channels AS ch ON ch.id = c.channel_id AND ch.tenant_id = c.tenant_id AND ch.channel_type = ?`, "facebook").
		Joins(`JOIN job_results AS j ON j.id = (
			SELECT newest.id FROM job_results AS newest
			WHERE newest.tenant_id = c.tenant_id AND newest.conversation_id = c.id AND newest.result_type = ?
			ORDER BY newest.created_at DESC, newest.id DESC LIMIT 1
		)`, "conversation_insight").
		Where("c.tenant_id = ? AND c.channel_id IN ?", tenantID, channelIDs).
		Order("c.last_message_at DESC, c.id ASC").
		Scan(&candidates).Error
	if err != nil || len(candidates) == 0 {
		return result, err
	}
	ids := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		ids = append(ids, candidate.ConversationID)
	}
	cursors, err := messengerjourney.LoadMessageCursors(ctx, database, tenantID, ids)
	if err != nil {
		return result, err
	}
	coverage := make(map[string]models.MessengerJourneyCoverage, len(ids))
	for start := 0; start < len(ids); start += 500 {
		end := start + 500
		if end > len(ids) {
			end = len(ids)
		}
		var rows []models.MessengerJourneyCoverage
		if err := database.WithContext(ctx).Where("tenant_id = ? AND conversation_id IN ?", tenantID, ids[start:end]).Find(&rows).Error; err != nil {
			return result, err
		}
		for _, row := range rows {
			coverage[row.ConversationID] = row
		}
	}
	for _, candidate := range candidates {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		current := cursors[candidate.ConversationID]
		if current.SourceMessageCount == 0 {
			continue // Nothing to analyze; status does not count it as pending.
		}
		row, exists := coverage[candidate.ConversationID]
		if messengerjourney.CoverageComplete(row, exists, current) {
			continue
		}
		if !insightProvesCursor(candidate.Detail, candidate.InsightAt, current) {
			result.NeedsAnalysis = append(result.NeedsAnalysis, candidate.ConversationID)
			continue
		}
		if result.Written >= limit {
			continue
		}
		written, err := recordReconciledCoverage(ctx, database, tenantID, candidate, current, now)
		if err != nil {
			return result, err
		}
		if written {
			result.Written++
		}
	}
	return result, nil
}

func recordReconciledCoverage(ctx context.Context, database *gorm.DB, tenantID string, candidate insightCoverageCandidate, current messengerjourney.MessageCursor, now time.Time) (bool, error) {
	written := false
	err := database.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		subject, ok, err := loadMessengerJourneySubject(tx, tenantID, candidate.ConversationID)
		if err != nil || !ok {
			return err
		}
		lastAt := current.SourceLastMessageAt.UTC()
		payload, err := json.Marshal(map[string]interface{}{
			"status": "complete", "source_last_message_at": lastAt,
			"source_last_message_id": current.SourceLastMessageID, "source_message_count": current.SourceMessageCount,
			"source_result_id": candidate.InsightID, "reconciled_from_insight": true,
		})
		if err != nil {
			return err
		}
		_, err = messengerjourney.RecordInTransaction(ctx, tx, messengerjourney.RecordInput{
			BaseInput: messengerjourney.BaseInput{
				EventID: "cqa_coverage_reconcile_" + engineJourneyHash(tenantID, candidate.ConversationID, candidate.InsightID,
					current.SourceLastMessageID, lastAt.Format(time.RFC3339Nano)),
				TenantID: tenantID, PageID: subject.PageID, PSID: subject.PSID, ChannelID: subject.ChannelID,
				ConversationID: subject.ConversationID, Source: "cqa_insight_reconcile",
				OccurredAt: candidate.InsightAt.UTC(), ObservedAt: now.UTC(),
			}, Kind: messengerjourney.KindCoverage, Payload: payload,
		})
		written = err == nil
		return err
	})
	return written, err
}
