package engine

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/vietbui/chat-quality-agent/ai"
	"github.com/vietbui/chat-quality-agent/config"
	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/db/models"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"testing"
	"time"
)

func resultStateDB(t *testing.T) *gorm.DB {
	t.Helper()
	database, err := gorm.Open(mysql.New(mysql.Config{DSN: "test:test@tcp(127.0.0.1:1)/test", SkipInitializeWithVersion: true}), &gorm.Config{DisableAutomaticPing: true, SkipDefaultTransaction: true, Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	pool, _ := database.DB()
	previous := db.DB
	db.DB = database
	t.Cleanup(func() { db.DB = previous; _ = pool.Close() })
	database.Callback().Create().Replace("gorm:create", func(tx *gorm.DB) { tx.RowsAffected = 1 })
	database.Callback().Update().Replace("gorm:update", func(tx *gorm.DB) { tx.RowsAffected = 1 })
	return database
}

func TestClassificationResultCountsAndStorageErrors(t *testing.T) {
	for _, tc := range []struct {
		name, content, failType string
		wantPass, wantErr       bool
		wantCount               int
	}{
		{name: "tagged", content: `{"tags":[{"rule_name":"Hỏi giá"}],"summary":"Hỏi giá"}`, wantPass: true, wantCount: 1},
		{name: "untagged", content: `{"tags":[],"summary":"No match"}`},
		{name: "tag write fails", content: `{"tags":[{"rule_name":"Hỏi giá"}]}`, failType: "classification_tag", wantErr: true},
		{name: "evaluation write fails", content: `{"tags":[{"rule_name":"Hỏi giá"}]}`, failType: "conversation_evaluation", wantErr: true, wantCount: 1},
		{name: "skip write fails", content: `{"tags":[]}`, failType: "conversation_evaluation", wantErr: true},
		{name: "malformed AI JSON", content: `{"tags":[]]`, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			database := resultStateDB(t)
			database.Callback().Create().Replace("gorm:create", func(tx *gorm.DB) {
				if r, ok := tx.Statement.Dest.(*models.JobResult); ok && r.ResultType == tc.failType {
					tx.AddError(errors.New("storage unavailable"))
				}
			})
			count, passed, err := NewAnalyzer(&config.Config{}).saveResults("run", "tenant", "conv", "classification", tc.content)
			if count != tc.wantCount || passed != tc.wantPass || (err != nil) != tc.wantErr {
				t.Fatalf("count=%d passed=%v err=%v", count, passed, err)
			}
		})
	}
}

func TestClassificationEvaluationStoresSourceMessageWatermark(t *testing.T) {
	for _, tc := range []struct {
		name    string
		content string
	}{
		{name: "tagged", content: `{"tags":[{"rule_name":"Hỏi giá"}],"summary":"Hỏi giá"}`},
		{name: "no match", content: `{"tags":[],"summary":"No match"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			database := resultStateDB(t)
			var evaluationDetail string
			database.Callback().Create().Replace("gorm:create", func(tx *gorm.DB) {
				if result, ok := tx.Statement.Dest.(*models.JobResult); ok && result.ResultType == "conversation_evaluation" {
					evaluationDetail = result.Detail
				}
				tx.RowsAffected = 1
			})
			source := time.Date(2026, 9, 17, 8, 4, 5, 123000000, time.UTC)
			if _, _, err := NewAnalyzer(&config.Config{}).saveResults("run", "tenant", "conv", "classification", tc.content, &source); err != nil {
				t.Fatal(err)
			}
			var detail map[string]interface{}
			if err := json.Unmarshal([]byte(evaluationDetail), &detail); err != nil {
				t.Fatalf("invalid evaluation detail: %v", err)
			}
			if got := detail["source_last_message_at"]; got != "2026-09-17T08:04:05.123Z" {
				t.Fatalf("source watermark = %v", got)
			}
		})
	}
}

func TestAnalysisEmptyAndReadFailure(t *testing.T) {
	for _, batch := range []bool{false, true} {
		for _, tc := range []struct {
			name             string
			found, readError bool
			reason, status   string
		}{
			{name: "no eligible conversations", reason: "no_matching_conversations", status: "success"},
			{name: "no messages", found: true, reason: "no_messages", status: "success"},
			{name: "message query failed", found: true, readError: true, status: "error"},
		} {
			t.Run(tc.name+map[bool]string{true: " batch", false: " single"}[batch], func(t *testing.T) {
				database := resultStateDB(t)
				database.Callback().Query().Replace("gorm:query", func(tx *gorm.DB) {
					switch dest := tx.Statement.Dest.(type) {
					case *models.AppSetting:
						dest.ValuePlain = map[bool]string{true: "true", false: "false"}[batch]
					case *[]models.Conversation:
						if tc.found {
							now := time.Now()
							*dest = []models.Conversation{{ID: "conv", LastMessageAt: &now}}
						}
					case *[]models.Message:
						if tc.readError {
							tx.AddError(errors.New("cannot read messages"))
						}
					}
				})
				run, err := NewAnalyzer(&config.Config{}).RunJobWithProvider(context.Background(), models.Job{ID: "job", TenantID: "tenant", JobType: "classification", InputChannelIDs: `["channel"]`, RulesConfig: `[]`, OutputSchedule: "none"}, 3, &emptyRunProvider{})
				if err != nil {
					t.Fatal(err)
				}
				var summary map[string]interface{}
				if err := json.Unmarshal([]byte(run.Summary), &summary); err != nil {
					t.Fatal(err)
				}
				reason, _ := summary["empty_reason"].(string)
				if run.Status != tc.status || reason != tc.reason {
					t.Fatalf("status=%s summary=%s error=%s", run.Status, run.Summary, run.ErrorMessage)
				}
			})
		}
	}
}

type emptyRunProvider struct{ MockAIProvider }

func (p *emptyRunProvider) AnalyzeChatBatch(ctx context.Context, prompt string, items []ai.BatchItem) (ai.AIResponse, error) {
	return ai.AIResponse{}, errors.New("unexpected AI call for empty run")
}
