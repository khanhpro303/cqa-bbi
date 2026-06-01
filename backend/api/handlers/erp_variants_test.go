package handlers

import "testing"

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
