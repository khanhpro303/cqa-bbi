package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/vietbui/chat-quality-agent/channels"
	"github.com/vietbui/chat-quality-agent/db"
)

// PendingOptionsSuffix is appended to a session key to store the ordered list
// of postback strings most recently presented to the user as a numbered menu
// (e.g. the ten_dong_bo_web options in the inventory "dongsp" flow, or the
// dòng-vs-SKU Level-1 picker). A later bare-number reply ("1"/"2"/"3") is
// resolved against this list so the worker can replay the chosen postback
// without round-tripping through Langflow.
const PendingOptionsSuffix = ":pending_options"

// BuildSessionKey returns the Redis session key shared by the worker and the
// ERP handler so both ends read/write the same pending_options entry. For
// group chats pass the Zalo group ID (zaloGroupID != ""); otherwise pass the
// individual zaloUserID and leave zaloGroupID blank.
func BuildSessionKey(channelID, zaloUserID, zaloGroupID string) string {
	if zaloGroupID != "" {
		return fmt.Sprintf("zalo_session:%s:group:%s", channelID, zaloGroupID)
	}
	return fmt.Sprintf("zalo_session:%s:%s", channelID, zaloUserID)
}

// ResolveNumericSelection resolves a bare-number reply against a previously
// presented numbered option list. It returns:
//   - payload: the stored postback at index n-1 (only when inRange is true)
//   - matched: true when text is a canonical positive integer and options is non-empty
//   - inRange: true when the parsed number falls within [1..len(options)]
//
// matched=true with inRange=false means the user typed a number but it is out
// of range — the caller should ask them to pick again while keeping the menu.
func ResolveNumericSelection(text string, options []string) (payload string, matched bool, inRange bool) {
	if len(options) == 0 {
		return "", false, false
	}
	trimmed := strings.TrimSpace(text)
	n, err := strconv.Atoi(trimmed)
	// Reject non-canonical numeric forms ("01", "+1", "") so only clean menu
	// picks are treated as selections.
	if err != nil || strconv.Itoa(n) != trimmed {
		return "", false, false
	}
	if n >= 1 && n <= len(options) {
		return options[n-1], true, true
	}
	return "", true, false
}

// StorePendingOptions persists the postbacks of a numbered menu under the
// session key so a later bare-number reply can replay the chosen option. TTL
// follows the session timeout (minutes), plus a 1-minute grace so the entry
// outlives the on-screen prompt. No-op when Redis is unavailable or the menu
// is empty.
func StorePendingOptions(ctx context.Context, sessionKey string, buttons []channels.ZaloOAButton, timeoutMinutes int) {
	if db.RedisClient == nil || len(buttons) == 0 {
		return
	}
	postbacks := make([]string, len(buttons))
	for i, b := range buttons {
		postbacks[i] = b.Payload
	}
	raw, err := json.Marshal(postbacks)
	if err != nil {
		log.Printf("[session_options] failed to marshal pending options for %s: %v", sessionKey, err)
		return
	}
	ttl := time.Duration(timeoutMinutes)*time.Minute + 1*time.Minute
	db.RedisClient.Set(ctx, sessionKey+PendingOptionsSuffix, raw, ttl)
}

// AwaitingVariantLineSuffix is appended to a session key to remember the exact
// product line (TEN_DONG_BO_WEB) the customer just picked when they chose
// "🔍 xem theo mã SKU cụ thể". The follow-up color/size reply arrives as free
// text and is routed to Langflow; without this lock the Agent re-derives a bare
// model code ("FF901") that LIKE-spans sibling lines ("LS2 FF901" vs
// "LS2 FF901 Carbon") and answers for both. The worker reads this on the next
// free-text turn to scope resolution to the chosen line.
const AwaitingVariantLineSuffix = ":awaiting_variant_line"

// StoreAwaitingVariantLine records the product line the customer chose at the
// skucuthe step so the next free-text (color/size) turn can be scoped to it.
// TTL follows the session timeout plus a 1-minute grace. No-op when Redis is
// unavailable or the line is empty.
func StoreAwaitingVariantLine(ctx context.Context, sessionKey, webName string, timeoutMinutes int) {
	if db.RedisClient == nil || strings.TrimSpace(webName) == "" {
		return
	}
	ttl := time.Duration(timeoutMinutes)*time.Minute + 1*time.Minute
	db.RedisClient.Set(ctx, sessionKey+AwaitingVariantLineSuffix, webName, ttl)
}

// TakeAwaitingVariantLine returns and deletes (single-use) the product line
// stored by StoreAwaitingVariantLine. Returns "" when no lock is set or Redis
// is unavailable. Single-use semantics keep the lock from leaking into
// unrelated later turns.
func TakeAwaitingVariantLine(ctx context.Context, sessionKey string) string {
	if db.RedisClient == nil {
		return ""
	}
	key := sessionKey + AwaitingVariantLineSuffix
	val, err := db.RedisClient.Get(ctx, key).Result()
	if err != nil || strings.TrimSpace(val) == "" {
		return ""
	}
	db.RedisClient.Del(ctx, key)
	return val
}

// PendingIntentSuffix is appended to a session key to remember whether the
// question that triggered a `products` disambiguation list was a PRICE ask or a
// STOCK ask. A later numeric pick on that list ("1"/"2") is intercepted
// deterministically by the worker and arrives with NO intent of its own, so the
// triggering question's intent is the only place to learn what the customer
// actually wanted. Without it every pick falls into the stock picker — the
// documented "đánh đổi intent GIÁ" where a price question silently turns into a
// tồn-kho prompt. The worker writes this on every Langflow-bound turn (so it
// always reflects the latest real question and never goes stale) and consumes it
// when resolving the pick.
const PendingIntentSuffix = ":pending_intent"

// Pending product-intent values stored under PendingIntentSuffix.
const (
	PendingIntentPrice = "PRICE"
	PendingIntentStock = "STOCK"
)

// StorePendingIntent records the product intent (PendingIntentPrice /
// PendingIntentStock) of the current question so a later disambiguation pick can
// be routed without an LLM round-trip. TTL follows the session timeout plus a
// 1-minute grace. No-op when Redis is unavailable or the intent is empty.
func StorePendingIntent(ctx context.Context, sessionKey, intent string, timeoutMinutes int) {
	if db.RedisClient == nil || intent == "" {
		return
	}
	ttl := time.Duration(timeoutMinutes)*time.Minute + 1*time.Minute
	db.RedisClient.Set(ctx, sessionKey+PendingIntentSuffix, intent, ttl)
}

// TakePendingIntent returns and deletes (single-use) the intent stored by
// StorePendingIntent. Returns "" when no intent is set or Redis is unavailable.
// Single-use semantics keep a resolved pick's intent from leaking into a later
// unrelated disambiguation.
func TakePendingIntent(ctx context.Context, sessionKey string) string {
	if db.RedisClient == nil {
		return ""
	}
	key := sessionKey + PendingIntentSuffix
	val, err := db.RedisClient.Get(ctx, key).Result()
	if err != nil || strings.TrimSpace(val) == "" {
		return ""
	}
	db.RedisClient.Del(ctx, key)
	return val
}

// AwaitingFollowupSuffix is appended to a session key to mark that the backend
// just pushed a Zalo prompt that expects the customer's next message as an
// answer — e.g. the debt period question (Tháng này / Tháng trước / Quý này),
// the orders date-range prompt (3/5/7 ngày), or the inventory dòng-vs-SKU
// picker. Those answers arrive as ordinary short text ("tháng này", "quý 2",
// "tuần qua") that the CASUAL/HANDOVER intent classifier would otherwise drop
// as small talk, closing the session and silencing the bot mid-flow. The
// worker sets this marker whenever it suppresses on the [RICH_MESSAGE_SENT]
// sentinel and consumes it on the next turn to force IN_SCOPE exactly once.
const AwaitingFollowupSuffix = ":awaiting_followup"

// StoreAwaitingFollowup records that a backend-pushed prompt is awaiting the
// customer's reply on the next turn. TTL follows the session timeout plus a
// 1-minute grace so the marker outlives the on-screen prompt. No-op when Redis
// is unavailable.
func StoreAwaitingFollowup(ctx context.Context, sessionKey string, timeoutMinutes int) {
	if db.RedisClient == nil {
		return
	}
	ttl := time.Duration(timeoutMinutes)*time.Minute + 1*time.Minute
	db.RedisClient.Set(ctx, sessionKey+AwaitingFollowupSuffix, "1", ttl)
}

// TakeAwaitingFollowup reports whether a follow-up marker was set and deletes it
// (single-use). Returns false when no marker is set or Redis is unavailable.
// Single-use semantics bypass the intent classifier for only the one
// continuation turn, so genuinely casual chatter on later turns is still
// classified normally.
func TakeAwaitingFollowup(ctx context.Context, sessionKey string) bool {
	if db.RedisClient == nil {
		return false
	}
	key := sessionKey + AwaitingFollowupSuffix
	val, err := db.RedisClient.Get(ctx, key).Result()
	if err != nil || strings.TrimSpace(val) == "" {
		return false
	}
	db.RedisClient.Del(ctx, key)
	return true
}
