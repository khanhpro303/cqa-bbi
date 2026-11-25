package engine

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/vietbui/chat-quality-agent/db"
)

func TestUnansweredLookupReceiptIsScopedToAttempt(t *testing.T) {
	client := unansweredHandoverTestRedis(t)
	previous := db.RedisClient
	db.RedisClient = client
	t.Cleanup(func() { db.RedisClient = previous })
	perms := GroupPermissionContext{
		AgentType: "public", TenantID: "tenant", ChannelID: "channel",
		ZaloUserID: "customer", TurnID: "turn", UnansweredLookupID: uuid.NewString(),
	}
	t.Cleanup(func() { _ = client.Del(context.Background(), unansweredLookupKey(&perms)).Err() })

	if HasUnansweredLookup(t.Context(), &perms) {
		t.Fatal("attempt has a receipt before a tool recorded it")
	}
	RecordUnansweredLookup(t.Context(), &perms)
	if !HasUnansweredLookup(t.Context(), &perms) {
		t.Fatal("completed scoped lookup did not record a receipt")
	}

	retry := perms
	retry.UnansweredLookupID = uuid.NewString()
	if HasUnansweredLookup(t.Context(), &retry) {
		t.Fatal("a retry accepted an earlier attempt's receipt")
	}
	private := perms
	private.AgentType = "private"
	if HasUnansweredLookup(t.Context(), &private) {
		t.Fatal("a private staff turn accepted a public lookup receipt")
	}
}
