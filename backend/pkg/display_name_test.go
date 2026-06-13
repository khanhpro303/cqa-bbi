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
		{"unidentified placeholder dropped to empty", "Khách chưa xác định", ""},
		{"unidentified placeholder with surrounding whitespace dropped", "  Khách chưa xác định  ", ""},
		{"garbled unverified placeholder dropped to empty", "KhÃ¡ch chÆ°a xÃ¡c thá»±c", ""},
		{"garbled unidentified placeholder dropped to empty", "KhÃ¡ch chÆ°a xÃ¡c Ä‘á»‹nh", ""},
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
