package engine

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/vietbui/chat-quality-agent/config"
	"github.com/vietbui/chat-quality-agent/db/models"
	"gorm.io/gorm"
)

type orderTestPool struct{ gorm.ConnPool }

func (p orderTestPool) BeginTx(context.Context, *sql.TxOptions) (gorm.ConnPool, error) {
	return &orderTestTx{p.ConnPool}, nil
}

type orderTestTx struct{ gorm.ConnPool }

func (orderTestTx) Commit() error   { return nil }
func (orderTestTx) Rollback() error { return nil }

func installOrderFlowCallbacks(t *testing.T, database *gorm.DB, messages []models.Message, queryErr error) *[]models.JobResult {
	t.Helper()
	// The latest-insight lookup uses Row/Scan. Dry-run prevents its MySQL
	// connection while the Query callback below still supplies transcript rows.

	database.Config.ConnPool = orderTestPool{database.Config.ConnPool}
	database.Statement.ConnPool = database.Config.ConnPool
	created := &[]models.JobResult{}
	database.Callback().Query().Replace("gorm:query", func(tx *gorm.DB) {
		switch dest := tx.Statement.Dest.(type) {
		case *[]models.Message:
			if queryErr != nil {
				tx.AddError(queryErr)
				return
			}
			*dest = append([]models.Message(nil), messages...)
		default:
			// The latest-insight lookup scans into an anonymous { Detail string }
			// value. Leave it empty to model a conversation's first analysis.
			value := reflect.ValueOf(tx.Statement.Dest)
			if value.Kind() == reflect.Pointer && !value.IsNil() {
				value = value.Elem()
				if value.Kind() == reflect.Struct {
					field := value.FieldByName("Detail")
					if field.IsValid() && field.CanSet() && field.Kind() == reflect.String {
						field.SetString("")
					}
				}
			}
		}
		tx.RowsAffected = 1
	})
	database.Callback().Create().Replace("gorm:create", func(tx *gorm.DB) {
		if result, ok := tx.Statement.Dest.(*models.JobResult); ok {
			*created = append(*created, *result)
		}
		tx.RowsAffected = 1
	})
	return created
}

func storedOrderOutcomes(t *testing.T, results []models.JobResult) []persistedOrderOutcome {
	t.Helper()
	for _, result := range results {
		if result.ResultType != "conversation_insight" {
			continue
		}
		var detail struct {
			OrderOutcomes []persistedOrderOutcome `json:"order_outcomes"`
		}
		if err := json.Unmarshal([]byte(result.Detail), &detail); err != nil {
			t.Fatalf("decode stored insight: %v", err)
		}
		return detail.OrderOutcomes
	}
	t.Fatal("conversation_insight was not stored")
	return nil
}

func TestSaveResultsValidatesOrderEvidenceBeforePersistence(t *testing.T) {
	now := time.Date(2026, 11, 16, 3, 0, 0, 0, time.UTC)
	messages := []models.Message{
		{ID: "tracking", TenantID: "tenant", ConversationID: "conv", SenderType: "agent", Content: "Đơn của chị đã gửi, mã vận đơn VN123456789", SentAt: now},
		{ID: "qr", TenantID: "tenant", ConversationID: "conv", SenderType: "agent", Content: "Em gửi mã QR 450.000đ để chị thanh toán", SentAt: now.Add(time.Minute)},
	}
	database := resultStateDB(t)
	created := installOrderFlowCallbacks(t, database, messages, nil)
	raw := `{"tags":[],"summary":"Đã gửi hàng","insights":{"intents":[],"products":[],"feedback":[],"lead_quality":{"level":"unknown"},"order_outcomes":[{"status":"confirmed","order_ref":"VN123456789","value_vnd":450000,"confirmation_message_id":"tracking","confirmation_evidence":"Đơn của chị đã gửi, mã vận đơn VN123456789","amount_message_id":"qr","amount_evidence":"Em gửi mã QR 450.000đ để chị thanh toán","confidence":0.97}]}}`

	_, _, err := NewAnalyzer(&config.Config{}).saveResults("run", "tenant", "conv", "classification", raw)
	if err != nil {
		t.Fatal(err)
	}
	outcomes := storedOrderOutcomes(t, *created)
	if len(outcomes) != 1 || outcomes[0].Status != "confirmed" {
		t.Fatalf("valid staff tracking evidence was not persisted: %#v", outcomes)
	}
	if outcomes[0].ValueVND != nil || outcomes[0].AmountMessageID != "" || outcomes[0].AmountEvidence != "" {
		t.Fatalf("QR amount must not become the order total: %#v", outcomes[0])
	}
	if outcomes[0].ConfirmationMessageID != "tracking" || outcomes[0].ConfirmationEvidence != messages[0].Content {
		t.Fatalf("verified confirmation evidence was not retained: %#v", outcomes[0])
	}
}

func TestSaveResultsPersistsEmptyOrderOutcomesArray(t *testing.T) {
	database := resultStateDB(t)
	created := installOrderFlowCallbacks(t, database, nil, nil)
	raw := `{"tags":[],"summary":"Chỉ hỏi giá","insights":{"intents":["hỏi giá"],"products":[],"feedback":[],"lead_quality":{"level":"medium","evidence":"Giá bao nhiêu?","reason":"Khách hỏi giá"},"order_outcomes":[]}}`

	_, _, err := NewAnalyzer(&config.Config{}).saveResults("run", "tenant", "conv", "classification", raw)
	if err != nil {
		t.Fatal(err)
	}
	for _, result := range *created {
		if result.ResultType != "conversation_insight" {
			continue
		}
		var detail map[string]json.RawMessage
		if err := json.Unmarshal([]byte(result.Detail), &detail); err != nil {
			t.Fatal(err)
		}
		value, ok := detail["order_outcomes"]
		if !ok || string(value) != "[]" {
			t.Fatalf("stored insight must contain order_outcomes: []; detail=%s", result.Detail)
		}
		return
	}
	t.Fatal("conversation_insight was not stored")
}

func TestSaveResultsReturnsEvidenceQueryFailure(t *testing.T) {
	database := resultStateDB(t)
	installOrderFlowCallbacks(t, database, nil, errors.New("messages unavailable"))
	raw := `{"tags":[],"summary":"Order","insights":{"intents":[],"products":[],"feedback":[],"lead_quality":{"level":"unknown"},"order_outcomes":[{"status":"confirmed","confirmation_message_id":"tracking","confirmation_evidence":"Mã vận đơn VN123"}]}}`

	_, _, err := NewAnalyzer(&config.Config{}).saveResults("run", "tenant", "conv", "classification", raw)
	if err == nil || !strings.Contains(err.Error(), "load order outcome evidence: messages unavailable") {
		t.Fatalf("expected evidence query error, got %v", err)
	}
}
