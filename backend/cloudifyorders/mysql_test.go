package cloudifyorders

import (
	"context"
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

// Opt-in: uses a uniquely named temporary schema, like the pancakelabels tests.
func testDatabase(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := os.Getenv("MESSENGER_LABELS_TEST_DSN")
	if dsn == "" {
		t.Skip("MySQL integration requires MESSENGER_LABELS_TEST_DSN")
	}
	cfg, err := mysqldriver.ParseDSN(dsn)
	if err != nil {
		t.Fatal("invalid test DSN")
	}
	cfg.DBName, cfg.ParseTime = "", true
	admin, err := gorm.Open(mysql.Open(cfg.FormatDSN()), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal("cannot connect to test MySQL")
	}
	name := "cqa_cloudify_" + strings.ReplaceAll(pkg.NewUUID(), "-", "")[:16]
	if err := admin.Exec("CREATE DATABASE `" + name + "`").Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { admin.Exec("DROP DATABASE `" + name + "`") })
	cfg.DBName = name
	database, err := gorm.Open(mysql.Open(cfg.FormatDSN()), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	if err := database.AutoMigrate(&models.AppSetting{}, &models.CRMGroup{}, &models.CRMGroupCustomerCode{},
		&models.CloudifyOrderWatch{}, &models.CloudifyOrderSnapshot{}, &models.CloudifyOrderEvent{}); err != nil {
		t.Fatal(err)
	}
	return database
}

func setSetting(t *testing.T, database *gorm.DB, tenantID, key, value string) {
	t.Helper()
	now := time.Now()
	database.Where("tenant_id = ? AND setting_key = ?", tenantID, key).Delete(&models.AppSetting{})
	if err := database.Create(&models.AppSetting{ID: pkg.NewUUID(), TenantID: tenantID, SettingKey: key, ValuePlain: value, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
}

type fakeFetcher struct {
	mu   sync.Mutex
	rows []map[string]any
	err  error
	n    int
}

func (f *fakeFetcher) ByWindow(time.Time, time.Time) ([]map[string]any, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.n++
	return f.rows, f.err
}

func (f *fakeFetcher) ByCode(code string) ([]map[string]any, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, row := range f.rows {
		if row["MA_HOA_DON"] == code {
			return []map[string]any{row}, nil
		}
	}
	return nil, nil
}

func (f *fakeFetcher) set(rows ...map[string]any) {
	f.mu.Lock()
	f.rows = rows
	f.mu.Unlock()
}

type sentMessage struct{ group, text string }

type fakeSender struct {
	recipients map[string][]Recipient
	fail       error
	sent       []sentMessage
}

func (s *fakeSender) Recipients(_ string, code string) ([]Recipient, error) {
	return s.recipients[code], nil
}

func (s *fakeSender) Send(_ context.Context, _ string, to Recipient, text string) error {
	if s.fail != nil {
		return s.fail
	}
	s.sent = append(s.sent, sentMessage{to.GroupID, text})
	return nil
}

func events(t *testing.T, database *gorm.DB, tenantID string) []models.CloudifyOrderEvent {
	t.Helper()
	var out []models.CloudifyOrderEvent
	if err := database.Where("tenant_id = ?", tenantID).Order("created_at, order_id").Find(&out).Error; err != nil {
		t.Fatal(err)
	}
	return out
}

func TestIngestIsIdempotentAcrossSources(t *testing.T) {
	database := testDatabase(t)
	ctx := context.Background()
	now := time.Date(2026, 11, 18, 9, 0, 0, 0, time.UTC)
	tenant := "tenant-1"

	res, err := Ingest(ctx, database, tenant, []map[string]any{sampleRow(1, "ĐH1", "1")}, SourcePoll, DiffOptions{Seeding: true}, now)
	if err != nil || res.Stored != 1 || len(res.Events) != 0 {
		t.Fatalf("seed: %+v %v", res, err)
	}
	opts := DiffOptions{CreatedCutoff: now.AddDate(0, 0, -1)}
	rows := []map[string]any{sampleRow(1, "ĐH1", "4"), sampleRow(2, "ĐH2", "1")}
	res, err = Ingest(ctx, database, tenant, rows, SourceWebhook, opts, now)
	if err != nil || len(res.Events) != 2 {
		t.Fatalf("first change: %+v %v", res, err)
	}
	// The poller sees the same state a moment later: nothing new. Reset the
	// snapshots first to simulate it reading before the webhook committed.
	database.Model(&models.CloudifyOrderSnapshot{}).Where("order_id = 1").Updates(map[string]any{"status": "1", "fingerprint": Fingerprint(sampleRow(1, "ĐH1", "1"))})
	database.Where("order_id = 2").Delete(&models.CloudifyOrderSnapshot{})
	res, err = Ingest(ctx, database, tenant, rows, SourcePoll, opts, now.Add(time.Second))
	if err != nil || len(res.Events) != 0 {
		t.Fatalf("duplicate transition must not create events: %+v %v", res, err)
	}
	got := events(t, database, tenant)
	if len(got) != 2 || got[0].Kind != KindStatusChanged || got[0].PrevStatus != "1" || got[0].Status != "4" || got[1].Kind != KindCreated {
		t.Fatalf("events = %+v", got)
	}
	if got[0].CustomerCode != "KH125" || got[0].Source != SourceWebhook || got[0].DeliveryStatus != DeliveryPending {
		t.Fatalf("event fields = %+v", got[0])
	}
}

func TestDeliverPending(t *testing.T) {
	database := testDatabase(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	tenant := "tenant-1"
	mk := func(kind, code, customer string, created time.Time) {
		database.Create(&models.CloudifyOrderEvent{ID: pkg.NewUUID(), TenantID: tenant, DedupKey: pkg.NewUUID(), Kind: kind, Source: SourcePoll,
			OrderCode: code, CustomerCode: customer, Status: "4", PrevStatus: "1", DeliveryStatus: DeliveryPending, CreatedAt: created})
	}
	mk(KindStatusChanged, "ĐH1", "KH1", now)
	mk(KindUpdated, "ĐH2", "KH1", now)                   // kind off by default
	mk(KindCreated, "ĐH3", "KH9", now)                   // no group
	mk(KindCreated, "ĐH4", "KH1", now.Add(-3*time.Hour)) // stale
	sender := &fakeSender{recipients: map[string][]Recipient{"KH1": {{GroupID: "g1", ChannelID: "c1", Name: "Shop"}}}}

	off := ParseSettings(map[string]string{})
	if _, err := DeliverPending(ctx, database, tenant, off, sender, now); err != nil {
		t.Fatal(err)
	}
	for _, e := range events(t, database, tenant) {
		if e.DeliveryStatus != DeliverySkipped || e.DeliveryNote != "notify_off" {
			t.Fatalf("notify off must skip everything: %+v", e)
		}
	}

	database.Model(&models.CloudifyOrderEvent{}).Where("tenant_id = ?", tenant).Updates(map[string]any{"delivery_status": DeliveryPending, "delivery_note": ""})
	on := ParseSettings(map[string]string{KeyNotifyEnabled: "true"})
	if _, err := DeliverPending(ctx, database, tenant, on, sender, now); err != nil {
		t.Fatal(err)
	}
	notes := map[string]string{}
	for _, e := range events(t, database, tenant) {
		notes[e.OrderCode] = e.DeliveryStatus + ":" + e.DeliveryNote
	}
	if notes["ĐH1"] != "sent:sent to 1 group(s)" || notes["ĐH2"] != "skipped:kind_off" || notes["ĐH3"] != "skipped:no_zalo_group" || notes["ĐH4"] != "skipped:stale" {
		t.Fatalf("notes = %v", notes)
	}
	if len(sender.sent) != 1 || sender.sent[0].text != "Đơn hàng ĐH1 của quý khách đã chuyển sang trạng thái: Đang giao." {
		t.Fatalf("sent = %+v", sender.sent)
	}
}

func TestDeliverRetriesThenFails(t *testing.T) {
	database := testDatabase(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	tenant := "tenant-1"
	database.Create(&models.CloudifyOrderEvent{ID: "e1", TenantID: tenant, DedupKey: "k1", Kind: KindCreated, Source: SourcePoll,
		OrderCode: "ĐH1", CustomerCode: "KH1", DeliveryStatus: DeliveryPending, CreatedAt: now})
	sender := &fakeSender{recipients: map[string][]Recipient{"KH1": {{GroupID: "g1"}}}, fail: errors.New("zalo down")}
	on := ParseSettings(map[string]string{KeyNotifyEnabled: "true"})
	at := now
	for i := 0; i < maxAttempts; i++ {
		DeliverPending(ctx, database, tenant, on, sender, at)
		// A retry before the backoff ends must not run.
		DeliverPending(ctx, database, tenant, on, sender, at.Add(time.Second))
		at = at.Add(retryDelay(i+1) + time.Second)
	}
	var e models.CloudifyOrderEvent
	database.First(&e, "id = ?", "e1")
	if e.DeliveryStatus != DeliveryFailed || e.Attempts != maxAttempts {
		t.Fatalf("event = %+v", e)
	}
}

func TestWatcherTickSeedsThenDetects(t *testing.T) {
	database := testDatabase(t)
	ctx := context.Background()
	tenant := "tenant-1"
	setSetting(t, database, tenant, KeyWatchEnabled, "true")
	fetcher := &fakeFetcher{}
	fetcher.set(sampleRow(1, "ĐH1", "1"))
	sender := &fakeSender{}
	clock := time.Date(2026, 11, 18, 9, 0, 0, 0, time.UTC)
	w := &Watcher{DB: database, NewFetcher: func(string) (Fetcher, error) { return fetcher, nil }, Sender: sender, Now: func() time.Time { return clock }, owner: "me"}

	w.Tick(ctx, tenant)
	watch, _ := LoadWatch(database, tenant)
	if watch.SeededAt == nil || watch.LastCount != 1 || len(events(t, database, tenant)) != 0 {
		t.Fatalf("first tick must seed silently: %+v", watch)
	}

	fetcher.set(sampleRow(1, "ĐH1", "4"), sampleRow(2, "ĐH2", "1"))
	clock = clock.Add(5 * time.Second)
	w.Tick(ctx, tenant)
	if fetcher.n != 1 || len(events(t, database, tenant)) != 0 {
		t.Fatal("must wait for the poll interval")
	}
	if err := Nudge(database, tenant, clock); err != nil {
		t.Fatal(err)
	}
	clock = clock.Add(time.Second)
	w.Tick(ctx, tenant)
	if fetcher.n != 2 {
		t.Fatal("a nudge must poll right away")
	}
	got := events(t, database, tenant)
	if len(got) != 2 {
		t.Fatalf("events = %+v", got)
	}

	// Another replica cannot take the tenant while the lease is live.
	other := &Watcher{DB: database, NewFetcher: w.NewFetcher, Sender: sender, Now: w.Now, owner: "other"}
	clock = clock.Add(time.Minute)
	other.Tick(ctx, tenant)
	if fetcher.n != 2 {
		t.Fatal("lease must keep a second replica out")
	}

	fetcher.err = errors.New("boom")
	w.Tick(ctx, tenant)
	watch, _ = LoadWatch(database, tenant)
	if watch.LastError == "" {
		t.Fatal("poll error must be recorded")
	}
}

func TestIngestByCode(t *testing.T) {
	database := testDatabase(t)
	ctx := context.Background()
	tenant := "tenant-1"
	seeded := time.Date(2026, 11, 17, 9, 0, 0, 0, time.UTC)
	LoadWatch(database, tenant)
	database.Model(&models.CloudifyOrderWatch{}).Where("tenant_id = ?", tenant).Update("seeded_at", seeded)
	fetcher := &fakeFetcher{}
	fetcher.set(sampleRow(7, "ĐH7", "1"))
	res, err := IngestByCode(ctx, database, fetcher, tenant, "ĐH7", seeded.Add(time.Hour))
	if err != nil || len(res.Events) != 1 || res.Events[0].Source != SourceWebhook {
		t.Fatalf("res = %+v err = %v", res, err)
	}
}

func TestZaloGroupSenderRecipients(t *testing.T) {
	database := testDatabase(t)
	// crm_groups.channel_id references channels; this test only needs groups.
	if sqlDB, err := database.DB(); err == nil {
		sqlDB.SetMaxOpenConns(1)
	}
	database.Exec("SET FOREIGN_KEY_CHECKS = 0")
	now := time.Now()
	tenant := "tenant-1"
	groups := []models.CRMGroup{
		{ID: "g-primary", TenantID: tenant, Name: "A", ZaloGroupID: "z1", ChannelID: "c1", CustomerCode: "KH1", CreatedAt: now, UpdatedAt: now},
		{ID: "g-multi", TenantID: tenant, Name: "B", ZaloGroupID: "z2", ChannelID: "c1", CustomerCode: "KH5", CreatedAt: now, UpdatedAt: now},
		{ID: "g-unlinked", TenantID: tenant, Name: "C", CustomerCode: "KH1", CreatedAt: now, UpdatedAt: now},
		{ID: "g-other", TenantID: "tenant-2", Name: "D", ZaloGroupID: "z3", ChannelID: "c2", CustomerCode: "KH1", CreatedAt: now, UpdatedAt: now},
	}
	for _, g := range groups {
		if err := database.Omit("Employees", "Customers", "Channel").Create(&g).Error; err != nil {
			t.Fatal(err)
		}
	}
	database.Create(&models.CRMGroupCustomerCode{ID: "x", TenantID: tenant, GroupID: "g-multi", CustomerCode: "KH1", CreatedAt: now, UpdatedAt: now})
	got, err := ZaloGroupSender{DB: database}.Recipients(tenant, "kh1")
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]bool{}
	for _, r := range got {
		ids[r.GroupID] = true
	}
	if len(got) != 2 || !ids["g-primary"] || !ids["g-multi"] {
		t.Fatalf("recipients = %+v", got)
	}
}
