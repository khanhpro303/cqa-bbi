package handlers

import (
	"reflect"
	"testing"
)

func TestIsPrivateDebtCustomerQuery(t *testing.T) {
	tests := []struct {
		name   string
		search string
		want   bool
	}{
		{"generic with diacritics", "công nợ của khách hàng", true},
		{"generic accent-stripped", "cong no cua khach hang", true},
		{"short generic", "công nợ khách", true},
		{"lookup phrasing", "tra cứu công nợ khách hàng", true},
		{"period this month", "công nợ tháng này", false},
		{"period quarter", "công nợ quý này", false},
		{"specific code", "S001", false},
		{"customer plus name", "công nợ khách hàng EGO", false},
		{"bare debt (handled as generic period)", "công nợ", false},
		{"empty", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isPrivateDebtCustomerQuery(tt.search); got != tt.want {
				t.Errorf("isPrivateDebtCustomerQuery(%q) = %v, want %v", tt.search, got, tt.want)
			}
		})
	}
}

func TestTokenizeCustomerQuery(t *testing.T) {
	tests := []struct {
		name   string
		search string
		want   []string
	}{
		{"code and name", "S001 Huy", []string{"S001", "Huy"}},
		{"strips debt stopwords", "công nợ của khách hàng EGO", []string{"EGO"}},
		{"pure generic yields nothing", "công nợ của khách hàng", nil},
		{"trims punctuation", "S001, Huy?", []string{"S001", "Huy"}},
		{"single code", "S001", []string{"S001"}},
		{"empty", "", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tokenizeCustomerQuery(tt.search)
			if len(got) == 0 && len(tt.want) == 0 {
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("tokenizeCustomerQuery(%q) = %v, want %v", tt.search, got, tt.want)
			}
		})
	}
}

func TestFoldDebtSearch(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"Công Nợ", "cong no"},
		{"Khách Hàng", "khach hang"},
		{"đối chiếu", "doi chieu"},
		{"  S001  ", "s001"},
	}
	for _, tt := range tests {
		if got := foldDebtSearch(tt.in); got != tt.want {
			t.Errorf("foldDebtSearch(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
