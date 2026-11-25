package cloudifyorders

import (
	"errors"
	"testing"

	"github.com/vietbui/chat-quality-agent/db/models"
)

func TestTrackedOrderDetailsMatchesOrderAndCustomer(t *testing.T) {
	tracked := models.CloudifyOrderConfirmation{OrderID: 16, OrderCode: "ĐH000016", CustomerCode: "S084"}
	valid := map[string]any{
		"id": float64(16), "MA_HOA_DON": "ĐH000016", "MA_KHACH_HANG": []any{395, "S084 - Chợ Bảo Hộ"},
		"TRANG_THAI": "Đang giao", "GHI_CHU": "Giao buổi sáng", "GIAM_GIA_HOA_DON": float64(10000),
		"DON_DAT_HANG_CHI_TIET": []any{map[string]any{
			"MA_HANG": []any{834, "AQ12-7.5 - Bình ắc quy"}, "TEN_HANG": "Bình ắc quy 12V-7.5Ah",
			"DVT": []any{39, "Bình"}, "SO_LUONG": "6", "DON_GIA": float64(485000),
		}},
	}
	wrong := []map[string]any{
		{"id": float64(16), "MA_HOA_DON": "ĐH000016", "MA_KHACH_HANG": []any{396, "S085 - Đại lý khác"}},
		{"id": float64(17), "MA_HOA_DON": "ĐH000016", "MA_KHACH_HANG": "S084"},
		{"id": float64(16), "MA_HOA_DON": "ĐH000017", "MA_KHACH_HANG": "S084"},
	}
	if _, err := trackedOrderDetails(wrong, tracked); !errors.Is(err, ErrTrackedOrderNotFound) {
		t.Fatalf("wrong order or customer must yield not found, got %v", err)
	}
	got, err := trackedOrderDetails(append(wrong, valid), tracked)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "4" || got.StatusName != "Đang giao" || got.Total != 2900000 || got.Discount != 10000 || got.Note != "Giao buổi sáng" || len(got.Lines) != 1 {
		t.Fatalf("unexpected details %#v", got)
	}
	wantLine := TrackedOrderLine{SKU: "AQ12-7.5", Name: "Bình ắc quy 12V-7.5Ah", Unit: "Bình", Quantity: 6, UnitPrice: 485000, Total: 2910000}
	if got.Lines[0] != wantLine {
		t.Fatalf("line = %#v, want %#v", got.Lines[0], wantLine)
	}
	if _, err := trackedOrderDetails([]map[string]any{valid, valid}, tracked); err == nil {
		t.Fatal("duplicate exact Cloudify matches must be rejected")
	}
	valid["GHI_CHU"] = false
	valid["DON_DAT_HANG_CHI_TIET"] = []any{}
	got, err = trackedOrderDetails([]map[string]any{valid}, tracked)
	if err != nil || got.Note != "" || got.Lines == nil || len(got.Lines) != 0 {
		t.Fatalf("empty optional details got %#v, err=%v", got, err)
	}
}
