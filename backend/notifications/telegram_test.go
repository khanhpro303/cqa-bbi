package notifications

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestNewTelegramNotifier(t *testing.T) {
	notifier := NewTelegramNotifier("test-bot-token", "-1001234567890")
	if notifier == nil {
		t.Fatal("Notifier should not be nil")
	}
	if notifier.botToken != "test-bot-token" {
		t.Errorf("Expected bot token 'test-bot-token', got %s", notifier.botToken)
	}
	if notifier.chatID != "-1001234567890" {
		t.Errorf("Expected chat ID '-1001234567890', got %s", notifier.chatID)
	}
}

func TestNewEmailNotifier(t *testing.T) {
	notifier := NewEmailNotifier("smtp.gmail.com", 587, "user", "pass", "from@test.com", []string{"to@test.com"})
	if notifier == nil {
		t.Fatal("Notifier should not be nil")
	}
	if notifier.smtpHost != "smtp.gmail.com" {
		t.Errorf("Expected smtp host 'smtp.gmail.com', got %s", notifier.smtpHost)
	}
}

func TestBuildTelegramChunks_ShortMessageStaysSingle(t *testing.T) {
	chunks := buildTelegramChunks("Tiêu đề", "Nội dung ngắn")
	if len(chunks) != 1 {
		t.Fatalf("expected 1 chunk, got %d", len(chunks))
	}
	if !strings.HasPrefix(chunks[0], "<b>Tiêu đề</b>") {
		t.Errorf("subject should be HTML-bolded at the top, got %q", chunks[0])
	}
	if strings.Contains(chunks[0], "(1/1)") {
		t.Errorf("single chunk should not carry a part counter")
	}
}

func TestBuildTelegramChunks_LongMessageSplitsRuneSafe(t *testing.T) {
	// A long Vietnamese alert (multibyte runes) well over the per-message cap.
	line := "• Nhóm GMF khu vực miền Trung: gửi chữ OK nhưng ảnh lỗi\n"
	body := strings.Repeat(line, 200)

	chunks := buildTelegramChunks("Cảnh báo lỗi gửi chiến dịch", body)
	if len(chunks) < 2 {
		t.Fatalf("expected the long alert to split into multiple parts, got %d", len(chunks))
	}

	var reassembled strings.Builder
	for i, chunk := range chunks {
		// Every chunk must be valid UTF-8 (no rune cut mid-character).
		if !utf8.ValidString(chunk) {
			t.Errorf("chunk %d is not valid UTF-8 — a multibyte rune was split", i+1)
		}
		// Every chunk must stay under the conservative cap, counter included.
		if got := utf8.RuneCountInString(chunk); got > telegramMaxRunes+16 {
			t.Errorf("chunk %d has %d runes, exceeds cap %d", i+1, got, telegramMaxRunes)
		}
		// Multi-part messages carry an ordered "(i/n)" prefix.
		prefix := strings.SplitN(chunk, "\n", 2)
		if want := "(" + itoa(i+1) + "/" + itoa(len(chunks)) + ")"; prefix[0] != want {
			t.Errorf("chunk %d prefix = %q, want %q", i+1, prefix[0], want)
		}
		if len(prefix) == 2 {
			reassembled.WriteString(prefix[1])
		}
	}

	// No content is lost: the original body survives (minus the prepended subject
	// line) across the reassembled parts.
	if !strings.Contains(reassembled.String(), "Nhóm GMF khu vực miền Trung") {
		t.Errorf("reassembled message lost its body content")
	}
}

// itoa is a tiny local int->string helper to keep the test free of strconv noise.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

func TestSplitComma(t *testing.T) {
	tests := []struct {
		input    string
		expected int
	}{
		{"a@b.com, c@d.com", 2},
		{"single@email.com", 1},
		{"a@b.com,c@d.com, e@f.com", 3},
		{"", 0}, // empty string returns 0 items after trim
	}

	for _, tt := range tests {
		result := splitComma(tt.input)
		if len(result) != tt.expected {
			t.Errorf("splitComma(%q) = %d items, want %d", tt.input, len(result), tt.expected)
		}
	}
}
