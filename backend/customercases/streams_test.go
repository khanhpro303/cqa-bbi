package customercases

import (
	"strings"
	"testing"
	"time"

	"github.com/vietbui/chat-quality-agent/db/models"
)

var testStreams = []Stream{
	{Key: "bao_hanh", Name: "Bảo hành", Description: "hàng lỗi, đổi trả, bảo hành", AlertGroupID: "g-warranty"},
	{Key: "marketing", Name: "Marketing", Description: "khuyến mãi, quảng cáo"},
}

func TestStreamsForParsesAndSkipsInvalid(t *testing.T) {
	meta := `{"complaint_streams":[
		{"key":"bao_hanh","name":"Bảo hành","description":"hàng lỗi","alert_group_id":"g1"},
		{"key":"","name":"Thiếu mã"},
		{"key":"x","name":""},
		{"key":"bao_hanh","name":"Trùng mã"},
		{"key":"Khach Hang!","name":"Khách hàng"},
		{"key":"sp","name":"  "}
	]}`
	got := StreamsFor(meta)
	if len(got) != 3 {
		t.Fatalf("want 3 streams, got %+v", got)
	}
	if got[0].Key != "bao_hanh" || got[0].AlertGroupID != "g1" {
		t.Errorf("first stream: %+v", got[0])
	}
	if got[1].Key != "thieu_ma" {
		t.Errorf("a missing key should come from the name: %+v", got[1])
	}
	if got[2].Key != "khach_hang" {
		t.Errorf("key should be normalised to a slug: %+v", got[2])
	}
	if StreamsFor(`{}`) != nil || StreamsFor(`not json`) != nil {
		t.Error("no streams expected")
	}
}

func TestParseDecisionReadsStream(t *testing.T) {
	idx, stream, _, err := ParseDecision(`{"decision":"new","case":0,"stream":"bao_hanh","reason":"hàng lỗi"}`, 0, testStreams)
	if err != nil || idx != -1 || stream != "bao_hanh" {
		t.Fatalf("got %d %q %v", idx, stream, err)
	}
	_, stream, _, _ = ParseDecision(`{"decision":"new","stream":"khong_co"}`, 0, testStreams)
	if stream != "" {
		t.Errorf("unknown stream accepted: %q", stream)
	}
	_, stream, _, _ = ParseDecision(`{"decision":"new","stream":"Marketing"}`, 0, testStreams)
	if stream != "marketing" {
		t.Errorf("stream should match case-insensitively by key or name: %q", stream)
	}
	// A stream is still read when the case choice is invalid.
	idx, stream, _, err = ParseDecision(`{"decision":"same","case":5,"stream":"bao_hanh"}`, 1, testStreams)
	if err == nil || idx != -1 || stream != "bao_hanh" {
		t.Errorf("invalid case: got %d %q %v", idx, stream, err)
	}
}

func TestBuildDecisionInputListsStreams(t *testing.T) {
	in := BuildDecisionInput(Report{Type: "product_defect", Title: "Nón bị nứt"}, nil, testStreams, time.UTC)
	for _, want := range []string{"<luong_xu_ly>", "bao_hanh: Bảo hành (hàng lỗi, đổi trả, bảo hành)", "marketing: Marketing"} {
		if !strings.Contains(in, want) {
			t.Errorf("input missing %q:\n%s", want, in)
		}
	}
	if strings.Contains(in, "g-warranty") {
		t.Errorf("input leaks alert group ids:\n%s", in)
	}
	if plain := BuildDecisionInput(Report{Title: "x"}, []models.CustomerCase{}, nil, time.UTC); strings.Contains(plain, "<luong_xu_ly>") {
		t.Errorf("no streams block expected:\n%s", plain)
	}
}

func TestStreamAlertGroup(t *testing.T) {
	if g, name := streamAlert(testStreams, "bao_hanh", ""); g != "g-warranty" || name != "Bảo hành" {
		t.Errorf("bao_hanh: %q %q", g, name)
	}
	if g, name := streamAlert(testStreams, "marketing", ""); g != "" || name != "Marketing" {
		t.Errorf("marketing has no group of its own: %q %q", g, name)
	}
	if g, name := streamAlert(testStreams, "gone", "Luồng cũ"); g != "" || name != "Luồng cũ" {
		t.Errorf("removed stream keeps the stored name: %q %q", g, name)
	}
}
