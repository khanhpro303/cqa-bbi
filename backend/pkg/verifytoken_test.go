package pkg

import (
	"regexp"
	"testing"
)

func TestNewVerifyToken(t *testing.T) {
	format := regexp.MustCompile(`^[A-Z0-9]{6}$`)
	seen := map[string]bool{}
	for i := 0; i < 200; i++ {
		token := NewVerifyToken()
		if !format.MatchString(token) {
			t.Fatalf("token %q is not 6 uppercase letters/digits", token)
		}
		seen[token] = true
	}
	if len(seen) < 195 {
		t.Fatalf("only %d distinct tokens out of 200", len(seen))
	}
}
