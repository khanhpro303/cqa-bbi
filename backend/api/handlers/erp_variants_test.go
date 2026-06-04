package handlers

import "testing"

func TestIsVariantPriceIntent(t *testing.T) {
	tests := []struct {
		name                       string
		intent, color, size, brand string
		want                       bool
	}{
		{
			// Turn-2 bug: agent sent exact_web_name=true with a colour pick. This
			// predicate must return true so the exact-web short-circuit is skipped
			// and the request flows into the pivot path instead of "không tìm thấy".
			name:   "price + colour pick → true (skip exact-web)",
			intent: "price", color: "Solid Carbon", size: "XL", want: true,
		},
		{
			// A real web-name pick carries no color/size → exact-web path is correct.
			name:   "price web-name pick, no attribute → false",
			intent: "price", want: false,
		},
		{
			// Stock web-name pick → exact-web/inventory path, never pivot.
			name:   "stock + attribute → false",
			intent: "stock", color: "đen", size: "XL", want: false,
		},
		{name: "case-insensitive price + size", intent: "PRICE", size: "L", want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isVariantPriceIntent(tt.intent, tt.color, tt.size, tt.brand); got != tt.want {
				t.Errorf("isVariantPriceIntent(%q,%q,%q,%q) = %v, want %v",
					tt.intent, tt.color, tt.size, tt.brand, got, tt.want)
			}
		})
	}
}

func TestShouldPivotToVariant(t *testing.T) {
	tests := []struct {
		name                               string
		intent, color, size, brand, parent string
		want                               bool
	}{
		{
			// The reported bug: "FF901 Carbon màu đen size XL đơn giá bán bao nhiêu".
			// Price intent + concrete color/size + a resolved parent line MUST pivot
			// to the variant resolver so the bot returns the exact DON_GIA_BAN, not
			// the family range "11.9tr–12.9tr".
			name:   "price + color + size + parent → pivot",
			intent: "price", color: "đen", size: "XL", parent: "SP458496", want: true,
		},
		{
			// Stock questions are already forced to product_variants → inventory by
			// the absence of stock in the products response; they must NOT pivot here.
			name:   "stock intent → no pivot",
			intent: "stock", color: "đen", size: "XL", parent: "SP458496", want: false,
		},
		{
			// Empty/omitted intent defaults to stock-safe: never show a price here.
			name:   "empty intent → no pivot",
			intent: "", color: "đen", size: "XL", parent: "SP458496", want: false,
		},
		{
			// Family price question ("FF901 giá bao nhiêu") names no variant → the
			// price_range IS the correct answer, so no pivot.
			name:   "price but no attribute → no pivot",
			intent: "price", parent: "SP458496", want: false,
		},
		{
			// products could not pin a single parent (disambiguation list or fuzzy
			// miss) → nothing to pivot into; fall through to the normal flow.
			name:   "price + attribute but no resolved parent → no pivot",
			intent: "price", color: "đen", size: "XL", parent: "", want: false,
		},
		{
			name:   "case-insensitive intent",
			intent: "PRICE", color: "đen", parent: "SP1", want: true,
		},
		{
			name:   "whitespace around intent and parent still pivots",
			intent: "  price  ", size: "L", parent: "  SP1  ", want: true,
		},
		{
			name:   "brand-only attribute with price intent pivots",
			intent: "price", brand: "LS2", parent: "SP1", want: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shouldPivotToVariant(tt.intent, tt.color, tt.size, tt.brand, tt.parent); got != tt.want {
				t.Errorf("shouldPivotToVariant(%q,%q,%q,%q,%q) = %v, want %v",
					tt.intent, tt.color, tt.size, tt.brand, tt.parent, got, tt.want)
			}
		})
	}
}

func TestParentCodeInLabel(t *testing.T) {
	tests := []struct {
		name       string
		parentCode string
		label      string
		want       bool
	}{
		{
			name:       "model code present in label",
			parentCode: "FF901",
			label:      "LS2 FF901 — Mũ Bảo Hiểm Lật Hàm 180 độ LS2 FF901 Advant X - Solid White - L",
			want:       true,
		},
		{
			name:       "case and space insensitive",
			parentCode: "ff 901",
			label:      "LS2 FF901 Advant X - Solid White - L",
			want:       true,
		},
		{
			name:       "dash insensitive",
			parentCode: "FF-901",
			label:      "LS2 FF901 Advant X",
			want:       true,
		},
		{
			name:       "model code absent",
			parentCode: "FF800",
			label:      "LS2 FF901 Advant X - Solid White - L",
			want:       false,
		},
		{
			name:       "empty parent code never matches",
			parentCode: "",
			label:      "anything",
			want:       false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := parentCodeInLabel(tt.parentCode, tt.label); got != tt.want {
				t.Errorf("parentCodeInLabel(%q, %q) = %v, want %v", tt.parentCode, tt.label, got, tt.want)
			}
		})
	}
}

func TestHasVariantAttribute(t *testing.T) {
	tests := []struct {
		name               string
		color, size, brand string
		want               bool
	}{
		{
			// Regression: the FF901 bug. Agent picked a line then called
			// product_variants with a hallucinated parent_code and NO color/size.
			// Resolution must be skipped so the backend returns empty instead of
			// fuzzy-resolving to a wrong accessory SKU.
			name: "no attribute - resolution must be skipped",
			want: false,
		},
		{
			name:  "whitespace-only attributes count as empty",
			color: "  ", size: "\t", brand: " ",
			want: false,
		},
		{name: "color present", color: "đỏ đen", want: true},
		{name: "size present", size: "L", want: true},
		{name: "brand present", brand: "LS2", want: true},
		{name: "all present", color: "trắng", size: "M", brand: "LS2", want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := hasVariantAttribute(tt.color, tt.size, tt.brand); got != tt.want {
				t.Errorf("hasVariantAttribute(%q,%q,%q) = %v, want %v",
					tt.color, tt.size, tt.brand, got, tt.want)
			}
		})
	}
}

func TestVariantBelongsToParent(t *testing.T) {
	tests := []struct {
		name            string
		parentCode      string
		effectiveParent string
		candidateMaCha  string
		candidateLabel  string
		want            bool
	}{
		{
			name:            "exact ma_cha match against resolved parent",
			parentCode:      "FF901",
			effectiveParent: "SP458484",
			candidateMaCha:  "sp458484", // case-insensitive
			candidateLabel:  "",
			want:            true,
		},
		{
			name:            "label carries model code when parent unresolved",
			parentCode:      "FF901",
			effectiveParent: "FF901", // not yet resolved to a real ma_cha
			candidateMaCha:  "SP458484",
			candidateLabel:  "LS2 FF901 Advant X - Solid White - L",
			want:            true,
		},
		{
			name:            "sibling parent leaks in - rejected",
			parentCode:      "FF901",
			effectiveParent: "SP458484",
			candidateMaCha:  "SP999999",
			candidateLabel:  "LS2 FF800 Different Model",
			want:            false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := variantBelongsToParent(tt.parentCode, tt.effectiveParent, tt.candidateMaCha, tt.candidateLabel)
			if got != tt.want {
				t.Errorf("variantBelongsToParent(%q,%q,%q,%q) = %v, want %v",
					tt.parentCode, tt.effectiveParent, tt.candidateMaCha, tt.candidateLabel, got, tt.want)
			}
		})
	}
}

// TestExactWebStockContinuation_SkipsRedundantPicker locks the fix for the
// inventory redundant-disambiguation bug: after the customer chose "🔍 Xem theo
// mã SKU cụ thể" for "LS2 FF901" and typed a variant ("Nardo Grey size XL"), the
// agent calls inventory with exact_web_name=true PLUS color/size. The exact-web
// branch must resolve that single SKU (so the handler returns its stock) instead
// of re-firing the dòng-vs-SKU picker. The decision pivots on
// hasVariantAttribute + filterVariantsByAttributes over the exact-web rows, so we
// assert that decision directly here (the handler's DB/Cloudify wiring is covered
// by integration, not unit, tests).
func TestExactWebStockContinuation_SkipsRedundantPicker(t *testing.T) {
	// Exact-web rows for "LS2 FF901": multiple SKUs share ten_dong_bo_web, which
	// is exactly why the old code's len(exactRows) > 1 check fired the picker.
	exactRows := []map[string]interface{}{
		{"MA": "SP458491", "MA_CHA": "SP458484", "TEN_DONG_BO_WEB": "LS2 FF901", "THUOC_TINH_1": "Trắng", "THUOC_TINH_2": "L"},
		{"MA": "SP458493", "MA_CHA": "SP458484", "TEN_DONG_BO_WEB": "LS2 FF901", "THUOC_TINH_1": "Nardo Grey", "THUOC_TINH_2": "XL"},
		{"MA": "SP458496", "MA_CHA": "SP458484", "TEN_DONG_BO_WEB": "LS2 FF901", "THUOC_TINH_1": "Trắng", "THUOC_TINH_2": "L"},
	}

	t.Run("color+size present resolves the single SKU, no picker", func(t *testing.T) {
		color, size := "Nardo Grey", "size XL"
		if !hasVariantAttribute(color, size, "") {
			t.Fatal("hasVariantAttribute must report true so the guard engages")
		}
		got := filterVariantsByAttributes(exactRows, color, size, "")
		if len(got) != 1 {
			t.Fatalf("expected exactly 1 matching SKU, got %d", len(got))
		}
		if sku := getMapString(got[0], "MA", "ma"); sku != "SP458493" {
			t.Errorf("resolved wrong SKU: got %q, want SP458493", sku)
		}
	})

	t.Run("bare line with no attributes keeps multiple rows so the picker fires", func(t *testing.T) {
		if hasVariantAttribute("", "", "") {
			t.Fatal("no attributes must report false so the guard is skipped")
		}
		// Without the guard the handler proceeds to the len>1 picker branch — the
		// correct behaviour for a bare "LS2 FF901" stock question.
		if len(exactRows) <= 1 {
			t.Fatal("fixture must have >1 row to exercise the picker path")
		}
	})
}

// TestGenericStockGuard_DistinctMaCha locks the Branch-2 (generic LIKE) guard
// that fixes "Shiba đen bóng size XXL tồn bao nhiêu?" → redundant dòng-vs-SKU
// picker. The generic inventory path now mirrors the exact-web guard: when the
// customer named color+size it filters the LIKE-matched rows by attribute and,
// IF they resolve to a single product line (distinctMaChaCount == 1), answers
// that SKU's stock directly instead of firing the picker. A broad LIKE that
// pulls sibling lines (distinctMaChaCount > 1) stays disambiguated.
func TestGenericStockGuard_DistinctMaCha(t *testing.T) {
	t.Run("color+size on one line resolves a single SKU and line", func(t *testing.T) {
		// LIKE "Shiba" → all sizes of one parent line (SP461294).
		rows := []map[string]interface{}{
			{"MA": "SP461290", "MA_CHA": "SP461294", "THUOC_TINH_1": "Đen bóng", "THUOC_TINH_2": "L"},
			{"MA": "SP461292", "MA_CHA": "SP461294", "THUOC_TINH_1": "Đen bóng", "THUOC_TINH_2": "XL"},
			{"MA": "SP461293", "MA_CHA": "SP461294", "THUOC_TINH_1": "Đen bóng", "THUOC_TINH_2": "XXL"},
		}
		got := filterVariantsByAttributes(rows, "đen bóng", "XXL", "")
		if len(got) != 1 {
			t.Fatalf("expected exactly 1 matching SKU, got %d", len(got))
		}
		if n := distinctMaChaCount(got); n != 1 {
			t.Fatalf("expected single line (ma_cha), got %d", n)
		}
		if sku := getMapString(got[0], "MA", "ma"); sku != "SP461293" {
			t.Errorf("resolved wrong SKU: got %q, want SP461293", sku)
		}
	})

	t.Run("color+size spanning sibling lines stays ambiguous", func(t *testing.T) {
		// A broad keyword that LIKE-matches two distinct lines, both carrying the
		// same color+size variant. The guard must NOT short-circuit here.
		rows := []map[string]interface{}{
			{"MA": "SP100001", "MA_CHA": "SP100000", "THUOC_TINH_1": "Đen bóng", "THUOC_TINH_2": "XXL"},
			{"MA": "SP200001", "MA_CHA": "SP200000", "THUOC_TINH_1": "Đen bóng", "THUOC_TINH_2": "XXL"},
		}
		got := filterVariantsByAttributes(rows, "đen bóng", "XXL", "")
		if len(got) != 2 {
			t.Fatalf("expected 2 matching SKUs across lines, got %d", len(got))
		}
		if n := distinctMaChaCount(got); n != 2 {
			t.Fatalf("expected 2 distinct lines so the picker is kept, got %d", n)
		}
	})

	t.Run("size mismatch matches nothing so the picker fires", func(t *testing.T) {
		rows := []map[string]interface{}{
			{"MA": "SP461290", "MA_CHA": "SP461294", "THUOC_TINH_1": "Đen bóng", "THUOC_TINH_2": "L"},
		}
		if got := filterVariantsByAttributes(rows, "đen bóng", "XXL", ""); len(got) != 0 {
			t.Fatalf("expected 0 matches for absent size, got %d", len(got))
		}
	})

	t.Run("distinctMaChaCount falls back to ma when ma_cha empty", func(t *testing.T) {
		rows := []map[string]interface{}{
			{"MA": "SP300001", "THUOC_TINH_1": "Đỏ", "THUOC_TINH_2": "M"},
			{"MA": "SP300001", "THUOC_TINH_1": "Đỏ", "THUOC_TINH_2": "M"},
		}
		if n := distinctMaChaCount(rows); n != 1 {
			t.Fatalf("expected 1 distinct key by ma fallback, got %d", n)
		}
	})
}
