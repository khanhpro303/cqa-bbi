package handlers

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/vietbui/chat-quality-agent/ai"
	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/db/models"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/logger"
)

// Intercept GORM operations to inject storage failures without an external DB.
func TestMessengerPromptSaveFailureIsNotReportedAsSuccess(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, body := range []string{`{"system_prompt_messenger_insights":"custom prompt"}`, `{"system_prompt_messenger_insights":""}`} {
		t.Run(body, func(t *testing.T) {
			database, err := gorm.Open(mysql.New(mysql.Config{DSN: "test:test@tcp(127.0.0.1:1)/test", SkipInitializeWithVersion: true}), &gorm.Config{
				DisableAutomaticPing: true, SkipDefaultTransaction: true, Logger: logger.Default.LogMode(logger.Silent),
			})
			if err != nil {
				t.Fatal(err)
			}
			pool, _ := database.DB()
			t.Cleanup(func() { _ = pool.Close() })
			previous := db.DB
			db.DB = database
			t.Cleanup(func() { db.DB = previous })
			_ = database.Callback().Query().Replace("gorm:query", func(tx *gorm.DB) { tx.AddError(gorm.ErrRecordNotFound) })
			saveAttempted := false
			audited := false
			_ = database.Callback().Create().Replace("gorm:create", func(tx *gorm.DB) {
				if setting, ok := tx.Statement.Dest.(*models.AppSetting); ok && setting.SettingKey == ai.MessengerInsightsPromptSettingKey {
					saveAttempted = true
					tx.AddError(errors.New("storage unavailable"))
				}
				if tx.Statement.Table == "activity_logs" {
					audited = true
				}
			})
			_ = database.Callback().Delete().Replace("gorm:delete", func(tx *gorm.DB) {
				// Only the new prompt's delete should fail; preceding unrelated settings
				// mutations remain successful so the handler reaches this branch.
				where, _ := tx.Statement.Clauses["WHERE"].Expression.(clause.Where)
				for _, expression := range where.Exprs {
					if expr, ok := expression.(clause.Expr); ok {
						for _, value := range expr.Vars {
							if value == ai.MessengerInsightsPromptSettingKey {
								saveAttempted = true
								tx.AddError(errors.New("storage unavailable"))
							}
						}
					}
				}
			})
			r := gin.New()
			r.PUT("/settings", SaveAIEnginesSettings)
			request := httptest.NewRequest(http.MethodPut, "/settings", strings.NewReader(body))
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			r.ServeHTTP(response, request)
			if !saveAttempted || response.Code != http.StatusInternalServerError || !strings.Contains(response.Body.String(), "messenger_insights_prompt_save_failed") || audited {
				t.Fatalf("failed persistence was accepted: attempted=%v, status=%d, audit=%v, body=%s", saveAttempted, response.Code, audited, response.Body.String())
			}
		})
	}
}
