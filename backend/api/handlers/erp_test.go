package handlers

import (
	"testing"
	"time"
)

func TestFilterProductsByGroupsUsesProductGroupBeforeBrand(t *testing.T) {
	products := []map[string]interface{}{
		{
			"MA":                 "SP001710",
			"TEN":                "Mũ Bảo Hiểm Fullface BULLDOG Torii - Solid Matt Black - M",
			"TEN_DONG_BO_WEB":    "Bulldog TORII",
			"NHAN_HIEU_NAME":     "BULLDOG",
			"LIST_TEN_NHOM_VTHH": "Nguyên Đầu",
			"DON_GIA_BAN":        1500000.0,
		},
		{
			"MA":                 "SP001710_DK",
			"TEN":                "Mũ Bảo Hiểm Fullface BULLDOG Torii - Solid Matt Black - M",
			"TEN_DONG_BO_WEB":    "Bulldog TORII",
			"NHAN_HIEU_NAME":     "",
			"LIST_TEN_NHOM_VTHH": "Nguyên Đầu",
			"DON_GIA_BAN":        0.0,
		},
		{
			"MA":                 "SAMPLE0219",
			"TEN":                "Sample Mũ Bảo Hiểm Fullface BULLDOG Torii II",
			"TEN_DONG_BO_WEB":    "SAMPLE",
			"NHAN_HIEU_NAME":     "BULLDOG",
			"LIST_TEN_NHOM_VTHH": "Sample",
			"DON_GIA_BAN":        0.0,
		},
	}

	filtered := filterProductsByGroups(products, []string{"Nguyên Đầu"})

	if len(filtered) != 2 {
		t.Fatalf("expected 2 TORII products in allowed group, got %d: %#v", len(filtered), filtered)
	}

	if filtered[0]["MA"] != "SP001710" {
		t.Errorf("expected first product SP001710, got %#v", filtered[0]["MA"])
	}
	if filtered[1]["MA"] != "SP001710_DK" {
		t.Errorf("expected second product SP001710_DK, got %#v", filtered[1]["MA"])
	}
}

func TestFilterProductsByGroupsAllowsAllWhenNoGroupsConfigured(t *testing.T) {
	products := []map[string]interface{}{
		{"MA": "SP001710", "LIST_TEN_NHOM_VTHH": "Nguyên Đầu"},
		{"MA": "SAMPLE0219", "LIST_TEN_NHOM_VTHH": "Sample"},
	}

	filtered := filterProductsByGroups(products, nil)

	if len(filtered) != len(products) {
		t.Fatalf("expected all products to pass without group restrictions, got %d", len(filtered))
	}
}

func TestIsGenericDebtSearch(t *testing.T) {
	tests := []struct {
		search   string
		expected bool
	}{
		{"", true},
		{"công nợ", true},
		{"cong no", true},
		{"xem công nợ", true},
		{"đơn hàng", false},
		{"SP001", false},
		{"công nợ tháng này", false},
	}

	for _, tc := range tests {
		result := isGenericDebtSearch(tc.search)
		if result != tc.expected {
			t.Errorf("isGenericDebtSearch(%q) = %v; expected %v", tc.search, result, tc.expected)
		}
	}
}

func TestParseDebtPeriodFromSearch(t *testing.T) {
	now := time.Now()

	tests := []struct {
		search        string
		expectedOk    bool
		expectTuNgay  string
		expectDenNgay string
	}{
		{
			search:        "công nợ tháng này",
			expectedOk:    true,
			expectTuNgay:  time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location()).Format("2006-01-02"),
			expectDenNgay: now.Format("2006-01-02"),
		},
		{
			search:        "thang truoc",
			expectedOk:    true,
			expectTuNgay:  time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location()).AddDate(0, -1, 0).Format("2006-01-02"),
			expectDenNgay: time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location()).AddDate(0, 0, -1).Format("2006-01-02"),
		},
		{
			search:        "quý này",
			expectedOk:    true,
			expectTuNgay:  time.Date(now.Year(), time.Month(((int(now.Month())-1)/3)*3+1), 1, 0, 0, 0, 0, now.Location()).Format("2006-01-02"),
			expectDenNgay: now.Format("2006-01-02"),
		},
		{
			search:     "some customer name",
			expectedOk: false,
		},
	}

	for _, tc := range tests {
		tuNgay, denNgay, ok := parseDebtPeriodFromSearch(tc.search)
		if ok != tc.expectedOk {
			t.Fatalf("parseDebtPeriodFromSearch(%q) ok = %v; expected %v", tc.search, ok, tc.expectedOk)
		}
		if ok {
			if tuNgay != tc.expectTuNgay {
				t.Errorf("parseDebtPeriodFromSearch(%q) tuNgay = %q; expected %q", tc.search, tuNgay, tc.expectTuNgay)
			}
			if denNgay != tc.expectDenNgay {
				t.Errorf("parseDebtPeriodFromSearch(%q) denNgay = %q; expected %q", tc.search, denNgay, tc.expectDenNgay)
			}
		}
	}
}
