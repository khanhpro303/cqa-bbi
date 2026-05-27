package handlers

import "testing"

func TestNormalizeExclusionFlag(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{"empty", "", ""},
		{"whitespace", "   ", ""},
		{"T uppercase", "T", "T"},
		{"t lowercase", "t", "T"},
		{"true word", "true", "T"},
		{"TRUE upper", "TRUE", "T"},
		{"numeric 1", "1", "T"},
		{"yes", "yes", "T"},
		{"y", "y", "T"},
		{"vietnamese loai", "loại", "T"},
		{"F uppercase", "F", "F"},
		{"false word", "false", "F"},
		{"numeric 0", "0", "F"},
		{"no", "no", "F"},
		{"n", "n", "F"},
		{"vietnamese giu", "giữ", "F"},
		{"invalid string", "maybe", ""},
		{"invalid number", "2", ""},
		{"padded T", "  T  ", "T"},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := normalizeExclusionFlag(tc.input)
			if got != tc.want {
				t.Fatalf("normalizeExclusionFlag(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}
