package workers

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"time"

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

// casualBudgetFrom reads the budget from channel metadata JSON. A missing or
// negative value uses the default.
func casualBudgetFrom(metadata string) casualBudget {
	out := casualBudget{FullAgentTurns: defaultCasualFullAgentTurns, QuietAfter: defaultCasualQuietAfter}
	var m map[string]interface{}
	if json.Unmarshal([]byte(metadata), &m) != nil {
		return out
	}
	if v, ok := m[metaCasualFullAgentTurns].(float64); ok && v >= 0 {
		out.FullAgentTurns = int(v)
	}
	if v, ok := m[metaCasualQuietAfter].(float64); ok && v >= 0 {
		out.QuietAfter = int(v)
	}
	return out
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

// lastCasualReply reports whether the streak-th turn is the last small-talk
// turn the bot answers before going quiet, so the reply can say so.
func lastCasualReply(streak int, b casualBudget) bool {
	return b.QuietAfter > 0 && streak == b.QuietAfter
}

// casualReplyInput is the small-talk model's input, in the shape the fixed
// part of the task prompt describes. Chat text is flattened like the
// classifier's, so a message cannot fake a speaker or a block.
func casualReplyInput(recent []chatmemory.RecentTurn, message string, staff, last bool) string {
	var b strings.Builder
	b.WriteString("<nguoi_nhan>")
	if staff {
		b.WriteString("nhân viên nội bộ")
	} else {
		b.WriteString("khách hàng")
	}
	b.WriteString("</nguoi_nhan>\n<luot_cuoi>")
	if last {
		b.WriteString("có")
	} else {
		b.WriteString("không")
	}
	b.WriteString("</luot_cuoi>\n")
	b.WriteString(intentClassifierInput(recent, message))
	return b.String()
}

// casualReply is the small-talk model's answer for one turn.
type casualReply struct {
	Text    string
	Handoff bool // the model says the main bot should answer
	Result  engine.LangflowResult
	Prompt  string // system prompt + input, for cost estimates
}

// parseCasualReply reads the model's answer. An empty answer or the handoff
// marker sends the turn to the main bot.
func parseCasualReply(content string) (string, bool) {
	text := strings.TrimSpace(content)
	if text == "" || strings.Contains(text, engine.CasualReplyHandoff) {
		return "", true
	}
	return text, false
}

// replyToCasualTurn answers a small-talk turn with the tenant's AI model and
// the editable small-talk prompt. An error means no provider or a failed
// call: the caller then uses the main bot.
func replyToCasualTurn(ctx context.Context, tenantID string, recent []chatmemory.RecentTurn, message string, staff, last bool) (casualReply, error) {
	cfg, _ := config.Load()
	resolved, err := engine.ResolveTenantAIProvider(tenantID, cfg)
	if err != nil {
		return casualReply{}, err
	}
	if resolved.Client == nil {
		return casualReply{}, errNoTenantAIProvider
	}
	systemPrompt := engine.BuildTaskPrompt(tenantID, engine.PromptCasualReply, nil)
	input := casualReplyInput(recent, message, staff, last)

	callCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	resp, err := resolved.Client.AnalyzeChat(callCtx, systemPrompt, input)
	if err != nil {
		return casualReply{}, err
	}
	text, handoff := parseCasualReply(resp.Content)
	return casualReply{
		Text:    text,
		Handoff: handoff,
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

const errNoTenantAIProvider = casualBudgetError("no AI provider configured for small-talk replies")

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
