package engine

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/vietbui/chat-quality-agent/ai"
	"github.com/vietbui/chat-quality-agent/db/models"
	"github.com/vietbui/chat-quality-agent/messengerjourney"
	"github.com/vietbui/chat-quality-agent/servicequality"
	"gorm.io/gorm"
)

// ABZ-MSG-BI-01: one "insight" journey event per Messenger insight. The payload
// holds only fixed codes and counts (lead level, need group, product model
// names, negative feedback categories, first response time). Quotes, reasons,
// summaries and message text never leave CQA.

const (
	insightPayloadSchema   = 1
	maxInsightProducts     = 5
	maxInsightProductRunes = 60
	// AdBazooka rejects a whole feed page when one insight is out of range, so
	// every bound below matches its sanitizer (messenger-journey.ts).
	maxInsightResponseSeconds  = 10_000_000
	maxInsightBackfillPerRun   = 500
	insightEventSourceAnalysis = "cqa_ai_analysis"
	insightEventSourceBackfill = "cqa_insight_backfill"
)

var feedbackCategoryCodes = map[string]string{
	"sản phẩm": "product", "san pham": "product", "product": "product",
	"giá": "price", "gia": "price", "price": "price",
	"giao hàng": "delivery", "giao hang": "delivery", "delivery": "delivery",
	"cskh": "service", "service": "service",
}

type storedInsightDetail struct {
	Intents             []string
	Need                string
	NeedSource          string
	Products            []ai.ProductInsight
	Feedback            []ai.FeedbackInsight
	LeadQuality         ai.LeadQualityInsight
	SourceLastMessageAt *time.Time
}

// decodeInsightField reads one field. A missing or malformed field gives the
// zero value, so one old field shape cannot drop the whole insight.
func decodeInsightField[T any](fields map[string]json.RawMessage, key string) T {
	var value T
	if raw, ok := fields[key]; ok && json.Unmarshal(raw, &value) != nil {
		var zero T
		return zero
	}
	return value
}

// decodeInsightList keeps the readable items of a list and drops the others.
func decodeInsightList[T any](fields map[string]json.RawMessage, key string) []T {
	items := decodeInsightField[[]json.RawMessage](fields, key)
	out := make([]T, 0, len(items))
	for _, raw := range items {
		var item T
		if json.Unmarshal(raw, &item) == nil {
			out = append(out, item)
		}
	}
	return out
}

// parseStoredInsightDetail reads a stored insight field by field. Insights
// saved by older prompts can hold another shape (for example lead_quality as a
// string). Those fields become empty; the rest, and the first response time
// from the messages, are still exported. Only a detail that is not a JSON
// object is unreadable.
func parseStoredInsightDetail(detail string) (storedInsightDetail, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(detail), &fields); err != nil || fields == nil {
		return storedInsightDetail{}, errInsightDetail
	}
	return storedInsightDetail{
		Intents:             decodeInsightList[string](fields, "intents"),
		Need:                decodeInsightField[string](fields, "need"),
		NeedSource:          decodeInsightField[string](fields, "need_source"),
		Products:            decodeInsightList[ai.ProductInsight](fields, "products"),
		Feedback:            decodeInsightList[ai.FeedbackInsight](fields, "feedback"),
		LeadQuality:         decodeInsightField[ai.LeadQualityInsight](fields, "lead_quality"),
		SourceLastMessageAt: decodeInsightField[*time.Time](fields, "source_last_message_at"),
	}, nil
}

// storedInsightNeed keeps the need and source the analyzer saved. The analyzer
// stores a keyword-resolved need in the same "need" field, so resolving it
// again would relabel a keyword result as "ai".
func storedInsightNeed(stored storedInsightDetail) (string, string) {
	need := ai.NormalizeNeed(stored.Need)
	if need != "" && (stored.NeedSource == "ai" || stored.NeedSource == "keyword") {
		return need, stored.NeedSource
	}
	return ai.ResolveNeed(stored.Need, stored.Intents)
}

func insightEventID(tenantID, conversationID, resultID string) string {
	return "cqa_insight_" + engineJourneyHash(tenantID, conversationID, resultID)
}

func insightLeadLevel(level string) string {
	switch level = strings.ToLower(strings.TrimSpace(level)); level {
	case "high", "medium", "low", "spam":
		return level
	}
	return "unknown"
}

// insightProductName trims a product name the way AdBazooka does (JavaScript
// trim also removes U+FEFF) and returns "" when the name cannot be exported.
// AdBazooka counts UTF-16 code units, so an emoji counts as two: a name that
// passes a rune count can still fail there and stop the whole journey sync.
func insightProductName(raw string) string {
	name := strings.TrimFunc(raw, func(r rune) bool { return unicode.IsSpace(r) || r == '\uFEFF' })
	if name == "" || !utf8.ValidString(name) || len(utf16.Encode([]rune(name))) > maxInsightProductRunes {
		return ""
	}
	return name
}

// messengerInsightPayload builds the exported facts from a stored insight and
// the messages it read. Messages after the insight's source cursor are ignored,
// and an unanswered first turn has no seconds, so the payload depends only on
// what the insight saw (a retry produces the same event fingerprint).
func messengerInsightPayload(detail string, messages []models.Message, policy servicequality.Policy) (json.RawMessage, error) {
	stored, err := parseStoredInsightDetail(detail)
	if err != nil {
		return nil, err
	}
	need, needSource := storedInsightNeed(stored)

	products := make([]string, 0, maxInsightProducts)
	seenProducts := map[string]bool{}
	for _, product := range stored.Products {
		name := insightProductName(product.Name)
		key := strings.ToLower(name)
		if name == "" || seenProducts[key] {
			continue
		}
		seenProducts[key] = true
		products = append(products, name)
		if len(products) == maxInsightProducts {
			break
		}
	}

	negative := []string{}
	seenFeedback := map[string]bool{}
	for _, feedback := range stored.Feedback {
		sentiment := strings.ToLower(strings.TrimSpace(feedback.Sentiment))
		if sentiment != "negative" && sentiment != "mixed" {
			continue
		}
		code := feedbackCategoryCodes[strings.ToLower(strings.TrimSpace(feedback.Category))]
		if code == "" {
			code = "other"
		}
		if !seenFeedback[code] {
			seenFeedback[code] = true
			negative = append(negative, code)
		}
	}
	sort.Strings(negative)

	read := make([]models.Message, 0, len(messages))
	customerMessages := 0
	var lastAt *time.Time
	for _, message := range messages {
		if stored.SourceLastMessageAt != nil && message.SentAt.After(*stored.SourceLastMessageAt) {
			continue
		}
		read = append(read, message)
		if message.SenderType == "customer" {
			customerMessages++
		}
		if lastAt == nil || message.SentAt.After(*lastAt) {
			at := message.SentAt.UTC()
			lastAt = &at
		}
	}
	firstStatus := "no_request"
	var firstSeconds *int64
	var withinTarget *bool
	if lastAt != nil {
		// "now" = the last read message, so waiting turns never age in the payload.
		timeline := servicequality.Calculate(read, nil, lastAt.Add(time.Second), policy)
		if len(timeline.Turns) > 0 {
			first := timeline.Turns[0]
			firstStatus = first.Status
			if first.Status == "answered" {
				seconds := int64(first.Seconds + 0.5)
				if seconds > maxInsightResponseSeconds {
					seconds = maxInsightResponseSeconds
				}
				ok := first.Seconds <= float64(policy.TargetMinutes*60)
				firstSeconds, withinTarget = &seconds, &ok
			} else {
				firstStatus = "waiting"
			}
		}
	}

	payload := map[string]any{
		"schema": insightPayloadSchema, "lead_level": insightLeadLevel(stored.LeadQuality.Level),
		"need": need, "need_source": needSource, "need_rules": ai.NeedRulesVersion,
		"products": products, "negative_feedback": negative,
		"messages": len(read), "customer_messages": customerMessages,
		"first_reply_status": firstStatus, "first_response_seconds": firstSeconds, "replied_within_target": withinTarget,
		"target_minutes": policy.TargetMinutes, "source_activity_at": lastAt,
	}
	return json.Marshal(payload)
}

// errInsightDetail marks an old insight whose stored JSON cannot be read. It
// is skipped: the insight itself stays valid for CQA.
var errInsightDetail = errors.New("unreadable Messenger insight detail")

// errNoInsightSubject marks a conversation that has no Messenger customer to
// attach an insight event to.
var errNoInsightSubject = errors.New("Messenger conversation has no journey subject")

// insightBackfillSkips holds insight event IDs that cannot be exported
// (unreadable detail, erased customer, no subject, conflicting event). Without
// it the same rows would fill every bounded run and the backfill would never
// reach the rest. It lives for the process: a restart retries them once.
var insightBackfillSkips = struct {
	sync.Mutex
	ids map[string]struct{}
}{ids: map[string]struct{}{}}

func insightBackfillSkipped(eventID string) bool {
	insightBackfillSkips.Lock()
	defer insightBackfillSkips.Unlock()
	_, skipped := insightBackfillSkips.ids[eventID]
	return skipped
}

func skipInsightBackfill(eventID string) {
	insightBackfillSkips.Lock()
	defer insightBackfillSkips.Unlock()
	insightBackfillSkips.ids[eventID] = struct{}{}
}

func recordInsightEvent(ctx context.Context, tx *gorm.DB, subject messengerJourneySubject, resultID, detail string, messages []models.Message, policy servicequality.Policy, insightAt, observedAt time.Time, source string) error {
	payload, err := messengerInsightPayload(detail, messages, policy)
	if err != nil {
		return errInsightDetail
	}
	if insightAt.After(observedAt) {
		insightAt = observedAt
	}
	_, err = messengerjourney.RecordInTransaction(ctx, tx, messengerjourney.RecordInput{
		BaseInput: messengerjourney.BaseInput{
			EventID:  insightEventID(subject.TenantID, subject.ConversationID, resultID),
			TenantID: subject.TenantID, PageID: subject.PageID, PSID: subject.PSID, ChannelID: subject.ChannelID,
			ConversationID: subject.ConversationID, Source: source, Version: engineJourneyHash(string(payload)),
			OccurredAt: insightAt.UTC(), ObservedAt: observedAt.UTC(),
		}, Kind: messengerjourney.KindInsight, EntityID: resultID, Payload: payload,
	})
	return err
}

func loadConversationMessages(tx *gorm.DB, tenantID string, conversationIDs []string) (map[string][]models.Message, error) {
	out := make(map[string][]models.Message, len(conversationIDs))
	for start := 0; start < len(conversationIDs); start += 200 {
		end := start + 200
		if end > len(conversationIDs) {
			end = len(conversationIDs)
		}
		var rows []models.Message
		if err := tx.Select("id,conversation_id,sender_type,content,content_type,sent_at").
			Where("tenant_id = ? AND conversation_id IN ?", tenantID, conversationIDs[start:end]).
			Order("sent_at ASC, id ASC").Find(&rows).Error; err != nil {
			return nil, err
		}
		for _, row := range rows {
			out[row.ConversationID] = append(out[row.ConversationID], row)
		}
	}
	return out, nil
}

// BackfillInsightEvents exports the latest existing insight of each Messenger
// conversation that has no insight event yet. It reads stored insights only:
// no AI call, so the backfill costs 0đ. Each run is bounded; later after-sync
// runs drain the rest.
func BackfillInsightEvents(ctx context.Context, database *gorm.DB, tenantID string, channelIDs []string, limit int, now time.Time) (int, error) {
	if len(channelIDs) == 0 {
		return 0, nil
	}
	if limit <= 0 || limit > maxInsightBackfillPerRun {
		limit = maxInsightBackfillPerRun
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
		return 0, err
	}
	exported := map[string]bool{}
	for start := 0; start < len(candidates); start += 500 {
		end := start + 500
		if end > len(candidates) {
			end = len(candidates)
		}
		ids := make([]string, 0, end-start)
		for _, candidate := range candidates[start:end] {
			ids = append(ids, insightEventID(tenantID, candidate.ConversationID, candidate.InsightID))
		}
		var found []string
		if err := database.WithContext(ctx).Model(&models.MessengerJourneyReceipt{}).
			Where("tenant_id = ? AND event_id IN ?", tenantID, ids).Pluck("event_id", &found).Error; err != nil {
			return 0, err
		}
		for _, id := range found {
			exported[id] = true
		}
	}
	missing := make([]insightCoverageCandidate, 0, limit)
	for _, candidate := range candidates {
		eventID := insightEventID(tenantID, candidate.ConversationID, candidate.InsightID)
		if !exported[eventID] && !insightBackfillSkipped(eventID) {
			missing = append(missing, candidate)
			if len(missing) == limit {
				break
			}
		}
	}
	if len(missing) == 0 {
		return 0, nil
	}
	policy, err := servicequality.LoadPolicy(database.WithContext(ctx), tenantID)
	if err != nil {
		policy = servicequality.DefaultPolicy()
	}
	conversationIDs := make([]string, 0, len(missing))
	for _, candidate := range missing {
		conversationIDs = append(conversationIDs, candidate.ConversationID)
	}
	messages, err := loadConversationMessages(database.WithContext(ctx), tenantID, conversationIDs)
	if err != nil {
		return 0, err
	}
	written := 0
	for _, candidate := range missing {
		if err := ctx.Err(); err != nil {
			return written, err
		}
		err := database.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			subject, ok, err := loadMessengerJourneySubject(tx, tenantID, candidate.ConversationID)
			if err != nil {
				return err
			}
			if !ok {
				return errNoInsightSubject
			}
			if err := recordInsightEvent(ctx, tx, subject, candidate.InsightID, candidate.Detail, messages[candidate.ConversationID], policy, candidate.InsightAt, now, insightEventSourceBackfill); err != nil {
				return err
			}
			written++
			return nil
		})
		// An erased customer, a malformed old insight, a chat without a customer
		// or a concurrent writer of the same event must not stop the rest of the
		// backfill, and must not be picked again by the next bounded run.
		if errors.Is(err, messengerjourney.ErrSubjectErased) || errors.Is(err, messengerjourney.ErrEventConflict) || errors.Is(err, errInsightDetail) || errors.Is(err, errNoInsightSubject) {
			skipInsightBackfill(insightEventID(tenantID, candidate.ConversationID, candidate.InsightID))
			continue
		}
		if err != nil {
			return written, err
		}
	}
	return written, nil
}
