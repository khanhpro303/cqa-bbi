package agents

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/vietbui/chat-quality-agent/chatmemory"
	"github.com/vietbui/chat-quality-agent/engine"
)

// ApplyTo signs the selected agent into the turn's permission context: the
// ERP MCP server then lists and serves only the agent's tools, and
// search_knowledge reads only its tagged documents. Identity, tier and
// data scope in the context are left as the caller resolved them.
func (s Selection) ApplyTo(p *engine.GroupPermissionContext) {
	p.AgentKey = s.Agent.Key
	p.AgentTools = append([]string{}, s.Agent.Tools...)
	p.KnowledgeTags = append([]string(nil), s.Agent.KnowledgeTags...)
}

// Runtime runs one agent turn. The Langflow runtime sends the composed
// prompt to a Langflow flow; the native runtime (separate work) calls the
// provider with the agent's tools directly.
type Runtime interface {
	Run(ctx context.Context, in RunInput) (RunResult, error)
}

// RunInput is everything a runtime needs for one turn.
type RunInput struct {
	Agent Agent
	// Tier is the security tier of the sender ("public" | "private").
	Tier      string
	SessionID string
	SenderID  string
	// Input is the user's message, with group context when there is one.
	Input string
	// SystemPrompt is already composed (team voice, agent prompt, context).
	SystemPrompt string
	// History is the recent conversation from chat memory, for runtimes
	// that do not keep their own (the native runtime).
	History []chatmemory.RecentTurn
	// PermissionToken is signed with the agent applied (Selection.ApplyTo).
	PermissionToken string
	// AgentToken authenticates the bot to the ERP MCP server for its tier.
	AgentToken   string
	CustomerCode string
	Deadline     time.Time
	// Langflow connection for the Langflow runtime; FlowID is already the
	// agent's flow (Agent.FlowFor).
	LangflowURL    string
	LangflowAPIKey string
	FlowID         string
}

// Usage is what a turn cost.
type Usage struct {
	Provider        string
	Model           string
	InputTokens     int
	OutputTokens    int
	HasUsage        bool
	ReportedCostUSD *float64
}

// RunResult is a runtime's answer.
type RunResult struct {
	Text string
	// Handoff is the key of the teammate the agent passed the turn to, or "".
	Handoff string
	Usage   Usage
}

// LangflowRuntime runs agents on the tenant's Langflow flows.
type LangflowRuntime struct {
	Client *engine.LangflowClient
}

// ErrNoFlow means the agent has no Langflow flow to run on.
var ErrNoFlow = errors.New("agent has no Langflow flow")

// ErrPermanent marks a runtime error that trying again will not fix: no API
// key for the agent's provider, a model id the provider does not know, a
// refused key. Callers answer with their fallback instead of retrying.
var ErrPermanent = errors.New("agent runtime error is permanent")

// Run sends the turn to the agent's flow.
func (r LangflowRuntime) Run(ctx context.Context, in RunInput) (RunResult, error) {
	if r.Client == nil {
		return RunResult{}, errors.New("langflow client not configured")
	}
	if in.FlowID == "" || in.LangflowURL == "" {
		return RunResult{}, ErrNoFlow
	}
	if !in.Deadline.IsZero() {
		var cancel context.CancelFunc
		ctx, cancel = context.WithDeadline(ctx, in.Deadline)
		defer cancel()
	}
	res, err := r.Client.RunFlowWithCustomerUsage(ctx, in.SessionID, in.SenderID, in.Input,
		in.LangflowURL, in.LangflowAPIKey, in.FlowID, in.CustomerCode, in.PermissionToken, in.AgentToken, in.SystemPrompt)
	if err != nil {
		return RunResult{}, err
	}
	return RunResult{
		Text: res.Text,
		Usage: Usage{
			Provider: res.Provider, Model: res.Model, InputTokens: res.InputTokens,
			OutputTokens: res.OutputTokens, HasUsage: res.HasUsage, ReportedCostUSD: res.ReportedCostUSD,
		},
	}, nil
}

var (
	runtimesMu sync.RWMutex
	runtimes   = map[string]Runtime{}
)

// RegisterRuntime makes a runtime available to agents under name. The
// Langflow runtime needs no registration: every agent can fall back to the
// AI Engine flows. A nil r removes the runtime.
func RegisterRuntime(name string, r Runtime) {
	runtimesMu.Lock()
	defer runtimesMu.Unlock()
	if r == nil {
		delete(runtimes, name)
		return
	}
	runtimes[name] = r
}

// RuntimeFor returns the registered runtime for name.
func RuntimeFor(name string) (Runtime, bool) {
	runtimesMu.RLock()
	defer runtimesMu.RUnlock()
	r, ok := runtimes[name]
	return r, ok
}

// RuntimeAvailable reports whether agents may be saved with runtime name.
func RuntimeAvailable(name string) bool {
	if name == "" || name == RuntimeLangflow {
		return true
	}
	_, ok := RuntimeFor(name)
	return ok
}

// NativeRuntime returns the runtime that answers the turn without Langflow:
// the answering agent runs on runtime "native" and this server has it
// installed. Otherwise the turn runs on Langflow as before (an agent saved
// as native while the runtime is missing keeps working on the tier's flow).
func (p Plan) NativeRuntime() (Runtime, bool) {
	sel, ok := p.Agent()
	if !ok || sel.Agent.Runtime != RuntimeNative {
		return nil, false
	}
	return RuntimeFor(RuntimeNative)
}
