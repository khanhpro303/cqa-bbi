package engine

import (
	"strings"
	"testing"
	"time"
)

// TestFriendlyRunError covers the sentinel→Vietnamese mapping and the image
// partial-failure parse, with raw errors passing through unchanged.
func TestFriendlyRunError(t *testing.T) {
	tests := []struct {
		name string
		code string
		want string
	}{
		{"group not found", "group_not_found", "Nhóm không tồn tại"},
		{"group has no zalo group", "group_has_no_zalo_group", "Nhóm chưa liên kết nhóm Zalo"},
		{"channel inactive", "channel_inactive", "Kênh Zalo OA đang tắt"},
		{"image partial failure", "text sent but 2/3 image(s) failed: image 1/3: boom", "Gửi chữ OK nhưng 2/3 ảnh lỗi"},
		{"empty", "", "Lỗi không xác định"},
		{"raw passthrough", "zalo error code 213", "zalo error code 213"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := friendlyRunError(tt.code); got != tt.want {
				t.Errorf("friendlyRunError(%q) = %q, want %q", tt.code, got, tt.want)
			}
		})
	}
}

// TestBuildCampaignAlertMessage checks the aggregated message includes the
// campaign name, fail count, each group line, the fallback for a blank group
// name, and the timestamp.
func TestBuildCampaignAlertMessage(t *testing.T) {
	at := time.Date(2026, 6, 4, 9, 30, 0, 0, time.UTC)
	fails := []alertFail{
		{GroupName: "Nhóm A", Reason: "Nhóm chưa liên kết nhóm Zalo"},
		{GroupName: "", Reason: "Lỗi không xác định"},
	}
	msg := buildCampaignAlertMessage("Khuyến mãi T6", fails, at)

	for _, want := range []string{
		"Khuyến mãi T6",
		"2 nhóm gửi lỗi",
		"• Nhóm A: Nhóm chưa liên kết nhóm Zalo",
		"• (không rõ nhóm): Lỗi không xác định",
		"09:30 04/06/2026",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("message missing %q\n---\n%s", want, msg)
		}
	}
}

func TestBuildCampaignAlertMessageBlankCampaignName(t *testing.T) {
	msg := buildCampaignAlertMessage("  ", nil, time.Now())
	if !strings.Contains(msg, "(không tên)") {
		t.Errorf("expected blank campaign name fallback, got: %s", msg)
	}
	if !strings.Contains(msg, "0 nhóm gửi lỗi") {
		t.Errorf("expected zero count for empty fails, got: %s", msg)
	}
}

// TestParseCampaignAlertConfig covers enabled/disabled, missing keys, and
// malformed JSON (all of which must no-op to false/"").
func TestParseCampaignAlertConfig(t *testing.T) {
	tests := []struct {
		name        string
		metadata    string
		wantEnabled bool
		wantGroup   string
	}{
		{"empty", "", false, ""},
		{"malformed", "{not json", false, ""},
		{"enabled with group", `{"campaign_alerts_enabled":true,"campaign_alert_group_id":"g-1"}`, true, "g-1"},
		{"enabled trims group", `{"campaign_alerts_enabled":true,"campaign_alert_group_id":"  g-2 "}`, true, "g-2"},
		{"disabled", `{"campaign_alerts_enabled":false,"campaign_alert_group_id":"g-3"}`, false, "g-3"},
		{"other keys ignored", `{"sync_interval":5}`, false, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotEnabled, gotGroup := parseCampaignAlertConfig(tt.metadata)
			if gotEnabled != tt.wantEnabled || gotGroup != tt.wantGroup {
				t.Errorf("parseCampaignAlertConfig(%q) = (%v,%q), want (%v,%q)",
					tt.metadata, gotEnabled, gotGroup, tt.wantEnabled, tt.wantGroup)
			}
		})
	}
}
