package workers

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/vietbui/chat-quality-agent/chatmemory"
)

func TestUnansweredHandoverAction(t *testing.T) {
	for _, tc := range []struct {
		name, text string
		want       bool
	}{
		{"exact action", "\n" + unansweredHandoverAction + " ", true},
		{"legacy exhausted knowledge retrieval", "No relevant supporting sources were found for that request.", true},
		{"clarifying question", "Anh/chị cho em tên mẫu cần kiểm tra nhé?", false},
		{"verified empty orders", "Hôm nay anh/chị không có đơn đang giao.", false},
		{"verified zero stock", "Mẫu này hiện không còn tồn khả dụng.", false},
		{"ERP picker", "[RICH_MESSAGE_SENT]", false},
		{"empty model response", " ", false},
		{"action mentioned in prose", "Dạ " + unansweredHandoverAction, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := isUnansweredHandoverAction(tc.text); got != tc.want {
				t.Fatalf("action = %t, want %t", got, tc.want)
			}
		})
	}
}

func TestUnansweredCustomerReplyRequiresConfirmedDelivery(t *testing.T) {
	if answer := unansweredCustomerReply(false, false); strings.Contains(answer, "đã chuyển") {
		t.Fatalf("failure reply promised handover: %q", answer)
	}
	if answer := unansweredCustomerReply(true, true); !strings.Contains(answer, "đã chuyển câu hỏi") {
		t.Fatalf("success reply did not acknowledge handover: %q", answer)
	}
}

func TestUnansweredCustomerReplyDistinguishesNoStaffFromFailedDelivery(t *testing.T) {
	if noStaff, unavailable := unansweredCustomerReply(false, false), unansweredCustomerReply(false, true); noStaff == unavailable ||
		!strings.Contains(noStaff, "chưa chuyển được") || strings.Contains(unavailable, "đã chuyển") {
		t.Fatalf("unexpected failure responses: %q, %q", noStaff, unavailable)
	}
}

func TestCurrentOrderFollowupUsesSenderOrderContext(t *testing.T) {
	recent := []chatmemory.RecentTurn{
		{Speaker: "Khách", Text: "Mình có đang có đơn hàng nào sắp giao không?"},
		{Speaker: "Bot", Text: "Chọn khoảng thời gian cần tra."},
		{Speaker: "Khách", Text: "3 ngày gần đây"},
		{Speaker: "Bot", Text: "Anh/chị không có đơn hàng nào trong 3 ngày gần đây."},
	}
	const followup = "Luôn mặc định trả lời kết quả của hiện tại"
	if !isCurrentOrderFollowup(followup, recent) || isCurrentOrderFollowup(followup, nil) {
		t.Fatal("the preference needs the sender's recent order question")
	}
	if isCurrentOrderFollowup(followup, []chatmemory.RecentTurn{{Speaker: "Bot", Text: "Có đơn hàng nào?"}}) {
		t.Fatal("a bot-only mention must not establish the sender's order context")
	}
	if got, err := classifyMessageIntent(context.Background(), "", followup, func() []chatmemory.RecentTurn { return recent }, true); err != nil || got != "IN_SCOPE" {
		t.Fatalf("order followup got %q, %v", got, err)
	}
	if isCurrentOrderFollowup("Luôn mặc định xem giá Bitcoin hiện tại", recent) ||
		isExplicitERPLookup("Giá Bitcoin hiện tại bao nhiêu?") ||
		parseMessageIntent("{\"label\":\"OUT_OF_SCOPE\"}", "giá Bitcoin") != "OUT_OF_SCOPE" {
		t.Fatal("an unrelated pricing question must remain eligible for OUT_OF_SCOPE")
	}
}

func TestCurrentOrderDeliveryTurn(t *testing.T) {
	const original = "Mình có đang có đơn hàng nào sắp giao không?"
	prior := func() []chatmemory.RecentTurn { return []chatmemory.RecentTurn{{Speaker: "Khách", Text: original}} }
	noHistory := func() []chatmemory.RecentTurn { return nil }
	for _, tc := range []struct {
		text    string
		history func() []chatmemory.RecentTurn
		want    bool
	}{
		{original, noHistory, true},
		{"Mình có đơn hàng nào sắp giao không em?", noHistory, true},
		{"Đơn nào đang giao ạ?", noHistory, true},
		{"Luôn mặc định trả lời kết quả của hiện tại", prior, true},
		{"Luôn mặc định trả lời kết quả của hiện tại", noHistory, false},
		{"Đơn hàng nào sắp giao trong 5 ngày vừa rồi?", prior, false},
		{"Đơn hàng DH260001 khi nào giao?", prior, false},
		{"Đơn nào sắp giao và cho mình xem công nợ", prior, false},
		{"Giá Bitcoin hiện tại?", prior, false},
	} {
		t.Run(tc.text, func(t *testing.T) {
			if got := isCurrentOrderDeliveryTurn(tc.text, tc.history); got != tc.want {
				t.Fatalf("current order lookup = %t, want %t", got, tc.want)
			}
		})
	}
}

func TestUnansweredHandoverUsesExistingChannelGroup(t *testing.T) {
	for _, tc := range []struct {
		metadata  string
		wantGroup string
	}{
		{`{"handover_alert_group_id":"management","unanswered_handover_enabled":true}`, "management"},
		{`{"campaign_alerts_enabled":true,"campaign_alert_group_id":"campaign","unanswered_handover_enabled":true}`, ""},
	} {
		var meta ChannelMetadata
		if err := json.Unmarshal([]byte(tc.metadata), &meta); err != nil {
			t.Fatal(err)
		}
		if meta.HandoverAlertGroupID != tc.wantGroup || !meta.UnansweredHandoverEnabled {
			t.Fatalf("got group %q, rollout=%t, want group %q and rollout enabled", meta.HandoverAlertGroupID,
				meta.UnansweredHandoverEnabled, tc.wantGroup)
		}
	}
}
