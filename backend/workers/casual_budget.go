package workers

import (
	"context"
	"encoding/json"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/hibiken/asynq"

	"github.com/vietbui/chat-quality-agent/chatmemory"
	"github.com/vietbui/chat-quality-agent/config"
	"github.com/vietbui/chat-quality-agent/engine"
)

// Small talk in a 1:1 chat goes to the main bot (PR #30): it reads the whole
// conversation, so it answers "cảm ơn" in a word and takes "đúng rồi" as the
// yes to its own question. But every main-bot turn carries the long system
// prompt, the tools and RAG, so a customer who asks one real question and then
// keeps chatting burns a full agent run on each "haha". The casual budget
// keeps the first few small-talk turns on the main bot, answers the rest with
// the tenant's own (cheaper) AI model and a short editable prompt, and can go
// quiet after a long run. A business question at any point goes back to the
// main bot and starts the count again.

// Channel metadata keys (channel page, Chatbot tab).
const (
	metaCasualFullAgentTurns = "casual_full_agent_turns"
	metaCasualQuietAfter     = "casual_quiet_after"
)

// Defaults when the channel has not set a value.
const (
	defaultCasualFullAgentTurns = 3
	defaultCasualQuietAfter     = 20
)

// casualBudget is a channel's small-talk budget.
type casualBudget struct {
	// FullAgentTurns small-talk turns in a row still go to the main bot.
	FullAgentTurns int
	// QuietAfter, when > 0, is the last small-talk turn in a row the bot
	// answers; later ones get no reply until the chat is back on business.
	QuietAfter int
}

// casualBudgetFrom reads the budget from channel metadata JSON. A missing,
// negative or fractional value uses the default. A quiet cutoff at or below
// the main-bot turns is moved to the turn after them, so the bot never goes
// quiet before the short reply that tells the customer it will.
func casualBudgetFrom(metadata string) casualBudget {
	out := casualBudget{FullAgentTurns: defaultCasualFullAgentTurns, QuietAfter: defaultCasualQuietAfter}
	var m map[string]interface{}
	if json.Unmarshal([]byte(metadata), &m) == nil {
		if v, ok := casualSetting(m[metaCasualFullAgentTurns]); ok {
			out.FullAgentTurns = v
		}
		if v, ok := casualSetting(m[metaCasualQuietAfter]); ok {
			out.QuietAfter = v
		}
	}
	if out.QuietAfter > 0 && out.QuietAfter <= out.FullAgentTurns {
		out.QuietAfter = out.FullAgentTurns + 1
	}
	return out
}

// casualSettingMax is the largest turn count either setting accepts (the
// channel form uses the same bound).
const casualSettingMax = 1000

// casualSetting accepts a whole JSON number from 0 to casualSettingMax.
func casualSetting(v interface{}) (int, bool) {
	f, ok := v.(float64)
	if !ok || f < 0 || f != math.Trunc(f) || f > casualSettingMax {
		return 0, false
	}
	return int(f), true
}

type casualRoute int

const (
	casualToAgent casualRoute = iota // main bot, as for any other turn
	casualToLite                     // short prompt on the tenant's AI model
	casualToQuiet                    // no reply
)

// routeCasualTurn picks how to answer the streak-th small-talk turn in a row.
// streak 0 means the count is unknown (no Redis): keep the main bot.
func routeCasualTurn(streak int, b casualBudget) casualRoute {
	switch {
	case streak <= 0:
		return casualToAgent
	case b.QuietAfter > 0 && streak > b.QuietAfter:
		return casualToQuiet
	case streak > b.FullAgentTurns:
		return casualToLite
	}
	return casualToAgent
}

// casualModeFor is what the small-talk model is asked for on a lite or quiet
// turn. Quiet turns still pass through it (a cheap call, no reply sent) so a
// business follow-up the classifier mislabelled CASUAL reaches the main bot
// instead of being dropped.
func casualModeFor(route casualRoute, streak int, b casualBudget) casualTurnMode {
	switch {
	case route == casualToQuiet:
		return casualReplyQuiet
	case b.QuietAfter > 0 && streak == b.QuietAfter:
		return casualReplyLast
	}
	return casualReplyNormal
}

// casualTurnID identifies the message for the streak count: the Zalo message
// id, or else the queue task id, which a retry keeps. "" means no stable id;
// the count is then skipped and the turn goes to the main bot.
func casualTurnID(ctx context.Context, msgID string) string {
	if strings.TrimSpace(msgID) != "" {
		return msgID
	}
	if id, ok := asynq.GetTaskID(ctx); ok && id != "" {
		return "task:" + id
	}
	return ""
}

// casualTurnMode says what the small-talk model is asked for.
type casualTurnMode int

const (
	casualReplyNormal casualTurnMode = iota // reply
	casualReplyLast                         // reply, and say this is the last small-talk reply
	casualReplyQuiet                        // only check whether the main bot is needed
)

// casualReplyInput is the small-talk model's input, in the shape the fixed
// part of the task prompt describes. Chat text is flattened like the
// classifier's, so a message cannot fake a speaker or a block.
func casualReplyInput(recent []chatmemory.RecentTurn, message string, staff bool, mode casualTurnMode) string {
	var b strings.Builder
	b.WriteString("<nguoi_nhan>")
	if staff {
		b.WriteString("nhân viên nội bộ")
	} else {
		b.WriteString("khách hàng")
	}
	b.WriteString("</nguoi_nhan>\n<luot_cuoi>")
	b.WriteString(yesNo(mode == casualReplyLast))
	b.WriteString("</luot_cuoi>\n<im_lang>")
	b.WriteString(yesNo(mode == casualReplyQuiet))
	b.WriteString("</im_lang>\n")
	b.WriteString(intentClassifierInput(recent, message))
	return b.String()
}

func yesNo(v bool) string {
	if v {
		return "có"
	}
	return "không"
}

// casualReplyMaxRunes caps a small-talk reply, ellipsis included; the prompt
// asks for 1-2 short sentences, so anything longer is cut.
const casualReplyMaxRunes = 600

// casualReplyTimeout bounds one small-talk model call. casualCallDeadline
// shortens it further so the main-bot fallback still fits in the lock.
const casualReplyTimeout = 8 * time.Second

// casualLockReserve is the part of the session lock kept for what may run
// after the small-talk call: the main bot (60s Langflow timeout) and the
// Zalo send, with headroom.
const casualLockReserve = 70 * time.Second

// casualCallDeadline is the latest a small-talk call may end, counted from
// when the turn took the session lock. A zero result means no time is left:
// the caller skips the call and uses the main bot.
func casualCallDeadline(turnStart, now time.Time) time.Time {
	deadline := turnStart.Add(engine.SessionLockTTL - casualLockReserve)
	if limit := now.Add(casualReplyTimeout); limit.Before(deadline) {
		deadline = limit
	}
	if !deadline.After(now.Add(time.Second)) {
		return time.Time{}
	}
	return deadline
}

// casualOutcome is what the small-talk model's answer means.
type casualOutcome int

const (
	casualSend     casualOutcome = iota // send Text to the customer
	casualHandoff                       // the model says the main bot is needed
	casualQuietAck                      // the model confirms no reply is needed (quiet mode)
	casualUnusable                      // empty or not sendable as text: use the main bot, keep CASUAL
)

// casualReply is the small-talk model's answer for one turn.
type casualReply struct {
	Text    string
	Outcome casualOutcome
	Result  engine.LangflowResult
	Prompt  string // system prompt + input, for cost estimates
}

// looksLikeJSON reports an answer wrapped like a JSON object or array.
func looksLikeJSON(text string) bool {
	return (strings.HasPrefix(text, "{") && strings.HasSuffix(text, "}")) ||
		(strings.HasPrefix(text, "[") && strings.HasSuffix(text, "]") && !strings.HasPrefix(text, "[CAN_") && !strings.HasPrefix(text, "[IM_"))
}

// parseCasualReply reads the model's answer. A JSON-shaped answer is
// unusable: the Zalo sender would post an object as a raw message template
// (links, buttons), and neither is a reply a customer should see.
func parseCasualReply(content string) (string, casualOutcome) {
	text := strings.TrimSpace(content)
	switch {
	case text == "", looksLikeJSON(text):
		// Checked first: a marker inside JSON is still off-format.
		return "", casualUnusable
	case strings.Contains(text, engine.CasualReplyHandoff):
		return "", casualHandoff
	case text == engine.CasualReplyQuiet:
		return "", casualQuietAck
	}
	if r := []rune(text); len(r) > casualReplyMaxRunes {
		text = strings.TrimSpace(string(r[:casualReplyMaxRunes-1])) + "…"
	}
	return text, casualSend
}

// replyToCasualTurn answers a small-talk turn with the tenant's AI model and
// the editable small-talk prompt, finishing by deadline. An error means no
// provider, no time left or a failed call: the caller then uses the main bot.
func replyToCasualTurn(ctx context.Context, tenantID string, recent []chatmemory.RecentTurn, message string, staff bool, mode casualTurnMode, deadline time.Time) (casualReply, error) {
	if deadline.IsZero() {
		return casualReply{}, errNoTimeForCasualReply
	}
	cfg, _ := config.Load()
	resolved, err := engine.ResolveTenantAIProvider(tenantID, cfg)
	if err != nil {
		return casualReply{}, err
	}
	if resolved.Client == nil {
		return casualReply{}, errNoTenantAIProvider
	}
	systemPrompt := engine.BuildTaskPrompt(tenantID, engine.PromptCasualReply, nil)
	input := casualReplyInput(recent, message, staff, mode)
	// Settings reads above count against the same deadline.
	if time.Until(deadline) < time.Second {
		return casualReply{}, errNoTimeForCasualReply
	}

	callCtx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	resp, err := resolved.Client.AnalyzeChat(callCtx, systemPrompt, input)
	if err != nil {
		return casualReply{}, err
	}
	text, outcome := parseCasualReply(resp.Content)
	return casualReply{
		Text:    text,
		Outcome: outcome,
		Prompt:  systemPrompt + "\n" + input,
		Result: engine.LangflowResult{
			Text:         text,
			InputTokens:  resp.InputTokens,
			OutputTokens: resp.OutputTokens,
			Model:        resp.Model,
			Provider:     resp.Provider,
			HasUsage:     resp.InputTokens > 0 || resp.OutputTokens > 0,
		},
	}, nil
}

type casualBudgetError string

func (e casualBudgetError) Error() string { return string(e) }

const (
	errNoTenantAIProvider   = casualBudgetError("no AI provider configured for small-talk replies")
	errNoTimeForCasualReply = casualBudgetError("no time left in the session lock for a small-talk call")
)

// onceTurns wraps a recent-turns loader so the classifier and the small-talk
// reply share one database read.
func onceTurns(load func() []chatmemory.RecentTurn) func() []chatmemory.RecentTurn {
	if load == nil {
		return nil
	}
	var once sync.Once
	var turns []chatmemory.RecentTurn
	return func() []chatmemory.RecentTurn {
		once.Do(func() { turns = load() })
		return turns
	}
}
