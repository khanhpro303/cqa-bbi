package handlers

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/vietbui/chat-quality-agent/config"
	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/db/models"
)

func TestPersonalZaloTimeoutNoticeConfig(t *testing.T) {
	cfg := &config.Config{ChatbotSessionTimeout: 30, ChatbotSessionTimeoutMessage: "mặc định"}
	off, err := parsePersonalZaloSessionConfig(models.Channel{Metadata: `{}`}, cfg)
	if err != nil || off.timeoutNoticeOn() || off.Managed || off.sessionTTL() != 30*time.Minute {
		t.Fatalf("unset notice = %+v (ttl %s), err=%v; want off, unmanaged, 30m", off, off.sessionTTL(), err)
	}
	if off.SessionTimeoutMessage != "mặc định" {
		t.Fatalf("default timeout message = %q", off.SessionTimeoutMessage)
	}
	on, err := parsePersonalZaloSessionConfig(models.Channel{Metadata: `{"session_timeout_notice":true,"session_timeout_message":"Em xin phép đóng phiên ạ"}`}, cfg)
	if err != nil || !on.timeoutNoticeOn() || !on.Managed || on.sessionTTL() != 31*time.Minute ||
		on.SessionTimeoutMessage != "Em xin phép đóng phiên ạ" {
		t.Fatalf("notice on = %+v (ttl %s), err=%v", on, on.sessionTTL(), err)
	}
}

func TestPersonalZaloTimeoutNoticeWanted(t *testing.T) {
	yes := true
	session := personalZaloSessionConfig{SessionTimeoutNotice: &yes, SessionTimeoutMinutes: 30}
	open := personalZaloTurn{SessionKey: "k", SessionID: "s"}
	direct := personalZaloChatbotRequest{ThreadType: personalZaloThreadUser}
	group := personalZaloChatbotRequest{ThreadType: personalZaloThreadGroup}
	assistant := session
	assistant.GroupAssistantMode = true
	tests := []struct {
		name    string
		session personalZaloSessionConfig
		req     personalZaloChatbotRequest
		turn    personalZaloTurn
		staff   bool
		want    bool
	}{
		{"direct", session, direct, open, false, true},
		{"direct staff", session, direct, open, true, true},
		{"switch off", personalZaloSessionConfig{}, direct, open, false, false},
		{"no session", session, direct, personalZaloTurn{}, false, false},
		{"group customer", session, group, open, false, true},
		{"group staff", session, group, open, true, false},
		{"group assistant mode", assistant, group, open, false, false},
		{"direct in assistant mode", assistant, direct, open, false, true},
	}
	for _, tt := range tests {
		if got := personalZaloTimeoutNoticeWanted(tt.session, tt.req, tt.turn, tt.staff); got != tt.want {
			t.Errorf("%s: wanted=%t, want %t", tt.name, got, tt.want)
		}
	}
}

func TestPersonalZaloIdleSessionClose(t *testing.T) {
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

	yes := true
	session := personalZaloSessionConfig{SessionTimeoutNotice: &yes, SessionTimeoutMinutes: 7, Managed: true}
	req := personalZaloChatbotRequest{
		TenantID: uuid.NewString(), ChannelID: uuid.NewString(), AccountExternalID: "123",
		ThreadID: "456", ThreadType: "user", SenderID: "456", Text: "tư vấn giúp tôi",
	}
	key := personalZaloSessionKey(req)
	t.Cleanup(func() { _ = client.Del(context.Background(), key).Err() })

	turn, err := planPersonalZaloTurn(t.Context(), session, req, false)
	if err != nil || !turn.RunFlow || turn.SessionKey != key || turn.SessionID == "" {
		t.Fatalf("turn = %+v, err=%v", turn, err)
	}
	if ttl := client.TTL(t.Context(), key).Val(); ttl <= 7*time.Minute || ttl > 8*time.Minute {
		t.Fatalf("session TTL=%s, want timeout + 1 minute", ttl)
	}
	run := func(id string) int {
		got, err := closeIdlePersonalZaloSession.Run(t.Context(), client, []string{key}, id, personalZaloTimeoutGrace.Milliseconds()).Int()
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	if got := run(turn.SessionID); got != -1 {
		t.Fatalf("session used since: close=%d, want -1", got)
	}
	if got := run("another-session"); got != 0 {
		t.Fatalf("replaced session: close=%d, want 0", got)
	}
	client.Expire(t.Context(), key, time.Minute)
	if got := run(turn.SessionID); got != 1 {
		t.Fatalf("idle session: close=%d, want 1", got)
	}
	if client.Exists(t.Context(), key).Val() != 0 {
		t.Fatal("idle close must delete the session")
	}
	if got := run(turn.SessionID); got != 0 {
		t.Fatalf("second close=%d, want 0", got)
	}
}
