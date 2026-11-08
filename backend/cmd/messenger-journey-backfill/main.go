// messenger-journey-backfill imports existing CQA observations without invoking AI.
// It never invents transitions between the first and latest saved label snapshot.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/vietbui/chat-quality-agent/channels"
	"github.com/vietbui/chat-quality-agent/config"
	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/db/models"
	"github.com/vietbui/chat-quality-agent/messengerjourney"
	"github.com/vietbui/chat-quality-agent/messengerlabels"
	"gorm.io/gorm"
)

type checkpoint struct {
	Tenant, Channel, Kind, After string
	StartedAt                    time.Time
}
type backfillIssue struct {
	SourceID string `json:"source_row_id"`
	Reason   string `json:"reason"`
}

type outcome struct {
	OrderID            string   `json:"order_id"`
	OrderRef           string   `json:"order_ref"`
	ValueVND           *int64   `json:"value_vnd"`
	Status             string   `json:"status"`
	OrderedAt          string   `json:"ordered_at"`
	EvidenceMessageIDs []string `json:"evidence_message_ids"`
	Confidence         float64  `json:"confidence"`
	SourceResultID     string   `json:"source_result_id,omitempty"`
}
type runner struct {
	database          *gorm.DB
	scope             checkpoint
	apply             bool
	recorded, skipped int
	issues            []backfillIssue
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() error {
	tenant := flag.String("tenant", "", "required tenant ID")
	channel := flag.String("channel", "", "optional Facebook channel ID")
	kind := flag.String("kind", "", "required: referrals, labels, or orders")
	path := flag.String("checkpoint", "", "checkpoint file (required with --apply)")
	limit := flag.Int("limit", 100, "maximum source rows this invocation (1..1000)")
	apply := flag.Bool("apply", false, "write observations; default is read-only dry-run")
	flag.Parse()
	if *tenant == "" || (*kind != "referrals" && *kind != "labels" && *kind != "orders") || *limit < 1 || *limit > 1000 {
		return errors.New("require --tenant, --kind referrals|labels|orders, and --limit 1..1000")
	}
	if *apply && *path == "" {
		return errors.New("--apply requires --checkpoint")
	}
	scope := checkpoint{Tenant: *tenant, Channel: *channel, Kind: *kind}
	if *path != "" {
		b, err := os.ReadFile(*path)
		if err == nil {
			var saved checkpoint
			if json.Unmarshal(b, &saved) != nil || saved.Tenant != scope.Tenant || saved.Channel != scope.Channel || saved.Kind != scope.Kind {
				return errors.New("checkpoint scope mismatch")
			}
			scope = saved
		} else if !os.IsNotExist(err) {
			return err
		}
	}
	if scope.StartedAt.IsZero() {
		scope.StartedAt = time.Now().UTC()
	}
	if *apply {
		if err := saveCheckpoint(*path, scope); err != nil {
			return err
		}
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if *apply && !cfg.MessengerJourneyEnabled {
		return errors.New("CQA_MESSENGER_JOURNEY_ENABLED must be true for --apply")
	}
	if err = db.Connect(cfg.DSN(), true); err != nil {
		return err
	}
	// Schema is deployed separately. Never invoke the application's broad AutoMigrate here.
	r := runner{database: db.DB, scope: scope, apply: *apply}
	ids, err := r.ids(*limit)
	if err != nil {
		return err
	}
	for _, id := range ids {
		if err = r.row(context.Background(), id); err != nil {
			return fmt.Errorf("backfill failed; checkpoint retained before source row: %w", err)
		}
		r.scope.After = id
		if *apply {
			if err = saveCheckpoint(*path, r.scope); err != nil {
				return err
			}
		}
	}
	report := map[string]any{"dry_run": !*apply, "source_rows": len(ids), "events_inserted": r.recorded, "rows_unresolvable": r.skipped, "may_have_more": len(ids) == *limit, "kind": *kind, "needs_attention": r.issues}
	return json.NewEncoder(os.Stdout).Encode(report)
}
func saveCheckpoint(path string, c checkpoint) error {
	b, err := json.Marshal(c)
	if err != nil {
		return err
	}
	// The file contains source row identifiers only, never customer IDs or payloads.
	f, err := os.CreateTemp(dir(path), ".journey-checkpoint-*")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(b)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(name, path)
}
func dir(path string) string {
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '/' {
			if i == 0 {
				return "/"
			}
			return path[:i]
		}
	}
	return "."
}
func (r *runner) ids(limit int) ([]string, error) {
	var query *gorm.DB
	switch r.scope.Kind {
	case "referrals":
		query = r.database.Model(&models.MessengerIntakeAttribution{}).Where("tenant_id=? AND id>?", r.scope.Tenant, r.scope.After)
		if r.scope.Channel != "" {
			query = query.Where("channel_id=?", r.scope.Channel)
		}
	case "labels":
		query = r.database.Model(&models.MessengerLabelSnapshot{}).Select("conversation_id AS id").Where("tenant_id=? AND conversation_id>?", r.scope.Tenant, r.scope.After)
		if r.scope.Channel != "" {
			query = query.Where("channel_id=?", r.scope.Channel)
		}
	case "orders":
		query = r.database.Table("job_results AS j").Select("j.id AS id").Joins("JOIN conversations c ON c.id=j.conversation_id AND c.tenant_id=j.tenant_id").Where("j.tenant_id=? AND j.id>? AND j.result_type='conversation_insight'", r.scope.Tenant, r.scope.After)
		if r.scope.Channel != "" {
			query = query.Where("c.channel_id=?", r.scope.Channel)
		}
	}
	var rows []struct{ ID string }
	err := query.Order("id ASC").Limit(limit).Scan(&rows).Error
	ids := make([]string, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.ID)
	}
	return ids, err
}
func eventID(parts ...string) string {
	b, _ := json.Marshal(parts)
	sum := sha256.Sum256(b)
	return "backfill:" + hex.EncodeToString(sum[:])
}
func (r *runner) write(ctx context.Context, in messengerjourney.RecordInput) error {
	if !r.apply {
		return nil
	}
	result, err := messengerjourney.Record(ctx, r.database, in)
	if errors.Is(err, messengerjourney.ErrSubjectErased) {
		r.skipped++
		return nil
	}
	if result.Inserted {
		r.recorded++
	}
	return err
}
func (r *runner) row(ctx context.Context, id string) error {
	switch r.scope.Kind {
	case "referrals":
		var v models.MessengerIntakeAttribution
		if err := r.database.First(&v, "id=? AND tenant_id=?", id, r.scope.Tenant).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				r.skipped++
				return nil
			}
			return err
		}
		if v.PageID == "" || v.PSID == "" {
			r.skipped++
			return nil
		}
		if v.CapturedAt.Before(messengerjourney.MinValidReferralTime) {
			// An intake row from before the time guard (Unix seconds read as milliseconds).
			v.CapturedAt = messengerjourney.RepairedReferralTime(v.CapturedAt, v.CreatedAt)
		}
		payload, _ := json.Marshal(map[string]any{"ad_id": v.AdID, "ref": v.Ref, "source": v.Source, "type": v.ReferralType, "message_id": v.MessageID, "referred_at": v.CapturedAt, "received_at": v.CreatedAt, "ads_context": legacyAdsContext(v.RawReferral), "backfill": true})
		conv := ""
		if v.ConversationID != nil {
			conv = *v.ConversationID
		}
		return r.write(ctx, messengerjourney.RecordInput{BaseInput: messengerjourney.BaseInput{EventID: eventID("referral", v.ID), TenantID: v.TenantID, PageID: v.PageID, PSID: v.PSID, ChannelID: v.ChannelID, ConversationID: conv, Source: "cqa_backfill", OccurredAt: v.CapturedAt, ObservedAt: v.CreatedAt}, Kind: messengerjourney.KindReferral, Payload: payload})
	case "labels":
		var v models.MessengerLabelSnapshot
		if err := r.database.First(&v, "conversation_id=? AND tenant_id=?", id, r.scope.Tenant).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				r.skipped++
				return nil
			}
			return err
		}
		var ch models.Channel
		if err := r.database.First(&ch, "id=? AND tenant_id=? AND channel_type='facebook'", v.ChannelID, r.scope.Tenant).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				r.skipped++
				return nil
			}
			return err
		}
		if v.PSID == "" || ch.ExternalID == "" {
			r.skipped++
			return nil
		}
		base := messengerjourney.BaseInput{TenantID: v.TenantID, PageID: ch.ExternalID, PSID: v.PSID, ChannelID: v.ChannelID, ConversationID: v.ConversationID, Source: "cqa_backfill"}
		if v.IntakeLabels != nil && v.IntakeLabelsCapturedAt != nil {
			base.EventID = eventID("labels_first", id)
			base.ObservedAt = *v.IntakeLabelsCapturedAt
			base.OccurredAt = base.ObservedAt
			if err := r.write(ctx, messengerjourney.RecordInput{BaseInput: base, Kind: messengerjourney.KindLabels, Payload: json.RawMessage(*v.IntakeLabels)}); err != nil {
				return err
			}
		}
		if v.Status == "success" && v.CheckedAt != nil {
			base.EventID = eventID("labels_latest", id, v.CheckedAt.UTC().Format(time.RFC3339Nano))
			base.ObservedAt = *v.CheckedAt
			base.OccurredAt = base.ObservedAt
			if err := r.write(ctx, messengerjourney.RecordInput{BaseInput: base, Kind: messengerjourney.KindLabels, Payload: json.RawMessage(v.Labels)}); err != nil {
				return err
			}
			return r.classification(ctx, ch, v)
		}
		return nil
	case "orders":
		var v models.JobResult
		if err := r.database.First(&v, "id=? AND tenant_id=?", id, r.scope.Tenant).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				r.skipped++
				return nil
			}
			return err
		}
		var conv models.Conversation
		if err := r.database.First(&conv, "id=? AND tenant_id=?", v.ConversationID, r.scope.Tenant).Error; err != nil {
			return err
		}
		var ch models.Channel
		if err := r.database.First(&ch, "id=? AND tenant_id=? AND channel_type='facebook'", conv.ChannelID, r.scope.Tenant).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				r.skipped++
				return nil
			}
			return err
		}
		psid := messengerlabels.ParticipantPSID(conv, ch.ExternalID)
		if psid == "" {
			var snap models.MessengerLabelSnapshot
			err := r.database.First(&snap, "conversation_id=? AND tenant_id=?", conv.ID, r.scope.Tenant).Error
			if err == nil {
				psid = snap.PSID
			} else if !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
		}
		if psid == "" {
			var attr models.MessengerIntakeAttribution
			err := r.database.First(&attr, "conversation_id=? AND tenant_id=?", conv.ID, r.scope.Tenant).Error
			if err == nil {
				psid = attr.PSID
			} else if !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
		}
		if psid == "" || ch.ExternalID == "" {
			r.skipped++
			return nil
		}
		var detail struct {
			OrderOutcomes []outcome `json:"order_outcomes"`
		}
		if err := json.Unmarshal([]byte(v.Detail), &detail); err != nil {
			return err
		}
		for _, o := range detail.OrderOutcomes {
			if o.OrderID == "" {
				r.skipped++
				continue
			}
			occurred, err := time.Parse(time.RFC3339Nano, o.OrderedAt)
			if err != nil {
				r.skipped++
				continue
			}
			if len(o.EvidenceMessageIDs) == 0 || o.ValueVND == nil {
				o.Status = "review"
			}
			o.SourceResultID = v.ID
			payload, err := json.Marshal(o)
			if err != nil {
				return err
			}
			if err = messengerjourney.ValidateOrderPayload(payload, v.CreatedAt); err != nil {
				r.skipped++
				if len(r.issues) < 100 {
					r.issues = append(r.issues, backfillIssue{SourceID: v.ID, Reason: "invalid_order_contract"})
				}
				continue
			}
			if err = r.write(ctx, messengerjourney.RecordInput{BaseInput: messengerjourney.BaseInput{EventID: eventID("order", v.ID, o.OrderID), TenantID: v.TenantID, PageID: ch.ExternalID, PSID: psid, ChannelID: ch.ID, ConversationID: conv.ID, Source: "cqa_backfill", OccurredAt: occurred, ObservedAt: v.CreatedAt}, Kind: messengerjourney.KindOrder, EntityID: o.OrderID, Payload: payload}); err != nil {
				return err
			}
		}
		return nil
	}
	return errors.New("unknown source kind")
}

// Classification here is derived with the policy available at migration time.
// It is deliberately not presented as the customer's historical classification.
func (r *runner) classification(ctx context.Context, ch models.Channel, snapshot models.MessengerLabelSnapshot) error {
	var state models.MessengerLabelState
	err := r.database.First(&state, "channel_id=? AND tenant_id=?", ch.ID, ch.TenantID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	var labels []channels.FacebookLabel
	var rules []messengerlabels.Rule
	if json.Unmarshal([]byte(snapshot.Labels), &labels) != nil || json.Unmarshal([]byte(state.Rules), &rules) != nil {
		return errors.New("invalid saved labels or classification policy")
	}
	observed := r.scope.StartedAt
	if observed.IsZero() {
		observed = snapshot.CheckedAt.UTC()
	}
	classification := messengerlabels.Classify(labels, nil, snapshot.Status, snapshot.CheckedAt, rules, state.Enabled, observed)
	rulesJSON, _ := json.Marshal(rules)
	hash := sha256.Sum256(append(rulesJSON, 0))
	version := hex.EncodeToString(hash[:16])
	payload, _ := json.Marshal(map[string]any{"classification": classification, "source_activity_at": snapshot.CheckedAt.UTC().Format(time.RFC3339Nano), "backfill": true})
	return r.write(ctx, messengerjourney.RecordInput{BaseInput: messengerjourney.BaseInput{EventID: eventID("classification", snapshot.ConversationID, version, classification, observed.Format(time.RFC3339Nano)), TenantID: ch.TenantID, PageID: ch.ExternalID, PSID: snapshot.PSID, ChannelID: ch.ID, ConversationID: snapshot.ConversationID, Source: "cqa_backfill_derived", ObservedAt: observed, OccurredAt: observed}, Kind: messengerjourney.KindClassification, PolicyVersion: version, Payload: payload})
}

func legacyAdsContext(raw string) map[string]string {
	var referral struct {
		AdsContext map[string]any `json:"ads_context_data"`
	}
	_ = json.Unmarshal([]byte(raw), &referral)
	selected := map[string]string{}
	for _, key := range []string{"ad_title", "photo_url", "video_url", "post_id", "product_id"} {
		if value, ok := referral.AdsContext[key].(string); ok && len(value) <= 2048 {
			selected[key] = value
		}
	}
	return selected
}
