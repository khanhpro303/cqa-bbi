package workers

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/vietbui/chat-quality-agent/chatmemory"
	"github.com/vietbui/chat-quality-agent/config"
)

func TestWebhookTime(t *testing.T) {
	now := time.Now().Add(-time.Minute).Truncate(time.Millisecond)
	if got := webhookTime(strconv.FormatInt(now.UnixMilli(), 10)); !got.Equal(now) {
		t.Fatalf("got %v, want %v", got, now)
	}
	for _, bad := range []string{"", "abc", "0", "1000"} {
		if got := webhookTime(bad); time.Since(got) > time.Second {
			t.Fatalf("%q should fall back to now, got %v", bad, got)
		}
	}
}

func TestChatMemoryTurnAgentType(t *testing.T) {
	if got := chatMemoryTurn("t", "c", "s", "u", "", "", "", "", true).AgentType; got != "private" {
		t.Fatalf("staff 1:1 = %s", got)
	}
	if got := chatMemoryTurn("t", "c", "s", "u", "g", "", "", "", false).AgentType; got != "public" {
		t.Fatalf("group = %s", got)
	}
}

func TestAppendChatMemoryContextDisabled(t *testing.T) {
	cfg := &config.Config{}
	cfg.ChatMemory.Enabled = true
	turn := chatmemory.Turn{TenantID: "t", ChannelID: "c", ZaloUserID: "u"}
	// Off unless the channel turns it on: the prompt is unchanged.
	if got := appendChatMemoryContext(context.Background(), cfg, `{}`, "base", turn); got != "base" {
		t.Fatalf("got %q", got)
	}
	// An empty prompt means the flow's own prompt; it must stay empty.
	on := `{"chat_memory_profile_in_prompt":true}`
	if got := appendChatMemoryContext(context.Background(), cfg, on, "", turn); got != "" {
		t.Fatalf("got %q", got)
	}
}
