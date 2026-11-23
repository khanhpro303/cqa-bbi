package customercases

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/db/models"
	"github.com/vietbui/chat-quality-agent/engine"
	"github.com/vietbui/chat-quality-agent/pkg"
)

// Needs MySQL 8 (or MariaDB 10.11), e.g.
//
//	CQA_TEST_MYSQL_DSN='root:root@tcp(127.0.0.1:3307)/cqa?parseTime=true&loc=UTC&charset=utf8mb4' go test ./customercases
func connectTestMySQL(t *testing.T) {
	t.Helper()
	dsn := os.Getenv("CQA_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("CQA_TEST_MYSQL_DSN not set")
	}
	if db.DB == nil {
		if err := db.Connect(dsn, false); err != nil {
			t.Fatal(err)
		}
		if err := db.AutoMigrate(); err != nil {
			t.Fatal(err)
		}
	}
}

type fakeModel struct {
	answers []string
	err     error
	inputs  []string
}

func (f *fakeModel) call(_ context.Context, _, _, _ string, input string) (string, error) {
	f.inputs = append(f.inputs, input)
	if f.err != nil {
		return "", f.err
	}
	if len(f.answers) == 0 {
		return "", errors.New("no answer queued")
	}
	a := f.answers[0]
	f.answers = f.answers[1:]
	return a, nil
}

func setupTenant(t *testing.T) (tenantID, channelID string) {
	t.Helper()
	tenantID, channelID = pkg.NewUUID(), pkg.NewUUID()
	now := time.Now()
	if err := db.DB.Create(&models.Tenant{ID: tenantID, Name: "t", Slug: "t-" + tenantID[:8], Settings: "{}", CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.DB.Create(&models.Channel{ID: channelID, TenantID: tenantID, ChannelType: "zalo_oa", Name: "OA", CredentialsEncrypted: []byte("x"), Metadata: "{}", CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	return tenantID, channelID
}

func stubAlerts(t *testing.T) *[]engine.CaseAlert {
	t.Helper()
	var sent []engine.CaseAlert
	orig := sendAlert
	sendAlert = func(_ context.Context, _ models.Channel, a engine.CaseAlert) bool {
		sent = append(sent, a)
		return true
	}
	t.Cleanup(func() { sendAlert = orig })
	return &sent
}

func stubModel(t *testing.T, f *fakeModel) {
	t.Helper()
	orig := callModel
	callModel = f.call
	t.Cleanup(func() { callModel = orig })
}

func TestRecordLifecycleMySQL(t *testing.T) {
	connectTestMySQL(t)
	ctx := context.Background()
	tenantID, channelID := setupTenant(t)
	alerts := stubAlerts(t)
	model := &fakeModel{}
	stubModel(t, model)
	start := time.Now().Add(-time.Second)

	base := Report{TenantID: tenantID, ChannelID: channelID, SubjectType: "user", SubjectID: "zu-1", ZaloUserID: "zu-1",
		ReporterName: "Anh Bình", CustomerName: "Anh Bình", CustomerCode: "KH01", SessionID: "s1", Source: SourceBot}

	// 1. First report: no earlier case, no model call, staff alerted.
	r1 := base
	r1.Type, r1.Severity, r1.Title, r1.Detail, r1.Evidence = "product_defect", "high", "Kính FF800 bị nứt", "Nhận hàng thấy kính nứt", "kính nứt rồi em"
	r1.Images = []models.CaseImage{{URL: "https://f1.zdn.vn/a.jpg", Description: "kính nứt"}}
	out, err := Record(ctx, r1)
	if err != nil || out.Action != ActionCreated {
		t.Fatalf("first report: %+v %v", out, err)
	}
	if len(model.inputs) != 0 || len(*alerts) != 1 || (*alerts)[0].Kind != ActionCreated || (*alerts)[0].ImageCount != 1 {
		t.Fatalf("model calls %d, alerts %+v", len(model.inputs), *alerts)
	}
	first := out.Case

	// 2. Customer asks again: the model says same → follow-up, no new
	// alert within the gap.
	model.answers = []string{`{"decision":"same","case":1,"reason":"hỏi tiến độ"}`}
	r2 := base
	r2.Title, r2.Detail, r2.Evidence = "Khách hỏi kính nứt xử lý tới đâu", "Hỏi tiến độ", "sao rồi em"
	r2.Images = []models.CaseImage{{URL: "https://f1.zdn.vn/b.jpg"}}
	out, err = Record(ctx, r2)
	if err != nil || out.Action != ActionFollowup || out.Case.ID != first.ID || out.Case.FollowupCount != 1 {
		t.Fatalf("follow-up: %+v %v", out, err)
	}
	if len(*alerts) != 1 {
		t.Fatalf("follow-up inside the gap must not alert again: %d", len(*alerts))
	}
	if !strings.Contains(model.inputs[0], "[1] Mới, chưa xử lý · Hàng lỗi") {
		t.Fatalf("decision input:\n%s", model.inputs[0])
	}
	var stored models.CustomerCase
	db.DB.Where("id = ?", first.ID).First(&stored)
	if len(ParseImages(stored.Images)) != 2 || stored.FollowupCount != 1 {
		t.Fatalf("stored = %+v", stored)
	}

	// 3. A different problem → new case.
	model.answers = []string{`{"decision":"new","case":0}`}
	r3 := base
	r3.Type, r3.Title, r3.Detail = "delivery", "Đơn DH02 giao thiếu 2 nón", "Thiếu 2 nón"
	out, err = Record(ctx, r3)
	if err != nil || out.Action != ActionCreated || out.Case.ID == first.ID {
		t.Fatalf("new problem: %+v %v", out, err)
	}

	// 4. Staff resolve the first case; the customer says it is still
	// broken → reopened and alerted.
	now := time.Now()
	db.DB.Model(&models.CustomerCase{}).Where("id = ?", first.ID).Updates(map[string]interface{}{"status": StatusResolved, "resolved_at": now})
	model.answers = []string{`{"decision":"same","case":1}`} // newest first: resolving touched the kính case
	r4 := base
	r4.Title, r4.Detail = "Kính đổi rồi vẫn nứt", "Vẫn còn lỗi"
	out, err = Record(ctx, r4)
	if err != nil || out.Action != ActionReopened || out.Case.ID != first.ID || out.Case.Status != StatusOpen {
		t.Fatalf("reopen: %+v %v", out, err)
	}
	if last := (*alerts)[len(*alerts)-1]; last.Kind != ActionReopened {
		t.Fatalf("reopen alert: %+v", last)
	}

	// 5. The summarizer finds the same problem in the session the bot
	// already recorded: linked, no event, no alert, never reopens.
	alertsBefore := len(*alerts)
	var eventsBefore int64
	db.DB.Model(&models.CustomerCaseEvent{}).Where("case_id = ?", first.ID).Count(&eventsBefore)
	model.answers = []string{`{"decision":"same","case":1}`}
	r5 := base
	r5.Source, r5.Title = SourceSummary, "Kính nứt"
	out, err = Record(ctx, r5)
	if err != nil || out.Action != ActionLinked || out.Case.ID != first.ID {
		t.Fatalf("summary: %+v %v", out, err)
	}
	var eventsAfter int64
	db.DB.Model(&models.CustomerCaseEvent{}).Where("case_id = ?", first.ID).Count(&eventsAfter)
	if eventsAfter != eventsBefore || len(*alerts) != alertsBefore {
		t.Fatalf("summary of a recorded session added events %d→%d alerts %d→%d", eventsBefore, eventsAfter, alertsBefore, len(*alerts))
	}

	// 6. The decision model fails → a new case (a duplicate beats a lost
	// complaint).
	model.err = errors.New("timeout")
	r6 := base
	r6.Title = "Sai giá"
	out, err = Record(ctx, r6)
	if err != nil || out.Action != ActionCreated {
		t.Fatalf("model error: %+v %v", out, err)
	}
	model.err = nil

	// 7. Customer code links chats: the same shop's group sees the 1:1 case.
	cands, _ := Candidates(ctx, tenantID, "group", "g-1", "KH01")
	if len(cands) < 3 {
		t.Fatalf("candidates by customer code = %d", len(cands))
	}
	if open := OpenCases(ctx, tenantID, "user", "zu-other", ""); len(open) != 0 {
		t.Fatalf("other chat sees cases: %d", len(open))
	}

	if !TouchedSince(ctx, tenantID, "user", "zu-1", start) || TouchedSince(ctx, tenantID, "user", "zu-1", time.Now().Add(time.Minute)) {
		t.Fatal("TouchedSince")
	}
	if LastBotReportAt(ctx, tenantID, "user", "zu-1").Before(start) {
		t.Fatal("LastBotReportAt")
	}
}

func TestRecentImagesMySQL(t *testing.T) {
	connectTestMySQL(t)
	ctx := context.Background()
	tenantID, channelID := setupTenant(t)
	convID := pkg.NewUUID()
	now := time.Now()
	if err := db.DB.Create(&models.Conversation{ID: convID, TenantID: tenantID, ChannelID: channelID, ExternalConversationID: "g-9", Metadata: "{}", CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	add := func(sender, contentType, attachments string, at time.Time) {
		if err := db.DB.Create(&models.Message{ID: pkg.NewUUID(), TenantID: tenantID, ConversationID: convID, ExternalMessageID: pkg.NewUUID(),
			SenderType: "customer", SenderExternalID: sender, Content: "x", ContentType: contentType, Attachments: attachments,
			SentAt: at, RawData: "{}", CreatedAt: now}).Error; err != nil {
			t.Fatal(err)
		}
	}
	add("zu-a", "image", `[{"type":"image","url":"https://f1.zdn.vn/mine.jpg","description":"móp"}]`, now.Add(-10*time.Minute))
	add("zu-b", "image", `[{"type":"image","url":"https://f1.zdn.vn/other.jpg"}]`, now.Add(-5*time.Minute))
	add("zu-a", "image", `[{"type":"image","url":"https://f1.zdn.vn/old.jpg"}]`, now.Add(-5*time.Hour))
	add("zu-a", "text", `[]`, now.Add(-time.Minute))

	got := RecentImages(ctx, tenantID, channelID, "g-9", "zu-a", now.Add(-2*time.Hour))
	if len(got) != 1 || got[0].URL != "https://f1.zdn.vn/mine.jpg" || got[0].Description != "móp" {
		t.Fatalf("recent images = %+v", got)
	}
	all := ConversationImages(ctx, tenantID, convID, now.Add(-time.Hour), now)
	if len(all) != 2 {
		t.Fatalf("session images = %+v", all)
	}
}
