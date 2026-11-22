package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

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

// perSenderGroupSessions, when true, scopes group-chat FLOW STATE (the
// processing lock, pending_options, and the awaiting_* sidecars) to the
// individual sender via a ":user:<zaloUserID>" segment, so two employees in the
// same group never share a pending menu/picker or block each other. The base
// activation key the worker writes inline stays group-level (one trigger
// activates the bot for the whole group). Default false → keys identical to
// before. Set ONCE at startup via SetPerSenderGroupSessions before any worker
// runs; read-only thereafter, so the plain bool needs no synchronisation.
var perSenderGroupSessions bool

// SetPerSenderGroupSessions wires the CHATBOT_GROUP_PER_SENDER_SESSIONS config
// flag into the key builder. Call once at startup.
func SetPerSenderGroupSessions(enabled bool) { perSenderGroupSessions = enabled }

// PerSenderGroupSessions reports whether group flow-state is scoped per sender.
func PerSenderGroupSessions() bool { return perSenderGroupSessions }

// BuildSessionKey returns the Redis key shared by the worker and the ERP handler
// for a turn's FLOW STATE (pending_options + awaiting_* sidecars + the
// processing lock) so both ends read/write the same entry. For group chats pass
// the Zalo group ID (zaloGroupID != ""); otherwise pass the individual
// zaloUserID and leave zaloGroupID blank.
//
// When per-sender group sessions are enabled, a group key additionally carries
// the sender (":user:<zaloUserID>") so each employee's flow state is private.
// With the feature off — the default — the returned string is byte-for-byte the
// same as before, which is what keeps 1:1 chats and existing group behaviour
// unchanged. Worker and ERP both route through this one function, so they can
// never disagree on the exact key.
func BuildSessionKey(channelID, zaloUserID, zaloGroupID string) string {
	if zaloGroupID != "" {
		if perSenderGroupSessions && zaloUserID != "" {
			return fmt.Sprintf("zalo_session:%s:group:%s:user:%s", channelID, zaloGroupID, zaloUserID)
		}
		return fmt.Sprintf("zalo_session:%s:group:%s", channelID, zaloGroupID)
	}
	return fmt.Sprintf("zalo_session:%s:%s", channelID, zaloUserID)
}

// sessionStateSuffixes lists every Redis key suffix that hangs off the base
// session key to carry cross-turn flow state (numbered menus, the variant-line
// continuation, the awaiting-followup marker, and the orders/debt by-customer
// state machines). The base session key (zalo_session:...) is keyed by
// channel+user/group and is therefore STABLE across open/close cycles — only the
// stored UUID rotates. So these sidecars must be torn down explicitly whenever a
// session ends; otherwise they outlive the conversation on their own TTL and
// leak into the next one (e.g. a stale debt/period state resuming after the
// staff member ended the chat and reopened it). Keep this list in sync with the
// Suffix constants below.
var sessionStateSuffixes = []string{
	PendingOptionsSuffix,
	AwaitingVariantLineSuffix,
	AwaitingFollowupSuffix,
	AwaitingOrderCustomerSuffix,
	AwaitingDebtCustomerSuffix,
	PromptDeliveredSuffix,
	PromptUndeliveredSuffix,
	CasualStreakSuffix,
	CasualPositionSuffix,
}

// ClearSessionState deletes the base session key AND every sidecar flow-state
// key hung off it, in a single Redis round-trip. Call this anywhere a session is
// torn down (end keyword, HANDOVER, idle timeout) and defensively before
// opening a fresh session so a prior unclean teardown cannot bleed stale state
// into the new conversation. No-op (nil) when Redis is unavailable. Returns the
// Redis DEL error, if any, so timeout/teardown callers can log it.
func ClearSessionState(ctx context.Context, sessionKey string) error {
	if db.RedisClient == nil {
		return nil
	}
	keys := make([]string, 0, len(sessionStateSuffixes)+1)
	keys = append(keys, sessionKey)
	for _, suffix := range sessionStateSuffixes {
		keys = append(keys, sessionKey+suffix)
	}
	return db.RedisClient.Del(ctx, keys...).Err()
}

// groupSessionScanCount is the COUNT hint passed to Redis SCAN when sweeping a
// group's session keys. It bounds work per round-trip without changing results.
const groupSessionScanCount = 200

// ClearGroupSessionState wipes every Redis session key belonging to a single
// GMF group — the base key "zalo_session:<channelID>:group:<zaloGroupID>" and
// all its flow-state sidecars — across ALL OA channels. Unlike ClearSessionState
// (which needs the exact session key, hence the channel ID), this is keyed only
// on the group's Zalo ID, so the owner-facing "clear cache" button truly wipes
// the group clean even if more than one channel ever served it. Two exact glob
// patterns are scanned (base + ":<suffix>" sidecars) rather than "<id>*" so a
// group ID that is a prefix of another can never over-match. Returns the number
// of keys deleted. No-op (0, nil) when Redis is unavailable.
func ClearGroupSessionState(ctx context.Context, zaloGroupID string) (int, error) {
	if db.RedisClient == nil {
		return 0, nil
	}
	if strings.TrimSpace(zaloGroupID) == "" {
		return 0, fmt.Errorf("clear group session: empty zalo group id")
	}
	patterns := []string{
		fmt.Sprintf("zalo_session:*:group:%s", zaloGroupID),   // base session key
		fmt.Sprintf("zalo_session:*:group:%s:*", zaloGroupID), // pending_options + awaiting_* sidecars
	}
	seen := make(map[string]struct{})
	keys := make([]string, 0)
	for _, pattern := range patterns {
		iter := db.RedisClient.Scan(ctx, 0, pattern, groupSessionScanCount).Iterator()
		for iter.Next(ctx) {
			key := iter.Val()
			if _, dup := seen[key]; dup {
				continue
			}
			seen[key] = struct{}{}
			keys = append(keys, key)
		}
		if err := iter.Err(); err != nil {
			return 0, fmt.Errorf("clear group session: scan %q: %w", pattern, err)
		}
	}
	if len(keys) == 0 {
		return 0, nil
	}
	if err := db.RedisClient.Del(ctx, keys...).Err(); err != nil {
		return 0, fmt.Errorf("clear group session: del %d keys: %w", len(keys), err)
	}
	return len(keys), nil
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

// CasualStreakSuffix is appended to a session key to count the small-talk
// turns in a row since the chat last asked something the bot works on. The
// key is a Redis sorted set of message ids scored by first arrival. A business
// turn deletes it; so does session teardown.
const CasualStreakSuffix = ":casual_streak"

// CasualPositionSuffix holds, per message id, the position BumpCasualStreak
// gave it. A business turn does not clear it, so a retry that runs after the
// streak was reset keeps the route it had the first time.
const CasualPositionSuffix = ":casual_position"

// bumpCasualStreakScript gives a message its position in the small-talk run
// atomically: an id that already has a position (from an earlier attempt,
// even across a reset) keeps it; otherwise it joins the run and the position
// is stored. KEYS: streak set, position hash. ARGV: msg id, score, ttl secs.
var bumpCasualStreakScript = redis.NewScript(`
local pos = redis.call("HGET", KEYS[2], ARGV[1])
if pos then
	return tonumber(pos)
end
redis.call("ZADD", KEYS[1], "NX", ARGV[2], ARGV[1])
redis.call("EXPIRE", KEYS[1], ARGV[3])
local rank = redis.call("ZRANK", KEYS[1], ARGV[1]) + 1
redis.call("HSET", KEYS[2], ARGV[1], rank)
redis.call("EXPIRE", KEYS[2], ARGV[3])
return rank
`)

// CasualHandedOff is the position stored for a message the small-talk model
// sent to the main bot. A retry of it goes straight to the main bot.
const CasualHandedOff = -1

// WasCasualHandoff reports whether msgID was already handed to the main bot
// by the small-talk model (an earlier attempt of this task).
func WasCasualHandoff(ctx context.Context, sessionKey, msgID string) bool {
	if db.RedisClient == nil || strings.TrimSpace(msgID) == "" {
		return false
	}
	v, err := db.RedisClient.HGet(ctx, sessionKey+CasualPositionSuffix, msgID).Int()
	return err == nil && v == CasualHandedOff
}

// BumpCasualStreak records msgID as a small-talk turn and returns its
// position in the run (1 for the first). A retry of the same msgID gets the
// position it got the first time, even across a reset, or CasualHandedOff.
// It returns 0 when Redis is unavailable, fails or msgID is empty. Callers
// send any result below 1 to the main bot.
func BumpCasualStreak(ctx context.Context, sessionKey, msgID string, timeoutMinutes int) int {
	if db.RedisClient == nil || strings.TrimSpace(msgID) == "" {
		return 0
	}
	if timeoutMinutes <= 0 {
		timeoutMinutes = 30
	}
	ttl := int((time.Duration(timeoutMinutes)*time.Minute + time.Minute) / time.Second)
	pos, err := bumpCasualStreakScript.Run(ctx, db.RedisClient,
		[]string{sessionKey + CasualStreakSuffix, sessionKey + CasualPositionSuffix},
		msgID, time.Now().UnixMicro(), ttl).Int()
	if err != nil {
		return 0
	}
	return pos
}

// MarkCasualHandoff records that msgID was handed to the main bot, so a retry
// (after a failed main-bot call) does not ask the small-talk model again.
func MarkCasualHandoff(ctx context.Context, sessionKey, msgID string) {
	if db.RedisClient == nil || strings.TrimSpace(msgID) == "" {
		return
	}
	db.RedisClient.HSet(ctx, sessionKey+CasualPositionSuffix, msgID, CasualHandedOff)
}

// ResetCasualStreak ends the small-talk run: the chat is back on business.
func ResetCasualStreak(ctx context.Context, sessionKey string) {
	if db.RedisClient == nil {
		return
	}
	db.RedisClient.Del(ctx, sessionKey+CasualStreakSuffix)
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

// PromptDeliveredSuffix / PromptUndeliveredSuffix record, per session work key,
// whether the ERP handler actually managed to push its Zalo prompt (stock
// picker, debt period, orders range…) during a Langflow tool call. The Agent
// answers [RICH_MESSAGE_SENT] either way, so without this receipt the worker
// suppressed its own reply even when the push had FAILED — the customer saw
// nothing at all. On failure the handler stores the prompt text so the worker
// can deliver it itself through the channel it already holds.
const (
	PromptDeliveredSuffix   = ":prompt_delivered"
	PromptUndeliveredSuffix = ":prompt_undelivered"
)

// promptReceiptTTL only has to outlive one Langflow round-trip.
const promptReceiptTTL = 10 * time.Minute

// RecordPromptDelivery stores the outcome of a backend-pushed prompt. A nil
// sendErr marks it delivered; otherwise the prompt text is kept for the worker
// to resend. No-op without Redis or a session key.
func RecordPromptDelivery(ctx context.Context, sessionKey, text string, sendErr error) {
	if db.RedisClient == nil || sessionKey == "" {
		return
	}
	if sendErr == nil {
		db.RedisClient.Set(ctx, sessionKey+PromptDeliveredSuffix, "1", promptReceiptTTL)
		db.RedisClient.Del(ctx, sessionKey+PromptUndeliveredSuffix)
		return
	}
	if strings.TrimSpace(text) != "" {
		db.RedisClient.Set(ctx, sessionKey+PromptUndeliveredSuffix, text, promptReceiptTTL)
	}
}

// TakePromptDelivery returns (and clears) the receipt for this turn: whether a
// prompt was delivered, and any prompt text that failed to send. known=false
// means no receipt exists at all (e.g. Redis down or an old backend).
func TakePromptDelivery(ctx context.Context, sessionKey string) (delivered bool, undelivered string, known bool) {
	if db.RedisClient == nil || sessionKey == "" {
		return false, "", false
	}
	dKey := sessionKey + PromptDeliveredSuffix
	uKey := sessionKey + PromptUndeliveredSuffix
	if v, err := db.RedisClient.Get(ctx, dKey).Result(); err == nil && v != "" {
		delivered = true
		known = true
	}
	if v, err := db.RedisClient.Get(ctx, uKey).Result(); err == nil && v != "" {
		undelivered = v
		known = true
	}
	db.RedisClient.Del(ctx, dKey, uKey)
	return delivered, undelivered, known
}

// ClearPromptDelivery drops any stale receipt before a new Langflow turn so a
// leftover from a previous turn can never be mistaken for this one.
func ClearPromptDelivery(ctx context.Context, sessionKey string) {
	if db.RedisClient == nil || sessionKey == "" {
		return
	}
	db.RedisClient.Del(ctx, sessionKey+PromptDeliveredSuffix, sessionKey+PromptUndeliveredSuffix)
}
