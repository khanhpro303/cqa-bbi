package agents

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/vietbui/chat-quality-agent/config"
	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/engine"
)

// Reasons a Selection carries after routing (Selection.Reason).
const (
	// ReasonSticky: the agent that answered the chat's last turn keeps it.
	ReasonSticky = "sticky"
	// ReasonRouted: the coordinator moved the turn to another agent.
	ReasonRouted = "router"
	// ReasonRouterKept: the coordinator kept the agent it started from.
	ReasonRouterKept = "router_kept"
	// ReasonRouterFailed: the coordinator gave no usable answer, so the
	// agent it started from answers.
	ReasonRouterFailed = "router_failed"
)

// routerTimeout bounds the coordinator call: a slow router must not hold
// the chat turn for long, and on timeout the current agent answers.
const routerTimeout = 12 * time.Second

// AskFunc asks a model for a short answer to input under systemPrompt.
type AskFunc func(ctx context.Context, systemPrompt, input string) (string, error)

// TenantAsk asks the model the tenant set for the coordinator task (AI
// Engine, per-task models), else the tenant's default model. With no model
// configured it answers "" and routing keeps the current agent.
func TenantAsk(cfg *config.Config, tenantID string) AskFunc {
	return func(ctx context.Context, systemPrompt, input string) (string, error) {
		resolved, err := engine.ResolveTenantAIProvider(tenantID, cfg, engine.PromptAgentRouter)
		if err != nil {
			return "", err
		}
		if resolved.Client == nil {
			return "", nil
		}
		resp, err := resolved.Client.AnalyzeChat(ctx, systemPrompt, input)
		if err != nil {
			return "", err
		}
		return resp.Content, nil
	}
}

// RouterInput lays out a message with no chat history the way the intent
// classifier reads a chat (see workers.intentClassifierInput, which the
// webhook worker passes with the recent lines): an empty <tin_gan_day>,
// then the new message, marked untrusted.
func RouterInput(message string) string {
	return "<tin_gan_day>\n</tin_gan_day>\nTin mới: " + engine.UntrustedText(message)
}

// NeedsRouting reports whether the coordinator has a choice to make: an
// agent answers and the team has more than one for the sender's audience.
func (p Plan) NeedsRouting() bool {
	return p.Mode == ModeAgent && len(p.Selection.Candidates) > 1
}

// Stuck reports whether the turn is held by the agent that answered the
// chat's last turn and the team does not let agents hand a chat over.
func (p Plan) Stuck() bool {
	return p.Selection.Reason == ReasonSticky && p.Selection.Team.Options.DisableHandoff
}

// stickyKey names the agent kept for a chat. The team is part of the key,
// so moving a channel to another team starts its chats afresh.
func stickyKey(teamID, chatKey string) string {
	return "agent_sticky:" + teamID + ":" + chatKey
}

// Stick hands the turn to the agent that answered chatKey's last turn,
// while it is still a candidate. Without Redis, or with one candidate,
// nothing changes.
func (p *Plan) Stick(ctx context.Context, chatKey string) {
	if !p.NeedsRouting() || db.RedisClient == nil || chatKey == "" {
		return
	}
	key, err := db.RedisClient.Get(ctx, stickyKey(p.Selection.Team.ID, chatKey)).Result()
	if err != nil {
		if !errors.Is(err, redis.Nil) {
			log.Printf("[agents] sticky lookup failed for team %s: %v", p.Selection.Team.ID, err)
		}
		return
	}
	p.StickTo(key)
}

// StickTo hands the turn to the candidate with key as the chat's current
// agent. It reports false, changing nothing, when no candidate has key.
func (p *Plan) StickTo(key string) bool {
	a, ok := p.Selection.candidate(key)
	if ok && p.Mode == ModeAgent {
		p.Selection.Agent, p.Selection.Reason = a, ReasonSticky
		return true
	}
	return false
}

// Remember keeps the answering agent on chatKey for the team's sticky time,
// or fallback (the chat session's timeout) when the team sets none. Only an
// agent the chat already had or the coordinator chose is kept: the team's
// plain pick on a small-talk turn must not hold the chat (with handoff off
// it would lock every new chat to the default agent).
func (p Plan) Remember(ctx context.Context, chatKey string, fallback time.Duration) {
	if !p.NeedsRouting() || db.RedisClient == nil || chatKey == "" {
		return
	}
	switch p.Selection.Reason {
	case ReasonSticky, ReasonRouted, ReasonRouterKept:
	default:
		return
	}
	ttl := fallback
	if m := p.Selection.Team.Options.StickyMinutes; m > 0 {
		ttl = time.Duration(m) * time.Minute
	}
	if ttl <= 0 {
		ttl = 30 * time.Minute
	}
	if err := db.RedisClient.Set(ctx, stickyKey(p.Selection.Team.ID, chatKey), p.Selection.Agent.Key, ttl).Err(); err != nil {
		log.Printf("[agents] sticky save failed for team %s: %v", p.Selection.Team.ID, err)
	}
}

// RouterPrompt is the coordinator's system prompt for this selection: the
// team's own router prompt when set, else the tenant's AI Engine text for
// the coordinator task, then the fixed format with the candidates and the
// agent currently answering (none when current is false).
func (s Selection) RouterPrompt(tenantID string, current bool) string {
	instructions := strings.TrimSpace(s.Team.RouterPrompt)
	if instructions == "" {
		instructions = engine.TaskPromptInstructions(tenantID, engine.PromptAgentRouter)
	}
	var list strings.Builder
	for _, a := range s.Candidates {
		list.WriteString("- ")
		list.WriteString(a.Key)
		list.WriteString(" | ")
		list.WriteString(oneLine(a.Name))
		if desc := oneLine(a.Description); desc != "" {
			list.WriteString(" | ")
			list.WriteString(desc)
		}
		if a.ID == s.Team.DefaultAgentID {
			list.WriteString(" (mặc định)")
		}
		list.WriteByte('\n')
	}
	currentKey := "chưa có"
	if current {
		currentKey = s.Agent.Key
	}
	p, _ := engine.LookupTaskPrompt(engine.PromptAgentRouter)
	return engine.ComposeTaskPrompt(instructions, p.Format, map[string]string{
		"agents":  strings.TrimRight(list.String(), "\n"),
		"current": currentKey,
	})
}

// Route asks the coordinator which candidate answers input (see
// RouterInput) and moves the turn to it. The agent the plan holds (sticky
// or the team's pick) answers when the coordinator fails, times out or names
// no candidate, so a router fault never leaves a chat unanswered. It returns
// the coordinator's reason for logs and the test console.
func (p *Plan) Route(ctx context.Context, tenantID string, ask AskFunc, input string) string {
	if !p.NeedsRouting() || p.Stuck() || ask == nil {
		return ""
	}
	prompt := p.Selection.RouterPrompt(tenantID, p.Selection.Reason == ReasonSticky)
	routeCtx, cancel := context.WithTimeout(ctx, routerTimeout)
	defer cancel()
	content, err := ask(routeCtx, prompt, input)
	if err != nil {
		log.Printf("[agents] coordinator failed for team %s: %v; %s answers", p.Selection.Team.ID, err, p.Selection.Agent.Key)
		p.Selection.Reason = ReasonRouterFailed
		return ""
	}
	key, reason := ParseRoute(content)
	a, ok := p.Selection.candidate(key)
	switch {
	case !ok:
		if strings.TrimSpace(content) != "" {
			log.Printf("[agents] coordinator for team %s named no candidate (%q); %s answers", p.Selection.Team.ID, key, p.Selection.Agent.Key)
		}
		p.Selection.Reason = ReasonRouterFailed
	case a.Key == p.Selection.Agent.Key:
		p.Selection.Reason = ReasonRouterKept
	default:
		p.Selection.Agent, p.Selection.Reason = a, ReasonRouted
	}
	return reason
}

// ParseRoute reads the coordinator's answer: a JSON object with "agent"
// (and an optional "reason"), possibly inside a code fence, or a bare key.
func ParseRoute(content string) (agentKey, reason string) {
	content = strings.TrimSpace(content)
	if start, end := strings.Index(content, "{"), strings.LastIndex(content, "}"); start >= 0 && end > start {
		var parsed struct {
			Agent  string `json:"agent"`
			Reason string `json:"reason"`
		}
		if json.Unmarshal([]byte(content[start:end+1]), &parsed) == nil {
			return strings.TrimSpace(parsed.Agent), strings.TrimSpace(parsed.Reason)
		}
	}
	content = strings.Trim(content, "`\"' \n")
	if strings.ContainsAny(content, " \n{}") {
		return "", ""
	}
	return content, ""
}

// candidate finds a candidate by key, ignoring case and spaces around it.
func (s Selection) candidate(key string) (Agent, bool) {
	key = strings.TrimSpace(key)
	if key == "" {
		return Agent{}, false
	}
	for _, a := range s.Candidates {
		if strings.EqualFold(a.Key, key) {
			return a, true
		}
	}
	return Agent{}, false
}

func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
