package workers

import (
	"context"
	"encoding/json"
	"log"
	"strings"
	"time"

	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/engine"
)

// Group assistant mode (channel metadata "group_assistant_mode") turns the bot
// into a member of each customer's Zalo group instead of a keyword-activated
// Q&A session:
//
//   - The first message in the group opens a session silently; no trigger
//     word, no welcome, and idle timeouts close it without a message.
//   - Every message is kept in a short group log. A context-aware classifier
//     (editable task prompt) decides whether the bot should answer; members
//     talking to each other get silence.
//   - The bot answers in one Langflow conversation per group, so it sees the
//     whole group's exchange. Messages it did not answer reach Langflow as a
//     block in front of the next question, each line labelled with who said it.
//
// Permissions are untouched: every turn still signs a token for its own
// sender, and per-sender flow state (menus, carts, drafts) stays per sender.

const (
	// groupLogPendingSuffix holds group messages Langflow has not seen yet.
	groupLogPendingSuffix = ":grp_pending"
	// groupLogRecentSuffix holds the last few group messages, bot replies
	// included, for the reply classifier.
	groupLogRecentSuffix = ":grp_recent"

	groupLogMaxPending = 30
	groupLogMaxRecent  = 12
	groupLogMaxChars   = 500

	groupRoleCustomer   = "khách"
	groupRoleStaff      = "nhân viên"
	groupRoleUnverified = "chưa xác thực"
	groupRoleBot        = "bot"
)

// groupLine is one message in a group's short log.
type groupLine struct {
	At   int64  `json:"at"`
	Name string `json:"name"`
	Role string `json:"role"`
	Text string `json:"text"`
}

// groupAssistantOn reports whether this turn runs in group assistant mode.
func groupAssistantOn(meta ChannelMetadata, zaloGroupID string) bool {
	return meta.GroupAssistant && zaloGroupID != ""
}

// groupSpeaker labels the sender of a group message.
func groupSpeaker(isWhitelisted, isCustomer bool, staffName, customerName string) (name, role string) {
	switch {
	case isCustomer:
		return firstNonBlank(customerName, "Khách"), groupRoleCustomer
	case isWhitelisted:
		return firstNonBlank(staffName, "Nhân viên"), groupRoleStaff
	default:
		return "Thành viên nhóm", groupRoleUnverified
	}
}

func firstNonBlank(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func newGroupLine(name, role, text string) groupLine {
	text = strings.TrimSpace(text)
	if r := []rune(text); len(r) > groupLogMaxChars {
		text = string(r[:groupLogMaxChars]) + "…"
	}
	return groupLine{At: time.Now().Unix(), Name: name, Role: role, Text: text}
}

// rememberGroupLine adds a message to the group's recent log and, when
// unseen is true, to the lines Langflow has not seen yet. Both live under the
// group session key, so ending the session clears them.
func rememberGroupLine(ctx context.Context, sessionKey string, line groupLine, unseen bool, ttl time.Duration) {
	if db.RedisClient == nil || sessionKey == "" || line.Text == "" {
		return
	}
	raw, err := json.Marshal(line)
	if err != nil {
		return
	}
	pipe := db.RedisClient.TxPipeline()
	recentKey := sessionKey + groupLogRecentSuffix
	pipe.RPush(ctx, recentKey, raw)
	pipe.LTrim(ctx, recentKey, -groupLogMaxRecent, -1)
	pipe.Expire(ctx, recentKey, ttl)
	if unseen {
		pendingKey := sessionKey + groupLogPendingSuffix
		pipe.RPush(ctx, pendingKey, raw)
		pipe.LTrim(ctx, pendingKey, -groupLogMaxPending, -1)
		pipe.Expire(ctx, pendingKey, ttl)
	}
	if _, err := pipe.Exec(ctx); err != nil {
		log.Printf("[group-assistant] remember line for %s: %v", sessionKey, err)
	}
}

// readGroupLines returns the lines stored under key, oldest first, with the
// stored form of each line.
func readGroupLines(ctx context.Context, key string) ([]groupLine, []string) {
	if db.RedisClient == nil {
		return nil, nil
	}
	raws, err := db.RedisClient.LRange(ctx, key, 0, -1).Result()
	if err != nil {
		return nil, nil
	}
	out := make([]groupLine, 0, len(raws))
	kept := make([]string, 0, len(raws))
	for _, raw := range raws {
		var l groupLine
		if json.Unmarshal([]byte(raw), &l) == nil && l.Text != "" {
			out = append(out, l)
			kept = append(kept, raw)
		}
	}
	return out, kept
}

// recentGroupLines returns the group's recent log for the reply classifier.
func recentGroupLines(ctx context.Context, sessionKey string) []groupLine {
	lines, _ := readGroupLines(ctx, sessionKey+groupLogRecentSuffix)
	return lines
}

// unseenGroupLines returns the lines Langflow has not seen and their stored
// form. Pass the stored form to markGroupLinesSeen once Langflow answered, so
// a retried turn resends them.
func unseenGroupLines(ctx context.Context, sessionKey string) ([]groupLine, []string) {
	return readGroupLines(ctx, sessionKey+groupLogPendingSuffix)
}

// markGroupLinesSeen removes exactly the given lines. Lines added meanwhile,
// or already removed by a groupmate's turn running at the same time, are left
// alone.
func markGroupLinesSeen(ctx context.Context, sessionKey string, raws []string) {
	if db.RedisClient == nil || len(raws) == 0 {
		return
	}
	key := sessionKey + groupLogPendingSuffix
	pipe := db.RedisClient.Pipeline()
	for _, raw := range raws {
		pipe.LRem(ctx, key, 1, raw)
	}
	if _, err := pipe.Exec(ctx); err != nil {
		log.Printf("[group-assistant] mark lines seen for %s: %v", sessionKey, err)
	}
}

var groupLogLocation = func() *time.Location {
	if loc, err := time.LoadLocation("Asia/Ho_Chi_Minh"); err == nil {
		return loc
	}
	return time.FixedZone("ICT", 7*3600)
}()

// renderGroupLines formats lines as "[10:31] Minh (khách): text". Message
// text is flattened to one line so a member cannot fake another speaker.
func renderGroupLines(lines []groupLine) string {
	var b strings.Builder
	for i, l := range lines {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString("[")
		b.WriteString(time.Unix(l.At, 0).In(groupLogLocation).Format("15:04"))
		b.WriteString("] ")
		b.WriteString(engine.UntrustedText(l.Name))
		b.WriteString(" (")
		b.WriteString(l.Role)
		b.WriteString("): ")
		b.WriteString(engine.UntrustedText(l.Text))
	}
	return b.String()
}

// groupAssistantInput is the Langflow input for a group turn: the group
// messages Langflow has not seen, then the question with its speaker.
func groupAssistantInput(unseen []groupLine, name, role, text string) string {
	var b strings.Builder
	if len(unseen) > 0 {
		b.WriteString("<tin_nhom>\n")
		b.WriteString(renderGroupLines(unseen))
		b.WriteString("\n</tin_nhom>\n")
	}
	b.WriteString("[")
	b.WriteString(engine.UntrustedText(name))
	b.WriteString(" (")
	b.WriteString(role)
	b.WriteString(")]: ")
	b.WriteString(text)
	return b.String()
}

// groupClassifierInput is the reply classifier's input: the recent group log
// and the new message with its speaker.
func groupClassifierInput(recent []groupLine, name, role, text string) string {
	var b strings.Builder
	if len(recent) > 0 {
		b.WriteString("<tin_gan_day>\n")
		b.WriteString(renderGroupLines(recent))
		b.WriteString("\n</tin_gan_day>\n")
	}
	b.WriteString("Tin mới: ")
	b.WriteString(renderGroupLines([]groupLine{newGroupLine(name, role, text)}))
	return b.String()
}

// appendGroupAssistantContext adds the editable group assistant instructions
// to the system prompt. An empty base prompt means the flow uses its own
// prompt, which this tweak would replace, so nothing is added then.
func appendGroupAssistantContext(tenantID, systemPrompt string) string {
	if strings.TrimSpace(systemPrompt) == "" {
		return systemPrompt
	}
	block := engine.BuildTaskPrompt(tenantID, engine.PromptGroupAssistant, nil)
	if strings.TrimSpace(block) == "" {
		return systemPrompt
	}
	return systemPrompt + "\n\n" + block
}

// hasPendingOptions reports whether the sender has a numbered menu waiting.
func hasPendingOptions(ctx context.Context, workKey string) bool {
	if db.RedisClient == nil {
		return false
	}
	n, err := db.RedisClient.Exists(ctx, workKey+engine.PendingOptionsSuffix).Result()
	return err == nil && n > 0
}
