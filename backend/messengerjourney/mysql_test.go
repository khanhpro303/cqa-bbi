package messengerjourney

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	mysqldriver "github.com/go-sql-driver/mysql"
	"github.com/vietbui/chat-quality-agent/db/models"
	"github.com/vietbui/chat-quality-agent/pkg"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func journeyTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := os.Getenv("MESSENGER_JOURNEY_TEST_DSN")
	if dsn == "" {
		dsn = os.Getenv("MESSENGER_LABELS_TEST_DSN")
	}
	if dsn == "" {
		t.Skip("MySQL integration requires MESSENGER_JOURNEY_TEST_DSN")
	}
	cfg, err := mysqldriver.ParseDSN(dsn)
	if err != nil {
		t.Fatal(err)
	}
	cfg.DBName = ""
	cfg.ParseTime = true
	cfg.Timeout = 5 * time.Second
	admin, err := gorm.Open(mysql.Open(cfg.FormatDSN()), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	name := "cqa_journey_test_" + strings.ReplaceAll(pkg.NewUUID(), "-", "")
	if err = admin.Exec("CREATE DATABASE `" + name + "` CHARACTER SET utf8mb4").Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = admin.Exec("DROP DATABASE `" + name + "`").Error })
	cfg.DBName = name
	db, err := gorm.Open(mysql.Open(cfg.FormatDSN()), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	pool, _ := db.DB()
	pool.SetMaxOpenConns(20)
	t.Cleanup(func() { _ = pool.Close() })
	if err = db.AutoMigrate(&models.MessengerIntakeAttribution{}, &models.MessengerJourneySequence{}, &models.MessengerJourneyReceipt{}, &models.MessengerJourneyEvent{}, &models.MessengerJourneyCustomer{}, &models.MessengerJourneyOrder{}, &models.MessengerJourneyCoverage{}, &models.MessengerJourneyErasure{}); err != nil {
		t.Fatal(err)
	}
	return db
}

func base(event string, at time.Time) BaseInput {
	return BaseInput{EventID: event, TenantID: "tenant-1", PageID: "123", PSID: "456", Source: "test", OccurredAt: at, ObservedAt: at}
}

func validOrderPayload(orderID, status string, at time.Time, provenance string) json.RawMessage {
	value := map[string]any{"order_id": orderID, "order_ref": "ref-" + orderID, "status": status, "ordered_at": at.UTC().Format(time.RFC3339Nano), "confidence": 0.9, "evidence_message_ids": []string{"message-1"}}
	if status == "confirmed" {
		value["value_vnd"] = int64(100_000)
	}
	if provenance != "" {
		value["source_result_id"] = provenance
	}
	payload, _ := json.Marshal(value)
	return payload
}

func TestJourneyHistoryProjectionDedupAndErasure(t *testing.T) {
	db := journeyTestDB(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Millisecond)
	if _, err := RecordReferral(ctx, db, ReferralInput{BaseInput: base("ref-b", now.Add(time.Hour)), Payload: []byte(`{"ad_id":"B"}`)}); err != nil {
		t.Fatal(err)
	}
	if _, err := RecordReferral(ctx, db, ReferralInput{BaseInput: base("ref-a", now), Payload: []byte(`{"ad_id":"A"}`)}); err != nil {
		t.Fatal(err)
	}
	dup, err := RecordReferral(ctx, db, ReferralInput{BaseInput: base("ref-a", now), Payload: []byte(`{"ad_id":"A"}`)})
	if err != nil || dup.Inserted {
		t.Fatalf("event dedupe failed: %+v %v", dup, err)
	}
	conflictBase := base("ref-a", now)
	conflictBase.PSID = "different-psid"
	if _, err := RecordReferral(ctx, db, ReferralInput{BaseInput: conflictBase, Payload: []byte(`{"ad_id":"A"}`)}); !errors.Is(err, ErrEventConflict) {
		t.Fatalf("reused event ID accepted for another subject: %v", err)
	}
	crossAt := now.Add(30 * time.Minute)
	if result, err := RecordReferral(ctx, db, ReferralInput{BaseInput: base("relay-ref", crossAt), Payload: []byte(`{"ad_id":"C","ref":"r","source":"ADS","type":"OPEN_THREAD"}`)}); err != nil || !result.Inserted {
		t.Fatalf("cross-source initial: %+v %v", result, err)
	}
	if result, err := RecordReferral(ctx, db, ReferralInput{BaseInput: base("backfill-ref", crossAt), Payload: []byte(`{"ad_id":"C","ref":"r","source":"ADS","referral_type":"OPEN_THREAD","ingest_source":"backfill"}`)}); err != nil || result.Inserted {
		t.Fatalf("cross-source natural dedupe: %+v %v", result, err)
	}
	if _, err := RecordReferral(ctx, db, ReferralInput{BaseInput: base("backfill-ref", crossAt), Payload: []byte(`{"ad_id":"DIFFERENT"}`)}); !errors.Is(err, ErrEventConflict) {
		t.Fatalf("semantic alias receipt failed: %v", err)
	}
	midAt := now.Add(45 * time.Minute)
	for _, mid := range []string{"m-1", "m-2"} {
		result, err := RecordReferral(ctx, db, ReferralInput{BaseInput: base("event-"+mid, midAt), Payload: json.RawMessage(`{"ad_id":"same","source":"ADS","message_id":"` + mid + `"}`)})
		if err != nil || !result.Inserted {
			t.Fatalf("distinct message %s collapsed: %+v %v", mid, result, err)
		}
	}
	labels := LabelsInput{BaseInput: base("labels-1", now), Labels: []byte(`[{"id":"7"}]`)}
	if result, err := RecordLabels(ctx, db, labels); err != nil || !result.Inserted {
		t.Fatalf("labels insert: %+v %v", result, err)
	}
	labels.EventID = "labels-retry"
	if result, err := RecordLabels(ctx, db, labels); err != nil || result.Inserted {
		t.Fatalf("semantic dedupe: %+v %v", result, err)
	}
	order := OrderInput{BaseInput: base("order-1", now), OrderID: "order-A", Payload: validOrderPayload("order-A", "confirmed", now, "")}
	if _, err := RecordOrder(ctx, db, order); err != nil {
		t.Fatal(err)
	}
	orderRetry := order
	orderRetry.Payload = validOrderPayload("order-A", "confirmed", now, "new-run")
	if result, err := RecordOrder(ctx, db, orderRetry); err != nil || result.Inserted {
		t.Fatalf("order provenance caused receipt conflict: %+v %v", result, err)
	}
	late := order
	late.EventID = "order-late"
	late.ObservedAt = now.Add(-time.Hour)
	late.Payload = validOrderPayload("order-A", "cancelled", now.Add(-2*time.Hour), "")
	if result, err := RecordOrder(ctx, db, late); err != nil || !result.Inserted {
		t.Fatalf("late revision history: %+v %v", result, err)
	}
	var currentOrder models.MessengerJourneyOrder
	if err := db.First(&currentOrder, "order_id = ?", "order-A").Error; err != nil {
		t.Fatal(err)
	}
	var orderBody struct {
		Status string `json:"status"`
	}
	_ = json.Unmarshal([]byte(currentOrder.Payload), &orderBody)
	if orderBody.Status != "confirmed" {
		t.Fatalf("late event overwrote current order: %+v", currentOrder)
	}
	labels.EventID = "labels-after-other-events"
	labels.ObservedAt = now.Add(90 * time.Minute)
	if result, err := RecordLabels(ctx, db, labels); err != nil || result.Inserted || result.Event.Kind != KindLabels {
		t.Fatalf("label dedupe returned wrong source event: %+v %v", result, err)
	}
	var customer models.MessengerJourneyCustomer
	if err := db.First(&customer, "subject_id = ?", SubjectKey("tenant-1", "123", "456")).Error; err != nil {
		t.Fatal(err)
	}
	if customer.LabelsObservedAt == nil || !customer.LabelsObservedAt.Equal(labels.ObservedAt) {
		t.Fatalf("unchanged label observation did not refresh freshness: %+v", customer.LabelsObservedAt)
	}
	if err := db.First(&customer, "subject_id = ?", SubjectKey("tenant-1", "123", "456")).Error; err != nil {
		t.Fatal(err)
	}
	adID := func(raw *string) string {
		if raw == nil {
			return ""
		}
		var body struct {
			AdID string `json:"ad_id"`
		}
		_ = json.Unmarshal([]byte(*raw), &body)
		return body.AdID
	}
	if adID(customer.FirstReferral) != "A" || adID(customer.LatestReferral) != "B" {
		t.Fatalf("wrong referral projection: %+v", customer)
	}
	legacy := models.MessengerIntakeAttribution{ID: pkg.NewUUID(), TenantID: "tenant-1", ChannelID: "channel-1", PageID: "123", PSID: "456", AdID: "legacy-ad", RawReferral: `{"ad_id":"legacy-ad","private":"raw"}`, CapturedAt: now, CreatedAt: now, UpdatedAt: now}
	if err := db.Create(&legacy).Error; err != nil {
		t.Fatal(err)
	}
	erased, err := EraseSubject(ctx, db, EraseInput{EventID: "delete-1", TenantID: "tenant-1", PageID: "123", PSID: "456", Source: "test", ObservedAt: now.Add(2 * time.Hour)})
	if err != nil || !erased.Inserted {
		t.Fatalf("erase: %+v %v", erased, err)
	}
	if _, err = RecordReferral(ctx, db, ReferralInput{BaseInput: base("delayed", now.Add(3*time.Hour)), Payload: []byte(`{"ad_id":"C"}`)}); !errors.Is(err, ErrSubjectErased) {
		t.Fatalf("delayed replay err=%v", err)
	}
	if _, err = RecordReferral(ctx, db, ReferralInput{BaseInput: base("ref-a", now), Payload: []byte(`{"ad_id":"A"}`)}); !errors.Is(err, ErrSubjectErased) {
		t.Fatalf("exact receipt retry bypassed erasure: %v", err)
	}
	var events []models.MessengerJourneyEvent
	if err = db.Order("sequence").Find(&events).Error; err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		if event.PSID != "" || event.Payload != "{}" {
			t.Fatalf("PII survived erasure: %+v", event)
		}
	}
	var orders int64
	db.Model(&models.MessengerJourneyOrder{}).Count(&orders)
	if orders != 0 {
		t.Fatalf("orders survived: %d", orders)
	}
	var legacyRows int64
	if err := db.Model(&models.MessengerIntakeAttribution{}).Where("tenant_id = ? AND page_id = ? AND psid = ?", "tenant-1", "123", "456").Count(&legacyRows).Error; err != nil {
		t.Fatal(err)
	}
	if legacyRows != 0 {
		t.Fatalf("legacy intake PII survived erasure: %d", legacyRows)
	}
}

func TestJourneySequenceRollbackAndConcurrentCommitOrder(t *testing.T) {
	db := journeyTestDB(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Millisecond)
	wantRollback := errors.New("rollback")
	err := db.Transaction(func(tx *gorm.DB) error {
		if _, err := RecordInTransaction(ctx, tx, RecordInput{BaseInput: base("rollback", now), Kind: KindReferral, Payload: []byte(`{}`)}); err != nil {
			return err
		}
		return wantRollback
	})
	if !errors.Is(err, wantRollback) {
		t.Fatal(err)
	}
	watermark, err := HighWatermark(ctx, db, "tenant-1")
	if err != nil || watermark != 0 {
		t.Fatalf("rollback watermark=%d err=%v", watermark, err)
	}
	locked := make(chan struct{})
	release := make(chan struct{})
	firstDone := make(chan error, 1)
	go func() {
		firstDone <- db.Transaction(func(tx *gorm.DB) error {
			_, err := RecordInTransaction(ctx, tx, RecordInput{BaseInput: base("first", now), Kind: KindReferral, Payload: []byte(`{}`)})
			if err != nil {
				return err
			}
			close(locked)
			<-release
			return nil
		})
	}()
	<-locked
	secondDone := make(chan error, 1)
	go func() {
		_, err := Record(ctx, db, RecordInput{BaseInput: base("second", now.Add(time.Second)), Kind: KindReferral, Payload: []byte(`{}`)})
		secondDone <- err
	}()
	select {
	case err := <-secondDone:
		t.Fatalf("second bypassed tenant lock: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	if err := <-firstDone; err != nil {
		t.Fatal(err)
	}
	if err := <-secondDone; err != nil {
		t.Fatal(err)
	}
	page, err := ListEvents(ctx, db, EventQuery{TenantID: "tenant-1", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Events) != 2 || page.Events[0].EventID != "first" || page.Events[0].Sequence != 1 || page.Events[1].Sequence != 2 {
		t.Fatalf("unsafe order: %+v", page.Events)
	}
	inserted := make(chan struct{})
	releaseInsert := make(chan struct{})
	insertDone := make(chan error, 1)
	go func() {
		insertDone <- db.Transaction(func(tx *gorm.DB) error {
			_, err := RecordInTransaction(ctx, tx, RecordInput{BaseInput: base("before-erase", now.Add(2*time.Second)), Kind: KindReferral, Payload: []byte(`{"ad_id":"last"}`)})
			if err != nil {
				return err
			}
			close(inserted)
			<-releaseInsert
			return nil
		})
	}()
	<-inserted
	eraseDone := make(chan error, 1)
	go func() {
		_, err := EraseSubject(ctx, db, EraseInput{EventID: "concurrent-erase", TenantID: "tenant-1", PageID: "123", PSID: "456", Source: "test", ObservedAt: now.Add(3 * time.Second)})
		eraseDone <- err
	}()
	select {
	case err := <-eraseDone:
		t.Fatalf("erase bypassed tenant lock: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	close(releaseInsert)
	if err := <-insertDone; err != nil {
		t.Fatal(err)
	}
	if err := <-eraseDone; err != nil {
		t.Fatal(err)
	}
	var erasedEvents []models.MessengerJourneyEvent
	if err := db.Find(&erasedEvents).Error; err != nil {
		t.Fatal(err)
	}
	for _, event := range erasedEvents {
		if event.PSID != "" || event.Payload != "{}" || event.ChannelID != "" || event.ConversationID != "" {
			t.Fatalf("concurrent insert survived erasure: %+v", event)
		}
	}
	var current int64
	if err := db.Model(&models.MessengerJourneyCustomer{}).Count(&current).Error; err != nil {
		t.Fatal(err)
	}
	if current != 0 {
		t.Fatalf("current projection survived erasure: %d", current)
	}
}

func TestConcurrentSequencesAreUnique(t *testing.T) {
	db := journeyTestDB(t)
	ctx := context.Background()
	now := time.Now().UTC()
	const count = 12
	var wg sync.WaitGroup
	errs := make(chan error, count)
	for i := 0; i < count; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			in := base("event-"+string(rune('a'+i)), now.Add(time.Duration(i)*time.Millisecond))
			_, err := RecordReferral(ctx, db, ReferralInput{BaseInput: in, Payload: []byte(`{}`)})
			errs <- err
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	page, err := ListEvents(ctx, db, EventQuery{TenantID: "tenant-1", Limit: count})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Events) != count {
		t.Fatalf("got %d", len(page.Events))
	}
	for i, e := range page.Events {
		if e.Sequence != uint64(i+1) {
			t.Fatalf("gap at %d: %d", i, e.Sequence)
		}
	}
}
