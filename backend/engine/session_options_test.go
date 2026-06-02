package engine

import (
	"context"
	"testing"

	"github.com/vietbui/chat-quality-agent/db"
)

func TestResolveNumericSelection(t *testing.T) {
	opts := []string{
		"#show_macha_options_by_web:Áo gió",
		"#show_macha_options_by_web:Áo khoác",
		"#show_macha_options:FF901",
	}
	cases := []struct {
		name        string
		in          string
		options     []string
		wantPayload string
		wantMatched bool
		wantInRange bool
	}{
		{"first option", "1", opts, opts[0], true, true},
		{"last option", "3", opts, opts[2], true, true},
		{"with surrounding spaces", " 2 ", opts, opts[1], true, true},
		{"tab and newline", "\t2\n", opts, opts[1], true, true},
		{"out of range high", "5", opts, "", true, false},
		{"zero out of range", "0", opts, "", true, false},
		{"not a number", "áo", opts, "", false, false},
		{"mixed text", "1 cái", opts, "", false, false},
		{"leading zero non-canonical", "01", opts, "", false, false},
		{"empty text", "", opts, "", false, false},
		{"no pending options", "1", nil, "", false, false},
		{"empty options slice", "1", []string{}, "", false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			payload, matched, inRange := ResolveNumericSelection(tc.in, tc.options)
			if payload != tc.wantPayload {
				t.Errorf("payload = %q; want %q", payload, tc.wantPayload)
			}
			if matched != tc.wantMatched {
				t.Errorf("matched = %v; want %v", matched, tc.wantMatched)
			}
			if inRange != tc.wantInRange {
				t.Errorf("inRange = %v; want %v", inRange, tc.wantInRange)
			}
		})
	}
}

func TestBuildSessionKey(t *testing.T) {
	cases := []struct {
		name        string
		channelID   string
		zaloUserID  string
		zaloGroupID string
		want        string
	}{
		{"individual", "ch1", "u123", "", "zalo_session:ch1:u123"},
		{"group overrides user", "ch1", "u123", "g999", "zalo_session:ch1:group:g999"},
		{"empty user, no group", "ch1", "", "", "zalo_session:ch1:"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := BuildSessionKey(tc.channelID, tc.zaloUserID, tc.zaloGroupID)
			if got != tc.want {
				t.Errorf("BuildSessionKey(%q,%q,%q) = %q; want %q",
					tc.channelID, tc.zaloUserID, tc.zaloGroupID, got, tc.want)
			}
		})
	}
}

// TestAwaitingVariantLineNilRedisSafe verifies the variant-line lock helpers are
// nil-safe: with no Redis configured (db.RedisClient == nil) Store is a no-op and
// Take returns "" without panicking. The Redis round-trip itself is exercised at
// runtime; this guards the degraded path that fires in unit/local contexts.
func TestAwaitingVariantLineNilRedisSafe(t *testing.T) {
	if db.RedisClient != nil {
		t.Skip("Redis configured; this test only covers the nil-Redis contract")
	}
	ctx := context.Background()
	const sessionKey = "zalo_session:ch1:u123"

	// Must not panic with Redis unavailable.
	StoreAwaitingVariantLine(ctx, sessionKey, "LS2 FF901", 10)
	StoreAwaitingVariantLine(ctx, sessionKey, "", 10) // empty line is a no-op

	if got := TakeAwaitingVariantLine(ctx, sessionKey); got != "" {
		t.Errorf("TakeAwaitingVariantLine with nil Redis = %q; want \"\"", got)
	}
}

// TestAwaitingVariantLineSuffixStable pins the Redis key suffix so a rename can't
// silently split writer (skucuthe handler) from reader (Langflow fall-through).
func TestAwaitingVariantLineSuffixStable(t *testing.T) {
	if AwaitingVariantLineSuffix != ":awaiting_variant_line" {
		t.Errorf("AwaitingVariantLineSuffix = %q; want \":awaiting_variant_line\"", AwaitingVariantLineSuffix)
	}
}

// TestAwaitingFollowupNilRedisSafe verifies the follow-up marker helpers are
// nil-safe: with no Redis configured Store is a no-op and Take returns false
// without panicking. This guards the degraded path in unit/local contexts.

func TestAwaitingFollowupNilRedisSafe(t *testing.T) {
	if db.RedisClient != nil {
		t.Skip("Redis configured; this test only covers the nil-Redis contract")
	}
	ctx := context.Background()
	const sessionKey = "zalo_session:ch1:u123"

	// Must not panic with Redis unavailable.
	StoreAwaitingFollowup(ctx, sessionKey, 10)

	if got := TakeAwaitingFollowup(ctx, sessionKey); got {
		t.Errorf("TakeAwaitingFollowup with nil Redis = %v; want false", got)
	}
}

// TestAwaitingFollowupSuffixStable pins the Redis key suffix so a rename can't
// silently split writer (worker sentinel suppression) from reader (worker
// pre-classification bypass) — they share the session key and must agree.
func TestAwaitingFollowupSuffixStable(t *testing.T) {
	if AwaitingFollowupSuffix != ":awaiting_followup" {
		t.Errorf("AwaitingFollowupSuffix = %q; want \":awaiting_followup\"", AwaitingFollowupSuffix)
	}
}

// TestOrderCustomerStateNilRedisSafe verifies the orders-by-customer state
// helpers are nil-safe: with no Redis configured Store is a no-op and Take
// returns ok=false without panicking.
func TestOrderCustomerStateNilRedisSafe(t *testing.T) {
	if db.RedisClient != nil {
		t.Skip("Redis configured; this test only covers the nil-Redis contract")
	}
	ctx := context.Background()
	const sessionKey = "zalo_session:ch1:u123"

	// Must not panic with Redis unavailable.
	StoreOrderCustomerState(ctx, sessionKey, OrderCustomerState{Stage: OrderCustomerStageTimeRange, Codes: []string{"S001"}}, 10)
	StoreOrderCustomerState(ctx, sessionKey, OrderCustomerState{}, 10) // empty stage is a no-op

	if _, ok := TakeOrderCustomerState(ctx, sessionKey); ok {
		t.Errorf("TakeOrderCustomerState with nil Redis ok = true; want false")
	}
}

// TestOrderCustomerSuffixStable pins the Redis key suffix so a rename can't
// silently split the handler (writer at prompt time) from the handler (reader
// on the next turn) — they share the session key and must agree.
func TestOrderCustomerSuffixStable(t *testing.T) {
	if AwaitingOrderCustomerSuffix != ":awaiting_order_customer" {
		t.Errorf("AwaitingOrderCustomerSuffix = %q; want \":awaiting_order_customer\"", AwaitingOrderCustomerSuffix)
	}
}
