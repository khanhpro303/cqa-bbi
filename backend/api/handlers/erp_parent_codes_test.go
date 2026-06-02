package handlers

import (
	"reflect"
	"testing"
)

// TestCollectParentCodesFromProducts pins the contract the STOCK-pick
// continuation depends on: the exact-web products response must surface the
// chosen line's parent SKU(s) so the agent can copy parent_codes[0] into a
// follow-up product_variants call. Regression guard for the FF901 "Nardo Grey
// size XL" dead-end, where the exact-web response carried no parent_codes and
// the variant lookup terminated at count=0.
func TestCollectParentCodesFromProducts(t *testing.T) {
	tests := []struct {
		name     string
		products []map[string]interface{}
		want     []string
	}{
		{
			name:     "empty input yields empty non-nil slice",
			products: nil,
			want:     []string{},
		},
		{
			name: "single line returns its ma_cha",
			products: []map[string]interface{}{
				{"MA": "SP458493", "ma_cha": "SP458484", "TEN_DONG_BO_WEB": "LS2 FF901"},
			},
			want: []string{"SP458484"},
		},
		{
			name: "dedupes repeated ma_cha across variant rows, preserving order",
			products: []map[string]interface{}{
				{"MA": "SP458493", "ma_cha": "SP458484"},
				{"MA": "SP458496", "ma_cha": "SP458484"},
				{"MA": "SP460001", "MA_CHA": "SP460000"},
			},
			want: []string{"SP458484", "SP460000"},
		},
		{
			name: "skips rows without a parent code",
			products: []map[string]interface{}{
				{"MA": "SP1", "name": "aggregated row, no parent"},
				{"MA": "SP2", "ma_cha": "SP458484"},
			},
			want: []string{"SP458484"},
		},
		{
			name: "reads uppercase MA_CHA key too",
			products: []map[string]interface{}{
				{"MA": "SP2", "MA_CHA": "SP999999"},
			},
			want: []string{"SP999999"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := collectParentCodesFromProducts(tt.products)
			if got == nil {
				t.Fatalf("collectParentCodesFromProducts returned nil; want non-nil slice")
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("collectParentCodesFromProducts() = %v, want %v", got, tt.want)
			}
		})
	}
}
