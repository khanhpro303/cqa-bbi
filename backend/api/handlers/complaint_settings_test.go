package handlers

import "testing"

func TestNormalizeComplaintSettings(t *testing.T) {
	ok := map[string]interface{}{
		"complaint_resolved_days": float64(30),
		"complaint_streams": []interface{}{
			map[string]interface{}{"key": "bao_hanh", "name": " Bảo hành ", "description": "Hàng lỗi"},
		},
	}
	if key, err := normalizeComplaintSettings("t", ok); err != nil {
		t.Fatalf("valid settings rejected: %s %v", key, err)
	}
	if streams := ok["complaint_streams"]; streams == nil {
		t.Fatal("streams dropped")
	}
	for _, bad := range []map[string]interface{}{
		{"complaint_resolved_days": float64(0)},
		{"complaint_resolved_days": float64(91)},
		{"complaint_resolved_days": 2.5},
		{"complaint_resolved_days": "14"},
		{"complaint_streams": "bao_hanh"},
		{"complaint_streams": []interface{}{map[string]interface{}{"key": "x y", "name": "X"}}},
	} {
		if _, err := normalizeComplaintSettings("t", bad); err == nil {
			t.Errorf("accepted %v", bad)
		}
	}
	empty := map[string]interface{}{"complaint_streams": nil}
	if _, err := normalizeComplaintSettings("t", empty); err != nil {
		t.Fatal(err)
	}
	if s, ok := empty["complaint_streams"].([]interface{}); ok && len(s) != 0 {
		t.Errorf("nil streams should become an empty list, got %v", empty["complaint_streams"])
	}
}
