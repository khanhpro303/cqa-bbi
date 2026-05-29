package handlers

import "testing"

// TestLeadingCustomerCode covers stripping the "<code> - <name>" label that
// Cloudify ships in MA_KHACH_HANG / customer codes down to the bare code.
func TestLeadingCustomerCode(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"S052 - Phượt 4P", "S052"},
		{"  S052 - Phượt 4P ", "S052"},
		{"EG05", "EG05"},
		{"", ""},
		{"BBI001 - Khách lẻ", "BBI001"},
	}
	for _, tc := range tests {
		if got := leadingCustomerCode(tc.in); got != tc.want {
			t.Errorf("leadingCustomerCode(%q) = %q; want %q", tc.in, got, tc.want)
		}
	}
}

// TestOrderCustomerCode covers the official Cloudify saorders/search shape
// where MA_KHACH_HANG is an array [id, "CODE - Name"], plus the legacy flat
// string / alias keys older payloads use.
func TestOrderCustomerCode(t *testing.T) {
	tests := []struct {
		name string
		item map[string]interface{}
		want string
	}{
		{
			name: "official array shape",
			item: map[string]interface{}{
				"MA_KHACH_HANG": []interface{}{float64(2273), "S052 - Phượt 4P"},
			},
			want: "S052",
		},
		{
			name: "array with only id",
			item: map[string]interface{}{
				"MA_KHACH_HANG": []interface{}{float64(2273)},
			},
			want: "2273",
		},
		{
			name: "flat string with label",
			item: map[string]interface{}{
				"MA_KHACH_HANG": "EG05 - EGO Store",
			},
			want: "EG05",
		},
		{
			name: "flat string bare code",
			item: map[string]interface{}{
				"MA_KHACH_HANG": "EG05",
			},
			want: "EG05",
		},
		{
			name: "legacy MA_KH alias",
			item: map[string]interface{}{
				"MA_KH": "BBI001",
			},
			want: "BBI001",
		},
		{
			name: "no customer field",
			item: map[string]interface{}{"foo": "bar"},
			want: "",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := orderCustomerCode(tc.item); got != tc.want {
				t.Errorf("orderCustomerCode() = %q; want %q", got, tc.want)
			}
		})
	}
}

// TestComputeOrderTotal verifies the total is derived from line items
// (Σ SO_LUONG × DON_GIA) minus the invoice-level discount, because the
// saorders/search payload carries no precomputed total field.
func TestComputeOrderTotal(t *testing.T) {
	tests := []struct {
		name string
		item map[string]interface{}
		want float64
	}{
		{
			name: "official warranty sample (zero price)",
			item: map[string]interface{}{
				"GIAM_GIA_HOA_DON": float64(0),
				"DON_DAT_HANG_CHI_TIET": []interface{}{
					map[string]interface{}{"SO_LUONG": float64(1), "DON_GIA": float64(0)},
				},
			},
			want: 0,
		},
		{
			name: "two lines minus discount",
			item: map[string]interface{}{
				"GIAM_GIA_HOA_DON": float64(20000),
				"DON_DAT_HANG_CHI_TIET": []interface{}{
					map[string]interface{}{"SO_LUONG": float64(2), "DON_GIA": float64(100000)},
					map[string]interface{}{"SO_LUONG": float64(1), "DON_GIA": float64(50000)},
				},
			},
			want: 230000,
		},
		{
			name: "discount larger than lines clamps to zero",
			item: map[string]interface{}{
				"GIAM_GIA_HOA_DON": float64(999999),
				"DON_DAT_HANG_CHI_TIET": []interface{}{
					map[string]interface{}{"SO_LUONG": float64(1), "DON_GIA": float64(1000)},
				},
			},
			want: 0,
		},
		{
			name: "missing line items",
			item: map[string]interface{}{},
			want: 0,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := computeOrderTotal(tc.item); got != tc.want {
				t.Errorf("computeOrderTotal() = %v; want %v", got, tc.want)
			}
		})
	}
}

// TestBuildOrdersSummaryRealShape feeds normalized records (as the orders
// handler builds them from saorders/search) and checks the per-status
// aggregation and the value derived from line items.
func TestBuildOrdersSummaryRealShape(t *testing.T) {
	records := []map[string]interface{}{
		{
			"status": "3",
			"total":  float64(230000),
			"don_dat_hang_chi_tiet": []interface{}{
				map[string]interface{}{"SO_LUONG": float64(3)},
			},
		},
		{
			"status": "2",
			"total":  float64(50000),
			"don_dat_hang_chi_tiet": []interface{}{
				map[string]interface{}{"SO_LUONG": float64(1)},
			},
		},
	}

	summary := buildOrdersSummary(records)
	if summary.TotalOrders != 2 {
		t.Errorf("TotalOrders = %d; want 2", summary.TotalOrders)
	}
	if summary.TotalValue != 280000 {
		t.Errorf("TotalValue = %v; want 280000", summary.TotalValue)
	}
	if summary.TotalQuantity != 4 {
		t.Errorf("TotalQuantity = %v; want 4", summary.TotalQuantity)
	}
	if len(summary.ByStatus) != 2 {
		t.Fatalf("ByStatus len = %d; want 2", len(summary.ByStatus))
	}
	// Canonical order: "3" (Đang giao) before "2" (Hoàn thành).
	if summary.ByStatus[0].Status != "3" || summary.ByStatus[0].StatusName != "Đang giao" {
		t.Errorf("ByStatus[0] = %+v; want status 3 Đang giao", summary.ByStatus[0])
	}
	if summary.ByStatus[1].Status != "2" || summary.ByStatus[1].StatusName != "Hoàn thành" {
		t.Errorf("ByStatus[1] = %+v; want status 2 Hoàn thành", summary.ByStatus[1])
	}
}
