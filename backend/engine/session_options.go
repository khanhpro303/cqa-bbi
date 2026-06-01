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
