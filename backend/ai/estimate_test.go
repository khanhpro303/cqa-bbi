package ai

import "testing"

func TestEstimateTokens(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want int
	}{
		{"empty", "", 0},
		{"short non-empty rounds up to 1", "abc", 1},
		{"ascii ~4 chars per token", "12345678", 2},
		{"vietnamese counted by rune not byte", "ăâđêô", 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := EstimateTokens(tt.in); got != tt.want {
				t.Errorf("EstimateTokens(%q) = %d, want %d", tt.in, got, tt.want)
			}
		})
	}
}
