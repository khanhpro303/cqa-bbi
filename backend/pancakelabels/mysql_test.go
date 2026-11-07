package pancakelabels

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	mysqldriver "github.com/go-sql-driver/mysql"
	"github.com/vietbui/chat-quality-agent/channels"
	"github.com/vietbui/chat-quality-agent/db/models"
	"github.com/vietbui/chat-quality-agent/messengerlabels"
	"github.com/vietbui/chat-quality-agent/pancake"
	"github.com/vietbui/chat-quality-agent/pkg"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

const testKey = "0123456789abcdef0123456789abcdef"

// Opt-in: uses a uniquely named temporary schema, like messengerlabels tests.
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
	name := "cqa_pancake_" + strings.ReplaceAll(pkg.NewUUID(), "-", "")[:16]
	if err := admin.Exec("CREATE DATABASE `" + name + "`").Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { admin.Exec("DROP DATABASE `" + name + "`") })
	cfg.DBName = name
	database, err := gorm.Open(mysql.Open(cfg.FormatDSN()), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	if err := database.AutoMigrate(
		&models.Tenant{}, &models.Channel{}, &models.Conversation{}, &models.MessengerLabelState{}, &models.MessengerLabelSnapshot{},
		&models.PancakeConnection{}, &models.PancakeLabelState{}, &models.PancakeLabelSnapshot{},
		&models.MessengerJourneySequence{}, &models.MessengerJourneyReceipt{}, &models.MessengerJourneyEvent{}, &models.MessengerJourneyCustomer{}, &models.MessengerJourneyOrder{},
		&models.MessengerJourneyCoverage{}, &models.MessengerJourneyErasure{},
	); err != nil {
		t.Fatal(err)
	}
	return database
}

type fakeReader struct {
	tags  []pancake.Tag
	pages []pancake.ConversationPage
	calls int
}

func (f *fakeReader) FetchTags(context.Context) ([]pancake.Tag, error) { return f.tags, nil }
func (f *fakeReader) ListInboxConversations(_ context.Context, cursor string) (pancake.ConversationPage, error) {
	if f.calls >= len(f.pages) {
		return pancake.ConversationPage{}, nil
	}
	page := f.pages[f.calls]
	f.calls++
	return page, nil
}

func seed(t *testing.T, database *gorm.DB) models.Channel {
	t.Helper()
	tenant := models.Tenant{ID: "tenant-a", Name: "Test", Slug: pkg.NewUUID(), Settings: "{}"}
	channel := models.Channel{ID: "page-a", TenantID: tenant.ID, ChannelType: "facebook", ExternalID: "100", Name: "Gara", IsActive: true, CredentialsEncrypted: []byte{}, Metadata: "{}"}
	for _, value := range []any{&tenant, &channel} {
		if err := database.Create(value).Error; err != nil {
			t.Fatal(err)
		}
	}
	for id, psid := range map[string]string{"conv-a": "300", "conv-b": "301", "conv-c": "302"} {
		conv := models.Conversation{ID: id, TenantID: tenant.ID, ChannelID: channel.ID, ExternalConversationID: "t_" + id, CustomerName: "Khách " + psid, Metadata: `{"participants":{"data":[{"id":"100"},{"id":"` + psid + `"}]}}`}
		if err := database.Create(&conv).Error; err != nil {
			t.Fatal(err)
		}
	}
	return channel
}

func TestMySQLPancakeFlow(t *testing.T) {
	t.Setenv("CQA_MESSENGER_JOURNEY_ENABLED", "true")
	database := testDatabase(t)
	ctx := context.Background()
	channel := seed(t, database)
	policy := messengerlabels.Policy{Enabled: true, Rules: []messengerlabels.Rule{{LabelID: "4", Category: "qualified"}, {LabelID: "7", Category: "potential"}}}

	// Enabling needs a Page token and a catalogue entry for each rule.
	if err := SavePolicy(database, channel, policy); !errors.Is(err, ErrNoPageToken) {
		t.Fatalf("want ErrNoPageToken, got %v", err)
	}
	if err := SavePageToken(database, channel, "page-secret", testKey); err != nil {
		t.Fatal(err)
	}
	state, _ := LoadState(database, channel)
	if PageToken(state, testKey) != "page-secret" || strings.Contains(string(state.PageTokenEncrypted), "page-secret") {
		t.Fatal("page token must round-trip encrypted")
	}
	// Meta enabled on the same Page blocks Pancake.
	if err := database.Create(&models.MessengerLabelState{ChannelID: channel.ID, TenantID: channel.TenantID, Enabled: true, Rules: "[]", Catalog: "[]", SyncStatus: "never"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := SaveCatalog(database, channel, []channels.FacebookLabel{{ID: "4", Name: "Chốt"}, {ID: "7", Name: "Hỏi giá"}}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := SavePolicy(database, channel, policy); !errors.Is(err, ErrMetaEnabled) {
		t.Fatalf("want ErrMetaEnabled, got %v", err)
	}
	database.Model(&models.MessengerLabelState{}).Where("channel_id = ?", channel.ID).Update("enabled", false)

	// A reconcile scan before enabling stores snapshots without journey rows.
	token, err := ClaimSync(database, channel, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ClaimSync(database, channel, time.Now()); !errors.Is(err, ErrBusy) {
		t.Fatalf("second claim must be busy, got %v", err)
	}
	reader := &fakeReader{
		tags: []pancake.Tag{{ID: "4", Text: "Chốt"}, {ID: "7", Text: "Hỏi giá"}},
		pages: []pancake.ConversationPage{{
			Updates:    []pancake.ConversationUpdate{{PSID: "300", PancakeConversationID: "100_300", Tags: []pancake.Tag{{ID: "4"}}}, {PSID: "999", Tags: []pancake.Tag{{ID: "7"}}}},
			NextCursor: "100_999",
		}},
	}
	index := NewPSIDIndex()
	if err := RunClaimedSync(ctx, database, channel, token, reader, index); err != nil {
		t.Fatal(err)
	}
	state, _ = LoadState(database, channel)
	if state.SyncStatus != "success" || state.ReconciledAt == nil || state.FullScanAt == nil || state.LeaseUntil != nil {
		t.Fatalf("state after sync %+v", state)
	}
	var events int64
	database.Model(&models.MessengerJourneyEvent{}).Count(&events)
	if events != 0 {
		t.Fatalf("disabled Page must not export, got %d events", events)
	}
	var snap models.PancakeLabelSnapshot
	database.Where("channel_id = ? AND psid = ?", channel.ID, "300").First(&snap)
	if snap.ConversationID == nil || *snap.ConversationID != "conv-a" || !sameLabels(snap.Labels, `[{"id":"4","page_label_name":"Chốt"}]`) {
		t.Fatalf("snapshot %+v", snap)
	}

	// Enabling exports current labels + classification.
	if err := SavePolicy(database, channel, policy); err != nil {
		t.Fatal(err)
	}
	var sources []string
	database.Model(&models.MessengerJourneyEvent{}).Order("sequence").Pluck("source", &sources)
	if len(sources) != 4 || sources[0] != "pancake_tags" || sources[1] != "cqa_pancake_policy" {
		t.Fatalf("events %v", sources)
	}
	// Meta cannot be enabled now (guard used by the Meta handler).
	if on, err := PancakeEnabled(database, channel); err != nil || !on {
		t.Fatalf("pancake enabled = %v %v", on, err)
	}

	// Socket event: newer timestamp changes labels; older one is ignored.
	now := time.Now()
	changed, err := Apply(ctx, database, channel, pancake.ConversationUpdate{PSID: "300", Tags: []pancake.Tag{{ID: "7"}}, Timestamp: now.UnixMicro()}, SourceSocket, "", now)
	if err != nil || !changed {
		t.Fatalf("apply newer: %v %v", changed, err)
	}
	changed, err = Apply(ctx, database, channel, pancake.ConversationUpdate{PSID: "300", Tags: []pancake.Tag{{ID: "4"}}, Timestamp: now.Add(-time.Hour).UnixMicro()}, SourceSocket, "", now)
	if err != nil || changed {
		t.Fatalf("stale event must be ignored: %v %v", changed, err)
	}
	var customer models.MessengerJourneyCustomer
	database.Where("subject_id = ?", "").First(&customer)
	database.Raw("SELECT classification FROM messenger_journey_customers WHERE psid = ?", "300").Scan(&customer)
	if customer.Classification != "potential" {
		t.Fatalf("classification %q", customer.Classification)
	}

	report, err := BuildReport(ctx, database, channel.TenantID, channel.ID, true, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if report.Source != "pancake" || !report.Enabled || !report.HasPageToken || report.Counts.Total != 3 || report.Counts.Potential != 1 || report.Counts.Unknown != 2 || report.Unmatched != 1 {
		t.Fatalf("report counts %+v unmatched %d", report.Counts, report.Unmatched)
	}

	// Listener and scheduler queries are valid SQL.
	if err := SaveUserToken(database, channel.TenantID, "user-secret", testKey); err != nil {
		t.Fatal(err)
	}
	l := NewListener(database, testKey, "")
	wanted, err := l.wanted()
	if err != nil || len(wanted["tenant-a"]) != 1 || wanted["tenant-a"][0].PancakePageID != "100" {
		t.Fatalf("wanted %+v %v", wanted, err)
	}
	if !l.claim("tenant-a", time.Now()) || NewListener(database, testKey, "").claim("tenant-a", time.Now()) {
		t.Fatal("lease must be exclusive")
	}
	if _, err := DuePages(database, time.Now(), 2); err != nil {
		t.Fatal(err)
	}
	conn, _ := LoadConnection(database, channel.TenantID)
	if UserToken(conn, testKey) != "user-secret" {
		t.Fatal("user token must round-trip")
	}
}
