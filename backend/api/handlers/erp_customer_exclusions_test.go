package handlers

import (
	"reflect"
	"testing"
)

func TestCleanCustomerCodes(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want []string
	}{
		{"empty", nil, []string{}},
		{"trims", []string{"  KH001  "}, []string{"KH001"}},
		{"drops blanks", []string{"", "   ", "KH001"}, []string{"KH001"}},
		{"dedupes preserving order", []string{"KH002", "KH001", "KH002"}, []string{"KH002", "KH001"}},
		{"dedupes after trim", []string{"KH001", " KH001 "}, []string{"KH001"}},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := cleanCustomerCodes(tc.in)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("cleanCustomerCodes(%#v) = %#v, want %#v", tc.in, got, tc.want)
			}
		})
	}
}

func TestFindGMFBlockedCodes(t *testing.T) {
	gmf := map[string]struct{}{
		"KH_GMF1": {},
		"KH_GMF2": {},
	}

	cases := []struct {
		name      string
		codes     []string
		gmfLinked map[string]struct{}
		want      []string
	}{
		{"no linked codes", []string{"KH001", "KH002"}, gmf, []string{}},
		{"one linked code", []string{"KH001", "KH_GMF1"}, gmf, []string{"KH_GMF1"}},
		{"multiple linked preserve order", []string{"KH_GMF2", "KH001", "KH_GMF1"}, gmf, []string{"KH_GMF2", "KH_GMF1"}},
		{"matches after trim", []string{" KH_GMF1 "}, gmf, []string{" KH_GMF1 "}},
		{"empty guard set", []string{"KH_GMF1"}, map[string]struct{}{}, []string{}},
		{"no codes", nil, gmf, []string{}},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := findGMFBlockedCodes(tc.codes, tc.gmfLinked)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("findGMFBlockedCodes(%#v) = %#v, want %#v", tc.codes, got, tc.want)
			}
		})
	}
}
