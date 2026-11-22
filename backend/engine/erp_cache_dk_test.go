package engine

import "testing"

func TestIsDKCopyParent(t *testing.T) {
	cases := map[string]bool{
		"SP000557_DK":   true,
		" SP000557_dk ": true,
		"SP000557":      false,
		"":              false,
		"DKSP1":         false,
		"SP_DKX":        false,
	}
	for code, want := range cases {
		if got := IsDKCopyParent(code); got != want {
			t.Errorf("IsDKCopyParent(%q) = %v; want %v", code, got, want)
		}
	}
}
