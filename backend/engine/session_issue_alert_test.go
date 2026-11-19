package engine

import (
	"strings"
	"testing"
	"time"
)

func TestBuildSessionIssueAlertMessage(t *testing.T) {
	at := time.Date(2026, 11, 18, 9, 30, 0, 0, time.UTC)
	got := BuildSessionIssueAlertMessage(SessionIssueAlert{
		CustomerName: "Anh Bình", CustomerCode: "KH01", ChatGroup: "Cửa hàng X",
		Summary: "Khách báo giao thiếu 2 thùng đơn hôm qua.",
		Issues: []SessionIssue{
			{Type: "delivery", Severity: "high", Detail: "Giao thiếu 2 thùng", Evidence: "thiếu 2 thùng rồi em"},
			{Type: "weird", Severity: "high", Detail: "Khác"},
		},
	}, at)
	want := "⚠️ Phiên chatbot có vấn đề cần xử lý\n" +
		"Khách: Anh Bình\nMã KH: KH01\nNhóm chat: Cửa hàng X\n" +
		"Tóm tắt: Khách báo giao thiếu 2 thùng đơn hôm qua.\n" +
		"• [Giao hàng] Giao thiếu 2 thùng (khách: \"thiếu 2 thùng rồi em\")\n" +
		"• [Khác] Khác\n" +
		"Thời điểm: 09:30 18/11/2026"
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
	if !strings.Contains(BuildSessionIssueAlertMessage(SessionIssueAlert{}, at), "Khách: (chưa xác minh)") {
		t.Fatal("missing name fallback")
	}
}
