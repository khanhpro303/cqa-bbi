package messengerintake

import (
	"os"
	"strings"
	"testing"
	"time"

	mysqldriver "github.com/go-sql-driver/mysql"
	"github.com/vietbui/chat-quality-agent/db/models"
	"github.com/vietbui/chat-quality-agent/pkg"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func intakeTestDatabase(t *testing.T) *gorm.DB {
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

	name := "cqa_intake_test_" + strings.ReplaceAll(pkg.NewUUID(), "-", "")
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
	if err := database.AutoMigrate(
		&models.Tenant{}, &models.Channel{}, &models.Conversation{},
		&models.MessengerLabelSnapshot{}, &models.MessengerIntakeAttribution{},
	); err != nil {
		t.Fatal(err)
	}
	return database
}

func TestMySQLReferralIsImmutableAndBindsToConversation(t *testing.T) {
	database := intakeTestDatabase(t)
	now := time.Now().UTC().Truncate(time.Millisecond)
	tenant := models.Tenant{ID: pkg.NewUUID(), Name: "Test", Slug: pkg.NewUUID(), Settings: "{}"}
	channel := models.Channel{
		ID: pkg.NewUUID(), TenantID: tenant.ID, ChannelType: "facebook", Name: "Page",
		ExternalID: "page-1", CredentialsEncrypted: []byte{}, IsActive: true, Metadata: "{}",
	}
	if err := database.Create(&tenant).Error; err != nil {
		t.Fatal(err)
	}
	if err := database.Create(&channel).Error; err != nil {
		t.Fatal(err)
	}

	first := Referral{PageID: "page-1", PSID: "psid-1", AdID: "ad-first", Source: "ADS", RawJSON: `{"ad_id":"ad-first"}`, CapturedAt: now}
	if err := CaptureReferral(database, channel, first); err != nil {
		t.Fatal(err)
	}
	second := first
	second.AdID = "ad-later"
	second.RawJSON = `{"ad_id":"ad-later"}`
	second.CapturedAt = now.Add(time.Minute)
	if err := CaptureReferral(database, channel, second); err != nil {
		t.Fatal(err)
	}

	var attribution models.MessengerIntakeAttribution
	if err := database.Where("channel_id = ? AND psid = ?", channel.ID, first.PSID).First(&attribution).Error; err != nil {
		t.Fatal(err)
	}
	if attribution.AdID != "ad-first" || !attribution.CapturedAt.Equal(now) || attribution.ConversationID != nil {
		t.Fatalf("intake overwritten or prematurely bound: %+v", attribution)
	}

	conversation := models.Conversation{
		ID: pkg.NewUUID(), TenantID: tenant.ID, ChannelID: channel.ID,
		ExternalConversationID: "conversation-1", ExternalUserID: first.PSID,
		CustomerName: "Customer", Metadata: "{}", CreatedAt: now, UpdatedAt: now,
	}
	if err := database.Create(&conversation).Error; err != nil {
		t.Fatal(err)
	}
	if err := BindConversation(database, tenant.ID, channel.ID, first.PSID, conversation.ID); err != nil {
		t.Fatal(err)
	}
	if err := database.First(&attribution, "id = ?", attribution.ID).Error; err != nil {
		t.Fatal(err)
	}
	if attribution.ConversationID == nil || *attribution.ConversationID != conversation.ID {
		t.Fatalf("referral was not bound: %+v", attribution)
	}
}
