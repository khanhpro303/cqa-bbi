package engine

import (
	"context"
	"testing"

	"github.com/vietbui/chat-quality-agent/db"
)

// TestAcquireSessionLockNilRedis verifies the nil-Redis contract: with no Redis
// configured the worker skips locking entirely, so AcquireSessionLock must
// report failure (not panic) and ReleaseSessionLock must be a no-op.
func TestAcquireSessionLockNilRedis(t *testing.T) {
	if db.RedisClient != nil {
		t.Skip("Redis configured; this test only covers the nil-Redis contract")
	}
	if token, ok := AcquireSessionLock(context.Background(), "zalo_session:c1:u1:lock"); ok || token != "" {
		t.Errorf("AcquireSessionLock with nil Redis = (%q, %v); want (\"\", false)", token, ok)
	}
	// Must not panic on a no-op release.
	ReleaseSessionLock(context.Background(), "zalo_session:c1:u1:lock", "any-token")
}

// TestSessionLockOwnerToken exercises the compare-and-del release against a live
// Redis: a second acquirer must not get the lock while held, and a release with
// the wrong token must NOT delete a lock owned by someone else.
func TestSessionLockOwnerToken(t *testing.T) {
	if db.RedisClient == nil {
		t.Skip("needs a live Redis client to exercise SetNX + compare-and-del")
	}
	ctx := context.Background()
	key := "zalo_session:test-lock:owner:lock"
	db.RedisClient.Del(ctx, key)
	defer db.RedisClient.Del(ctx, key)

	tokenA, okA := AcquireSessionLock(ctx, key)
	if !okA || tokenA == "" {
		t.Fatalf("first AcquireSessionLock = (%q, %v); want a token and true", tokenA, okA)
	}

	// While A holds it, B cannot acquire.
	if tokenB, okB := AcquireSessionLock(ctx, key); okB || tokenB != "" {
		t.Errorf("second AcquireSessionLock while held = (%q, %v); want (\"\", false)", tokenB, okB)
	}

	// A release with a foreign token must NOT delete A's lock.
	ReleaseSessionLock(ctx, key, "not-the-owner")
	if tokenB, okB := AcquireSessionLock(ctx, key); okB || tokenB != "" {
		t.Errorf("after foreign-token release, lock was wrongly freed = (%q, %v)", tokenB, okB)
	}

	// A's own release frees it; B can now acquire.
	ReleaseSessionLock(ctx, key, tokenA)
	if tokenB, okB := AcquireSessionLock(ctx, key); !okB || tokenB == "" {
		t.Errorf("after owner release, AcquireSessionLock = (%q, %v); want a token and true", tokenB, okB)
	}
}
