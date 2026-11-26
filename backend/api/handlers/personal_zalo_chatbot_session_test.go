package handlers

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/vietbui/chat-quality-agent/config"
	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/db/models"
)

func TestNormalizePersonalZaloChatbotRequest(t *testing.T) {
	tests := []struct {
		name       string
		req        personalZaloChatbotRequest
		want       bool
		threadType string
		senderID   string
	}{
		{name: "legacy direct", req: personalZaloChatbotRequest{AccountExternalID: "123", ThreadID: "456"}, want: true, threadType: "user", senderID: "456"},
		{name: "new direct", req: personalZaloChatbotRequest{AccountExternalID: "123", ThreadID: "456", ThreadType: "user", SenderID: "456"}, want: true, threadType: "user", senderID: "456"},
		{name: "group sender", req: personalZaloChatbotRequest{SchemaVersion: "personal-zalo-chatbot-v2", AccountExternalID: "123", ThreadID: "999", ThreadType: "group", SenderID: "456"}, want: true, threadType: "group", senderID: "456"},
		{name: "v1 group rejected", req: personalZaloChatbotRequest{SchemaVersion: "personal-zalo-chatbot-v1", AccountExternalID: "123", ThreadID: "999", ThreadType: "group", SenderID: "456"}},
		{name: "direct sender mismatch", req: personalZaloChatbotRequest{AccountExternalID: "123", ThreadID: "456", ThreadType: "user", SenderID: "789"}},
		{name: "self message", req: personalZaloChatbotRequest{AccountExternalID: "123", ThreadID: "999", ThreadType: "group", SenderID: "123"}},
		{name: "group without sender", req: personalZaloChatbotRequest{AccountExternalID: "123", ThreadID: "999", ThreadType: "group"}},
		{name: "unknown type", req: personalZaloChatbotRequest{AccountExternalID: "123", ThreadID: "456", ThreadType: "room", SenderID: "456"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := normalizePersonalZaloChatbotRequest(&tt.req); got != tt.want {
				t.Fatalf("normalize=%t, want %t", got, tt.want)
			}
			if tt.want && (tt.req.ThreadType != tt.threadType || tt.req.SenderID != tt.senderID) {
				t.Fatalf("normalized type/sender = %q/%q, want %q/%q", tt.req.ThreadType, tt.req.SenderID, tt.threadType, tt.senderID)
			}
		})
	}
}

func TestPersonalZaloGroupAddressing(t *testing.T) {
	session := personalZaloSessionConfig{
		SessionKeyword: "Chào Bull;hi bull", SessionEndKeyword: "Tạm biệt Bull", GroupBotNames: "Bull;bot",
	}
	for _, text := range []string{"chào bull", "Hi Bull", "Bull ơi kiểm tra giúp", "@bot kiểm tra giúp", "nhờ BOT tra giúp"} {
		if !personalZaloGroupMessageAddressed(text, session) {
			t.Errorf("%q should address the bot", text)
		}
	}
	for _, text := range []string{"mọi người kiểm tra giúp", "robot kiểm tra giúp", "bullish quá", "anh ơi mai em giao"} {
		if personalZaloGroupMessageAddressed(text, session) {
			t.Errorf("%q should stay silent", text)
		}
	}
}

func TestPlanPersonalZaloLegacyDirectAndSilentGroup(t *testing.T) {
	previousRedis := db.RedisClient
	db.RedisClient = nil
	t.Cleanup(func() { db.RedisClient = previousRedis })
	session := personalZaloSessionConfig{SessionTimeoutMinutes: 30, GroupBotNames: "bot"}
	direct := personalZaloChatbotRequest{TenantID: "tenant", ChannelID: "channel", AccountExternalID: "123", ThreadID: "456", ThreadType: "user", SenderID: "456", Text: "xin chào"}
	turn, err := planPersonalZaloTurn(t.Context(), session, direct)
	if err != nil || !turn.RunFlow || !strings.HasPrefix(turn.FlowScope, "personal-zalo-") {
		t.Fatalf("legacy direct turn = %+v, err=%v", turn, err)
	}
	group := direct
	group.ThreadType, group.ThreadID, group.Text = "group", "999", "mọi người kiểm tra giúp"
	turn, err = planPersonalZaloTurn(t.Context(), session, group)
	if err != nil || turn.RunFlow || turn.Reply != "" {
		t.Fatalf("unaddressed group turn = %+v, err=%v", turn, err)
	}
}

func TestPersonalZaloConfiguredSessions(t *testing.T) {
	addr := os.Getenv("TEST_REDIS_ADDR")
	if addr == "" {
		t.Skip("TEST_REDIS_ADDR not set")
	}
	client := redis.NewClient(&redis.Options{Addr: addr})
	if err := client.Ping(t.Context()).Err(); err != nil {
		t.Fatalf("connect to test Redis: %v", err)
	}
	previousRedis := db.RedisClient
	db.RedisClient = client
	t.Cleanup(func() {
		db.RedisClient = previousRedis
		_ = client.Close()
	})

	cfg := &config.Config{
		ChatbotSessionEndKeyword: "tạm biệt", ChatbotSessionWelcomeMessage: "xin chào",
		ChatbotSessionGoodbyeMessage: "hẹn gặp lại", ChatbotSessionTimeout: 7,
	}
	channel := models.Channel{Metadata: `{"session_keyword":"chào bot","session_timeout_minutes":7,"group_bot_names":"Bull;bot"}`}
	session, err := parsePersonalZaloSessionConfig(channel, cfg)
	if err != nil {
		t.Fatal(err)
	}
	base := personalZaloChatbotRequest{
		TenantID: uuid.NewString(), ChannelID: uuid.NewString(), AccountExternalID: "123",
		ThreadID: "456", ThreadType: "user", SenderID: "456",
	}
	keys := []string{personalZaloSessionKey(base)}
	t.Cleanup(func() { _ = client.Del(context.Background(), keys...).Err() })

	base.Text = "tư vấn giúp tôi"
	beforeOpen, err := planPersonalZaloTurn(t.Context(), session, base)
	if err != nil || beforeOpen.RunFlow || beforeOpen.Reply != "" {
		t.Fatalf("turn before open = %+v, err=%v", beforeOpen, err)
	}
	base.Text = "chào bot"
	opened, err := planPersonalZaloTurn(t.Context(), session, base)
	if err != nil || opened.RunFlow || opened.Reply != "xin chào" || opened.FlowScope == "" {
		t.Fatalf("open turn = %+v, err=%v", opened, err)
	}
	base.Text = "tư vấn giúp tôi"
	active, err := planPersonalZaloTurn(t.Context(), session, base)
	if err != nil || !active.RunFlow || active.FlowScope != opened.FlowScope {
		t.Fatalf("active turn = %+v, err=%v; opened=%+v", active, err, opened)
	}
	ttl, err := client.TTL(t.Context(), personalZaloSessionKey(base)).Result()
	if err != nil || ttl <= 6*time.Minute || ttl > 7*time.Minute {
		t.Fatalf("session TTL=%s, err=%v", ttl, err)
	}
	base.Text = "chào bot"
	reopened, err := planPersonalZaloTurn(t.Context(), session, base)
	if err != nil || reopened.FlowScope == opened.FlowScope {
		t.Fatalf("reopen did not isolate history: opened=%+v reopened=%+v err=%v", opened, reopened, err)
	}
	base.Text = "tạm biệt"
	closed, err := planPersonalZaloTurn(t.Context(), session, base)
	if err != nil || closed.Reply != "hẹn gặp lại" {
		t.Fatalf("close turn = %+v, err=%v", closed, err)
	}
	base.Text = "tư vấn tiếp"
	afterClose, err := planPersonalZaloTurn(t.Context(), session, base)
	if err != nil || afterClose.RunFlow || afterClose.Reply != "" {
		t.Fatalf("turn after close = %+v, err=%v", afterClose, err)
	}

	groupOne := base
	groupOne.ThreadType, groupOne.ThreadID, groupOne.SenderID, groupOne.Text = "group", "999", "456", "Bull ơi kiểm tra giúp"
	groupTwo := groupOne
	groupTwo.SenderID = "789"
	keys = append(keys, personalZaloSessionKey(groupOne), personalZaloSessionKey(groupTwo))
	first, err := planPersonalZaloTurn(t.Context(), session, groupOne)
	if err != nil || !first.RunFlow {
		t.Fatalf("first group turn = %+v, err=%v", first, err)
	}
	second, err := planPersonalZaloTurn(t.Context(), session, groupTwo)
	if err != nil || !second.RunFlow || second.FlowScope == first.FlowScope {
		t.Fatalf("per-sender group isolation failed: first=%+v second=%+v err=%v", first, second, err)
	}
	if err := client.Expire(t.Context(), personalZaloSessionKey(groupOne), -time.Second).Err(); err != nil {
		t.Fatal(err)
	}
	afterTimeout, err := planPersonalZaloTurn(t.Context(), session, groupOne)
	if err != nil || !afterTimeout.RunFlow || afterTimeout.FlowScope == first.FlowScope {
		t.Fatalf("group turn after timeout = %+v, err=%v", afterTimeout, err)
	}
	groupOne.Text = "mọi người cứ trao đổi"
	silent, err := planPersonalZaloTurn(t.Context(), session, groupOne)
	if err != nil || silent.RunFlow || silent.Reply != "" {
		t.Fatalf("unaddressed active group turn = %+v, err=%v", silent, err)
	}
}
