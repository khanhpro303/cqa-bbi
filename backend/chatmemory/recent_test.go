package chatmemory

import (
	"strings"
	"testing"

	"github.com/vietbui/chat-quality-agent/db/models"
)

func TestRecentTurnsFrom(t *testing.T) {
	bot := `{"cqa_live":true,"cqa_source":"chatbot"}`
	newestFirst := []models.Message{
		{SenderType: "customer", Content: "đúng rồi"},
		{SenderType: "agent", RawData: bot, Content: "Anh/chị đang hỏi tồn kho của mẫu FF399 đúng không?\n\nNếu đúng, em sẽ kiểm tra."},
		{SenderType: "customer", Content: "mẫu đó còn không"},
		{SenderType: "agent", Content: "Dạ em là nhân viên, em gửi anh ảnh nhé"},
		{SenderType: "customer", Content: "   "},
	}
	got := recentTurnsFrom(newestFirst, " đúng rồi ", 6)
	want := []RecentTurn{
		{"Nhân viên", "Dạ em là nhân viên, em gửi anh ảnh nhé"},
		{"Khách", "mẫu đó còn không"},
		{"Bot", "Anh/chị đang hỏi tồn kho của mẫu FF399 đúng không? Nếu đúng, em sẽ kiểm tra."},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d turns %+v; want %d", len(got), got, len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("turn %d = %+v; want %+v", i, got[i], want[i])
		}
	}
}

func TestRecentTurnsFromKeepsEarlierSameText(t *testing.T) {
	// The current message is not stored yet (or under another text): the
	// newest stored message is a bot question and must be kept.
	got := recentTurnsFrom([]models.Message{
		{SenderType: "agent", RawData: `{"cqa_source":"chatbot"}`, Content: "Anh cần màu nào?"},
	}, "đen", 6)
	if len(got) != 1 || got[0].Speaker != "Bot" {
		t.Fatalf("got %+v; want the bot question kept", got)
	}
}

func TestRecentTurnsFromLimits(t *testing.T) {
	long := strings.Repeat("a", 1000)
	var msgs []models.Message
	for i := 0; i < 10; i++ {
		msgs = append(msgs, models.Message{SenderType: "customer", Content: long})
	}
	got := recentTurnsFrom(msgs, "x", 6)
	total := 0
	for _, turn := range got {
		if n := len([]rune(turn.Text)); n > recentTurnChars+1 {
			t.Fatalf("turn has %d runes; want at most %d", n, recentTurnChars+1)
		}
		total += len(turn.Text)
	}
	if len(got) == 0 || len(got) > 6 || total > recentTurnsMaxChars {
		t.Fatalf("got %d turns, %d chars; want 1..6 turns within %d chars", len(got), total, recentTurnsMaxChars)
	}
}
