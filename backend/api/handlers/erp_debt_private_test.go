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

func TestScopeApprovedDebtCodes(t *testing.T) {
	allowed := []string{"S052 - Phượt 4P", "EG05"}
	tests := []struct {
		name      string
		resolved  []string
		scopeType string
		ownCode   string
		allowed   []string
		want      []string
	}{
		{"all keeps resolved", []string{"S001", "S002"}, "all", "", nil, []string{"S001", "S002"}},
		{"all strips labels + dedupes", []string{"S001 - A", "S001"}, "all", "", nil, []string{"S001"}},
		{"assigned in group", []string{"S052"}, "assigned", "", allowed, []string{"S052"}},
		{"assigned out of group", []string{"S999"}, "assigned", "", allowed, []string{}},
		{"assigned mixed keeps only in-group", []string{"S052", "S999", "EG05"}, "assigned", "", allowed, []string{"EG05", "S052"}},
		{"own matches own", []string{"S001"}, "own", "S001", nil, []string{"S001"}},
		{"own rejects other", []string{"S002"}, "own", "S001", nil, []string{}},
		{"unknown scope denies", []string{"S001"}, "weird", "", allowed, []string{}},
		{"empty resolved", nil, "all", "", nil, []string{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := scopeApprovedDebtCodes(tt.resolved, tt.scopeType, tt.ownCode, tt.allowed)
			if len(got) == 0 && len(tt.want) == 0 {
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("scopeApprovedDebtCodes(%v, %q) = %v, want %v", tt.resolved, tt.scopeType, got, tt.want)
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
