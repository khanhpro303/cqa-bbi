package workers

import "testing"

func TestQuietGroupCasualTurn(t *testing.T) {
	tests := []struct {
		intent, group string
		want          bool
	}{
		{"CASUAL", "g1", true},
		{"CASUAL", "", false},
		{"IN_SCOPE", "g1", false},
		{"OUT_OF_SCOPE", "g1", false},
		{"HANDOVER", "g1", false},
	}
	for _, tt := range tests {
		if got := quietGroupCasualTurn(tt.intent, tt.group); got != tt.want {
			t.Errorf("quietGroupCasualTurn(%q, %q) = %v, want %v", tt.intent, tt.group, got, tt.want)
		}
	}
}
