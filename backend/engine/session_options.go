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

// Product price-vs-stock intent is no longer stored as a session sidecar. The
// Agent now classifies it on the original question and the ERP handler bakes it
// into the #stockpick_web pending-option postbacks (see
// engine.BuildStockPickPendingButtons / ParseStockPickWeb), so the numeric pick
// replays the captured intent verbatim — a single source of truth in the Agent,
// not a second keyword classifier in the worker.

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

// AwaitingOrderCustomerSuffix is appended to a session key to carry the
// private-bot (employee) "orders by customer" flow across turns. Unlike
// AwaitingFollowup — a bare flag — this entry holds a payload: the customer
// codes the staff member is asking about (already intersected with their
// scope, so the later time-range turn can never widen access) plus the stage
// the flow is paused at. It outlives a single prompt: the staff member first
// names a customer, then answers the 3/5/7-ngày prompt, and only that second
// turn carries the resolved codes — they would otherwise be lost because the
// reply ("7 ngày gần đây") has no customer token.
const AwaitingOrderCustomerSuffix = ":awaiting_order_customer"

// Order-customer flow stages stored in OrderCustomerState.Stage.
const (
	// OrderCustomerStagePick — the name matched several customers; the next
	// turn picks one (a code/name) or "tất cả" to keep them all.
	OrderCustomerStagePick = "pick"
	// OrderCustomerStageTimeRange — the customer is resolved; the next turn
	// supplies the date window (3/5/7 ngày).
	OrderCustomerStageTimeRange = "timerange"
)

// OrderCustomerState is the cross-turn payload for the private-bot orders flow.
// Codes are bare customer codes (e.g. "S001") that have ALREADY been
// intersected with the staff member's scope when stored, so replaying them on
// a later turn cannot leak orders outside their allowed set.
type OrderCustomerState struct {
	Stage string   `json:"stage"`
	Codes []string `json:"codes"`
}

// StoreOrderCustomerState persists the orders-by-customer flow state under the
// session key so the next free-text turn (a customer pick or a date window)
// can resume it. TTL follows the session timeout plus a 1-minute grace. No-op
// when Redis is unavailable or the stage is empty.
func StoreOrderCustomerState(ctx context.Context, sessionKey string, state OrderCustomerState, timeoutMinutes int) {
	if db.RedisClient == nil || strings.TrimSpace(state.Stage) == "" {
		return
	}
	raw, err := json.Marshal(state)
	if err != nil {
		log.Printf("[session_options] failed to marshal order-customer state for %s: %v", sessionKey, err)
		return
	}
	ttl := time.Duration(timeoutMinutes)*time.Minute + 1*time.Minute
	db.RedisClient.Set(ctx, sessionKey+AwaitingOrderCustomerSuffix, raw, ttl)
}

// TakeOrderCustomerState returns and deletes (single-use) the orders-by-customer
// flow state. ok is false when no state is set, Redis is unavailable, or the
// stored value cannot be decoded. Single-use semantics keep a stale customer
// scope from leaking into unrelated later turns.
func TakeOrderCustomerState(ctx context.Context, sessionKey string) (OrderCustomerState, bool) {
	if db.RedisClient == nil {
		return OrderCustomerState{}, false
	}
	key := sessionKey + AwaitingOrderCustomerSuffix
	val, err := db.RedisClient.Get(ctx, key).Result()
	if err != nil || strings.TrimSpace(val) == "" {
		return OrderCustomerState{}, false
	}
	db.RedisClient.Del(ctx, key)
	var state OrderCustomerState
	if err := json.Unmarshal([]byte(val), &state); err != nil || strings.TrimSpace(state.Stage) == "" {
		return OrderCustomerState{}, false
	}
	return state, true
}

// AwaitingDebtCustomerSuffix is the debt-flow twin of
// AwaitingOrderCustomerSuffix: it carries the private-bot "debt by customer"
// flow across turns. The staff member first names a customer, then answers the
// "tháng này / tháng trước / quý này" prompt; only that second turn carries the
// resolved codes (the reply "tháng trước" has no customer token), so the codes
// are stashed here. A separate suffix keeps an in-flight orders flow and an
// in-flight debt flow from colliding on the same session key.
const AwaitingDebtCustomerSuffix = ":awaiting_debt_customer"

// Debt-customer flow stages stored in DebtCustomerState.Stage.
const (
	// DebtCustomerStagePick — the name matched several customers; the next
	// turn picks one (a code/name). Mirrors OrderCustomerStagePick.
	DebtCustomerStagePick = "pick"
	// DebtCustomerStagePeriod — the customer is resolved; the next turn
	// supplies the period (tháng này / tháng trước / quý này).
	DebtCustomerStagePeriod = "period"
)

// DebtCustomerState is the cross-turn payload for the private-bot debt flow.
// Codes are bare customer codes (e.g. "S001") that have ALREADY been
// intersected with the staff member's scope when stored, so replaying them on
// a later turn cannot leak another customer's debt.
type DebtCustomerState struct {
	Stage string   `json:"stage"`
	Codes []string `json:"codes"`
}

// StoreDebtCustomerState persists the debt-by-customer flow state under the
// session key so the next free-text turn (a customer pick or a period word)
// can resume it. TTL follows the session timeout plus a 1-minute grace. No-op
// when Redis is unavailable or the stage is empty.
func StoreDebtCustomerState(ctx context.Context, sessionKey string, state DebtCustomerState, timeoutMinutes int) {
	if db.RedisClient == nil || strings.TrimSpace(state.Stage) == "" {
		return
	}
	raw, err := json.Marshal(state)
	if err != nil {
		log.Printf("[session_options] failed to marshal debt-customer state for %s: %v", sessionKey, err)
		return
	}
	ttl := time.Duration(timeoutMinutes)*time.Minute + 1*time.Minute
	db.RedisClient.Set(ctx, sessionKey+AwaitingDebtCustomerSuffix, raw, ttl)
}

// TakeDebtCustomerState returns and deletes (single-use) the debt-by-customer
// flow state. ok is false when no state is set, Redis is unavailable, or the
// stored value cannot be decoded. Single-use semantics keep a stale customer
// scope from leaking into unrelated later turns.
func TakeDebtCustomerState(ctx context.Context, sessionKey string) (DebtCustomerState, bool) {
	if db.RedisClient == nil {
		return DebtCustomerState{}, false
	}
	key := sessionKey + AwaitingDebtCustomerSuffix
	val, err := db.RedisClient.Get(ctx, key).Result()
	if err != nil || strings.TrimSpace(val) == "" {
		return DebtCustomerState{}, false
	}
	db.RedisClient.Del(ctx, key)
	var state DebtCustomerState
	if err := json.Unmarshal([]byte(val), &state); err != nil || strings.TrimSpace(state.Stage) == "" {
		return DebtCustomerState{}, false
	}
	return state, true
}
