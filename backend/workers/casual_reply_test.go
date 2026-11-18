package workers

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/vietbui/chat-quality-agent/db"
)

func TestReplyToCasualTurn(t *testing.T) {
	client := redis.NewClient(&redis.Options{})
	client.AddHook(casualWarningRedisHook{values: make(map[string]string)})
	previous := db.RedisClient
	db.RedisClient = client
	t.Cleanup(func() {
		db.RedisClient = previous
		_ = client.Close()
	})

	sendFailure := errors.New("Zalo unavailable")
	tests := []struct {
		name, owner, message string
		sendErr              error
		end                  bool
	}{
		{"failed first warning", "session1:alice", botCasualWarning, sendFailure, false},
		{"first warning retried", "session1:alice", botCasualWarning, nil, false},
		{"different sender", "session1:bob", botCasualWarning, nil, false},
		{"new session", "session2:bob", botCasualWarning, nil, false},
		{"failed closing reply", "session2:bob", botCasualSessionEnded, sendFailure, false},
		{"second consecutive message", "session2:bob", botCasualSessionEnded, nil, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var sent string
			end, err := replyToCasualTurn(context.Background(), "test:casual_warning", tt.owner, time.Minute, func(message string) error {
				sent = message
				return tt.sendErr
			})
			if !errors.Is(err, tt.sendErr) || end != tt.end || sent != tt.message {
				t.Fatalf("reply = %q, end = %v, err = %v; want %q, %v, %v", sent, end, err, tt.message, tt.end, tt.sendErr)
			}
		})
	}
}

type casualWarningRedisHook struct {
	values map[string]string
}

func (casualWarningRedisHook) DialHook(next redis.DialHook) redis.DialHook { return next }

func (casualWarningRedisHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return next
}

func (h casualWarningRedisHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		switch cmd.Name() {
		case "get":
			value, ok := h.values[cmd.Args()[1].(string)]
			if !ok {
				return redis.Nil
			}
			cmd.(*redis.StringCmd).SetVal(value)
		case "set":
			h.values[cmd.Args()[1].(string)] = cmd.Args()[2].(string)
		default:
			return next(ctx, cmd)
		}
		return nil
	}
}
