package engine

import (
	"strings"
	"testing"
	"time"
)

func TestBuildCaseAlertMessage(t *testing.T) {
	at := time.Date(2026, 11, 23, 21, 23, 0, 0, time.UTC)
	created := BuildCaseAlertMessage(CaseAlert{Kind: "created", Severity: "high", CustomerName: "Shop A", CustomerCode: "KH01",
		ChatGroup: "BBI x Shop A", ReporterName: "Hậu", TypeLabel: "Thanh toán, công nợ", Title: "Đơn DH0565913-3 không tạo được chứng từ",
		Detail: "Ảnh: thông báo đã xử lý bởi chứng từ khác", Evidence: "đang bị gì ạ", ImageCount: 1}, at)
	for _, want := range []string{"🚨 Khiếu nại mới", "Khách: Shop A", "Mã KH: KH01", "Nhóm chat: BBI x Shop A", "Người báo: Hậu", "[Thanh toán, công nợ] Đơn DH0565913-3", "Ảnh khách gửi: 1 ảnh", "21:23 23/11/2026"} {
		if !strings.Contains(created, want) {
			t.Errorf("created alert missing %q:\n%s", want, created)
		}
	}
	if strings.Contains(strings.ToLower(created), "ticket") || strings.Contains(created, "#") {
		t.Errorf("alert shows a ticket number:\n%s", created)
	}
	follow := BuildCaseAlertMessage(CaseAlert{Kind: "followup", FollowupCount: 3, CustomerName: "Anh Bình", ZaloUserID: "zu", Title: "Kính nứt", StatusLabel: "Đang xử lý"}, at)
	if !strings.Contains(follow, "lần 3") || !strings.Contains(follow, "Trạng thái: Đang xử lý") || !strings.Contains(follow, "Zalo ID: zu") {
		t.Errorf("follow-up alert:\n%s", follow)
	}
	reopened := BuildCaseAlertMessage(CaseAlert{Kind: "reopened", Title: "Kính nứt", FromSummary: true}, at)
	if !strings.Contains(reopened, "vẫn chưa ổn") || !strings.Contains(reopened, "tóm tắt phiên") {
		t.Errorf("reopened alert:\n%s", reopened)
	}
}
