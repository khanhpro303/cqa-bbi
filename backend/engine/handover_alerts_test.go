package engine

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/vietbui/chat-quality-agent/db/models"
)

func TestParseHandoverAlertGroupID(t *testing.T) {
	tests := []struct {
		name     string
		metadata string
		want     string
	}{
		{"empty", "", ""},
		{"malformed", "{", ""},
		{"explicit group", `{"handover_alert_group_id":" g-1 "}`, "g-1"},
		{"explicit wins over campaign", `{"handover_alert_group_id":"g-1","campaign_alerts_enabled":true,"campaign_alert_group_id":"g-2"}`, "g-1"},
		{"falls back to enabled campaign group", `{"campaign_alerts_enabled":true,"campaign_alert_group_id":"g-2"}`, "g-2"},
		{"blank explicit falls back", `{"handover_alert_group_id":"","campaign_alerts_enabled":true,"campaign_alert_group_id":"g-2"}`, "g-2"},
		{"disabled campaign group ignored", `{"campaign_alerts_enabled":false,"campaign_alert_group_id":"g-2"}`, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ParseHandoverAlertGroupID(tt.metadata); got != tt.want {
				t.Errorf("ParseHandoverAlertGroupID(%q) = %q, want %q", tt.metadata, got, tt.want)
			}
		})
	}
}

func TestBuildHandoverAlertMessage(t *testing.T) {
	at := time.Date(2026, 11, 18, 9, 5, 0, 0, time.UTC)
	msg := BuildHandoverAlertMessage(HandoverAlert{
		ZaloUserID:   "u-123",
		CustomerName: "Anh Minh",
		CustomerCode: "KH001",
		ChatGroup:    "Shop Minh",
		Message:      "  hàng giao sai màu, gọi lại cho tôi  ",
	}, at)
	for _, want := range []string{
		"Khách cần nhân viên hỗ trợ",
		"Khách: Anh Minh",
		"Mã KH: KH001",
		"Nhóm chat: Shop Minh",
		"Zalo ID: u-123",
		`Tin nhắn: "hàng giao sai màu, gọi lại cho tôi"`,
		"09:05 18/11/2026",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("message missing %q\n---\n%s", want, msg)
		}
	}
}

func TestBuildHandoverAlertMessageUnknownCustomerAndLongQuote(t *testing.T) {
	long := strings.Repeat("á", handoverAlertMaxQuote+50)
	msg := BuildHandoverAlertMessage(HandoverAlert{ZaloUserID: "u-1", Message: long}, time.Now())
	if !strings.Contains(msg, "Khách: (chưa xác minh)") {
		t.Errorf("expected unverified-customer fallback, got: %s", msg)
	}
	if strings.Contains(msg, "Mã KH:") || strings.Contains(msg, "Nhóm chat:") {
		t.Errorf("expected empty optional lines to be omitted, got: %s", msg)
	}
	if !strings.Contains(msg, strings.Repeat("á", handoverAlertMaxQuote)+"…") || strings.Contains(msg, strings.Repeat("á", handoverAlertMaxQuote+1)) {
		t.Errorf("expected quote truncated to %d runes", handoverAlertMaxQuote)
	}
}

type recordingGroupSender struct{ calls int }

func (r *recordingGroupSender) SendGroupMessage(context.Context, string, string) error {
	r.calls++
	return nil
}

// Without an alert group the worker must not promise the customer that staff
// were told, and nothing may be sent.
func TestNotifyHandoverWithoutGroupReportsNotNotified(t *testing.T) {
	sender := &recordingGroupSender{}
	ok := NotifyHandover(context.Background(), sender, HandoverAlert{
		TenantID:   "t-1",
		Channel:    models.Channel{ID: "c-1", Metadata: `{"campaign_alerts_enabled":false}`},
		ZaloUserID: "u-1",
		Message:    "cho gặp nhân viên",
	})
	if ok {
		t.Fatal("NotifyHandover returned true with no alert group configured")
	}
	if sender.calls != 0 {
		t.Fatalf("expected no group message, got %d", sender.calls)
	}
}
