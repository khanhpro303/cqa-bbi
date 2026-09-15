package engine

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/vietbui/chat-quality-agent/ai"
	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/db/models"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/logger"
)

func TestJobPromptLoadsOnlyCurrentTenantMessengerOverride(t *testing.T) {
	database, err := gorm.Open(mysql.New(mysql.Config{DSN: "test:test@tcp(127.0.0.1:1)/test", SkipInitializeWithVersion: true}), &gorm.Config{
		DisableAutomaticPing: true, Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatal(err)
	}
	pool, _ := database.DB()
	t.Cleanup(func() { _ = pool.Close() })
	previous := db.DB
	db.DB = database
	t.Cleanup(func() { db.DB = previous })
	queries := 0
	_ = database.Callback().Query().Replace("gorm:query", func(tx *gorm.DB) {
		queries++
		where, _ := tx.Statement.Clauses["WHERE"].Expression.(clause.Where)
		for _, expression := range where.Exprs {
			if expr, ok := expression.(clause.Expr); ok && expr.SQL == "tenant_id = ? AND setting_key = ?" && len(expr.Vars) == 2 && expr.Vars[1] == ai.MessengerInsightsPromptSettingKey {
				switch expr.Vars[0] {
				case "tenant-custom":
					setting := tx.Statement.Dest.(*models.AppSetting)
					setting.ValuePlain = "Tenant-specific instructions {{rules}}"
				case "tenant-blank":
				case "tenant-error":
					tx.AddError(errors.New("storage unavailable"))
				default:
					tx.AddError(gorm.ErrRecordNotFound)
				}
				return
			}
		}
		t.Fatal("prompt lookup must filter both tenant and setting key")
	})
	job := models.Job{JobType: "classification", RulesConfig: `{"profile":"messenger_insights","rules":[{"name":"Hỏi giá","description":"Khách hỏi giá"}]}`}
	for _, tenant := range []string{"tenant-custom", "tenant-missing", "tenant-blank", "tenant-error"} {
		job.TenantID = tenant
		prompt, err := buildJobSystemPrompt(context.Background(), job)
		if tenant == "tenant-error" {
			if err == nil || prompt != "" {
				t.Fatal("storage error must stop analysis rather than silently ignore the override")
			}
			continue
		}
		if err != nil || !strings.Contains(prompt, "Hỏi giá") || strings.Contains(prompt, "{{rules}}") {
			t.Fatalf("unexpected prompt for %s: %s, %v", tenant, prompt, err)
		}
		if tenant == "tenant-custom" {
			if !strings.Contains(prompt, "Tenant-specific instructions") {
				t.Fatal("saved override was not used")
			}
		} else if prompt != ai.BuildClassificationPrompt(job.RulesConfig) {
			t.Fatal("tenant without an override must use the built-in prompt")
		}
	}
	for _, job := range []models.Job{
		{JobType: "classification", RulesConfig: `[{"name":"Legacy"}]`},
		{JobType: "qc_analysis", RulesContent: "Chào hỏi", SkipConditions: "Spam"},
	} {
		if _, err := buildJobSystemPrompt(context.Background(), job); err != nil {
			t.Fatal(err)
		}
	}
	if queries != 4 {
		t.Fatalf("non-Messenger jobs queried the Messenger setting: %d queries", queries)
	}
}
