package engine

import (
	"context"
	"time"

	"github.com/vietbui/chat-quality-agent/db"
)

func unansweredLookupKey(p *GroupPermissionContext) string {
	if p == nil || p.AgentType != "public" || p.TenantID == "" || p.ChannelID == "" ||
		p.ZaloUserID == "" || p.TurnID == "" || p.UnansweredLookupID == "" {
		return ""
	}
	return "unanswered_lookup:" + p.TenantID + ":" + p.ChannelID + ":" + p.UnansweredLookupID
}

// RecordUnansweredLookup records a completed scoped tool call. The gateway
// supplies permissions from the worker's signed token, never from tool arguments.
func RecordUnansweredLookup(ctx context.Context, permissions *GroupPermissionContext) {
	if key := unansweredLookupKey(permissions); key != "" && db.RedisClient != nil {
		db.RedisClient.Set(ctx, key, "1", 5*time.Minute)
	}
}

// HasUnansweredLookup only accepts a receipt for this worker attempt.
func HasUnansweredLookup(ctx context.Context, permissions *GroupPermissionContext) bool {
	if key := unansweredLookupKey(permissions); key != "" && db.RedisClient != nil {
		return db.RedisClient.Get(ctx, key).Val() == "1"
	}
	return false
}
