package chatmemory

import (
	"strings"
	"testing"
)

func TestRecentTurnsFrom(t *testing.T) {
	newestFirst := []recentRow{
		{ExternalMessageID: "m5", SenderType: "customer", Content: "đúng rồi"},
		{ExternalMessageID: "m4", SenderType: "agent", Source: "chatbot", Content: "Anh/chị đang hỏi tồn kho của mẫu FF399 đúng không?\n\nNếu đúng, em sẽ kiểm tra."},
		{ExternalMessageID: "m3", SenderType: "customer", Content: "mẫu đó còn không"},
		{ExternalMessageID: "m2", SenderType: "agent", Content: "Dạ em là nhân viên, em gửi anh ảnh nhé"},
		{ExternalMessageID: "m1", SenderType: "customer", Content: "   "},
	}
	got := recentTurnsFrom(newestFirst, "m5", "đúng rồi", 6)
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

func TestRecentTurnsFromExcludesCurrentByID(t *testing.T) {
	// The current message sorts below an earlier identical reply (same
	// second): the id, not the position or text, decides what is dropped.
	got := recentTurnsFrom([]recentRow{
		{ExternalMessageID: "old", SenderType: "customer", Content: "ok"},
		{ExternalMessageID: "cur", SenderType: "customer", Content: "ok"},
		{ExternalMessageID: "q", SenderType: "agent", Source: "chatbot", Content: "Anh cần màu nào?"},
	}, "cur", "ok", 6)
	if len(got) != 2 || got[0].Speaker != "Bot" || got[1] != (RecentTurn{"Khách", "ok"}) {
		t.Fatalf("got %+v; want the bot question and the earlier ok", got)
	}
}

func TestRecentTurnsFromWithoutIDDropsOneMatch(t *testing.T) {
	got := recentTurnsFrom([]recentRow{
		{SenderType: "customer", Content: "ok"},
		{SenderType: "agent", Source: "chatbot", Content: "Anh cần màu nào?"},
		{SenderType: "customer", Content: "ok"},
	}, "", " ok ", 6)
	if len(got) != 2 || got[0] != (RecentTurn{"Khách", "ok"}) || got[1].Speaker != "Bot" {
		t.Fatalf("got %+v; want only the current ok dropped", got)
	}
}

func TestRecentTurnsFromLimits(t *testing.T) {
	long := strings.Repeat("a", 1000)
	var rows []recentRow
	for i := 0; i < 10; i++ {
		rows = append(rows, recentRow{SenderType: "customer", Content: long})
	}
	got := recentTurnsFrom(rows, "x", "x", 6)
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
