package workers

import "testing"

func TestIsObviousBusinessQuery(t *testing.T) {
	yes := []string{"FF805 còn tồn bao nhiêu á", "ff805", "giá LS2 FF901 bao nhiêu", "SP000123", "công nợ tháng này", "đơn hàng của tôi"}
	no := []string{"ok", "dạ", "cảm ơn", "hello", "chào bull", ""}
	for _, s := range yes {
		if !isObviousBusinessQuery(s) {
			t.Errorf("expected business query: %q", s)
		}
	}
	for _, s := range no {
		if isObviousBusinessQuery(s) {
			t.Errorf("expected NOT business query: %q", s)
		}
	}
}
