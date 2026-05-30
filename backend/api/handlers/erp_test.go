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

func TestDominantMaCha(t *testing.T) {
	tests := []struct {
		name string
		rows []map[string]interface{}
		want string
	}{
		{
			name: "empty rows",
			rows: nil,
			want: "",
		},
		{
			name: "rows without ma_cha",
			rows: []map[string]interface{}{
				{"MA": "SKU1"},
				{"MA": "SKU2"},
			},
			want: "",
		},
		{
			name: "single ma_cha across variants",
			rows: []map[string]interface{}{
				{"MA": "FF901-RED-L", "MA_CHA": "FF901"},
				{"MA": "FF901-BLK-M", "MA_CHA": "FF901"},
			},
			want: "FF901",
		},
		{
			name: "dominant wins over minority",
			rows: []map[string]interface{}{
				{"MA": "FF901-RED-L", "MA_CHA": "FF901"},
				{"MA": "FF901-BLK-M", "MA_CHA": "FF901"},
				{"MA": "FF901-BLK-L", "MA_CHA": "FF901"},
				{"MA": "FF800-RED-L", "MA_CHA": "FF800"},
			},
			want: "FF901",
		},
		{
			name: "lowercase ma_cha key fallback",
			rows: []map[string]interface{}{
				{"ma": "FF700-RED-L", "ma_cha": "FF700"},
				{"ma": "FF700-BLK-M", "ma_cha": "FF700"},
			},
			want: "FF700",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := dominantMaCha(tc.rows); got != tc.want {
				t.Fatalf("dominantMaCha() = %q; want %q", got, tc.want)
			}
		})
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

func TestMapDebtItemForLLM(t *testing.T) {
	tests := []struct {
		name string
		in   map[string]interface{}
		want map[string]interface{}
	}{
		{
			name: "canonical fields present",
			in: map[string]interface{}{
				"MA_KHACH_HANG":              "EG05",
				"TEN_KHACH_HANG":             "EGO Store",
				"NO_SO_DU_DAU_KY":            1050000.0,
				"NO_SO_DU_CUOI_KY":           2000000.0,
				"NO_SO_DU_CUOI_KY_NGUYEN_TE": 85.5,
				"tu_ngay":                    "2026-05-10",
				"den_ngay":                   "2026-05-20",
			},
			want: map[string]interface{}{
				"MA_KHACH_HANG":              "EG05",
				"TEN_KHACH_HANG":             "EGO Store",
				"NO_SO_DU_DAU_KY":            1050000.0,
				"no_so_du_dau_ky":            1050000.0,
				"NO_SO_DU_CUOI_KY":           2000000.0,
				"no_so_du_cuoi_ky":           2000000.0,
				"NO_SO_DU_CUOI_KY_NGUYEN_TE": 85.5,
				"no_so_du_cuoi_ky_nguyen_te": 85.5,
				"tu_ngay":                    "2026-05-10",
				"den_ngay":                   "2026-05-20",
			},
		},
		{
			name: "legacy aliases fall back",
			in: map[string]interface{}{
				"ma_kh":  "BBI001",
				"ten_kh": "Khách lẻ",
				// Old ERP shape: NO_TRUOC = opening, NO_SAU = closing, no nguyên tệ.
				"NO_TRUOC": 500000.0,
				"NO_SAU":   750000.0,
			},
			want: map[string]interface{}{
				"MA_KHACH_HANG":              "BBI001",
				"TEN_KHACH_HANG":             "Khách lẻ",
				"NO_SO_DU_DAU_KY":            500000.0,
				"no_so_du_dau_ky":            500000.0,
				"NO_SO_DU_CUOI_KY":           750000.0,
				"no_so_du_cuoi_ky":           750000.0,
				"NO_SO_DU_CUOI_KY_NGUYEN_TE": 0.0,
				"no_so_du_cuoi_ky_nguyen_te": 0.0,
				"NO_TRUOC":                   500000.0,
				"NO_SAU":                     750000.0,
				"ma_kh":                      "BBI001",
				"ten_kh":                     "Khách lẻ",
			},
		},
		{
			name: "missing fields default to zero / empty",
			in: map[string]interface{}{
				"MA_KHACH_HANG": "X",
			},
			want: map[string]interface{}{
				"MA_KHACH_HANG":              "X",
				"TEN_KHACH_HANG":             "",
				"NO_SO_DU_DAU_KY":            0.0,
				"no_so_du_dau_ky":            0.0,
				"NO_SO_DU_CUOI_KY":           0.0,
				"no_so_du_cuoi_ky":           0.0,
				"NO_SO_DU_CUOI_KY_NGUYEN_TE": 0.0,
				"no_so_du_cuoi_ky_nguyen_te": 0.0,
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := mapDebtItemForLLM(tc.in)
			if len(got) != len(tc.want) {
				t.Fatalf("len(got) = %d, want %d\ngot: %#v\nwant: %#v", len(got), len(tc.want), got, tc.want)
			}
			for k, want := range tc.want {
				gotVal, ok := got[k]
				if !ok {
					t.Errorf("missing key %q (got %#v)", k, got)
					continue
				}
				if gotVal != want {
					t.Errorf("key %q: got %#v, want %#v", k, gotVal, want)
				}
			}
		})
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

func TestFilterVariantsByAttributes(t *testing.T) {
	variants := []map[string]interface{}{
		{"MA": "FF901-DB-L", "MA_CHA": "FF901", "THUOC_TINH_1": "Đen bóng", "THUOC_TINH_2": "L", "NHAN_HIEU_NAME": "FF"},
		{"MA": "FF901-DB-XL", "MA_CHA": "FF901", "THUOC_TINH_1": "Đen bóng", "THUOC_TINH_2": "XL", "NHAN_HIEU_NAME": "FF"},
		{"MA": "FF901-XANH-L", "MA_CHA": "FF901", "THUOC_TINH_1": "Xanh navy", "THUOC_TINH_2": "L", "NHAN_HIEU_NAME": "FF"},
		{"MA": "FF901-DB-M", "MA_CHA": "FF901", "THUOC_TINH_1": "Đen bóng", "THUOC_TINH_2": "M", "NHAN_HIEU_NAME": "Bulldog"},
	}

	tests := []struct {
		name     string
		color    string
		size     string
		brand    string
		wantMAs  []string
	}{
		{
			name:    "exact color and size returns single variant",
			color:   "đen bóng",
			size:    "L",
			wantMAs: []string{"FF901-DB-L"},
		},
		{
			name:    "partial color match across sizes",
			color:   "đen",
			wantMAs: []string{"FF901-DB-L", "FF901-DB-XL", "FF901-DB-M"},
		},
		{
			name:    "size only match",
			size:    "L",
			wantMAs: []string{"FF901-DB-L", "FF901-XANH-L"},
		},
		{
			name:    "brand narrows result",
			color:   "đen",
			brand:   "Bulldog",
			wantMAs: []string{"FF901-DB-M"},
		},
		{
			name:    "no match returns empty",
			color:   "trắng",
			wantMAs: []string{},
		},
		{
			name:    "no filters returns all",
			wantMAs: []string{"FF901-DB-L", "FF901-DB-XL", "FF901-XANH-L", "FF901-DB-M"},
		},
		{
			name:    "case insensitive size",
			size:    "xl",
			wantMAs: []string{"FF901-DB-XL"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := filterVariantsByAttributes(variants, tc.color, tc.size, tc.brand)
			gotMAs := make([]string, 0, len(got))
			for _, v := range got {
				gotMAs = append(gotMAs, v["MA"].(string))
			}
			if len(gotMAs) != len(tc.wantMAs) {
				t.Fatalf("got %d variants %v; want %d %v", len(gotMAs), gotMAs, len(tc.wantMAs), tc.wantMAs)
			}
			for i, want := range tc.wantMAs {
				if gotMAs[i] != want {
					t.Errorf("variant[%d] = %q; want %q", i, gotMAs[i], want)
				}
			}
		})
	}
}

func TestCollectAvailableAttributes(t *testing.T) {
	tests := []struct {
		name       string
		variants   []map[string]interface{}
		wantColors []string
		wantSizes  []string
		wantBrands []string
	}{
		{
			name: "dedupes and sorts attribute values",
			variants: []map[string]interface{}{
				{"THUOC_TINH_1": "Đen bóng", "THUOC_TINH_2": "L", "NHAN_HIEU_NAME": "FF"},
				{"THUOC_TINH_1": "Đen bóng", "THUOC_TINH_2": "XL", "NHAN_HIEU_NAME": "FF"},
				{"THUOC_TINH_1": "Xanh navy", "THUOC_TINH_2": "L", "NHAN_HIEU_NAME": "Bulldog"},
				{"THUOC_TINH_1": "Trắng", "THUOC_TINH_2": "M", "NHAN_HIEU_NAME": "FF"},
			},
			wantColors: []string{"Trắng", "Xanh navy", "Đen bóng"},
			wantSizes:  []string{"L", "M", "XL"},
			wantBrands: []string{"Bulldog", "FF"},
		},
		{
			name: "skips empty attribute values",
			variants: []map[string]interface{}{
				{"THUOC_TINH_1": "", "THUOC_TINH_2": "L", "NHAN_HIEU_NAME": ""},
				{"THUOC_TINH_1": "Đen bóng", "THUOC_TINH_2": "", "NHAN_HIEU_NAME": "FF"},
			},
			wantColors: []string{"Đen bóng"},
			wantSizes:  []string{"L"},
			wantBrands: []string{"FF"},
		},
		{
			name:       "empty input returns empty slices",
			variants:   nil,
			wantColors: []string{},
			wantSizes:  []string{},
			wantBrands: []string{},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gotColors, gotSizes, gotBrands := collectAvailableAttributes(tc.variants)
			if !equalStringSlices(gotColors, tc.wantColors) {
				t.Errorf("colors = %v; want %v", gotColors, tc.wantColors)
			}
			if !equalStringSlices(gotSizes, tc.wantSizes) {
				t.Errorf("sizes = %v; want %v", gotSizes, tc.wantSizes)
			}
			if !equalStringSlices(gotBrands, tc.wantBrands) {
				t.Errorf("brands = %v; want %v", gotBrands, tc.wantBrands)
			}
		})
	}
}

func TestSlimVariantsForLLM(t *testing.T) {
	variants := []map[string]interface{}{
		{
			"MA":             "FF901-DB-L",
			"TEN":            "Áo thun FF901",
			"THUOC_TINH_1":   "Đen bóng",
			"THUOC_TINH_2":   "L",
			"DON_GIA_BAN":    250000.0,
			"NHAN_HIEU_NAME": "FF",
			"DVT":            "cái",
			"LINK_ANH":       "https://cdn.example/ff901.jpg",
		},
	}
	got := slimVariantsForLLM(variants)
	if len(got) != 1 {
		t.Fatalf("expected 1 slim entry, got %d", len(got))
	}
	row := got[0]
	if row["ma"] != "FF901-DB-L" {
		t.Errorf("ma = %v; want FF901-DB-L", row["ma"])
	}
	if row["color"] != "Đen bóng" {
		t.Errorf("color = %v; want Đen bóng", row["color"])
	}
	if row["size"] != "L" {
		t.Errorf("size = %v; want L", row["size"])
	}
	if row["price"] != 250000.0 {
		t.Errorf("price = %v; want 250000", row["price"])
	}
}

func TestSlimVariantsForLLMRespectsCap(t *testing.T) {
	variants := make([]map[string]interface{}, 0, 15)
	for i := 0; i < 15; i++ {
		variants = append(variants, map[string]interface{}{
			"MA":          "SKU",
			"DON_GIA_BAN": 1.0,
		})
	}
	got := slimVariantsForLLM(variants)
	if len(got) != 10 {
		t.Errorf("expected cap at 10, got %d", len(got))
	}
}

func TestParseAttributeLine(t *testing.T) {
	body := "COLOR: Gloss Black\nSIZE: L\nBRAND: NONE\n"
	tests := []struct {
		tag  string
		want string
	}{
		{"COLOR", "Gloss Black"},
		{"SIZE", "L"},
		{"BRAND", "NONE"},
		{"MISSING", ""},
	}
	for _, tc := range tests {
		t.Run(tc.tag, func(t *testing.T) {
			got := parseAttributeLine(body, tc.tag)
			if got != tc.want {
				t.Errorf("parseAttributeLine(%q) = %q; want %q", tc.tag, got, tc.want)
			}
		})
	}
}

func TestParseAttributeLineTolerantToFormatting(t *testing.T) {
	tests := []struct {
		name string
		body string
		tag  string
		want string
	}{
		{
			name: "leading whitespace and lowercase tag",
			body: "  color: Matt White\n",
			tag:  "COLOR",
			want: "Matt White",
		},
		{
			name: "value wrapped in backticks",
			body: "COLOR: `Gloss Black`",
			tag:  "COLOR",
			want: "Gloss Black",
		},
		{
			name: "value wrapped in quotes",
			body: "BRAND: \"Bulldog\"",
			tag:  "BRAND",
			want: "Bulldog",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := parseAttributeLine(tc.body, tc.tag)
			if got != tc.want {
				t.Errorf("got %q; want %q", got, tc.want)
			}
		})
	}
}

func TestPickValidatedAttribute(t *testing.T) {
	allowed := []string{"Gloss Black", "Matt White", "Carbon"}
	tests := []struct {
		name      string
		candidate string
		want      string
	}{
		{"exact match returns canonical value", "Gloss Black", "Gloss Black"},
		{"case-insensitive match returns canonical case", "gloss black", "Gloss Black"},
		{"NONE returns empty", "NONE", ""},
		{"empty returns empty", "", ""},
		{"hallucinated value not in allowed list returns empty", "Glossy Red", ""},
		{"whitespace trimmed before validation", "  Carbon  ", "Carbon"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := pickValidatedAttribute(tc.candidate, allowed)
			if got != tc.want {
				t.Errorf("pickValidatedAttribute(%q) = %q; want %q", tc.candidate, got, tc.want)
			}
		})
	}
}

func TestJoinForPrompt(t *testing.T) {
	if got := joinForPrompt(nil); got != "(không có giá trị nào)" {
		t.Errorf("nil input: got %q", got)
	}
	if got := joinForPrompt([]string{"A", "B", "C"}); got != "A | B | C" {
		t.Errorf("populated input: got %q", got)
	}
}

func equalStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestFetchInventoryStockForSKU_CacheHitSkipsClient verifies that the helper
// returns the cached stock value WITHOUT touching the Cloudify client when a
// cache entry exists. Passing a nil client would panic if the helper tried
// to reach upstream — the test relies on the early return inside the helper.
func TestFetchInventoryStockForSKU_CacheHitSkipsClient(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	cache := engine.NewInventoryStockCache(time.Minute)
	cache.Set(ctx, "tenant-1", "SKU-CACHED-1", 17)

	got, err := fetchInventoryStockForSKU(ctx, nil, cache, "tenant-1", "SKU-CACHED-1", "inventory_receipt/search", false)
	if err != nil {
		t.Fatalf("unexpected error on cache hit: %v", err)
	}
	if got != 17 {
		t.Fatalf("expected 17, got %v", got)
	}
}

// TestFetchInventoryStockForSKU_DisabledCacheRequiresClient verifies that when
// the cache is disabled (zero TTL) and no entry exists, the helper attempts a
// live call — with a nil client this manifests as a runtime panic, so we use
// a recover guard to confirm the early-return path is the cache-hit-only one.
func TestFetchInventoryStockForSKU_DisabledCacheRequiresClient(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	disabled := engine.NewInventoryStockCache(0)

	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected the helper to attempt a client call with disabled cache + no entry")
		}
	}()

	_, _ = fetchInventoryStockForSKU(ctx, nil, disabled, "tenant-1", "SKU-NOPE", "inventory_receipt/search", false)
}

func TestOrderStatusDisplayName(t *testing.T) {
	cases := map[string]string{
		"0":  "Hủy",
		"1":  "Đang thực hiện",
		"2":  "Hoàn thành",
		"3":  "Đang giao",
		"":   "Không xác định",
		"7":  "Khác (mã 7)",
		"99": "Khác (mã 99)",
	}
	for code, want := range cases {
		if got := orderStatusDisplayName(code); got != want {
			t.Errorf("orderStatusDisplayName(%q) = %q; want %q", code, got, want)
		}
	}
}

func TestSumOrderLineQuantity(t *testing.T) {
	tests := []struct {
		name string
		item map[string]interface{}
		want float64
	}{
		{
			name: "missing field returns zero",
			item: map[string]interface{}{"trang_thai": "1"},
			want: 0,
		},
		{
			name: "nil field returns zero",
			item: map[string]interface{}{"don_dat_hang_chi_tiet": nil},
			want: 0,
		},
		{
			name: "wrong type returns zero",
			item: map[string]interface{}{"don_dat_hang_chi_tiet": "not a slice"},
			want: 0,
		},
		{
			name: "sums SO_LUONG across lines (lowercase key)",
			item: map[string]interface{}{
				"don_dat_hang_chi_tiet": []interface{}{
					map[string]interface{}{"SO_LUONG": 2.0},
					map[string]interface{}{"SO_LUONG": 3.5},
					map[string]interface{}{"so_luong": 1.0},
				},
			},
			want: 6.5,
		},
		{
			name: "uppercase ERP key also works",
			item: map[string]interface{}{
				"DON_DAT_HANG_CHI_TIET": []interface{}{
					map[string]interface{}{"SO_LUONG": 4.0},
				},
			},
			want: 4.0,
		},
		{
			name: "non-map line items are skipped",
			item: map[string]interface{}{
				"don_dat_hang_chi_tiet": []interface{}{
					"junk",
					map[string]interface{}{"SO_LUONG": 2.0},
				},
			},
			want: 2.0,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := sumOrderLineQuantity(tc.item); got != tc.want {
				t.Errorf("sumOrderLineQuantity() = %v; want %v", got, tc.want)
			}
		})
	}
}

func TestBuildOrdersSummary(t *testing.T) {
	tests := []struct {
		name              string
		items             []map[string]interface{}
		wantTotalOrders   int
		wantTotalValue    float64
		wantTotalQuantity float64
		wantBuckets       []OrdersStatusBucket // expected order matches actual
	}{
		{
			name:              "empty input returns zero summary",
			items:             nil,
			wantTotalOrders:   0,
			wantTotalValue:    0,
			wantTotalQuantity: 0,
			wantBuckets:       nil,
		},
		{
			name: "single status bucket",
			items: []map[string]interface{}{
				{"trang_thai": "1", "total": 100000.0, "don_dat_hang_chi_tiet": []interface{}{
					map[string]interface{}{"SO_LUONG": 2.0},
				}},
			},
			wantTotalOrders:   1,
			wantTotalValue:    100000.0,
			wantTotalQuantity: 2.0,
			wantBuckets: []OrdersStatusBucket{
				{Status: "1", StatusName: "Đang thực hiện", Count: 1, Quantity: 2.0, Value: 100000.0},
			},
		},
		{
			name: "all four statuses ordered canonically (3,1,2,0)",
			items: []map[string]interface{}{
				{"trang_thai": "0", "total": 50000.0, "don_dat_hang_chi_tiet": []interface{}{
					map[string]interface{}{"SO_LUONG": 1.0},
				}},
				{"trang_thai": "1", "total": 200000.0, "don_dat_hang_chi_tiet": []interface{}{
					map[string]interface{}{"SO_LUONG": 2.0},
				}},
				{"trang_thai": "2", "total": 300000.0, "don_dat_hang_chi_tiet": []interface{}{
					map[string]interface{}{"SO_LUONG": 3.0},
				}},
				{"trang_thai": "3", "total": 400000.0, "don_dat_hang_chi_tiet": []interface{}{
					map[string]interface{}{"SO_LUONG": 4.0},
				}},
				{"trang_thai": "1", "total": 150000.0, "don_dat_hang_chi_tiet": []interface{}{
					map[string]interface{}{"SO_LUONG": 1.5},
				}},
			},
			wantTotalOrders:   5,
			wantTotalValue:    1100000.0,
			wantTotalQuantity: 11.5,
			wantBuckets: []OrdersStatusBucket{
				{Status: "3", StatusName: "Đang giao", Count: 1, Quantity: 4.0, Value: 400000.0},
				{Status: "1", StatusName: "Đang thực hiện", Count: 2, Quantity: 3.5, Value: 350000.0},
				{Status: "2", StatusName: "Hoàn thành", Count: 1, Quantity: 3.0, Value: 300000.0},
				{Status: "0", StatusName: "Hủy", Count: 1, Quantity: 1.0, Value: 50000.0},
			},
		},
		{
			name: "unknown status falls back to Khác bucket and sorts after canonical ones",
			items: []map[string]interface{}{
				{"trang_thai": "7", "total": 70000.0},
				{"trang_thai": "1", "total": 100000.0},
				{"trang_thai": "5", "total": 50000.0},
			},
			wantTotalOrders:   3,
			wantTotalValue:    220000.0,
			wantTotalQuantity: 0,
			wantBuckets: []OrdersStatusBucket{
				{Status: "1", StatusName: "Đang thực hiện", Count: 1, Quantity: 0, Value: 100000.0},
				{Status: "5", StatusName: "Khác (mã 5)", Count: 1, Quantity: 0, Value: 50000.0},
				{Status: "7", StatusName: "Khác (mã 7)", Count: 1, Quantity: 0, Value: 70000.0},
			},
		},
		{
			name: "missing line items leave quantity at zero, value still sums",
			items: []map[string]interface{}{
				{"TRANG_THAI": "2", "TONG_TIEN": 999000.0},
				{"TRANG_THAI": "2", "TONG_TIEN": 1000.0},
			},
			wantTotalOrders:   2,
			wantTotalValue:    1000000.0,
			wantTotalQuantity: 0,
			wantBuckets: []OrdersStatusBucket{
				{Status: "2", StatusName: "Hoàn thành", Count: 2, Quantity: 0, Value: 1000000.0},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := buildOrdersSummary(tc.items)
			if got.TotalOrders != tc.wantTotalOrders {
				t.Errorf("TotalOrders = %d; want %d", got.TotalOrders, tc.wantTotalOrders)
			}
			if got.TotalValue != tc.wantTotalValue {
				t.Errorf("TotalValue = %v; want %v", got.TotalValue, tc.wantTotalValue)
			}
			if got.TotalQuantity != tc.wantTotalQuantity {
				t.Errorf("TotalQuantity = %v; want %v", got.TotalQuantity, tc.wantTotalQuantity)
			}
			if len(got.ByStatus) != len(tc.wantBuckets) {
				t.Fatalf("ByStatus length = %d; want %d (got=%+v)", len(got.ByStatus), len(tc.wantBuckets), got.ByStatus)
			}
			for i, want := range tc.wantBuckets {
				gotBucket := got.ByStatus[i]
				if gotBucket != want {
					t.Errorf("ByStatus[%d] = %+v; want %+v", i, gotBucket, want)
				}
			}
		})
	}
}

func TestTrimOrdersForLLM(t *testing.T) {
	items := []map[string]interface{}{
		{"order_id": "OLD", "date": "2026-05-20"},
		{"order_id": "NEWEST", "date": "2026-05-27"},
		{"order_id": "MID", "date": "2026-05-24"},
		{"order_id": "OLDER", "date": "2026-05-22"},
	}

	t.Run("sorts by date desc when under cap", func(t *testing.T) {
		got := trimOrdersForLLM(items, 10)
		wantOrder := []string{"NEWEST", "MID", "OLDER", "OLD"}
		if len(got) != len(wantOrder) {
			t.Fatalf("length = %d; want %d", len(got), len(wantOrder))
		}
		for i, want := range wantOrder {
			if got[i]["order_id"] != want {
				t.Errorf("position %d = %v; want %v", i, got[i]["order_id"], want)
			}
		}
	})

	t.Run("caps to max keeping newest", func(t *testing.T) {
		got := trimOrdersForLLM(items, 2)
		if len(got) != 2 {
			t.Fatalf("length = %d; want 2", len(got))
		}
		if got[0]["order_id"] != "NEWEST" {
			t.Errorf("first = %v; want NEWEST", got[0]["order_id"])
		}
		if got[1]["order_id"] != "MID" {
			t.Errorf("second = %v; want MID", got[1]["order_id"])
		}
	})

	t.Run("items without parseable date go last", func(t *testing.T) {
		mixed := []map[string]interface{}{
			{"order_id": "NO_DATE"},
			{"order_id": "DATED", "date": "2026-05-25"},
		}
		got := trimOrdersForLLM(mixed, 10)
		if got[0]["order_id"] != "DATED" {
			t.Errorf("first = %v; want DATED", got[0]["order_id"])
		}
		if got[1]["order_id"] != "NO_DATE" {
			t.Errorf("second = %v; want NO_DATE", got[1]["order_id"])
		}
	})

	t.Run("empty input returns empty slice", func(t *testing.T) {
		got := trimOrdersForLLM(nil, 5)
		if len(got) != 0 {
			t.Errorf("expected empty, got %v", got)
		}
	})
}

func TestResolveGlobalMethodPermission(t *testing.T) {
	// orders allows POST only; debt allows GET only; products has a custom path.
	validConfig := `{
		"orders":   {"get": false, "post": true,  "path": ""},
		"debt":     {"get": true,  "post": false, "path": ""},
		"products": {"get": true,  "post": true,  "path": "danhmucvattuhanghoa/search"}
	}`

	tests := []struct {
		name        string
		config      string
		resource    string
		method      string
		wantSysRes  string
		wantAllowed bool
		wantErr     bool
	}{
		{
			name:        "system key match, GET allowed",
			config:      validConfig,
			resource:    "debt",
			method:      "GET",
			wantSysRes:  "debt",
			wantAllowed: true,
		},
		{
			name:        "system key match, POST not ticked",
			config:      validConfig,
			resource:    "debt",
			method:      "POST",
			wantSysRes:  "debt",
			wantAllowed: false,
		},
		{
			name:        "POST allowed for orders",
			config:      validConfig,
			resource:    "orders",
			method:      "POST",
			wantSysRes:  "orders",
			wantAllowed: true,
		},
		{
			name:        "custom path match re-routes to system key",
			config:      validConfig,
			resource:    "danhmucvattuhanghoa/search",
			method:      "GET",
			wantSysRes:  "products",
			wantAllowed: true,
		},
		{
			name:        "resource not in map is blocked, no error",
			config:      validConfig,
			resource:    "inventory",
			method:      "GET",
			wantSysRes:  "",
			wantAllowed: false,
		},
		{
			name:        "malformed JSON fails closed with error",
			config:      `{"orders": {"post": true`,
			resource:    "orders",
			method:      "POST",
			wantSysRes:  "",
			wantAllowed: false,
			wantErr:     true,
		},
		{
			name:        "unknown HTTP method is blocked",
			config:      validConfig,
			resource:    "orders",
			method:      "PUT",
			wantSysRes:  "orders",
			wantAllowed: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sysRes, allowed, err := resolveGlobalMethodPermission(tt.config, tt.resource, tt.method)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr = %v", err, tt.wantErr)
			}
			if sysRes != tt.wantSysRes {
				t.Errorf("systemResource = %q, want %q", sysRes, tt.wantSysRes)
			}
			if allowed != tt.wantAllowed {
				t.Errorf("allowed = %v, want %v", allowed, tt.wantAllowed)
			}
		})
	}
}

// TestMethodPermissionResource guards the alias that both the global HTTP-method
// gate and the scope check rely on. product_variants must resolve under
// products so a tenant's method whitelist (which lists products, not the
// finer-grained product_variants) does not block variant lookups — the bug that
// surfaced as "HTTP Method POST không được cho phép đối với tài nguyên
// 'product_variants'" after variant queries shipped.
func TestMethodPermissionResource(t *testing.T) {
	tests := []struct {
		resource string
		want     string
	}{
		{"product_variants", "products"},
		{"products", "products"},
		{"inventory", "inventory"},
		{"orders", "orders"},
		{"debt", "debt"},
		{"", ""},
	}
	for _, tt := range tests {
		if got := methodPermissionResource(tt.resource); got != tt.want {
			t.Errorf("methodPermissionResource(%q) = %q; want %q", tt.resource, got, tt.want)
		}
	}
}
