package customercases

import (
	"strings"
	"testing"
	"time"

	"github.com/vietbui/chat-quality-agent/db/models"
)

func TestParseDecision(t *testing.T) {
	cases := []struct {
		in      string
		want    int
		wantErr bool
	}{
		{`{"decision":"new","case":0,"reason":"khác đơn"}`, -1, false},
		{`{"decision":"same","case":2,"reason":"cùng đơn"}`, 1, false},
		{"```json\n{\"decision\":\"same\",\"case\":\"[1]\"}\n```", 0, false},
		{`{"decision":"same","case":3}`, -1, true},
		{`{"decision":"same"}`, -1, true},
		{`{"decision":"maybe","case":1}`, -1, true},
		{`not json`, -1, true},
	}
	for _, c := range cases {
		got, _, _, err := ParseDecision(c.in, 2, nil)
		if got != c.want || (err != nil) != c.wantErr {
			t.Errorf("ParseDecision(%q) = %d, %v; want %d, err=%v", c.in, got, err, c.want, c.wantErr)
		}
	}
}

func TestBuildDecisionInputNumbersCandidates(t *testing.T) {
	loc := time.UTC
	at := time.Date(2026, 11, 20, 13, 23, 0, 0, loc)
	in := BuildDecisionInput(Report{Type: "payment", Title: "Lỗi chứng từ DH0565913-3", Evidence: "đang bị gì ạ",
		Images: []models.CaseImage{{URL: "u", Description: "Thông báo: Đơn hàng số <> đã được xử lý"}}},
		[]models.CustomerCase{
			{ID: "a", Type: "delivery", Title: "Giao thiếu 2 nón", Status: StatusOpen, CreatedAt: at},
			{ID: "b", Type: "payment", Title: "Chứng từ DH0565913-3 báo lỗi", Status: StatusResolved, CreatedAt: at, Resolution: "Đã xoá chứng từ cũ"},
		}, nil, loc)
	for _, want := range []string{"<van_de_moi>", "Loại: Thanh toán, công nợ", "Ảnh khách gửi: Thông báo", "[1] Mới, chưa xử lý · Giao hàng", "[2] Đã xử lý xong", "Cách đã xử lý: Đã xoá chứng từ cũ"} {
		if !strings.Contains(in, want) {
			t.Errorf("input missing %q:\n%s", want, in)
		}
	}
	if strings.Contains(in, "\"a\"") || strings.Contains(in, " b ") {
		t.Errorf("input leaks case ids:\n%s", in)
	}
}

func TestRenderForBotHasNoIDs(t *testing.T) {
	c := models.CustomerCase{ID: "5f0c1b2e-1111-2222-3333-444455556666", Type: "product_defect", Title: "Kính nứt",
		Status: StatusInProgress, FollowupCount: 2, CustomerUpdate: "Thứ 5 giao kính mới", CreatedAt: time.Now()}
	out := RenderForBot([]models.CustomerCase{c}, time.UTC)
	if strings.Contains(out, c.ID) || strings.Contains(out, "#") {
		t.Fatalf("render leaks an id: %s", out)
	}
	for _, want := range []string{"Hàng lỗi: Kính nứt", "Đang xử lý", "hỏi lại 2 lần", "Thứ 5 giao kính mới"} {
		if !strings.Contains(out, want) {
			t.Errorf("render missing %q: %s", want, out)
		}
	}
	if RenderForBot(nil, time.UTC) != "(không có)" {
		t.Error("empty list")
	}
}

func TestMergeImagesDedupesAndCaps(t *testing.T) {
	have := []models.CaseImage{{URL: "a"}}
	add := []models.CaseImage{{URL: "a"}, {URL: ""}, {URL: "b"}}
	got := mergeImages(have, add)
	if len(got) != 2 || got[1].URL != "b" {
		t.Fatalf("merge = %+v", got)
	}
	var many []models.CaseImage
	for i := 0; i < 30; i++ {
		many = append(many, models.CaseImage{URL: string(rune('A' + i))})
	}
	if n := len(mergeImages(nil, many)); n != maxImagesPerCase {
		t.Fatalf("cap = %d", n)
	}
	if mergeImages(nil, nil) == nil {
		t.Fatal("merge must return a non-nil list (stored as [])")
	}
}

// Off until staff turn it on (after the Langflow MCP Tools node lists
// complaint_record).
func TestEnabledForDefaultsOff(t *testing.T) {
	if EnabledFor("") || EnabledFor("{}") || EnabledFor(`{"complaint_cases":false}`) || EnabledFor(`{"complaint_cases":"yes"}`) {
		t.Fatal("unset must mean off")
	}
	if !EnabledFor(`{"complaint_cases":true}`) {
		t.Fatal("true must turn it on")
	}
}

func TestValidTypeAndSeverity(t *testing.T) {
	if ValidType("Product_Defect") != "product_defect" || ValidType("bot_failure") != "other" {
		t.Fatal("type")
	}
	if ValidSeverity("HIGH") != "high" || ValidSeverity("urgent") != "medium" {
		t.Fatal("severity")
	}
}
