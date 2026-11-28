// Package native runs specialist agents without Langflow: the agent's own
// model (its provider and model, with the tenant's key for that provider)
// calls the agent's MCP tools on the ERP MCP server directly, in a loop,
// until it answers. Install it with agents.RegisterRuntime(agents.RuntimeNative, …).
package native

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"regexp"
	"strings"
	"time"

	"github.com/vietbui/chat-quality-agent/agents"
	"github.com/vietbui/chat-quality-agent/ai"
	"github.com/vietbui/chat-quality-agent/chatmemory"
	"github.com/vietbui/chat-quality-agent/config"
	"github.com/vietbui/chat-quality-agent/engine"
)

const (
	// DefaultMaxToolCalls bounds a turn's tool calls when the agent leaves
	// options.max_tool_calls at 0. A stock question takes two or three
	// (search_products, then inventory_by_line).
	DefaultMaxToolCalls = 8
	// maxToolResultChars caps one tool result sent back to the model.
	maxToolResultChars = 40_000
	// DefaultTurnTimeout bounds a turn without a deadline: every model and
	// tool call of the turn, retries included (a Langflow turn gets 60s for
	// one call).
	DefaultTurnTimeout = 2 * time.Minute
)

// Model is the agent's model client.
type Model struct {
	Client   ai.ToolCaller
	Provider string
	Model    string
}

// ModelResolver builds the client for an agent's provider and model.
type ModelResolver func(ctx context.Context, tenantID, provider, model string) (Model, error)

// Runtime is the native agent runtime.
type Runtime struct {
	// Tools is the ERP MCP server the agent's tools live on.
	Tools ToolServer
	// Models builds the agent's model client (TenantModels in production).
	Models ModelResolver
}

// New returns the runtime that calls tools on tools and resolves models
// with the tenant's AI settings.
func New(cfg *config.Config, tools ToolServer) *Runtime {
	return &Runtime{Tools: tools, Models: TenantModels(cfg)}
}

// TenantModels resolves an agent's model with the tenant's key and base URL
// for its provider (engine.ResolveTenantAIModel).
func TenantModels(cfg *config.Config) ModelResolver {
	return func(_ context.Context, tenantID, provider, model string) (Model, error) {
		resolved, err := engine.ResolveTenantAIModel(tenantID, cfg, provider, model)
		if errors.Is(err, engine.ErrLoadTenantAISettings) {
			return Model{}, err
		}
		if err != nil {
			// No key, a bad model id, a key that does not decrypt.
			return Model{}, fmt.Errorf("%w: %v", agents.ErrPermanent, err)
		}
		caller, ok := resolved.Client.(ai.ToolCaller)
		if !ok || resolved.Client == nil {
			return Model{}, fmt.Errorf("%w: provider %s cannot call tools", agents.ErrPermanent, resolved.Provider)
		}
		return Model{Client: caller, Provider: resolved.Provider, Model: resolved.Model}, nil
	}
}

var _ agents.Runtime = (*Runtime)(nil)

// Run answers one turn: the composed system prompt, the conversation from
// chat memory and the user's message go to the agent's model with the
// agent's tools; tool calls run on the ERP MCP server with the turn's
// tokens until the model answers or the agent's tool-call budget is spent.
func (r *Runtime) Run(ctx context.Context, in agents.RunInput) (agents.RunResult, error) {
	deadline := in.Deadline
	if deadline.IsZero() {
		deadline = time.Now().Add(DefaultTurnTimeout)
	}
	ctx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	started := time.Now()
	agent := in.Agent
	if r.Models == nil {
		return agents.RunResult{}, fmt.Errorf("%w: native runtime has no model resolver", agents.ErrPermanent)
	}
	model, err := r.Models(ctx, agent.TenantID, agent.Provider, agent.Model)
	if err != nil {
		return agents.RunResult{}, fmt.Errorf("agent %s model %s/%s: %w", agent.Key, agent.Provider, agent.Model, err)
	}
	tools, err := r.agentTools(ctx, in)
	if err != nil {
		return agents.RunResult{}, err
	}
	allowed := make(map[string]bool, len(tools))
	for _, t := range tools {
		allowed[t.Name] = true
	}

	budget := agent.Options.MaxToolCalls
	if budget <= 0 {
		budget = DefaultMaxToolCalls
	}
	messages := conversation(in.History, in.Input)
	usage := agents.Usage{Provider: model.Provider, Model: model.Model}
	allCosts := true
	var called []string
	calls := 0
	for round := 0; ; round++ {
		req := ai.ToolChatRequest{
			System: in.SystemPrompt, Messages: messages, Tools: tools,
			Temperature: agent.Options.Temperature,
		}
		// The budget is spent (or the model keeps asking for tools it may
		// not use): this request must answer with what it has.
		final := calls >= budget || round > budget
		if final && len(tools) > 0 {
			req.ToolChoice = ai.ToolChoiceNone
		}
		resp, err := model.Client.ChatWithTools(ctx, req)
		if err != nil {
			return agents.RunResult{Usage: usage}, classifyModelError(err)
		}
		addUsage(&usage, &allCosts, resp.AIResponse)
		if len(resp.ToolCalls) == 0 || len(tools) == 0 || final {
			log.Printf("[agents/native] agent=%s model=%s/%s rounds=%d tools=%s took=%s",
				agent.Key, usage.Provider, usage.Model, round+1, strings.Join(called, ","), time.Since(started).Round(time.Millisecond))
			return agents.RunResult{Text: strings.TrimSpace(resp.Content), Usage: usage}, nil
		}
		messages = append(messages, resp.Message)
		for _, call := range resp.ToolCalls {
			var result ToolResult
			switch {
			case !allowed[call.Name]:
				result = refusal("tool_not_allowed", "Công cụ này không thuộc phạm vi của trợ lý đang trả lời.")
			case !json.Valid(call.Arguments):
				result = refusal("invalid_arguments", "Tham số công cụ không phải JSON hợp lệ.")
			case calls >= budget:
				result = refusal("tool_call_limit", "Đã hết lượt gọi công cụ của tin nhắn này.")
			default:
				calls++
				called = append(called, call.Name)
				result, err = r.Tools.CallTool(ctx, agent.TenantID, Auth{AgentToken: in.AgentToken, PermissionToken: in.PermissionToken}, call.Name, call.Arguments)
				if err != nil {
					log.Printf("[agents/native] agent=%s tool %s failed: %v", agent.Key, call.Name, err)
					result = refusal("tool_unavailable", "Chưa gọi được công cụ. Vui lòng thử lại sau.")
				}
			}
			messages = append(messages, ai.ToolChatMessage{
				Role: ai.RoleTool, ToolCallID: call.ID, ToolName: call.Name,
				Text: capText(result.Text, maxToolResultChars), IsError: result.IsError,
			})
		}
	}
}

// agentTools lists the tools the turn may call: those the MCP server lists
// for the turn's tokens (already narrowed to the agent when the permission
// token carries it) and that the agent has. Without an agent token the MCP
// server serves nothing, so the agent answers without tools.
func (r *Runtime) agentTools(ctx context.Context, in agents.RunInput) ([]ai.ToolSpec, error) {
	if len(in.Agent.Tools) == 0 {
		return nil, nil
	}
	if in.AgentToken == "" || r.Tools == nil {
		log.Printf("[agents/native] agent=%s has tools but this turn has no ERP agent token; answering without tools", in.Agent.Key)
		return nil, nil
	}
	listed, err := r.Tools.ListTools(ctx, in.Agent.TenantID, Auth{AgentToken: in.AgentToken, PermissionToken: in.PermissionToken})
	if err != nil {
		return nil, fmt.Errorf("list agent tools: %w", err)
	}
	out := make([]ai.ToolSpec, 0, len(listed))
	for _, t := range listed {
		if in.Agent.HasTool(t.Name) {
			out = append(out, t)
		}
	}
	return out, nil
}

// conversation turns the chat-memory excerpt and the user's message into
// model messages: customer turns are the user, bot and staff turns the
// assistant (staff replies keep their label, so the model does not take
// them for its own). Turns of one role are merged. Bot or staff turns before
// the first customer turn are folded into it, each with its speaker label,
// because some providers refuse a conversation that opens with the
// assistant.
func conversation(history []chatmemory.RecentTurn, input string) []ai.ToolChatMessage {
	var out []ai.ToolChatMessage
	var lead []string
	add := func(role, speaker, text string) {
		text = strings.TrimSpace(text)
		if text == "" {
			return
		}
		if speaker == speakerStaff {
			text = speaker + ": " + text
		}
		if len(out) == 0 {
			if role == ai.RoleAssistant {
				if speaker != speakerStaff {
					text = speaker + ": " + text
				}
				lead = append(lead, text)
				return
			}
			if len(lead) > 0 {
				text = strings.Join(lead, "\n") + "\n" + speaker + ": " + text
			}
		}
		if n := len(out); n > 0 && out[n-1].Role == role {
			out[n-1].Text += "\n" + text
			return
		}
		out = append(out, ai.ToolChatMessage{Role: role, Text: text})
	}
	for _, turn := range history {
		switch turn.Speaker {
		case speakerBot, speakerStaff:
			add(ai.RoleAssistant, turn.Speaker, turn.Text)
		default:
			add(ai.RoleUser, speakerCustomer, turn.Text)
		}
	}
	add(ai.RoleUser, speakerCustomer, input)
	return out
}

// Speakers of chatmemory.RecentTurn.
const (
	speakerCustomer = "Khách"
	speakerBot      = "Bot"
	speakerStaff    = "Nhân viên"
)

func addUsage(u *agents.Usage, allCosts *bool, resp ai.AIResponse) {
	if resp.Provider != "" {
		u.Provider = resp.Provider
	}
	if resp.Model != "" {
		u.Model = resp.Model
	}
	u.InputTokens += resp.InputTokens
	u.OutputTokens += resp.OutputTokens
	if resp.InputTokens > 0 || resp.OutputTokens > 0 {
		u.HasUsage = true
	}
	switch {
	case resp.ReportedCostUSD == nil:
		*allCosts = false
		u.ReportedCostUSD = nil
	case *allCosts:
		total := *resp.ReportedCostUSD
		if u.ReportedCostUSD != nil {
			total += *u.ReportedCostUSD
		}
		u.ReportedCostUSD = &total
	}
}

// refusal is a tool result the runtime gives itself, in the MCP server's
// error shape.
func refusal(code, message string) ToolResult {
	b, _ := json.Marshal(map[string]string{"error": code, "message": message})
	return ToolResult{Text: string(b), IsError: true}
}

func capText(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// refusedRequest matches the client errors (bad key, unknown model, bad
// request) that the provider will refuse again.
var refusedRequest = regexp.MustCompile(`\b(400|401|403|404|422)\b`)

func classifyModelError(err error) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	if msg := err.Error(); refusedRequest.MatchString(msg) && !strings.Contains(msg, "429") {
		return fmt.Errorf("%w: %v", agents.ErrPermanent, err)
	}
	return err
}
