package messengerlabels

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
	"github.com/vietbui/chat-quality-agent/channels"
	"github.com/vietbui/chat-quality-agent/db/models"
	"github.com/vietbui/chat-quality-agent/pkg"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Opt-in integration test. Creates/drops only a uniquely named temporary schema;
// never migrates or truncates the database named in the supplied DSN.
func labelTestDatabase(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := os.Getenv("MESSENGER_LABELS_TEST_DSN")
	if dsn == "" {
		t.Skip("MySQL integration requires MESSENGER_LABELS_TEST_DSN")
	}
	cfg, err := mysqldriver.ParseDSN(dsn)
	if err != nil {
		t.Fatal("invalid test DSN")
	}
	cfg.DBName = ""
	cfg.ParseTime = true
	cfg.Timeout = 5 * time.Second
	admin, err := gorm.Open(mysql.Open(cfg.FormatDSN()), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal("cannot connect to test MySQL")
	}
	adminSQL, err := admin.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = adminSQL.Close() })
	name := "cqa_labels_test_" + strings.ReplaceAll(pkg.NewUUID(), "-", "")
	if err := admin.Exec("CREATE DATABASE `" + name + "` CHARACTER SET utf8mb4").Error; err != nil {
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
		t.Fatal("cannot open temporary test schema")
	}
	pool, err := database.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pool.Close() })
	if err := database.AutoMigrate(&models.Tenant{}, &models.Channel{}, &models.Conversation{}, &models.MessengerLabelState{}, &models.MessengerLabelSnapshot{}); err != nil {
		t.Fatal(err)
	}
	return database
}

type integrationReader struct{ *fakeReader }

func (f integrationReader) FetchPageLabels(context.Context) ([]channels.FacebookLabel, error) {
	return []channels.FacebookLabel{{ID: "11", Name: "Phù hợp"}}, nil
}

func TestMySQLSyncClaimsCheckpointsAndTenantIsolation(t *testing.T) {
	database := labelTestDatabase(t)
	ctx := context.Background()
	now := time.Now()
	tenant := models.Tenant{ID: "tenant-a", Name: "Test", Slug: pkg.NewUUID(), Settings: "{}"}
	channel := models.Channel{ID: "page-a", TenantID: tenant.ID, ChannelType: "facebook", Name: "Test Page", IsActive: true, CredentialsEncrypted: []byte{}, Metadata: "{}"}
	for _, value := range []interface{}{&tenant, &channel} {
		if err := database.Create(value).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []string{"a", "b", "c"} {
		conv := models.Conversation{ID: id, TenantID: tenant.ID, ChannelID: channel.ID, ExternalConversationID: id, Metadata: `{"participants":{"data":[{"id":"100"},{"id":"300"}]}}`}
		if err := database.Create(&conv).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := SaveCatalog(database, channel, []channels.FacebookLabel{{ID: "11", Name: "Phù hợp"}, {ID: "99", Name: "Ad 99"}}, now); err != nil {
		t.Fatal(err)
	}
	if err := SavePolicy(database, channel, Policy{true, []Rule{{"11", "qualified"}, {"99", "unqualified"}}}); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	claimed := make(chan string, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			token, err := ClaimSync(database, channel, now)
			if err == nil {
				claimed <- token
			} else if !errors.Is(err, ErrBusy) {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	close(claimed)
	if len(claimed) != 1 {
		t.Fatalf("expected one owner, got %d", len(claimed))
	}
	oldToken := <-claimed
	intakeAd, intakeEmpty := `[{"id":"99","page_label_name":"Ad 99"}]`, `[]`
	snapshots := []models.MessengerLabelSnapshot{
		{ConversationID: "a", TenantID: tenant.ID, ChannelID: channel.ID, PSID: "200", Labels: "[]", IntakeLabels: &intakeAd, Status: "success", CheckedAt: &now, IntakeLabelsCapturedAt: &now, AttemptedAt: now},
		{ConversationID: "b", TenantID: tenant.ID, ChannelID: channel.ID, PSID: "201", Labels: "[]", Status: "error", ErrorKind: "unsupported", AttemptedAt: now},
	}
	if err := saveBatch(database, channel, oldToken, "b", snapshots, 1); err != nil {
		t.Fatal(err)
	}
	state, err := LoadState(database, channel)
	var prunedRules []Rule
	if err == nil {
		err = json.Unmarshal([]byte(state.Rules), &prunedRules)
	}
	if err != nil || len(prunedRules) != 1 || prunedRules[0] != (Rule{LabelID: "11", Category: "qualified"}) {
		t.Fatalf("new intake baseline did not prune persisted policy: %s, %v", state.Rules, err)
	}
	report, err := BuildReport(ctx, database, tenant.ID, channel.ID, time.Now())
	if err != nil || report.Counts.Unknown != 3 || report.Counts.Unclassified != 0 {
		t.Fatalf("mixed sync report: %+v %v", report, err)
	}
	if _, err := BuildReport(ctx, database, "other-tenant", channel.ID, time.Now()); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("tenant leaked: %v", err)
	}
	if err := SavePolicy(database, channel, Policy{true, []Rule{{"99", "qualified"}}}); !errors.Is(err, ErrIntakeLabel) {
		t.Fatalf("intake label policy accepted: %v", err)
	}
	if err := database.Create(&models.MessengerLabelSnapshot{ConversationID: "c", TenantID: tenant.ID, ChannelID: channel.ID, PSID: "300", Labels: "[]", IntakeLabels: &intakeEmpty, Status: "never", IntakeLabelsCapturedAt: &now, AttemptedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	if err := database.Model(&models.MessengerLabelState{}).Where("channel_id = ?", channel.ID).Update("lease_until", now.Add(-time.Minute)).Error; err != nil {
		t.Fatal(err)
	}
	newToken, err := ClaimSync(database, channel, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := renewLease(database, channel, oldToken); !errors.Is(err, errLeaseLost) {
		t.Fatalf("stale renewal: %v", err)
	}
	if err := saveBatch(database, channel, oldToken, "c", snapshots, 0); !errors.Is(err, errLeaseLost) {
		t.Fatalf("stale write: %v", err)
	}
	if err := finishSync(database, channel, oldToken, "success", ""); !errors.Is(err, errLeaseLost) {
		t.Fatalf("stale cleanup: %v", err)
	}
	reader := integrationReader{&fakeReader{labels: map[string][]channels.FacebookLabel{"300": {{ID: "11"}}}}}
	if err := RunClaimedSync(ctx, database, channel, "100", newToken, reader); err != nil {
		t.Fatal(err)
	}
	if len(reader.calls) != 1 {
		t.Fatalf("did not resume checkpoint, calls=%v", reader.calls)
	}
	report, err = BuildReport(ctx, database, tenant.ID, channel.ID, time.Now())
	if err != nil || report.Sync.Status != "partial" || report.Counts.Unclassified != 1 || report.Counts.Qualified != 1 || report.Counts.Unknown != 1 {
		t.Fatalf("partial report: %+v %v", report, err)
	}
	if _, err := ClaimSync(database, channel, time.Now()); !errors.Is(err, ErrBusy) {
		t.Fatalf("cooldown missing: %v", err)
	}
	conversationD := models.Conversation{ID: "d", TenantID: tenant.ID, ChannelID: channel.ID, ExternalConversationID: "d", Metadata: `{"participants":{"data":[{"id":"100"},{"id":"400"}]}}`}
	if err := database.Create(&conversationD).Error; err != nil {
		t.Fatal(err)
	}
	if err := SaveCatalog(database, channel, []channels.FacebookLabel{{ID: "11", Name: "Phù hợp"}, {ID: "99", Name: "Ad 99"}}, time.Now()); err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	var policyErr, captureErr error
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-start
		policyErr = SavePolicy(database, channel, Policy{true, []Rule{{"11", "qualified"}, {"99", "unqualified"}}})
	}()
	go func() {
		defer wg.Done()
		<-start
		captureErr = saveIntakeLabelSnapshot(database, models.MessengerLabelSnapshot{ConversationID: "d", TenantID: tenant.ID, ChannelID: channel.ID, PSID: "400", Labels: intakeAd, IntakeLabels: &intakeAd, Status: "success", CheckedAt: &now, IntakeLabelsCapturedAt: &now, AttemptedAt: now})
	}()
	close(start)
	wg.Wait()
	if captureErr != nil || policyErr != nil && !errors.Is(policyErr, ErrIntakeLabel) {
		t.Fatalf("concurrent policy/capture failed: policy=%v capture=%v", policyErr, captureErr)
	}
	state, err = LoadState(database, channel)
	if err != nil || strings.Contains(state.Rules, `"label_id":"99"`) {
		t.Fatalf("concurrent intake label remained in policy: %s, %v", state.Rules, err)
	}
	if err := database.Model(&models.MessengerLabelState{}).Where("channel_id = ?", channel.ID).Update("sync_finished_at", time.Now().Add(-2*time.Minute)).Error; err != nil {
		t.Fatal(err)
	}
	immutabilityToken, err := ClaimSync(database, channel, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	laterAt := now.Add(time.Minute)
	laterIntake := `[{"id":"88","page_label_name":"Later label"}]`
	if err := saveBatch(database, channel, immutabilityToken, "d", []models.MessengerLabelSnapshot{{ConversationID: "d", TenantID: tenant.ID, ChannelID: channel.ID, PSID: "400", Labels: laterIntake, IntakeLabels: &laterIntake, Status: "success", CheckedAt: &laterAt, IntakeLabelsCapturedAt: &laterAt, AttemptedAt: laterAt}}, 0); err != nil {
		t.Fatal(err)
	}
	var immutable models.MessengerLabelSnapshot
	if err := database.First(&immutable, "conversation_id = ?", "d").Error; err != nil {
		t.Fatal(err)
	}
	var immutableLabels []channels.FacebookLabel
	if immutable.IntakeLabels != nil {
		err = json.Unmarshal([]byte(*immutable.IntakeLabels), &immutableLabels)
	}
	if err != nil || len(immutableLabels) != 1 || immutableLabels[0] != (channels.FacebookLabel{ID: "99", Name: "Ad 99"}) || immutable.IntakeLabelsCapturedAt == nil || !immutable.IntakeLabelsCapturedAt.Equal(now) {
		t.Fatalf("tracking batch overwrote intake baseline: %+v", immutable)
	}
	if err := finishSync(database, channel, immutabilityToken, "success", ""); err != nil {
		t.Fatal(err)
	}
	if err := database.Delete(&models.Conversation{}, "id = ?", "a").Error; err != nil {
		t.Fatal(err)
	}
	var count int64
	if err := database.Model(&models.MessengerLabelSnapshot{}).Where("conversation_id = ?", "a").Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("cascade failed: %d %v", count, err)
	}
}
