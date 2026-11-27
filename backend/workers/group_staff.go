package workers

import (
	"context"
	"strings"
	"time"
	"unicode"

	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/orderactions"
)

// Staff in customer groups. In a customer's Zalo group BBI staff mostly talk
// to the customer ("anh ơi đơn FF399 chiều em giao nhé"). Those messages read
// like business questions, so a classifier that only sees the text answers
// them and the bot talks over the staff member. With the channel switch
// "group_staff_call_only" (on unless turned off) a staff message in a
// customer group reaches the bot only when:
//
//   - it calls the bot: one of the channel's bot names (group_bot_names,
//     default "bot") as a whole word, e.g. "Bull ơi FF399 còn không", or the
//     session keyword itself; or
//   - the staff member is talking with the bot already: the bot answered
//     them in the last few minutes, or they have a numbered menu or an open
//     cart waiting. The usual classifier then decides, so a free-text reply
//     to the bot's own question still gets answered.
//
// Every other staff message is not for the bot: no classifier call, no reply.
// In group assistant mode it still joins the group log, so the bot's next
// answer knows what the staff member told the customer.
//
// Customers, unverified members, 1:1 chats and groups without a customer code
// (staff or alert groups) are unchanged.

// staffEngagedWindow is how long after the bot answered a staff member their
// messages are read as possibly meant for the bot without a call.
const staffEngagedWindow = 5 * time.Minute

// staffEngagedSuffix marks a staff member who is talking with the bot. It
// hangs off the group session key with the sender's id appended.
const staffEngagedSuffix = ":staff_engaged:"

// defaultGroupBotNames is used when the channel sets no bot names.
var defaultGroupBotNames = []string{"bot"}

type staffTurn int

const (
	// staffTurnUngated: not a gated staff message; handle it as before.
	staffTurnUngated staffTurn = iota
	// staffTurnCalled: the staff member called the bot; answer it.
	staffTurnCalled
	// staffTurnEngaged: the staff member is talking with the bot; let the
	// classifier decide.
	staffTurnEngaged
	// staffTurnNotForBot: staff talking to the customer; stay silent.
	staffTurnNotForBot
)

// staffCallOnlyOn reports whether staff messages in this chat reach the bot
// only when they call it: the switch is on, the chat is a customer's CRM
// group (one with a customer code) and the sender is staff.
func staffCallOnlyOn(meta ChannelMetadata, zaloGroupID string, isCRMGroup bool, groupCustomerCode, role string) bool {
	if meta.GroupStaffCallOnly != nil && !*meta.GroupStaffCallOnly {
		return false
	}
	return role == groupRoleStaff && zaloGroupID != "" && isCRMGroup && strings.TrimSpace(groupCustomerCode) != ""
}

type zaloSessionStart int

const (
	zaloSessionStayClosed zaloSessionStart = iota
	zaloSessionManual
	zaloSessionAccountMention
	zaloSessionStaffCall
	zaloSessionAssistant
)

// zaloSessionStartFor decides how a turn without an active session should
// start. An exact opening keyword keeps its welcome-only behavior even if it
// also contains a configured bot name.
func zaloSessionStartFor(meta ChannelMetadata, text string, accountMentioned, staffGated, groupAssistant bool) zaloSessionStart {
	switch {
	case matchesSessionKeyword(text, meta.SessionKeyword):
		return zaloSessionManual
	case accountMentioned:
		return zaloSessionAccountMention
	case groupAssistant:
		return zaloSessionAssistant
	case staffGated && !matchesSessionKeyword(text, meta.SessionEndKeyword) && callsGroupBot(text, groupBotNames(meta), ""):
		return zaloSessionStaffCall
	default:
		return zaloSessionStayClosed
	}
}

// stripGroupBotAccountMention recognizes Zalo's GMF marker for the connected
// OA itself. Matching the account ID keeps this independent of the display
// name shown by Zalo and of the aliases configured for text calls.
func stripGroupBotAccountMention(text, accountID string) (string, bool) {
	accountID = strings.TrimSpace(accountID)
	if accountID == "" {
		return strings.TrimSpace(text), false
	}
	marker := "[@" + accountID + "]"
	if !strings.Contains(text, marker) {
		return strings.TrimSpace(text), false
	}
	return strings.TrimSpace(strings.ReplaceAll(text, marker, "")), true
}

func matchesSessionKeyword(text, keywords string) bool {
	for _, kw := range strings.Split(keywords, ";") {
		if kw = strings.TrimSpace(kw); kw != "" && strings.EqualFold(text, kw) {
			return true
		}
	}
	return false
}

// groupBotNames returns the names staff use to call the bot in a group.
func groupBotNames(meta ChannelMetadata) []string {
	var names []string
	for _, n := range strings.Split(meta.GroupBotNames, ";") {
		if n = strings.TrimSpace(n); n != "" {
			names = append(names, n)
		}
	}
	if len(names) == 0 {
		return defaultGroupBotNames
	}
	return names
}

// callsGroupBot reports whether text calls the bot: it contains one of the
// names as a whole word or phrase ("Bull ơi", "@bot", "nhờ bot tra giúp"),
// or it is exactly one of the session keywords.
func callsGroupBot(text string, names []string, sessionKeywords string) bool {
	t := strings.TrimSpace(text)
	if t == "" {
		return false
	}
	if matchesSessionKeyword(t, sessionKeywords) {
		return true
	}
	lower := strings.ToLower(t)
	for _, name := range names {
		if containsWord(lower, strings.ToLower(strings.TrimSpace(name))) {
			return true
		}
	}
	return false
}

// containsWord reports whether word occurs in s with no letter or digit
// right before or after it, so "bot" matches "bot ơi" but not "robot".
func containsWord(s, word string) bool {
	if word == "" {
		return false
	}
	for from := 0; from <= len(s)-len(word); {
		idx := strings.Index(s[from:], word)
		if idx < 0 {
			return false
		}
		start, end := from+idx, from+idx+len(word)
		if !wordRuneBefore(s, start) && !wordRuneAfter(s, end) {
			return true
		}
		from = start + 1
	}
	return false
}

func wordRuneBefore(s string, i int) bool {
	if i == 0 {
		return false
	}
	r := []rune(s[:i])
	return isWordRune(r[len(r)-1])
}

func wordRuneAfter(s string, i int) bool {
	if i >= len(s) {
		return false
	}
	for _, r := range s[i:] {
		return isWordRune(r)
	}
	return false
}

func isWordRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.Is(unicode.Mn, r)
}

func staffEngagedKey(sessionKey, senderID string) string {
	return sessionKey + staffEngagedSuffix + senderID
}

// markStaffEngaged starts or extends the window in which this staff member's
// messages may be meant for the bot without calling it.
func markStaffEngaged(ctx context.Context, sessionKey, senderID string) {
	if db.RedisClient == nil || sessionKey == "" || senderID == "" {
		return
	}
	db.RedisClient.Set(ctx, staffEngagedKey(sessionKey, senderID), "1", staffEngagedWindow)
}

// clearStaffEngaged ends the window, e.g. when the staff member ends the
// session with the end keyword.
func clearStaffEngaged(ctx context.Context, sessionKey, senderID string) {
	if db.RedisClient == nil || sessionKey == "" || senderID == "" {
		return
	}
	db.RedisClient.Del(ctx, staffEngagedKey(sessionKey, senderID))
}

func staffEngaged(ctx context.Context, sessionKey, senderID string) bool {
	if db.RedisClient == nil {
		return false
	}
	n, err := db.RedisClient.Exists(ctx, staffEngagedKey(sessionKey, senderID)).Result()
	return err == nil && n > 0
}

// classifyStaffTurn sorts a gated staff message: a call to the bot, a turn in
// a conversation the staff member is already having with the bot, or a
// message to the customer that the bot leaves alone.
func classifyStaffTurn(ctx context.Context, meta ChannelMetadata, tenantID, sessionKey, workKey, senderID, text string) staffTurn {
	if callsGroupBot(text, groupBotNames(meta), meta.SessionKeyword) {
		return staffTurnCalled
	}
	if staffEngaged(ctx, sessionKey, senderID) || hasPendingOptions(ctx, workKey) {
		return staffTurnEngaged
	}
	if _, _, open := orderactions.OpenCart(ctx, tenantID, workKey); open {
		return staffTurnEngaged
	}
	return staffTurnNotForBot
}
