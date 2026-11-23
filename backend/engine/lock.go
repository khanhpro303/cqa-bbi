package engine

import (
	"context"
	"errors"
	"log"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"github.com/vietbui/chat-quality-agent/db"
)

// SessionLockTTL bounds how long a single Zalo message may hold its per-session
// processing lock. It MUST comfortably exceed the worst-case end-to-end
// processing time — the Langflow client timeout is 60s (see
// engine.NewLangflowClient) plus ERP round-trips — so the lock never
// auto-expires while its holder is still working. The previous 45s TTL was
// SHORTER than the 60s Langflow ceiling, so a long AI turn let the lock lapse
// mid-flight and a second message could acquire the "free" lock and run
// concurrently against the same session state. 90s gives headroom over 60s.
const SessionLockTTL = 90 * time.Second

// releaseLockScript deletes the lock key only if it still holds the caller's
// token. This makes release a compare-and-del: a worker can never delete a lock
// that a *later* worker acquired after this one's TTL lapsed (the classic
// unconditional-DEL bug). Atomic via Lua so the get+del cannot interleave.
var releaseLockScript = redis.NewScript(`
if redis.call("get", KEYS[1]) == ARGV[1] then
	return redis.call("del", KEYS[1])
end
return 0
`)

// AcquireSessionLock attempts to take the processing lock at lockKey with a
// unique owner token. Returns (token, true) on success; the caller passes the
// token back to ReleaseSessionLock. Returns ("", false) when the lock is held
// by another worker or Redis errors. Callers gate on db.RedisClient != nil
// before locking, but this is defensive when Redis is unavailable.
func AcquireSessionLock(ctx context.Context, lockKey string) (string, bool) {
	if db.RedisClient == nil {
		return "", false
	}
	token := uuid.NewString()
	ok, err := db.RedisClient.SetNX(ctx, lockKey, token, SessionLockTTL).Result()
	if err != nil || !ok {
		return "", false
	}
	return token, true
}

// ReleaseSessionLock releases a lock previously taken by AcquireSessionLock, but
// only if it still belongs to this token (compare-and-del). No-op when Redis is
// unavailable or token is empty, so it is always safe to defer.
func ReleaseSessionLock(ctx context.Context, lockKey, token string) {
	if db.RedisClient == nil || token == "" {
		return
	}
	_ = releaseLockScript.Run(ctx, db.RedisClient, []string{lockKey}, token).Err()
}

// extendLockScript resets the lock's TTL only if it still holds the caller's
// token (compare-and-pexpire).
var extendLockScript = redis.NewScript(`
if redis.call("get", KEYS[1]) == ARGV[1] then
	return redis.call("pexpire", KEYS[1], ARGV[2])
end
return 0
`)

// ExtendSessionLock gives the holder of lockKey a fresh SessionLockTTL, for a
// turn that spent time on a step before its Langflow call (reading images).
// It reports whether the lease was renewed; a Redis error is retried once.
func ExtendSessionLock(ctx context.Context, lockKey, token string) (bool, error) {
	if db.RedisClient == nil || token == "" {
		return true, nil
	}
	var err error
	for attempt := 0; attempt < 2; attempt++ {
		var n int
		n, err = extendLockScript.Run(ctx, db.RedisClient, []string{lockKey}, token, SessionLockTTL.Milliseconds()).Int()
		if err == nil {
			if n != 1 {
				return false, errors.New("lock held by another turn")
			}
			return true, nil
		}
		log.Printf("[lock] extend %s: %v", lockKey, err)
	}
	return false, err
}
