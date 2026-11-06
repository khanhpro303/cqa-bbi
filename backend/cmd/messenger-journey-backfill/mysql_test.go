package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	driver "github.com/go-sql-driver/mysql"
	"github.com/vietbui/chat-quality-agent/db/models"
	"github.com/vietbui/chat-quality-agent/messengerjourney"
	"github.com/vietbui/chat-quality-agent/pkg"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func backfillDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := os.Getenv("MESSENGER_JOURNEY_TEST_DSN")
	if dsn == "" {
		t.Skip("requires MESSENGER_JOURNEY_TEST_DSN")
	}
	cfg, err := driver.ParseDSN(dsn)
	if err != nil {
		t.Fatal("invalid test DSN")
	}
	cfg.DBName = ""
	cfg.ParseTime = true
	admin, err := gorm.Open(mysql.Open(cfg.FormatDSN()), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal("cannot connect test MySQL")
	}
	adminPool, _ := admin.DB()
	t.Cleanup(func() { adminPool.Close() })
	name := "cqa_backfill_test_" + strings.ReplaceAll(pkg.NewUUID(), "-", "")
	if err = admin.Exec("CREATE DATABASE `" + name + "`").Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := admin.Exec("DROP DATABASE `" + name + "`").Error; err != nil {
			t.Error(err)
		}
	})
	cfg.DBName = name
	database, err := gorm.Open(mysql.Open(cfg.FormatDSN()), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	pool, _ := database.DB()
	t.Cleanup(func() { pool.Close() })
	if err = database.AutoMigrate(&models.MessengerIntakeAttribution{}, &models.MessengerJourneySequence{}, &models.MessengerJourneyEvent{}, &models.MessengerJourneyCustomer{}, &models.MessengerJourneyOrder{}, &models.MessengerJourneyCoverage{}, &models.MessengerJourneyErasure{}, &models.MessengerJourneyReceipt{}); err != nil {
		t.Fatal(err)
	}
	return database
}

func TestMySQLReferralBackfillDryRunRetryAndErasure(t *testing.T) {
	database := backfillDB(t)
	now := time.Now().UTC().Truncate(time.Millisecond)
	source := models.MessengerIntakeAttribution{ID: "row-a", TenantID: "tenant", ChannelID: "channel", PageID: "123", PSID: "456", AdID: "789", Source: "ADS", ReferralType: "OPEN_THREAD", RawReferral: `{}`, CapturedAt: now.Add(-time.Hour), CreatedAt: now, UpdatedAt: now}
	if err := database.Create(&source).Error; err != nil {
		t.Fatal(err)
	}
	other := source
	other.ID = "row-b"
	other.TenantID = "other"
	other.PSID = "999"
	if err := database.Create(&other).Error; err != nil {
		t.Fatal(err)
	}
	r := runner{database: database, scope: checkpoint{Tenant: "tenant", Kind: "referrals"}}
	ids, err := r.ids(10)
	if err != nil || len(ids) != 1 || ids[0] != "row-a" {
		t.Fatalf("tenant scope: %v %v", ids, err)
	}
	if err = r.row(context.Background(), ids[0]); err != nil {
		t.Fatal(err)
	}
	var count int64
	database.Model(&models.MessengerJourneyEvent{}).Count(&count)
	if count != 0 {
		t.Fatal("dry-run wrote history")
	}
	r.apply = true
	if err = r.row(context.Background(), ids[0]); err != nil {
		t.Fatal(err)
	}
	if err = r.row(context.Background(), ids[0]); err != nil {
		t.Fatal(err)
	}
	database.Model(&models.MessengerJourneyEvent{}).Count(&count)
	if count != 1 {
		t.Fatalf("retry event count=%d", count)
	}
	var event models.MessengerJourneyEvent
	if err = database.First(&event).Error; err != nil {
		t.Fatal(err)
	}
	if !event.OccurredAt.Equal(source.CapturedAt) || !event.ObservedAt.Equal(source.CreatedAt) {
		t.Fatal("backfill reset source timestamps")
	}
	_, err = messengerjourney.EraseSubject(context.Background(), database, messengerjourney.EraseInput{EventID: "erase", TenantID: "tenant", PageID: "123", PSID: "456", Source: "test", ObservedAt: now.Add(time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	if err = r.row(context.Background(), ids[0]); err != nil {
		t.Fatal(fmt.Errorf("erased row should be a resumable no-op: %w", err))
	}
	database.Model(&models.MessengerJourneyCustomer{}).Count(&count)
	if count != 0 {
		t.Fatal("backfill resurrected erased subject")
	}
}

func TestMySQLLabelsBackfillDerivesOnlyCurrentClassification(t *testing.T) {
	database := backfillDB(t)
	now := time.Now().UTC().Truncate(time.Millisecond)
	for _, ddl := range []string{
		`CREATE TABLE channels(id char(36) PRIMARY KEY,tenant_id char(36),channel_type varchar(20),external_id varchar(64))`,
		`CREATE TABLE messenger_label_snapshots(conversation_id char(36) PRIMARY KEY,tenant_id char(36),channel_id char(36),psid varchar(64),labels json,intake_labels json,status varchar(20),checked_at datetime(3),intake_labels_captured_at datetime(3))`,
		`CREATE TABLE messenger_label_states(channel_id char(36) PRIMARY KEY,tenant_id char(36),enabled bool,rules json)`,
	} {
		if err := database.Exec(ddl).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := database.Exec(`INSERT INTO channels VALUES('channel','tenant','facebook','123')`).Error; err != nil {
		t.Fatal(err)
	}
	if err := database.Exec(`INSERT INTO messenger_label_states VALUES('channel','tenant',true,'[{"label_id":"1","category":"potential"}]')`).Error; err != nil {
		t.Fatal(err)
	}
	if err := database.Exec(`INSERT INTO messenger_label_snapshots VALUES('conv','tenant','channel','456','[{"id":"1","page_label_name":"Tiềm năng"}]','[]','success',?,?)`, now.Add(-time.Minute), now.Add(-time.Hour)).Error; err != nil {
		t.Fatal(err)
	}
	r := runner{database: database, scope: checkpoint{Tenant: "tenant", Kind: "labels", StartedAt: now}, apply: true}
	if err := r.row(context.Background(), "conv"); err != nil {
		t.Fatal(err)
	}
	if err := r.row(context.Background(), "conv"); err != nil {
		t.Fatal(err)
	}
	var current models.MessengerJourneyCustomer
	if err := database.First(&current).Error; err != nil {
		t.Fatal(err)
	}
	if current.Classification != "potential" {
		t.Fatalf("classification=%s", current.Classification)
	}
	var events []models.MessengerJourneyEvent
	if err := database.Order("sequence").Find(&events).Error; err != nil {
		t.Fatal(err)
	}
	if len(events) != 3 {
		t.Fatalf("expected only saved first/latest labels + derived current classification, got %d events", len(events))
	}
	if events[2].Kind != messengerjourney.KindClassification || events[2].Source != "cqa_backfill_derived" || !events[2].ObservedAt.Equal(now) {
		t.Fatal("backfill invented historical classification")
	}
}

func TestMySQLOrderBackfillKeepsRevisionsAndQuarantinesMalformedOutcomes(t *testing.T) {
	database := backfillDB(t)
	now := time.Now().UTC().Truncate(time.Millisecond)
	for _, ddl := range []string{
		`CREATE TABLE channels(id char(36) PRIMARY KEY,tenant_id char(36),channel_type varchar(20),external_id varchar(64))`,
		`CREATE TABLE conversations(id char(36) PRIMARY KEY,tenant_id char(36),channel_id char(36),metadata json)`,
		`CREATE TABLE job_results(id char(36) PRIMARY KEY,tenant_id char(36),conversation_id char(36),result_type varchar(30),detail json,created_at datetime(3))`,
	} {
		if err := database.Exec(ddl).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := database.Exec(`INSERT INTO channels VALUES('channel','tenant','facebook','123')`).Error; err != nil {
		t.Fatal(err)
	}
	if err := database.Exec(`INSERT INTO conversations VALUES('conv','tenant','channel','{"participants":{"data":[{"id":"123"},{"id":"456"}]}}')`).Error; err != nil {
		t.Fatal(err)
	}
	amount := int64(100000)
	order := outcome{OrderID: "order-1", OrderRef: "O-1", ValueVND: &amount, Status: "confirmed", OrderedAt: now.Add(-time.Hour).Format(time.RFC3339Nano), EvidenceMessageIDs: []string{"message-1"}, Confidence: 0.9}
	insert := func(id string, orders []outcome, at time.Time) {
		t.Helper()
		detail, err := json.Marshal(map[string]any{"order_outcomes": orders})
		if err != nil {
			t.Fatal(err)
		}
		if err = database.Exec(`INSERT INTO job_results VALUES(?,'tenant','conv','conversation_insight',?,?)`, id, string(detail), at).Error; err != nil {
			t.Fatal(err)
		}
	}
	insert("row-a", []outcome{order}, now.Add(-30*time.Minute))
	order.Status = "cancelled"
	insert("row-b", []outcome{order}, now.Add(-20*time.Minute))
	invalid := make([]outcome, 4)
	for i := range invalid {
		invalid[i] = order
	}
	invalid[0].Status = "nonsense"
	invalid[1].OrderRef = ""
	invalid[2].Confidence = 2
	invalid[3].OrderedAt = now.Add(time.Hour).Format(time.RFC3339Nano)
	insert("row-c", invalid, now.Add(-time.Minute))
	r := runner{database: database, scope: checkpoint{Tenant: "tenant", Kind: "orders", StartedAt: now}, apply: true}
	for _, id := range []string{"row-a", "row-b", "row-c", "row-a", "row-b"} {
		if err := r.row(context.Background(), id); err != nil {
			t.Fatal(err)
		}
	}
	var events int64
	database.Model(&models.MessengerJourneyEvent{}).Where("kind='order'").Count(&events)
	if events != 2 {
		t.Fatalf("want2 valid revisions only, got%d", events)
	}
	var current models.MessengerJourneyOrder
	if err := database.First(&current).Error; err != nil {
		t.Fatal(err)
	}
	var saved outcome
	if err := json.Unmarshal([]byte(current.Payload), &saved); err != nil {
		t.Fatal(err)
	}
	if saved.Status != "cancelled" {
		t.Fatalf("latest=%s", saved.Status)
	}
	if r.skipped != 4 || len(r.issues) != 4 {
		t.Fatalf("malformed outcomes not quarantined: %d/%d", r.skipped, len(r.issues))
	}
}
