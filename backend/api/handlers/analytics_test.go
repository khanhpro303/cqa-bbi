package handlers

import "testing"

func TestUnknownSenderLabel(t *testing.T) {
	tests := []struct {
		name             string
		senderExternalID string
		want             string
	}{
		{"empty id falls back to bare Unknown", "", "Unknown"},
		{"long zalo id uses last 4 digits", "1234567890", "Unknown (7890)"},
		{"exactly 4 digits kept whole", "4821", "Unknown (4821)"},
		{"shorter than 4 digits kept whole", "12", "Unknown (12)"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := unknownSenderLabel(tt.senderExternalID); got != tt.want {
				t.Errorf("unknownSenderLabel(%q) = %q, want %q", tt.senderExternalID, got, tt.want)
			}
		})
	}
}
