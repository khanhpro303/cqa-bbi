package handlers

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/vietbui/chat-quality-agent/customercases"
	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/db/models"
	"github.com/vietbui/chat-quality-agent/engine"
	"github.com/vietbui/chat-quality-agent/pkg"
)

func TestComplaintToolRecordsForTheTokensChat(t *testing.T) {
	t.Setenv("JWT_SECRET", stockFixtureSigningKey)
	t.Setenv("ENCRYPTION_KEY", stockFixtureSigningKey)
	t.Setenv("DB_PASSWORD", "fixture")
	saved := db.DB
	db.DB = nil
	t.Cleanup(func() { db.DB = saved })

	var got []customercases.Report
	orig := recordComplaint
	recordComplaint = func(_ context.Context, r customercases.Report) (customercases.Outcome, error) {
		got = append(got, r)
		return customercases.Outcome{Action: customercases.ActionFollowup, Case: models.CustomerCase{
			ID: "case-uuid-secret", TenantID: r.TenantID, Title: "Kính nứt", Type: "product_defect",
			Status: customercases.StatusInProgress, FollowupCount: 2, CustomerUpdate: "Thứ 5 giao kính mới. Mã ticket: #66656",
		}, StaffNotified: r.SubjectID == "zu-2"}, nil
	}
	t.Cleanup(func() { recordComplaint = orig })

	sign := func(p engine.GroupPermissionContext) string {
		token, err := engine.SignPermissionToken(p, stockFixtureSigningKey)
		if err != nil {
			t.Fatal(err)
		}
		return token
	}
	args := func(v map[string]any) map[string]json.RawMessage {
		out := map[string]json.RawMessage{}
		for k, val := range v {
			b, _ := json.Marshal(val)
			out[k] = b
		}
		return out
	}
	body := func(res any) (string, bool) {
		b, _ := json.Marshal(res)
		var parsed struct {
			IsError bool `json:"isError"`
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		}
		_ = json.Unmarshal(b, &parsed)
		return parsed.Content[0].Text, parsed.IsError
	}

	group := engine.GroupPermissionContext{TenantID: "t1", AgentType: "public", ZaloUserID: "zu-1", ChannelID: "ch-1", ZaloGroupID: "g-1", CustomerCode: "KH01"}
	res := handleComplaintTool(context.Background(), "t1", "public", sign(group), args(map[string]any{
		"type": "product_defect", "severity": "high", "title": "Kính nứt", "detail": "Ảnh: kính nứt", "evidence": "nứt rồi em",
	}))
	out, failed := body(res)
	if failed || len(got) != 1 {
		t.Fatalf("call failed: %s", out)
	}
	r := got[0]
	if r.SubjectType != "group" || r.SubjectID != "g-1" || r.ZaloUserID != "zu-1" || r.CustomerCode != "KH01" || r.Source != customercases.SourceBot || r.Type != "product_defect" {
		t.Fatalf("report = %+v", r)
	}
	if strings.Contains(out, "case-uuid-secret") || strings.Contains(out, "66656") || !strings.Contains(out, `"action":"followup"`) || !strings.Contains(out, "Thứ 5 giao kính mới") {
		t.Fatalf("tool result must carry the action and update, never the id: %s", out)
	}
	if !strings.Contains(out, `"note"`) {
		t.Fatalf("unnotified report needs the note: %s", out)
	}

	// A customer cannot report for another customer code.
	direct := engine.GroupPermissionContext{TenantID: "t1", AgentType: "public", ZaloUserID: "zu-2", ChannelID: "ch-1", CustomerCode: "KH02"}
	directOut, _ := body(handleComplaintTool(context.Background(), "t1", "public", sign(direct), args(map[string]any{"title": "Giao chậm", "detail": "x", "customer_code": "KH99"})))
	if r := got[len(got)-1]; r.SubjectType != "user" || r.SubjectID != "zu-2" || r.CustomerCode != "KH02" {
		t.Fatalf("customer_code from a customer must be ignored: %+v", r)
	}
	if strings.Contains(directOut, `"note"`) || !strings.Contains(directOut, `"staff_notified":true`) {
		t.Fatalf("notified report: %s", directOut)
	}

	// A customer in someone else's group (not a CRM member): the report is
	// theirs, never the group's, and keeps their own code.
	guest := engine.GroupPermissionContext{TenantID: "t1", AgentType: "public", ZaloUserID: "zu-4", ChannelID: "ch-1", ZaloGroupID: "g-1", CustomerCode: "KH04", GroupGuest: true}
	handleComplaintTool(context.Background(), "t1", "public", sign(guest), args(map[string]any{"title": "Giao thiếu", "detail": "x"}))
	if r := got[len(got)-1]; r.SubjectType != "user" || r.SubjectID != "zu-4" || r.CustomerCode != "KH04" || r.ZaloGroupID != "g-1" {
		t.Fatalf("guest report = %+v", r)
	}

	for _, tc := range []struct {
		name, tenant, agent, token string
	}{
		{"no token", "t1", "public", ""},
		{"other tenant", "t2", "public", sign(direct)},
		{"other agent", "t1", "private", sign(direct)},
	} {
		if out, failed := body(handleComplaintTool(context.Background(), tc.tenant, tc.agent, tc.token, args(map[string]any{"title": "x", "detail": "y"}))); !failed || !strings.Contains(out, "forbidden_token") {
			t.Errorf("%s: %s", tc.name, out)
		}
	}
	noChat := engine.GroupPermissionContext{TenantID: "t1", AgentType: "public", ZaloUserID: "zu-3"}
	if out, failed := body(handleComplaintTool(context.Background(), "t1", "public", sign(noChat), args(map[string]any{"title": "x", "detail": "y"}))); !failed || !strings.Contains(out, "cuộc trò chuyện") {
		t.Errorf("token without a chat: %s", out)
	}
	if out, failed := body(handleComplaintTool(context.Background(), "t1", "public", sign(direct), args(map[string]any{"title": " ", "detail": ""}))); !failed {
		t.Errorf("empty report accepted: %s", out)
	}
}

func TestComplaintToolIsListed(t *testing.T) {
	tool := complaintMCPTool()
	if tool.Name != "complaint_record" {
		t.Fatal(tool.Name)
	}
	req := tool.InputSchema["required"].([]string)
	if strings.Join(req, ",") != "title,detail" {
		t.Fatalf("required = %v", req)
	}
}

// The channel switch is checked on every call, not only when the tool list
// is built: a flow that still lists the tool cannot write once it is off.
func TestComplaintToolRespectsChannelSwitchMySQL(t *testing.T) {
	dsn := os.Getenv("CQA_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("CQA_TEST_MYSQL_DSN not set")
	}
	t.Setenv("JWT_SECRET", stockFixtureSigningKey)
	t.Setenv("ENCRYPTION_KEY", stockFixtureSigningKey)
	t.Setenv("DB_PASSWORD", "fixture")
	saved := db.DB
	if err := db.Connect(dsn, false); err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.DB = saved })
	orig := recordComplaint
	calls := 0
	recordComplaint = func(_ context.Context, r customercases.Report) (customercases.Outcome, error) {
		calls++
		return customercases.Outcome{Action: customercases.ActionCreated, Case: models.CustomerCase{TenantID: r.TenantID}, StaffNotified: true}, nil
	}
	t.Cleanup(func() { recordComplaint = orig })

	tenant, channel := pkg.NewUUID(), pkg.NewUUID()
	now := time.Now()
	db.DB.Create(&models.Tenant{ID: tenant, Name: "t", Slug: "t-" + tenant[:8], Settings: "{}", CreatedAt: now, UpdatedAt: now})
	db.DB.Create(&models.Channel{ID: channel, TenantID: tenant, ChannelType: "zalo_oa", Name: "OA", CredentialsEncrypted: []byte("x"), Metadata: "{}", CreatedAt: now, UpdatedAt: now})
	token, err := engine.SignPermissionToken(engine.GroupPermissionContext{TenantID: tenant, AgentType: "public", ZaloUserID: "zu-1", ChannelID: channel}, stockFixtureSigningKey)
	if err != nil {
		t.Fatal(err)
	}
	call := func() string {
		b, _ := json.Marshal(handleComplaintTool(context.Background(), tenant, "public", token, map[string]json.RawMessage{"title": json.RawMessage(`"Kính nứt"`), "detail": json.RawMessage(`"x"`)}))
		return string(b)
	}
	if out := call(); calls != 0 || !strings.Contains(out, "chưa bật") {
		t.Fatalf("switch off (default): calls=%d %s", calls, out)
	}
	db.DB.Model(&models.Channel{}).Where("id = ?", channel).Update("metadata", `{"complaint_cases":true}`)
	if out := call(); calls != 1 || strings.Contains(out, "chưa bật") {
		t.Fatalf("switch on: calls=%d %s", calls, out)
	}
}
