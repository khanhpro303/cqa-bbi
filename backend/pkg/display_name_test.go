package pkg

import "testing"

func TestSanitizeZaloDisplayName(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"unverified placeholder dropped to empty", UnverifiedZaloDisplayName, ""},
		{"placeholder with surrounding whitespace dropped", "  " + UnverifiedZaloDisplayName + " ", ""},
		{"real name kept unchanged", "Nguyễn Văn A", "Nguyễn Văn A"},
		{"empty stays empty", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := SanitizeZaloDisplayName(tt.in); got != tt.want {
				t.Errorf("SanitizeZaloDisplayName(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
