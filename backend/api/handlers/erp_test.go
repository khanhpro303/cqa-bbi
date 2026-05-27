package handlers

import (
	"context"
	"testing"
	"time"

	"github.com/vietbui/chat-quality-agent/engine"
)

func TestCalculateProductPriceRange(t *testing.T) {
	tests := []struct {
		name      string
		products  []map[string]interface{}
		wantMin   float64
		wantMax   float64
		wantLabel string
	}{
		{
			name: "multiple prices returns min max range",
			products: []map[string]interface{}{
				{"DON_GIA_BAN": 2900000.0},
				{"DON_GIA_BAN": 3490000.0},
			},
			wantMin:   2900000.0,
			wantMax:   3490000.0,
			wantLabel: "2.900.000đ - 3.490.000đ",
		},
		{
			name: "same prices returns single price",
			products: []map[string]interface{}{
				{"DON_GIA_BAN": 2900000.0},
				{"DON_GIA_BAN": 2900000.0},
			},
			wantMin:   2900000.0,
			wantMax:   2900000.0,
			wantLabel: "2.900.000đ",
		},
		{
			name: "zero prices are ignored",
			products: []map[string]interface{}{
				{"DON_GIA_BAN": 0.0},
				{"DON_GIA_BAN": 3490000.0},
			},
			wantMin:   3490000.0,
			wantMax:   3490000.0,
			wantLabel: "3.490.000đ",
		},
		{
			name: "all zero prices returns contact",
			products: []map[string]interface{}{
				{"DON_GIA_BAN": 0.0},
				{"DON_GIA_BAN": 0.0},
			},
			wantMin:   0,
			wantMax:   0,
			wantLabel: "Liên hệ",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := calculateProductPriceRange(tc.products)
			if got.Min != tc.wantMin {
				t.Errorf("Min = %v; want %v", got.Min, tc.wantMin)
			}
			if got.Max != tc.wantMax {
				t.Errorf("Max = %v; want %v", got.Max, tc.wantMax)
			}
			if got.Label != tc.wantLabel {
				t.Errorf("Label = %q; want %q", got.Label, tc.wantLabel)
			}
		})
	}
}

func TestEnrichProductsWithPriceRangesUsesVariantsByMaCha(t *testing.T) {
	products := []map[string]interface{}{
		{
			"MA":                 "SP001710",
			"MA_CHA":             "SP456836",
			"LIST_TEN_NHOM_VTHH": "Nguyên Đầu",
			"DON_GIA_BAN":        1500000.0,
		},
	}

	loadVariants := func(ctx context.Context, maCha string) ([]map[string]interface{}, error) {
		if maCha != "SP456836" {
			t.Fatalf("unexpected ma_cha lookup: %s", maCha)
		}
		return []map[string]interface{}{
			{"MA": "SP001710", "MA_CHA": maCha, "LIST_TEN_NHOM_VTHH": "Nguyên Đầu", "DON_GIA_BAN": 2900000.0},
			{"MA": "SP001711", "MA_CHA": maCha, "LIST_TEN_NHOM_VTHH": "Nguyên Đầu", "DON_GIA_BAN": 3490000.0},
			{"MA": "SP001712", "MA_CHA": maCha, "LIST_TEN_NHOM_VTHH": "Nguyên Đầu", "DON_GIA_BAN": 0.0},
			{"MA": "SAMPLE001", "MA_CHA": maCha, "LIST_TEN_NHOM_VTHH": "Sample", "DON_GIA_BAN": 990000.0},
		}, nil
	}

	enriched := enrichProductsWithPriceRanges(context.Background(), products, []string{"Nguyên Đầu"}, loadVariants)

	if len(enriched) != 1 {
		t.Fatalf("expected 1 enriched product, got %d", len(enriched))
	}
	if enriched[0]["price_range"] != "2.900.000đ - 3.490.000đ" {
		t.Errorf("price_range = %#v; want range from allowed variants", enriched[0]["price_range"])
	}
	if enriched[0]["price_min"] != 2900000.0 {
		t.Errorf("price_min = %#v; want 2900000", enriched[0]["price_min"])
	}
	if enriched[0]["price_max"] != 3490000.0 {
		t.Errorf("price_max = %#v; want 3490000", enriched[0]["price_max"])
	}
}

func TestFilterProductsByGroupsThenRankWebGroupsAppliesPermissionsBeforeGrouping(t *testing.T) {
	products := []map[string]interface{}{
		{"MA": "SP000-SKU", "MA_CHA": "SP000", "TEN_DONG_BO_WEB": "Sample Line", "LIST_TEN_NHOM_VTHH": "Sample"},
		{"MA": "SP200-SKU1", "MA_CHA": "SP200", "TEN_DONG_BO_WEB": "FF901", "LIST_TEN_NHOM_VTHH": "Nguyên Đầu"},
		{"MA": "SP200-SKU2", "MA_CHA": "SP200", "TEN_DONG_BO_WEB": "FF901", "LIST_TEN_NHOM_VTHH": "Nguyên Đầu"},
		{"MA": "SP300-SKU1", "MA_CHA": "SP300", "TEN_DONG_BO_WEB": "FF901", "LIST_TEN_NHOM_VTHH": "Nguyên Đầu"},
		{"MA": "SP100-SKU1", "MA_CHA": "SP100", "TEN_DONG_BO_WEB": "FF901 Carbon", "LIST_TEN_NHOM_VTHH": "Nguyên Đầu"},
	}

	filtered := filterProductsByGroups(products, []string{"Nguyên Đầu"})
	groups := engine.RankProductWebGroups(filtered)

	if len(groups) != 2 {
		t.Fatalf("expected 2 web groups after filtering, got %d: %#v", len(groups), groups)
	}
	if groups[0].WebName != "FF901" || groups[0].Count != 3 {
		t.Fatalf("top group = %#v; want FF901 count=3", groups[0])
	}
	wantParents := []string{"SP200", "SP300"}
	if len(groups[0].ParentCodes) != len(wantParents) {
		t.Fatalf("FF901 parent codes = %#v; want %#v", groups[0].ParentCodes, wantParents)
	}
	for i, want := range wantParents {
		if groups[0].ParentCodes[i] != want {
			t.Fatalf("FF901 parent[%d] = %q; want %q", i, groups[0].ParentCodes[i], want)
		}
	}
	if groups[1].WebName != "FF901 Carbon" || groups[1].Count != 1 {
		t.Fatalf("group[1] = %#v; want FF901 Carbon count=1", groups[1])
	}
	for _, g := range groups {
		if g.WebName == "Sample Line" {
			t.Fatalf("sample product group should be filtered out: %#v", groups)
		}
	}
}

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

func TestProductMatchesAllowedGroupsAcrossAllFields(t *testing.T) {
	tests := []struct {
		name          string
		product       map[string]interface{}
		allowedGroups []string
		want          bool
	}{
		{
			name: "matches by MA prefix",
			product: map[string]interface{}{
				"MA":  "SP458484-RED-M",
				"TEN": "Random product name",
			},
			allowedGroups: []string{"SP458484"},
			want:          true,
		},
		{
			name: "matches by MA_CHA prefix",
			product: map[string]interface{}{
				"MA":     "FF901-001",
				"MA_CHA": "SP458484",
				"TEN":    "Random",
			},
			allowedGroups: []string{"SP4584"},
			want:          true,
		},
		{
			name: "no match across all five fields",
			product: map[string]interface{}{
				"MA":             "ABC123",
				"MA_CHA":         "XYZ999",
				"TEN":            "Foo",
				"NHAN_HIEU_NAME": "Bar",
			},
			allowedGroups: []string{"helmet"},
			want:          false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := productMatchesAllowedGroups(tc.product, tc.allowedGroups)
			if got != tc.want {
				t.Errorf("productMatchesAllowedGroups() = %v; want %v", got, tc.want)
			}
		})
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

func TestSlimProductsForLLM(t *testing.T) {
	tests := []struct {
		name string
		in   []map[string]interface{}
		want []map[string]interface{}
	}{
		{
			name: "empty input returns empty slice",
			in:   nil,
			want: []map[string]interface{}{},
		},
		{
			name: "single product picks all expected fields",
			in: []map[string]interface{}{
				{
					"TEN_DONG_BO_WEB":    "LS2 FF901",
					"price_range":        "9.200.000đ - 9.900.000đ",
					"NHAN_HIEU_NAME":     "LS2",
					"LIST_TEN_NHOM_VTHH": "Lật Hàm",
					"DVT":                "Cái",
					"DON_GIA_BAN":        9900000,
					"LINK_ANH":           "https://example/image.jpg",
					"MA":                 "SP458484",
				},
			},
			want: []map[string]interface{}{
				{
					"name":               "LS2 FF901",
					"price_range":        "9.200.000đ - 9.900.000đ",
					"nhan_hieu_name":     "LS2",
					"list_ten_nhom_vthh": "Lật Hàm",
					"dvt":                "Cái",
				},
			},
		},
		{
			name: "variants with same name dedupe to one row",
			in: []map[string]interface{}{
				{"TEN_DONG_BO_WEB": "LS2 FF901", "price_range": "9.200.000đ - 9.900.000đ", "NHAN_HIEU_NAME": "LS2"},
				{"TEN_DONG_BO_WEB": "LS2 FF901", "price_range": "9.200.000đ - 9.900.000đ", "NHAN_HIEU_NAME": "LS2"},
				{"TEN_DONG_BO_WEB": "LS2 FF901 Carbon", "price_range": "12.000.000đ - 13.500.000đ", "NHAN_HIEU_NAME": "LS2"},
			},
			want: []map[string]interface{}{
				{"name": "LS2 FF901", "price_range": "9.200.000đ - 9.900.000đ", "nhan_hieu_name": "LS2", "list_ten_nhom_vthh": "", "dvt": ""},
				{"name": "LS2 FF901 Carbon", "price_range": "12.000.000đ - 13.500.000đ", "nhan_hieu_name": "LS2", "list_ten_nhom_vthh": "", "dvt": ""},
			},
		},
		{
			name: "missing fields default to empty strings",
			in: []map[string]interface{}{
				{"TEN": "Generic helmet"},
			},
			want: []map[string]interface{}{
				{"name": "Generic helmet", "price_range": "", "nhan_hieu_name": "", "list_ten_nhom_vthh": "", "dvt": ""},
			},
		},
		{
			name: "product without resolvable name is skipped",
			in: []map[string]interface{}{
				{"TEN_DONG_BO_WEB": "", "ten_dong_bo_web": "", "TEN": "", "ten": "", "name": ""},
				{"TEN_DONG_BO_WEB": "LS2 FF901", "price_range": "x"},
			},
			want: []map[string]interface{}{
				{"name": "LS2 FF901", "price_range": "x", "nhan_hieu_name": "", "list_ten_nhom_vthh": "", "dvt": ""},
			},
		},
		{
			name: "caps result at slimProductsForLLMLimit",
			in: []map[string]interface{}{
				{"name": "p1", "price_range": "1"},
				{"name": "p2", "price_range": "2"},
				{"name": "p3", "price_range": "3"},
				{"name": "p4", "price_range": "4"},
				{"name": "p5", "price_range": "5"},
				{"name": "p6", "price_range": "6"},
				{"name": "p7", "price_range": "7"},
			},
			want: []map[string]interface{}{
				{"name": "p1", "price_range": "1", "nhan_hieu_name": "", "list_ten_nhom_vthh": "", "dvt": ""},
				{"name": "p2", "price_range": "2", "nhan_hieu_name": "", "list_ten_nhom_vthh": "", "dvt": ""},
				{"name": "p3", "price_range": "3", "nhan_hieu_name": "", "list_ten_nhom_vthh": "", "dvt": ""},
				{"name": "p4", "price_range": "4", "nhan_hieu_name": "", "list_ten_nhom_vthh": "", "dvt": ""},
				{"name": "p5", "price_range": "5", "nhan_hieu_name": "", "list_ten_nhom_vthh": "", "dvt": ""},
			},
		},
		{
			name: "falls back from TEN_DONG_BO_WEB to TEN when web name is empty",
			in: []map[string]interface{}{
				{"TEN_DONG_BO_WEB": "", "TEN": "Helmet X", "price_range": "5đ - 9đ"},
			},
			want: []map[string]interface{}{
				{"name": "Helmet X", "price_range": "5đ - 9đ", "nhan_hieu_name": "", "list_ten_nhom_vthh": "", "dvt": ""},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := slimProductsForLLM(tc.in)

			if len(got) != len(tc.want) {
				t.Fatalf("slimProductsForLLM length = %d; want %d (got=%v)", len(got), len(tc.want), got)
			}
			for i, w := range tc.want {
				if len(got[i]) != len(w) {
					t.Errorf("row %d field count = %d; want %d", i, len(got[i]), len(w))
				}
				for k, v := range w {
					if got[i][k] != v {
						t.Errorf("row %d key %q = %v; want %v", i, k, got[i][k], v)
					}
				}
			}
		})
	}
}
