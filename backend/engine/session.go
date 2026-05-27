package engine

import (
	"context"
	"fmt"

	"github.com/vietbui/chat-quality-agent/db"
)

// SessionKey returns the Redis key used to store the active Langflow session
// ID for a Zalo user. When zaloGroupID is non-empty the session is keyed by
// the group (one session shared by all members of the group); otherwise it is
// keyed by the individual sender.
//
// Mirrors the construction in backend/workers/tasks.go where the session is
// created. Keep both call sites in lock-step — any change here must be
// reflected in workers/tasks.go and vice versa.
func SessionKey(channelID, zaloUserID, zaloGroupID string) string {
	if zaloGroupID != "" {
		return fmt.Sprintf("zalo_session:%s:group:%s", channelID, zaloGroupID)
	}
	return fmt.Sprintf("zalo_session:%s:%s", channelID, zaloUserID)
}

// ResolveActiveSessionID reads the active session ID from Redis. Returns "" if
// Redis is not configured, the key is missing, or the read fails — callers
// should treat empty as "no active session" and skip downstream work that
// requires a session ID (e.g. persisting chat history).
func ResolveActiveSessionID(ctx context.Context, channelID, zaloUserID, zaloGroupID string) string {
	if db.RedisClient == nil {
		return ""
	}
	key := SessionKey(channelID, zaloUserID, zaloGroupID)
	val, err := db.RedisClient.Get(ctx, key).Result()
	if err != nil {
		return ""
	}
	return val
}
