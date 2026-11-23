package handlers

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/vietbui/chat-quality-agent/customercases"
	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/db/models"
	"github.com/vietbui/chat-quality-agent/engine"
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
			Status: customercases.StatusInProgress, FollowupCount: 2, CustomerUpdate: "Thứ 5 giao kính mới",
		}}, nil
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
	if strings.Contains(out, "case-uuid-secret") || !strings.Contains(out, `"action":"followup"`) || !strings.Contains(out, "Thứ 5 giao kính mới") {
		t.Fatalf("tool result must carry the action and update, never the id: %s", out)
	}

	// A customer cannot report for another customer code.
	direct := engine.GroupPermissionContext{TenantID: "t1", AgentType: "public", ZaloUserID: "zu-2", ChannelID: "ch-1", CustomerCode: "KH02"}
	handleComplaintTool(context.Background(), "t1", "public", sign(direct), args(map[string]any{"title": "Giao chậm", "detail": "x", "customer_code": "KH99"}))
	if r := got[len(got)-1]; r.SubjectType != "user" || r.SubjectID != "zu-2" || r.CustomerCode != "KH02" {
		t.Fatalf("customer_code from a customer must be ignored: %+v", r)
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
