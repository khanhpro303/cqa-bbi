package customercases

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
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
	if err := db.DB.Create(&models.Channel{ID: channelID, TenantID: tenantID, ChannelType: "zalo_oa", Name: "OA", CredentialsEncrypted: []byte("x"), Metadata: `{"complaint_cases":true}`, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
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
	r1.TurnID = "turn-1"
	out, err := Record(ctx, r1)
	if err != nil || out.Action != ActionCreated || !out.StaffNotified {
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
	if err != nil || out.Action != ActionFollowup || out.Case.ID != first.ID || out.Case.FollowupCount != 1 || !out.StaffNotified {
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

	if rec, notified := TurnCaseState(ctx, tenantID, "turn-1"); !rec || !notified {
		t.Fatalf("TurnCaseState this turn = %t %t", rec, notified)
	}
	if rec, _ := TurnCaseState(ctx, tenantID, "turn-other"); rec {
		t.Fatal("another turn must not see these reports")
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
	raw := `{"webhook":{"cqa_signature_verified":true}}`
	add := func(sender, contentType, attachments string, at time.Time) {
		if err := db.DB.Create(&models.Message{ID: pkg.NewUUID(), TenantID: tenantID, ConversationID: convID, ExternalMessageID: pkg.NewUUID(),
			SenderType: "customer", SenderExternalID: sender, Content: "x", ContentType: contentType, Attachments: attachments,
			SentAt: at, RawData: raw, CreatedAt: now}).Error; err != nil {
			t.Fatal(err)
		}
	}
	add("zu-a", "image", `[{"type":"image","url":"https://f1.zdn.vn/mine.jpg","description":"móp"}]`, now.Add(-10*time.Minute))
	add("zu-b", "image", `[{"type":"image","url":"https://f1.zdn.vn/other.jpg"}]`, now.Add(-5*time.Minute))
	add("zu-a", "image", `[{"type":"image","url":"https://f1.zdn.vn/old.jpg"}]`, now.Add(-5*time.Hour))
	add("zu-a", "text", `[]`, now.Add(-time.Minute))
	add("zu-a", "image", `[{"type":"image","url":"http://169.254.169.254/latest"}]`, now.Add(-3*time.Minute))
	// An unsigned webhook's image (log mode) is not evidence.
	raw = `{"webhook":{}}`
	add("zu-a", "image", `[{"type":"image","url":"https://f1.zdn.vn/unsigned.jpg"}]`, now.Add(-2*time.Minute))

	got := RecentImages(ctx, tenantID, channelID, "g-9", "zu-a", now.Add(-2*time.Hour))
	if len(got) != 1 || got[0].URL != "https://f1.zdn.vn/mine.jpg" || got[0].Description != "móp" {
		t.Fatalf("recent images = %+v", got)
	}
	all := ConversationImages(ctx, tenantID, convID, now.Add(-time.Hour), now)
	if len(all) != 2 {
		t.Fatalf("session images = %+v", all)
	}
}

// An alert that does not go out leaves the report unnotified, so the worker
// and the summarizer fall back to their own alerts.
func TestRecordAlertFailureMySQL(t *testing.T) {
	connectTestMySQL(t)
	ctx := context.Background()
	tenantID, channelID := setupTenant(t)
	orig := sendAlert
	sendAlert = func(context.Context, models.Channel, engine.CaseAlert) bool { return false }
	t.Cleanup(func() { sendAlert = orig })
	stubModel(t, &fakeModel{})
	start := time.Now().Add(-time.Second)

	out, err := Record(ctx, Report{TenantID: tenantID, ChannelID: channelID, SubjectType: "group", SubjectID: "g-x", ZaloUserID: "zu-a",
		Source: SourceBot, Title: "Giao thiếu hàng", TurnID: "turn-a"})
	if err != nil || out.Action != ActionCreated || out.StaffNotified {
		t.Fatalf("%+v %v", out, err)
	}
	if rec, notified := TurnCaseState(ctx, tenantID, "turn-a"); !rec || notified {
		t.Fatalf("TurnCaseState = %t %t, want recorded and not notified", rec, notified)
	}

	// A second report in the same turn that does reach staff does not hide
	// the first one's failed alert.
	sendAlert = func(context.Context, models.Channel, engine.CaseAlert) bool { return true }
	stubModel(t, &fakeModel{answers: []string{`{"decision":"new","case":0}`}})
	if out, err := Record(ctx, Report{TenantID: tenantID, ChannelID: channelID, SubjectType: "group", SubjectID: "g-x", ZaloUserID: "zu-a",
		Source: SourceBot, Title: "Sai giá đơn DH9", TurnID: "turn-a"}); err != nil || !out.StaffNotified {
		t.Fatalf("%+v %v", out, err)
	}
	if rec, notified := TurnCaseState(ctx, tenantID, "turn-a"); !rec || notified {
		t.Fatalf("TurnCaseState = %t %t, one report still unnotified", rec, notified)
	}
	_ = start

	// The switch off on the channel: recorded, but staff are not alerted.
	db.DB.Model(&models.Channel{}).Where("id = ?", channelID).Update("metadata", `{"complaint_cases":false}`)
	sendAlert = func(context.Context, models.Channel, engine.CaseAlert) bool {
		t.Fatal("alert with the switch off")
		return true
	}
	stubModel(t, &fakeModel{answers: []string{`{"decision":"new","case":0}`}})
	out, err = Record(ctx, Report{TenantID: tenantID, ChannelID: channelID, SubjectType: "group", SubjectID: "g-x", ZaloUserID: "zu-a",
		Source: SourceBot, Title: "Sai giá"})
	if err != nil || out.StaffNotified {
		t.Fatalf("%+v %v", out, err)
	}
}

// Two reports of one shop from two chats at the same moment: the lock makes
// the second see the first, so the model can match them.
func TestRecordConcurrentSameCustomerMySQL(t *testing.T) {
	connectTestMySQL(t)
	ctx := context.Background()
	tenantID, channelID := setupTenant(t)
	stubAlerts(t)
	var mu sync.Mutex
	calls := 0
	orig := callModel
	callModel = func(context.Context, string, string, string, string) (string, error) {
		mu.Lock()
		calls++
		mu.Unlock()
		return `{"decision":"same","case":1}`, nil
	}
	t.Cleanup(func() { callModel = orig })

	var wg sync.WaitGroup
	results := make([]Outcome, 2)
	errs := make([]error, 2)
	for i, subject := range []string{"g-shop", "zu-owner"} {
		wg.Add(1)
		go func(i int, subjectType, subject string) {
			defer wg.Done()
			results[i], errs[i] = Record(ctx, Report{TenantID: tenantID, ChannelID: channelID, SubjectType: subjectType, SubjectID: subject,
				ZaloUserID: "zu-owner", CustomerCode: "KH-SHOP", Source: SourceBot, Title: "Kính nứt", Images: []models.CaseImage{{URL: "https://f1.zdn.vn/" + subject + ".jpg"}}})
		}(i, map[int]string{0: "group", 1: "user"}[i], subject)
	}
	wg.Wait()
	if errs[0] != nil || errs[1] != nil {
		t.Fatal(errs)
	}
	actions := results[0].Action + "," + results[1].Action
	if actions != ActionCreated+","+ActionFollowup && actions != ActionFollowup+","+ActionCreated {
		t.Fatalf("actions = %s (model calls %d)", actions, calls)
	}
	if results[0].Case.ID != results[1].Case.ID {
		t.Fatal("two cases for one problem")
	}
	var c models.CustomerCase
	db.DB.Where("id = ?", results[0].Case.ID).First(&c)
	if len(ParseImages(c.Images)) != 2 {
		t.Fatalf("images = %s", c.Images)
	}
}

// Staff cancel a case while the model is deciding: the report opens a new
// case instead of reopening the cancelled one.
func TestRecordCaseCancelledDuringMatchMySQL(t *testing.T) {
	connectTestMySQL(t)
	ctx := context.Background()
	tenantID, channelID := setupTenant(t)
	stubAlerts(t)
	stubModel(t, &fakeModel{})
	base := Report{TenantID: tenantID, ChannelID: channelID, SubjectType: "user", SubjectID: "zu-c", ZaloUserID: "zu-c", Source: SourceBot}
	r := base
	r.Title = "Kính nứt"
	first, err := Record(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	orig := callModel
	callModel = func(context.Context, string, string, string, string) (string, error) {
		db.DB.Model(&models.CustomerCase{}).Where("id = ?", first.Case.ID).Updates(map[string]interface{}{"status": StatusCancelled, "resolved_at": time.Now()})
		return `{"decision":"same","case":1}`, nil
	}
	t.Cleanup(func() { callModel = orig })
	r.Title = "Kính nứt, hỏi lại"
	out, err := Record(ctx, r)
	if err != nil || out.Action != ActionCreated || out.Case.ID == first.Case.ID {
		t.Fatalf("%+v %v", out, err)
	}
	var c models.CustomerCase
	db.DB.Where("id = ?", first.Case.ID).First(&c)
	if c.Status != StatusCancelled {
		t.Fatalf("cancelled case was changed: %s", c.Status)
	}
}

// A guest in someone else's group matches only their own chat's cases, even
// when the group has a case under the same customer code.
func TestRecordSubjectOnlyMySQL(t *testing.T) {
	connectTestMySQL(t)
	ctx := context.Background()
	tenantID, channelID := setupTenant(t)
	stubAlerts(t)
	model := &fakeModel{}
	stubModel(t, model)
	if _, err := Record(ctx, Report{TenantID: tenantID, ChannelID: channelID, SubjectType: "group", SubjectID: "g-host", CustomerCode: "KH-SAME",
		Source: SourceBot, Title: "Đơn của nhóm giao thiếu"}); err != nil {
		t.Fatal(err)
	}
	out, err := Record(ctx, Report{TenantID: tenantID, ChannelID: channelID, SubjectType: "user", SubjectID: "zu-guest", ZaloUserID: "zu-guest",
		CustomerCode: "KH-SAME", SubjectOnly: true, Source: SourceBot, Title: "Đơn của tôi giao thiếu"})
	if err != nil || out.Action != ActionCreated || len(model.inputs) != 0 || !out.Case.Private {
		t.Fatalf("guest saw the group's case: %+v %v (model calls %d)", out, err, len(model.inputs))
	}
	// The other way: the group, matching by its customer code, does not see
	// the guest's private case.
	for _, c := range OpenCases(ctx, tenantID, "group", "g-host", "KH-SAME") {
		if c.ID == out.Case.ID {
			t.Fatal("group sees the guest's private case")
		}
	}
	if cands, _ := Candidates(ctx, tenantID, "group", "g-host", "KH-SAME"); len(cands) != 1 {
		t.Fatalf("group candidates = %d, want only its own", len(cands))
	}
}

// A summary link counts as known to staff only when the case's current
// opening reached them.
func TestSummaryLinkNotifiedMySQL(t *testing.T) {
	connectTestMySQL(t)
	ctx := context.Background()
	tenantID, channelID := setupTenant(t)
	delivered := false
	orig := sendAlert
	sendAlert = func(context.Context, models.Channel, engine.CaseAlert) bool { return delivered }
	t.Cleanup(func() { sendAlert = orig })
	model := &fakeModel{}
	stubModel(t, model)
	base := Report{TenantID: tenantID, ChannelID: channelID, SubjectType: "user", SubjectID: "zu-s", ZaloUserID: "zu-s"}

	// Opened by the bot, alert delivered.
	delivered = true
	r := base
	r.Source, r.Title, r.SessionID = SourceBot, "Kính nứt", "s-1"
	first, err := Record(ctx, r)
	if err != nil || !first.StaffNotified {
		t.Fatalf("%+v %v", first, err)
	}
	// Resolved, then reopened, but the reopen alert fails.
	db.DB.Model(&models.CustomerCase{}).Where("id = ?", first.Case.ID).Updates(map[string]interface{}{"status": StatusResolved, "resolved_at": time.Now()})
	delivered = false
	model.answers = []string{`{"decision":"same","case":1}`}
	r.Title, r.SessionID = "Vẫn nứt", "s-2"
	if out, err := Record(ctx, r); err != nil || out.Action != ActionReopened || out.StaffNotified {
		t.Fatalf("%+v %v", out, err)
	}
	// The summary of another session links to it: the old alert does not
	// count for the reopened case.
	model.answers = []string{`{"decision":"same","case":1}`}
	s := base
	s.Source, s.Title, s.SessionID = SourceSummary, "Kính nứt", "s-3"
	if out, err := Record(ctx, s); err != nil || out.Action != ActionLinked || out.StaffNotified {
		t.Fatalf("summary link after an undelivered reopen: %+v %v", out, err)
	}
	// The summary of session s-1, whose bot report reached staff.
	model.answers = []string{`{"decision":"same","case":1}`}
	s.SessionID = "s-1"
	if out, err := Record(ctx, s); err != nil || out.Action != ActionLinked || !out.StaffNotified {
		t.Fatalf("summary of a notified session: %+v %v", out, err)
	}
}

// A retried message (same turn id) does not count as asking again, and a
// reopened case gets its own alert even inside the old follow-up cooldown.
func TestRecordRetryAndReopenCooldownMySQL(t *testing.T) {
	connectTestMySQL(t)
	ctx := context.Background()
	tenantID, channelID := setupTenant(t)
	alerts := stubAlerts(t)
	model := &fakeModel{}
	stubModel(t, model)
	base := Report{TenantID: tenantID, ChannelID: channelID, SubjectType: "user", SubjectID: "zu-r", ZaloUserID: "zu-r", Source: SourceBot}
	r := base
	r.Title, r.TurnID = "Kính nứt", "turn-1"
	first, err := Record(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	r.Title, r.TurnID = "Kính nứt, xử lý chưa", "turn-2"
	model.answers = []string{`{"decision":"same","case":1}`}
	if out, err := Record(ctx, r); err != nil || out.Action != ActionFollowup || out.Case.FollowupCount != 1 {
		t.Fatalf("%+v %v", out, err)
	}
	// turn-2 retried
	model.answers = []string{`{"decision":"same","case":1}`}
	out, err := Record(ctx, r)
	if err != nil || out.Case.FollowupCount != 1 || !out.StaffNotified {
		t.Fatalf("retry counted again: %+v %v", out, err)
	}
	var n int64
	db.DB.Model(&models.CustomerCaseEvent{}).Where("case_id = ? AND turn_id = ?", first.Case.ID, "turn-2").Count(&n)
	if n != 1 {
		t.Fatalf("turn-2 events = %d", n)
	}
	// Retried again, and this time the model says "new": the retry flag
	// returns what the turn already recorded instead of a second case.
	model.answers = []string{`{"decision":"new","case":0}`}
	retry := r
	retry.Retry = true
	out, err = Record(ctx, retry)
	if err != nil || out.Case.ID != first.Case.ID || out.Action != ActionFollowup {
		t.Fatalf("retry opened another case: %+v %v", out, err)
	}
	var cases int64
	db.DB.Model(&models.CustomerCase{}).Where("tenant_id = ?", tenantID).Count(&cases)
	if cases != 1 {
		t.Fatalf("cases = %d", cases)
	}

	// Resolved, then reopened within the follow-up cooldown of the first
	// alert: the reopen alerts, and a later follow-up relies on that alert.
	db.DB.Model(&models.CustomerCase{}).Where("id = ?", first.Case.ID).Updates(map[string]interface{}{"status": StatusResolved, "resolved_at": time.Now()})
	before := len(*alerts)
	orig := sendAlert
	sendAlert = func(context.Context, models.Channel, engine.CaseAlert) bool { return false }
	model.answers = []string{`{"decision":"same","case":1}`}
	r.Title, r.TurnID = "Vẫn nứt", "turn-3"
	if out, err := Record(ctx, r); err != nil || out.Action != ActionReopened || out.StaffNotified {
		t.Fatalf("reopen: %+v %v", out, err)
	}
	sendAlert = orig
	model.answers = []string{`{"decision":"same","case":1}`}
	r.Title, r.TurnID = "Sao rồi em", "turn-4"
	out, err = Record(ctx, r)
	if err != nil || out.Action != ActionFollowup || len(*alerts) != before+1 {
		t.Fatalf("follow-up after an undelivered reopen must alert: %+v %v alerts %d→%d", out, err, before, len(*alerts))
	}
}
