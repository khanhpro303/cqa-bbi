package workers

import (
	"context"
	"strconv"
	"testing"

	"github.com/redis/go-redis/v9"

	"github.com/vietbui/chat-quality-agent/db"
)

// counterRedisHook serves GET, INCR and EXPIRE from memory.
type counterRedisHook struct{ values map[string]int }

func (counterRedisHook) DialHook(next redis.DialHook) redis.DialHook { return next }

func (counterRedisHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return next
}

func (h counterRedisHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		key, _ := cmd.Args()[1].(string)
		switch cmd.Name() {
		case "get":
			v, ok := h.values[key]
			if !ok {
				return redis.Nil
			}
			cmd.(*redis.StringCmd).SetVal(strconv.Itoa(v))
		case "incr":
			h.values[key]++
			cmd.(*redis.IntCmd).SetVal(int64(h.values[key]))
		case "expire":
			cmd.(*redis.BoolCmd).SetVal(true)
		default:
			return next(ctx, cmd)
		}
		return nil
	}
}

func TestVerifyAttemptsLockAfterFiveFailures(t *testing.T) {
	client := redis.NewClient(&redis.Options{})
	client.AddHook(counterRedisHook{values: map[string]int{}})
	previous := db.RedisClient
	db.RedisClient = client
	t.Cleanup(func() {
		db.RedisClient = previous
		_ = client.Close()
	})

	ctx := context.Background()
	for i := 0; i < verifyMaxFailures; i++ {
		if verifyAttemptsExceeded(ctx, "t1", "attacker") {
			t.Fatalf("locked after %d failures, want %d allowed", i, verifyMaxFailures)
		}
		recordVerifyFailure(ctx, "t1", "attacker")
	}
	if !verifyAttemptsExceeded(ctx, "t1", "attacker") {
		t.Fatal("sender is not locked after the maximum number of wrong codes")
	}
	if verifyAttemptsExceeded(ctx, "t1", "someone-else") || verifyAttemptsExceeded(ctx, "t2", "attacker") {
		t.Fatal("lock leaked to another sender or tenant")
	}
}
