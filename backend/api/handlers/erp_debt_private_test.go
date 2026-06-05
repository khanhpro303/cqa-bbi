package handlers

import (
	"reflect"
	"strings"
	"testing"
)

func TestRenderDebtCustomerPickText(t *testing.T) {
	candidates := []debtCustomerCandidate{
		{Code: "S001_1", Name: "Arrow SG (Huy)"},
		{Code: "S001_2", Name: "Arrow HN (Huy)"},
	}
	got := renderDebtCustomerPickText(candidates)
	for _, want := range []string{"S001_1", "Arrow SG (Huy)", "S001_2", "Arrow HN (Huy)", "tất cả"} {
		if !strings.Contains(got, want) {
			t.Errorf("renderDebtCustomerPickText() = %q, missing %q", got, want)
		}
	}
}

func TestRenderDebtCustomerPickTextCodeOnly(t *testing.T) {
	got := renderDebtCustomerPickText([]debtCustomerCandidate{{Code: "S001_1"}})
	if !strings.Contains(got, "S001_1") {
		t.Errorf("renderDebtCustomerPickText() = %q, missing code", got)
	}
}

func TestRenderDebtCustomerPickTextFallback(t *testing.T) {
	if got := renderDebtCustomerPickText(nil); got != debtCustomerPickByCodeText {
		t.Errorf("renderDebtCustomerPickText(nil) = %q, want fallback %q", got, debtCustomerPickByCodeText)
	}
}

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
		ownCodes  []string
		allowed   []string
		want      []string
	}{
		{"all keeps resolved", []string{"S001", "S002"}, "all", nil, nil, []string{"S001", "S002"}},
		{"all keeps full label, dedupes by code", []string{"S001 - A", "S001"}, "all", nil, nil, []string{"S001 - A"}},
		{"all preserves label for ERP", []string{"S001_1 - Huy"}, "all", nil, nil, []string{"S001_1 - Huy"}},
		{"assigned in group", []string{"S052"}, "assigned", nil, allowed, []string{"S052"}},
		{"assigned keeps label when in group", []string{"S052 - Phượt 4P"}, "assigned", nil, allowed, []string{"S052 - Phượt 4P"}},
		{"assigned out of group", []string{"S999"}, "assigned", nil, allowed, []string{}},
		{"assigned mixed keeps only in-group", []string{"S052", "S999", "EG05"}, "assigned", nil, allowed, []string{"EG05", "S052"}},
		{"own matches own", []string{"S001"}, "own", []string{"S001"}, nil, []string{"S001"}},
		{"own rejects other", []string{"S002"}, "own", []string{"S001"}, nil, []string{}},
		{"own multi keeps both shops", []string{"S001 - A", "S002 - B"}, "own", []string{"S001", "S002"}, nil, []string{"S001 - A", "S002 - B"}},
		{"unknown scope denies", []string{"S001"}, "weird", nil, allowed, []string{}},
		{"empty resolved", nil, "all", nil, nil, []string{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := scopeApprovedDebtCodes(tt.resolved, tt.scopeType, tt.ownCodes, tt.allowed)
			if len(got) == 0 && len(tt.want) == 0 {
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("scopeApprovedDebtCodes(%v, %q) = %v, want %v", tt.resolved, tt.scopeType, got, tt.want)
			}
		})
	}
}

func TestExactCustomerCodePick(t *testing.T) {
	codes := []string{"S001", "S001_1", "S001_2"}
	tests := []struct {
		name   string
		search string
		codes  []string
		want   string
	}{
		// The loop-killer: picking the prefix code "S001" must resolve to exactly
		// S001, not re-match its siblings via LIKE.
		{"exact prefix code wins", "S001", codes, "S001"},
		{"exact sub-code", "S001_1", codes, "S001_1"},
		{"case-insensitive", "s001_2", codes, "S001_2"},
		{"trims whitespace", "  S001  ", codes, "S001"},
		{"no match returns empty", "S999", codes, ""},
		{"partial is not exact", "S00", codes, ""},
		{"empty search", "", codes, ""},
		{"empty codes", "S001", nil, ""},
		{"trims candidate label whitespace", "S001", []string{" S001 "}, "S001"},
		// Labelled candidates: a bare-code reply maps to the FULL stored value so
		// the " - <name>" label survives to the ERP query, and the bare leading
		// code never selects a sibling.
		{"bare reply returns full label", "S001_1", []string{"S001_1 - Huy"}, "S001_1 - Huy"},
		{"full reply returns full label", "S001_1 - Huy", []string{"S001_1 - Huy"}, "S001_1 - Huy"},
		{"bare prefix does not select sibling", "S001", []string{"S001_1 - Huy", "S001_2 - Nam"}, ""},
		{"bare reply picks exact among labelled siblings", "S001_2", []string{"S001_1 - Huy", "S001_2 - Nam"}, "S001_2 - Nam"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := exactCustomerCodePick(tt.search, tt.codes); got != tt.want {
				t.Errorf("exactCustomerCodePick(%q, %v) = %q, want %q", tt.search, tt.codes, got, tt.want)
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
