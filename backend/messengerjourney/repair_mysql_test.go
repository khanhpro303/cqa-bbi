package messengerjourney

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/vietbui/chat-quality-agent/db/models"
	"gorm.io/gorm"
)

func TestRepairedReferralTimeDecodesSecondsReadAsMilliseconds(t *testing.T) {
	received := time.Date(2026, 11, 2, 9, 4, 58, 403_000_000, time.UTC)
	bad := time.UnixMilli(time.Date(2026, 11, 2, 9, 4, 56, 0, time.UTC).Unix())
	if got := RepairedReferralTime(bad, received); !got.Equal(time.Date(2026, 11, 2, 9, 4, 56, 0, time.UTC)) {
		t.Fatalf("decoded %s", got)
	}
	if got := RepairedReferralTime(time.Unix(0, 0), received); !got.Equal(received) {
		t.Fatalf("implausible value must fall back to receive time, got %s", got)
	}
	in, err := normalizedInput(RecordInput{BaseInput: BaseInput{EventID: "e", TenantID: "t", PageID: "1", PSID: "2", Source: "s", OccurredAt: bad, ObservedAt: received}, Kind: KindReferral})
	if err != nil || !in.OccurredAt.Equal(time.Date(2026, 11, 2, 9, 4, 56, 0, time.UTC)) {
		t.Fatalf("Record must not keep a 1970 referral: %s %v", in.OccurredAt, err)
	}
}

func TestRepairReferralTimesFixesEventReceiptCustomerAndIntake(t *testing.T) {
	db := journeyTestDB(t)
	ctx := context.Background()
	real := time.Date(2026, 11, 2, 9, 4, 56, 0, time.UTC)
	received := real.Add(2403 * time.Millisecond)
	bad := time.UnixMilli(real.Unix()).UTC()
	intake := models.MessengerIntakeAttribution{ID: "intake-1", TenantID: "tenant-1", ChannelID: "channel-1", PageID: "123", PSID: "456", RawReferral: `{}`, CapturedAt: bad, CreatedAt: received, UpdatedAt: received}
	if err := db.Create(&intake).Error; err != nil {
		t.Fatal(err)
	}
	payload := func(referredAt, receivedAt time.Time) json.RawMessage {
		encoded, _ := json.Marshal(map[string]any{"ref": "abz_test", "source": "SHORTLINK", "type": "OPEN_THREAD", "referred_at": referredAt, "received_at": receivedAt, "backfill": true})
		return encoded
	}
	// Store the old bad event directly: Record now repairs such input itself.
	in, err := normalizedInput(RecordInput{BaseInput: BaseInput{EventID: "backfill:1", TenantID: "tenant-1", PageID: "123", PSID: "456", Source: "cqa_backfill", OccurredAt: received, ObservedAt: received}, Kind: KindReferral, Payload: payload(bad, received)})
	if err != nil {
		t.Fatal(err)
	}
	in.OccurredAt = bad
	if err = db.Transaction(func(tx *gorm.DB) error {
		sequence, err := lockTenant(tx, in.TenantID)
		if err != nil {
			return err
		}
		_, err = recordLocked(tx, in, &sequence)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	other := base("relay-1", real.Add(time.Hour))
	other.PSID = "789"
	if _, err = RecordReferral(ctx, db, ReferralInput{BaseInput: other, Payload: []byte(`{"ad_id":"A"}`)}); err != nil {
		t.Fatal(err)
	}

	dry, err := RepairReferralTimes(ctx, db, false)
	if err != nil || dry.IntakeRows != 1 || dry.Events != 1 || len(dry.Times) != 1 || dry.Times[0].OccurredAt != "2026-11-02T09:04:56.000Z" {
		t.Fatalf("dry run: %+v %v", dry, err)
	}
	var stored models.MessengerJourneyEvent
	if db.First(&stored, "event_id = ?", "backfill:1").Error != nil || !stored.OccurredAt.Equal(bad) {
		t.Fatalf("dry run wrote: %s", stored.OccurredAt)
	}

	done, err := RepairReferralTimes(ctx, db, true)
	if err != nil || done.IntakeRows != 1 || done.Events != 1 || done.Receipts != 1 || done.Customers != 1 {
		t.Fatalf("apply: %+v %v", done, err)
	}
	if err = db.First(&stored, "event_id = ?", "backfill:1").Error; err != nil || !stored.OccurredAt.Equal(real) {
		t.Fatalf("event time: %s %v", stored.OccurredAt, err)
	}
	var body map[string]any
	_ = json.Unmarshal([]byte(stored.Payload), &body)
	if body["referred_at"] != done.Times[0].PayloadReferredAt || done.Times[0].PayloadReferredAt == "" {
		t.Fatalf("payload referred_at %v vs %q", body["referred_at"], done.Times[0].PayloadReferredAt)
	}
	if parsed, _ := time.Parse(time.RFC3339Nano, done.Times[0].PayloadReferredAt); !parsed.Equal(real) {
		t.Fatalf("payload time %s", parsed)
	}
	var receipt models.MessengerJourneyReceipt
	if db.First(&receipt, "event_id = ?", "backfill:1").Error != nil || receipt.Fingerprint != eventFingerprint(recordInputOf(stored)) {
		t.Fatal("receipt fingerprint not recomputed")
	}
	if stored.NaturalKey == nil || *stored.NaturalKey != *referralNaturalKey(recordInputOf(stored)) {
		t.Fatal("natural key not recomputed")
	}
	var customer models.MessengerJourneyCustomer
	if db.First(&customer, "subject_id = ?", stored.SubjectKey).Error != nil || customer.FirstReferralAt == nil || !customer.FirstReferralAt.Equal(real) || !customer.LatestReferralAt.Equal(real) {
		t.Fatalf("customer referral times: %+v", customer)
	}
	if db.First(&intake, "id = ?", "intake-1").Error != nil || !intake.CapturedAt.Equal(real) {
		t.Fatalf("intake captured_at %s", intake.CapturedAt)
	}

	// A backfill retry from the repaired intake row is the same event, not a conflict.
	retry, err := Record(ctx, db, RecordInput{BaseInput: BaseInput{EventID: "backfill:1", TenantID: "tenant-1", PageID: "123", PSID: "456", Source: "cqa_backfill", OccurredAt: intake.CapturedAt, ObservedAt: intake.CreatedAt}, Kind: KindReferral, Payload: payload(intake.CapturedAt, intake.CreatedAt)})
	if err != nil || retry.Inserted || retry.Event.Sequence != stored.Sequence {
		t.Fatalf("retry after repair: %+v %v", retry, err)
	}
	if again, err := RepairReferralTimes(ctx, db, true); err != nil || again.IntakeRows != 0 || again.Events != 0 {
		t.Fatalf("second run must find nothing: %+v %v", again, err)
	}
	var untouched models.MessengerJourneyEvent
	if db.First(&untouched, "event_id = ?", "relay-1").Error != nil || !untouched.OccurredAt.Equal(real.Add(time.Hour)) {
		t.Fatal("other events must not change")
	}
}
