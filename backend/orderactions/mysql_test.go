package orderactions

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	mysqldriver "github.com/go-sql-driver/mysql"
	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/db/models"
	"github.com/vietbui/chat-quality-agent/pkg"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// testDatabase gives each test its own MySQL database. It runs only when
// MESSENGER_LABELS_TEST_DSN points at a MySQL server (same as the other
// MySQL integration tests).
func testDatabase(t *testing.T) {
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
	name := "cqa_orderactions_" + strings.ReplaceAll(pkg.NewUUID(), "-", "")[:16]
	if err := admin.Exec("CREATE DATABASE `" + name + "`").Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { admin.Exec("DROP DATABASE `" + name + "`") })
	cfg.DBName = name
	database, err := gorm.Open(mysql.Open(cfg.FormatDSN()), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	if err := database.AutoMigrate(&models.OrderAction{}); err != nil {
		t.Fatal(err)
	}
	saved := db.DB
	db.DB = database
	t.Cleanup(func() { db.DB = saved })
}

type fakeWriter struct {
	calls    int
	endpoint string
	err      error
	response string
}

func (w *fakeWriter) WriteJSON(_ context.Context, _ string, endpoint string, _ any) (json.RawMessage, error) {
	w.calls++
	w.endpoint = endpoint
	if w.err != nil {
		return nil, w.err
	}
	return json.RawMessage(w.response), nil
}

func draft(t *testing.T, kind string) *models.OrderAction {
	t.Helper()
	action := &models.OrderAction{
		TenantID: "tenant", Kind: kind, WorkKey: "zalo_session:c:u", ZaloUserID: "u",
		CustomerCode: "KH125", OrderID: 25458, OrderCode: "CQA-260101-ABCDEF",
		Payload: `{"MA_HOA_DON":"CQA-260101-ABCDEF"}`,
	}
	if err := Save(context.Background(), action); err != nil {
		t.Fatal(err)
	}
	return action
}

func status(t *testing.T, id string) string {
	t.Helper()
	var action models.OrderAction
	if err := db.DB.First(&action, "id = ?", id).Error; err != nil {
		t.Fatal(err)
	}
	return action.Status
}

func TestCommitWritesExactlyOnce(t *testing.T) {
	testDatabase(t)
	action := draft(t, KindCreate)
	writer := &fakeWriter{response: `{"code":200,"message":"Success","data":{"id":25460,"MA_HOA_DON":"CQA-260101-ABCDEF"}}`}

	if _, err := Commit(context.Background(), action.ID, "someone-else", writer); !errors.Is(err, ErrNotClaimable) {
		t.Fatalf("another user confirmed: %v", err)
	}
	outcome, err := Commit(context.Background(), action.ID, "u", writer)
	if err != nil || outcome.Status != StatusDone || outcome.OrderCode != "CQA-260101-ABCDEF" || writer.endpoint != "saorders/create" {
		t.Fatalf("first confirm: %+v %v %s", outcome, err, writer.endpoint)
	}
	if _, err := Commit(context.Background(), action.ID, "u", writer); !errors.Is(err, ErrNotClaimable) {
		t.Fatalf("redelivered confirm: %v", err)
	}
	if writer.calls != 1 || status(t, action.ID) != StatusDone {
		t.Fatalf("writes=%d status=%s", writer.calls, status(t, action.ID))
	}
}

func TestCommitOutcomes(t *testing.T) {
	testDatabase(t)
	rejected := draft(t, KindUpdate)
	writer := &fakeWriter{err: fmt.Errorf("%w: HTTP 400", pkg.ErrCloudifyRejected)}
	if outcome, _ := Commit(context.Background(), rejected.ID, "u", writer); outcome.Status != StatusFailed || writer.endpoint != "saorders/25458/update" {
		t.Errorf("rejected write: %+v %s", outcome, writer.endpoint)
	}
	timedOut := draft(t, KindCreate)
	writer = &fakeWriter{err: context.DeadlineExceeded}
	if outcome, _ := Commit(context.Background(), timedOut.ID, "u", writer); outcome.Status != StatusUnknown || !strings.Contains(outcome.Message, "CQA-260101-ABCDEF") {
		t.Errorf("timed-out write: %+v", outcome)
	}
}

func TestSaveSupersedesAndExpiry(t *testing.T) {
	testDatabase(t)
	first := draft(t, KindCreate)
	second := draft(t, KindCreate)
	if status(t, first.ID) != StatusSuperseded {
		t.Errorf("older draft should be superseded, got %s", status(t, first.ID))
	}
	if pending, ok := Pending(context.Background(), "tenant", "zalo_session:c:u"); !ok || pending.ID != second.ID {
		t.Errorf("pending draft = %+v %v", pending, ok)
	}
	db.DB.Model(&models.OrderAction{}).Where("id = ?", second.ID).Update("expires_at", time.Now().Add(-time.Minute))
	if _, ok := Pending(context.Background(), "tenant", "zalo_session:c:u"); ok {
		t.Error("expired draft must not be pending")
	}
	if _, err := Commit(context.Background(), second.ID, "u", &fakeWriter{}); !errors.Is(err, ErrNotClaimable) {
		t.Errorf("expired draft committed: %v", err)
	}
	third := draft(t, KindCancel)
	if !Decline(context.Background(), third.ID, "u") || status(t, third.ID) != StatusDeclined {
		t.Error("decline failed")
	}
}
