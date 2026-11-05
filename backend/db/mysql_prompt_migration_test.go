package db

import (
	"os"
	"strings"
	"testing"
	"time"

	mysqldriver "github.com/go-sql-driver/mysql"
	"github.com/vietbui/chat-quality-agent/ai"
	"github.com/vietbui/chat-quality-agent/db/models"
	"github.com/vietbui/chat-quality-agent/pkg"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func promptMigrationTestDatabase(t *testing.T) *gorm.DB {
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

	name := "cqa_prompt_migration_test_" + strings.ReplaceAll(pkg.NewUUID(), "-", "")
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
	return database
}

func TestMigrateMessengerOrderOutcomePromptsRunsOnce(t *testing.T) {
	database := promptMigrationTestDatabase(t)
	if err := database.AutoMigrate(&models.AppSetting{}, &models.DataMigration{}); err != nil {
		t.Fatal(err)
	}
	previousDB := DB
	DB = database
	t.Cleanup(func() { DB = previousDB })

	prompt := models.AppSetting{
		ID: pkg.NewUUID(), TenantID: pkg.NewUUID(), SettingKey: ai.MessengerInsightsPromptSettingKey,
		ValuePlain: "Prompt tenant {{rules}}\nCHỈ trả về JSON.", CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	if err := database.Create(&prompt).Error; err != nil {
		t.Fatal(err)
	}
	if err := migrateMessengerOrderOutcomePrompts(); err != nil {
		t.Fatal(err)
	}
	if err := database.First(&prompt, "id = ?", prompt.ID).Error; err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(prompt.ValuePlain, "## Quy tắc nhận diện đơn hàng") {
		t.Fatal("legacy override did not receive the editable order-outcome section")
	}

	userEdited := "Prompt tenant đã chủ động bỏ order outcomes {{rules}}"
	if err := database.Model(&prompt).Update("value_plain", userEdited).Error; err != nil {
		t.Fatal(err)
	}
	if err := migrateMessengerOrderOutcomePrompts(); err != nil {
		t.Fatal(err)
	}
	if err := database.First(&prompt, "id = ?", prompt.ID).Error; err != nil {
		t.Fatal(err)
	}
	if prompt.ValuePlain != userEdited {
		t.Fatalf("restart rewrote the user-edited prompt: %s", prompt.ValuePlain)
	}
}

func TestMigrateMessengerOrderOutcomePromptsRollsBackMarkerOnFailure(t *testing.T) {
	database := promptMigrationTestDatabase(t)
	if err := database.AutoMigrate(&models.DataMigration{}); err != nil {
		t.Fatal(err)
	}
	previousDB := DB
	DB = database
	t.Cleanup(func() { DB = previousDB })

	if err := migrateMessengerOrderOutcomePrompts(); err == nil {
		t.Fatal("expected migration to fail when app_settings is unavailable")
	}
	var count int64
	if err := database.Model(&models.DataMigration{}).Where("`key` = ?", messengerOrderOutcomePromptMigrationKey).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("failed migration committed its one-time marker")
	}
}
