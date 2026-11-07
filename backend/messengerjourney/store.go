package messengerjourney

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/vietbui/chat-quality-agent/db/models"
	"github.com/vietbui/chat-quality-agent/pkg"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func SubjectKey(tenantID, pageID, psid string) string {
	sum := sha256.Sum256([]byte(tenantID + "\x00" + pageID + "\x00" + psid))
	return hex.EncodeToString(sum[:])
}

func stableID(parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:])
}

func canonicalHash(payload json.RawMessage, dropProvenance bool, sortArray bool) string {
	var value any
	if json.Unmarshal(payload, &value) != nil {
		return stableID(string(payload))
	}
	if object, ok := value.(map[string]any); ok && dropProvenance {
		for _, key := range []string{"source_result_id", "source_job_id", "backfill", "ingest_source"} {
			delete(object, key)
		}
	}
	if values, ok := value.([]any); ok && sortArray {
		sort.SliceStable(values, func(i, j int) bool {
			left, _ := json.Marshal(values[i])
			right, _ := json.Marshal(values[j])
			return string(left) < string(right)
		})
	}
	canonical, _ := json.Marshal(value)
	return stableID(string(canonical))
}

func referralNaturalKey(in RecordInput) *string {
	if in.Kind != KindReferral {
		return nil
	}
	var body map[string]any
	_ = json.Unmarshal(in.Payload, &body)
	referralType := body["type"]
	if referralType == nil {
		referralType = body["referral_type"]
	}
	field := func(value any) string {
		if value == nil {
			return ""
		}
		return strings.TrimSpace(fmt.Sprint(value))
	}
	parts := []string{in.TenantID, in.PageID, in.PSID, in.OccurredAt.UTC().Format(time.RFC3339Nano), field(body["ad_id"]), field(body["ref"]), field(body["source"]), field(referralType), field(body["message_id"])}
	contextValue := body["ads_context"]
	if contextValue == nil {
		contextValue = body["ads_context_data"]
	}
	if contextValue == nil {
		if referral, ok := body["referral"].(map[string]any); ok {
			contextValue = referral["ads_context_data"]
		}
	}
	selected := map[string]string{}
	if context, ok := contextValue.(map[string]any); ok {
		for _, key := range []string{"ad_title", "photo_url", "video_url", "post_id", "product_id"} {
			if value, ok := context[key].(string); ok && value != "" {
				selected[key] = value
			}
		}
	}
	encoded, _ := json.Marshal(selected)
	parts = append(parts, canonicalHash(encoded, false, false))
	value := stableID(parts...)
	return &value
}

func normalizedInput(in RecordInput) (RecordInput, error) {
	in.EventID, in.TenantID, in.PageID = strings.TrimSpace(in.EventID), strings.TrimSpace(in.TenantID), strings.TrimSpace(in.PageID)
	in.PSID, in.Kind, in.Source = strings.TrimSpace(in.PSID), strings.TrimSpace(in.Kind), strings.TrimSpace(in.Source)
	allowed := map[string]bool{KindReferral: true, KindLabels: true, KindClassification: true, KindOrder: true, KindCoverage: true, KindDeletion: true, KindInsight: true}
	if in.EventID == "" || in.TenantID == "" || in.PageID == "" || !allowed[in.Kind] || in.Source == "" {
		return in, ErrInvalidInput
	}
	if in.Kind != KindDeletion && in.PSID == "" {
		return in, ErrInvalidInput
	}
	if in.Kind == KindOrder && strings.TrimSpace(in.EntityID) == "" {
		return in, ErrInvalidInput
	}
	if in.Kind == KindDeletion && in.SubjectKey == "" {
		return in, ErrInvalidInput
	}
	if in.SubjectKey == "" {
		in.SubjectKey = SubjectKey(in.TenantID, in.PageID, in.PSID)
	}
	if in.ObservedAt.IsZero() {
		in.ObservedAt = time.Now().UTC()
	}
	if in.OccurredAt.IsZero() {
		in.OccurredAt = in.ObservedAt
	}
	in.ObservedAt, in.OccurredAt = in.ObservedAt.UTC(), in.OccurredAt.UTC()
	if len(in.Payload) == 0 {
		in.Payload = json.RawMessage(`{}`)
	}
	if len(in.Payload) > maxPayloadBytes || !json.Valid(in.Payload) {
		return in, ErrInvalidInput
	}
	if in.Kind == KindOrder {
		if err := ValidateOrderPayload(in.Payload, in.ObservedAt); err != nil {
			return in, err
		}
	}
	return in, nil
}

func Record(ctx context.Context, database *gorm.DB, in RecordInput) (RecordResult, error) {
	var result RecordResult
	err := database.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var err error
		result, err = RecordInTransaction(ctx, tx, in)
		return err
	})
	return result, err
}

func RecordInTransaction(ctx context.Context, tx *gorm.DB, raw RecordInput) (RecordResult, error) {
	in, err := normalizedInput(raw)
	if err != nil {
		return RecordResult{}, err
	}
	tx = tx.WithContext(ctx)
	sequence, err := lockTenant(tx, in.TenantID)
	if err != nil {
		return RecordResult{}, err
	}
	return recordLocked(tx, in, &sequence)
}

// lockTenant must be acquired before any event, projection, or erasure
// mutation for a tenant. Holding it until commit establishes cursor order.
func lockTenant(tx *gorm.DB, tenantID string) (models.MessengerJourneySequence, error) {
	now := time.Now().UTC()
	// INSERT IGNORE creates the mutex row without consuming a sequence.
	if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&models.MessengerJourneySequence{TenantID: tenantID, UpdatedAt: now}).Error; err != nil {
		return models.MessengerJourneySequence{}, err
	}
	var sequence models.MessengerJourneySequence
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&sequence, "tenant_id = ?", tenantID).Error; err != nil {
		return models.MessengerJourneySequence{}, err
	}
	return sequence, nil
}

func recordLocked(tx *gorm.DB, in RecordInput, sequence *models.MessengerJourneySequence) (RecordResult, error) {
	now := time.Now().UTC()
	// Erasure is authoritative even for an exact producer retry. This check must
	// precede receipts/events or a caller could recreate a legacy PSID row after
	// accepting the duplicate as successful.
	if in.Kind != KindDeletion {
		var erased int64
		if err := tx.Model(&models.MessengerJourneyErasure{}).Where("subject_id = ?", in.SubjectKey).Count(&erased).Error; err != nil {
			return RecordResult{}, err
		}
		if erased > 0 {
			return RecordResult{}, ErrSubjectErased
		}
	}
	fingerprint := eventFingerprint(in)
	var receipt models.MessengerJourneyReceipt
	err := tx.Where("tenant_id = ? AND event_id = ?", in.TenantID, in.EventID).First(&receipt).Error
	if err == nil {
		if receipt.Fingerprint != fingerprint {
			return RecordResult{}, ErrEventConflict
		}
		var canonical models.MessengerJourneyEvent
		if err := tx.First(&canonical, "tenant_id = ? AND sequence = ?", in.TenantID, receipt.CanonicalSequence).Error; err != nil {
			return RecordResult{}, err
		}
		return RecordResult{Event: canonical, Inserted: false}, nil
	}
	if !errorsIsNotFound(err) {
		return RecordResult{}, err
	}
	var existing models.MessengerJourneyEvent
	err = tx.Where("tenant_id = ? AND event_id = ?", in.TenantID, in.EventID).First(&existing).Error
	if err == nil {
		existingInput := RecordInput{BaseInput: BaseInput{EventID: existing.EventID, TenantID: existing.TenantID, PageID: existing.PageID, PSID: existing.PSID, ChannelID: existing.ChannelID, ConversationID: existing.ConversationID, Source: existing.Source, Version: existing.Version, OccurredAt: existing.OccurredAt, ObservedAt: existing.ObservedAt}, Kind: existing.Kind, EntityID: existing.EntityID, PolicyVersion: existing.PolicyVersion, Payload: json.RawMessage(existing.Payload), SubjectKey: existing.SubjectKey}
		if eventFingerprint(existingInput) != fingerprint {
			return RecordResult{}, ErrEventConflict
		}
		if err := createReceipt(tx, in.EventID, in.TenantID, fingerprint, existing.Sequence, now); err != nil {
			return RecordResult{}, err
		}
		return RecordResult{Event: existing, Inserted: false}, nil
	}
	if !errorsIsNotFound(err) {
		return RecordResult{}, err
	}
	if duplicate, err := semanticDuplicate(tx, in); err != nil {
		return RecordResult{}, err
	} else if duplicate != nil {
		if err := createReceipt(tx, in.EventID, in.TenantID, fingerprint, duplicate.Sequence, now); err != nil {
			return RecordResult{}, err
		}
		return RecordResult{Event: *duplicate, Inserted: false}, nil
	}

	sequence.NextSequence++
	if err := tx.Model(sequence).Updates(map[string]any{"next_sequence": sequence.NextSequence, "updated_at": now}).Error; err != nil {
		return RecordResult{}, err
	}
	event := models.MessengerJourneyEvent{
		ID: pkg.NewUUID(), TenantID: in.TenantID, Sequence: sequence.NextSequence, EventID: in.EventID,
		Kind: in.Kind, NaturalKey: referralNaturalKey(in), SubjectKey: in.SubjectKey, PageID: in.PageID, PSID: in.PSID, ChannelID: in.ChannelID,
		ConversationID: in.ConversationID, EntityID: in.EntityID, Source: in.Source, Version: in.Version,
		PolicyVersion: in.PolicyVersion, Payload: string(in.Payload), OccurredAt: in.OccurredAt, ObservedAt: in.ObservedAt, CreatedAt: now,
	}
	if err := tx.Create(&event).Error; err != nil {
		return RecordResult{}, err
	}
	if err := createReceipt(tx, in.EventID, in.TenantID, fingerprint, event.Sequence, now); err != nil {
		return RecordResult{}, err
	}
	if in.Kind != KindDeletion {
		if err := applyProjection(tx, event); err != nil {
			return RecordResult{}, err
		}
	}
	return RecordResult{Event: event, Inserted: true}, nil
}

func eventFingerprint(in RecordInput) string {
	dropProvenance := in.Kind == KindOrder
	return stableID(in.TenantID, in.PageID, in.SubjectKey, in.Kind, in.Source, in.EntityID, in.Version, in.PolicyVersion, in.OccurredAt.UTC().Format(time.RFC3339Nano), canonicalHash(in.Payload, dropProvenance, false))
}

func createReceipt(tx *gorm.DB, eventID, tenantID, fingerprint string, sequence uint64, now time.Time) error {
	return tx.Create(&models.MessengerJourneyReceipt{ID: pkg.NewUUID(), TenantID: tenantID, EventID: eventID, Fingerprint: fingerprint, CanonicalSequence: sequence, CreatedAt: now}).Error
}

func semanticDuplicate(tx *gorm.DB, in RecordInput) (*models.MessengerJourneyEvent, error) {
	var sequence uint64
	switch in.Kind {
	case KindReferral:
		natural := referralNaturalKey(in)
		if natural == nil {
			return nil, nil
		}
		var event models.MessengerJourneyEvent
		err := tx.First(&event, "tenant_id = ? AND natural_key = ?", in.TenantID, *natural).Error
		if err == nil {
			return &event, nil
		}
		if !errorsIsNotFound(err) {
			return nil, err
		}
	case KindLabels:
		var row models.MessengerJourneyCustomer
		err := tx.Select("labels_hash,labels_sequence,labels_observed_at").First(&row, "subject_id = ?", in.SubjectKey).Error
		if err == nil && row.LabelsHash == canonicalHash(in.Payload, false, true) {
			sequence = row.LabelsSequence
			if row.LabelsObservedAt == nil || row.LabelsObservedAt.Before(in.ObservedAt) {
				if err := tx.Model(&models.MessengerJourneyCustomer{}).Where("subject_id = ?", in.SubjectKey).Updates(map[string]any{
					"labels_observed_at": in.ObservedAt, "channel_id": in.ChannelID, "conversation_id": in.ConversationID, "updated_at": time.Now().UTC(),
				}).Error; err != nil {
					return nil, err
				}
			}
		} else if err != nil && !errorsIsNotFound(err) {
			return nil, err
		}
	case KindClassification:
		var row models.MessengerJourneyCustomer
		err := tx.Select("classification,classification_policy_version,classification_sequence").First(&row, "subject_id = ?", in.SubjectKey).Error
		var body struct {
			Classification string `json:"classification"`
		}
		_ = json.Unmarshal(in.Payload, &body)
		if err == nil && row.Classification == body.Classification && row.ClassificationPolicyVersion == in.PolicyVersion {
			sequence = row.ClassificationSequence
		} else if err != nil && !errorsIsNotFound(err) {
			return nil, err
		}
	case KindOrder:
		var row models.MessengerJourneyOrder
		err := tx.Select("state_hash,last_sequence").First(&row, "id = ?", stableID(in.TenantID, in.EntityID)).Error
		if err == nil && row.StateHash == canonicalHash(in.Payload, true, false) {
			sequence = row.LastSequence
		} else if err != nil && !errorsIsNotFound(err) {
			return nil, err
		}
	case KindCoverage:
		var row models.MessengerJourneyCoverage
		err := tx.Select("payload,last_sequence").First(&row, "subject_id = ?", in.SubjectKey).Error
		if err == nil && row.Payload == string(in.Payload) {
			sequence = row.LastSequence
		} else if err != nil && !errorsIsNotFound(err) {
			return nil, err
		}
	}
	if sequence == 0 {
		return nil, nil
	}
	var event models.MessengerJourneyEvent
	if err := tx.First(&event, "tenant_id = ? AND sequence = ?", in.TenantID, sequence).Error; err != nil {
		return nil, err
	}
	return &event, nil
}

func errorsIsNotFound(err error) bool { return err == gorm.ErrRecordNotFound }

func applyProjection(tx *gorm.DB, event models.MessengerJourneyEvent) error {
	now := time.Now().UTC()
	if event.Kind == KindOrder {
		row := models.MessengerJourneyOrder{ID: stableID(event.TenantID, event.EntityID), TenantID: event.TenantID, OrderID: event.EntityID,
			SubjectKey: event.SubjectKey, PageID: event.PageID, PSID: event.PSID, ChannelID: event.ChannelID, ConversationID: event.ConversationID,
			Version: event.Version, Payload: event.Payload, StateHash: canonicalHash(json.RawMessage(event.Payload), true, false), OccurredAt: event.OccurredAt, ObservedAt: event.ObservedAt, LastSequence: event.Sequence, CreatedAt: now, UpdatedAt: now}
		var current models.MessengerJourneyOrder
		err := tx.First(&current, "id = ?", row.ID).Error
		if errorsIsNotFound(err) {
			return tx.Create(&row).Error
		}
		if err != nil {
			return err
		}
		if current.ObservedAt.After(row.ObservedAt) || (current.ObservedAt.Equal(row.ObservedAt) && current.LastSequence >= row.LastSequence) {
			return nil
		}
		return tx.Model(&current).Updates(map[string]any{"subject_id": row.SubjectKey, "page_id": row.PageID, "psid": row.PSID, "channel_id": row.ChannelID, "conversation_id": row.ConversationID,
			"version": row.Version, "payload": row.Payload, "state_hash": row.StateHash, "occurred_at": row.OccurredAt, "observed_at": row.ObservedAt, "last_sequence": row.LastSequence, "updated_at": now}).Error
	}
	if event.Kind == KindCoverage {
		var body struct {
			Status           string `json:"status"`
			ThroughMessageID string `json:"through_message_id"`
		}
		_ = json.Unmarshal([]byte(event.Payload), &body)
		row := models.MessengerJourneyCoverage{SubjectKey: event.SubjectKey, TenantID: event.TenantID, PageID: event.PageID, PSID: event.PSID,
			ConversationID: event.ConversationID, Status: body.Status, ThroughMessageID: body.ThroughMessageID, Payload: event.Payload,
			ObservedAt: event.ObservedAt, LastSequence: event.Sequence, CreatedAt: now, UpdatedAt: now}
		var current models.MessengerJourneyCoverage
		err := tx.First(&current, "subject_id = ?", row.SubjectKey).Error
		if errorsIsNotFound(err) {
			return tx.Create(&row).Error
		}
		if err != nil {
			return err
		}
		if current.ObservedAt.After(row.ObservedAt) || (current.ObservedAt.Equal(row.ObservedAt) && current.LastSequence >= row.LastSequence) {
			return nil
		}
		return tx.Model(&current).Updates(map[string]any{"conversation_id": row.ConversationID, "status": row.Status, "through_message_id": row.ThroughMessageID, "payload": row.Payload,
			"observed_at": row.ObservedAt, "last_sequence": row.LastSequence, "updated_at": now}).Error
	}

	row := models.MessengerJourneyCustomer{SubjectKey: event.SubjectKey, TenantID: event.TenantID, PageID: event.PageID, PSID: event.PSID,
		ChannelID: event.ChannelID, ConversationID: event.ConversationID, LastSequence: event.Sequence, CreatedAt: now, UpdatedAt: now}
	if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&row).Error; err != nil {
		return err
	}
	base := map[string]any{"channel_id": event.ChannelID, "conversation_id": event.ConversationID, "last_sequence": event.Sequence, "updated_at": now}
	switch event.Kind {
	case KindReferral:
		// First/latest referral follow event time, while history always follows sequence.
		if err := tx.Model(&models.MessengerJourneyCustomer{}).Where("subject_id = ? AND (first_referral_at IS NULL OR first_referral_at > ?)", event.SubjectKey, event.OccurredAt).
			Updates(map[string]any{"first_referral": event.Payload, "first_referral_at": event.OccurredAt, "last_sequence": event.Sequence, "updated_at": now}).Error; err != nil {
			return err
		}
		base["latest_referral"], base["latest_referral_at"] = event.Payload, event.OccurredAt
		return tx.Model(&models.MessengerJourneyCustomer{}).Where("subject_id = ? AND (latest_referral_at IS NULL OR latest_referral_at < ? OR (latest_referral_at = ? AND last_sequence < ?))", event.SubjectKey, event.OccurredAt, event.OccurredAt, event.Sequence).Updates(base).Error
	case KindLabels:
		base["labels"], base["labels_hash"], base["labels_sequence"], base["labels_observed_at"] = event.Payload, canonicalHash(json.RawMessage(event.Payload), false, true), event.Sequence, event.ObservedAt
		return tx.Model(&models.MessengerJourneyCustomer{}).Where("subject_id = ? AND (labels_observed_at IS NULL OR labels_observed_at < ? OR (labels_observed_at = ? AND last_sequence < ?))", event.SubjectKey, event.ObservedAt, event.ObservedAt, event.Sequence).Updates(base).Error
	case KindClassification:
		var body struct {
			Classification string `json:"classification"`
		}
		_ = json.Unmarshal([]byte(event.Payload), &body)
		base["classification"], base["classification_policy_version"], base["classification_sequence"], base["classification_observed_at"] = body.Classification, event.PolicyVersion, event.Sequence, event.ObservedAt
		return tx.Model(&models.MessengerJourneyCustomer{}).Where("subject_id = ? AND (classification_observed_at IS NULL OR classification_observed_at < ? OR (classification_observed_at = ? AND last_sequence < ?))", event.SubjectKey, event.ObservedAt, event.ObservedAt, event.Sequence).Updates(base).Error
	}
	return nil
}

func RecordReferral(ctx context.Context, db *gorm.DB, in ReferralInput) (RecordResult, error) {
	return Record(ctx, db, RecordInput{BaseInput: in.BaseInput, Kind: KindReferral, Payload: in.Payload})
}
func RecordLabels(ctx context.Context, db *gorm.DB, in LabelsInput) (RecordResult, error) {
	return Record(ctx, db, RecordInput{BaseInput: in.BaseInput, Kind: KindLabels, Payload: in.Labels})
}
func RecordClassification(ctx context.Context, db *gorm.DB, in ClassificationInput) (RecordResult, error) {
	payload := in.Payload
	if len(payload) == 0 {
		payload, _ = json.Marshal(map[string]string{"classification": in.Classification})
	}
	return Record(ctx, db, RecordInput{BaseInput: in.BaseInput, Kind: KindClassification, PolicyVersion: in.PolicyVersion, Payload: payload})
}
func RecordOrder(ctx context.Context, db *gorm.DB, in OrderInput) (RecordResult, error) {
	return Record(ctx, db, RecordInput{BaseInput: in.BaseInput, Kind: KindOrder, EntityID: in.OrderID, Payload: in.Payload})
}
func RecordCoverage(ctx context.Context, db *gorm.DB, in CoverageInput) (RecordResult, error) {
	payload := in.Payload
	if len(payload) == 0 {
		payload, _ = json.Marshal(map[string]string{"status": in.Status, "through_message_id": in.ThroughMessageID})
	}
	return Record(ctx, db, RecordInput{BaseInput: in.BaseInput, Kind: KindCoverage, Payload: payload})
}

// BindConversation attaches a conversation discovered after an early referral.
// Historical events remain an honest record of what was known when observed.
func BindConversation(ctx context.Context, db *gorm.DB, tenantID, channelID, pageID, psid, conversationID string) error {
	if tenantID == "" || pageID == "" || psid == "" || conversationID == "" {
		return ErrInvalidInput
	}
	subject := SubjectKey(tenantID, pageID, psid)
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&models.MessengerJourneyCustomer{}).Where("subject_id = ? AND tenant_id = ?", subject, tenantID).Updates(map[string]any{"channel_id": channelID, "conversation_id": conversationID}).Error; err != nil {
			return err
		}
		if err := tx.Model(&models.MessengerJourneyOrder{}).Where("subject_id = ? AND tenant_id = ?", subject, tenantID).Updates(map[string]any{"channel_id": channelID, "conversation_id": conversationID}).Error; err != nil {
			return err
		}
		return tx.Model(&models.MessengerJourneyCoverage{}).Where("subject_id = ? AND tenant_id = ?", subject, tenantID).Update("conversation_id", conversationID).Error
	})
}

// EraseSubject removes Page-scoped identifiers and content while retaining a
// non-identifying tombstone so downstream replicas can delete the same subject.
func EraseSubject(ctx context.Context, db *gorm.DB, in EraseInput) (RecordResult, error) {
	if in.TenantID == "" || in.PageID == "" || in.PSID == "" || in.EventID == "" {
		return RecordResult{}, ErrInvalidInput
	}
	subject := SubjectKey(in.TenantID, in.PageID, in.PSID)
	erasureEventID := "erasure_" + stableID(in.TenantID, in.PageID, in.EventID)
	var result RecordResult
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		sequence, err := lockTenant(tx, in.TenantID)
		if err != nil {
			return err
		}
		// The legacy immutable first-attribution row contains raw referral data
		// and must be erased atomically with the canonical journal projections.
		if err := tx.Where("tenant_id = ? AND page_id = ? AND psid = ?", in.TenantID, in.PageID, in.PSID).Delete(&models.MessengerIntakeAttribution{}).Error; err != nil {
			return err
		}
		if err := tx.Where("subject_id = ?", subject).Delete(&models.MessengerJourneyCustomer{}).Error; err != nil {
			return err
		}
		if err := tx.Where("subject_id = ?", subject).Delete(&models.MessengerJourneyOrder{}).Error; err != nil {
			return err
		}
		if err := tx.Where("subject_id = ?", subject).Delete(&models.MessengerJourneyCoverage{}).Error; err != nil {
			return err
		}
		if err := tx.Model(&models.MessengerJourneyEvent{}).Where("subject_id = ?", subject).Updates(map[string]any{"psid": "", "payload": "{}", "channel_id": "", "conversation_id": "", "entity_id": "", "version": "", "policy_version": ""}).Error; err != nil {
			return err
		}
		marker := models.MessengerJourneyErasure{SubjectKey: subject, TenantID: in.TenantID, PageID: in.PageID, EventID: erasureEventID, ErasedAt: in.ObservedAt.UTC(), CreatedAt: time.Now().UTC()}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&marker).Error; err != nil {
			return err
		}
		deletion, err := normalizedInput(RecordInput{BaseInput: BaseInput{EventID: erasureEventID, TenantID: in.TenantID, PageID: in.PageID, Source: in.Source, OccurredAt: in.ObservedAt, ObservedAt: in.ObservedAt}, Kind: KindDeletion, SubjectKey: subject, Payload: json.RawMessage(`{}`)})
		if err != nil {
			return err
		}
		result, err = recordLocked(tx, deletion, &sequence)
		return err
	})
	return result, err
}
