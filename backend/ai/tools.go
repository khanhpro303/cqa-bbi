package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
)

// Tool calling. The native agent runtime (agents/native) runs a model with
// the agent's MCP tools: the model asks for tool calls, the runtime runs them
// and sends the results back, until the model answers in text. Every
// provider of NewProvider implements ToolCaller.

// ToolSpec is a function the model may call.
type ToolSpec struct {
	Name        string
	Description string
	// Parameters is the JSON schema of the arguments, an "object" schema.
	Parameters map[string]any
}

// ToolCall is one call the model asked for.
type ToolCall struct {
	ID   string
	Name string
	// Arguments is the JSON object the model wrote. It may be invalid JSON:
	// the caller checks it before running the tool.
	Arguments json.RawMessage
}

// Roles of a tool-calling conversation.
const (
	RoleUser      = "user"
	RoleAssistant = "assistant"
	// RoleTool carries the result of one ToolCall (ToolCallID, ToolName).
	RoleTool = "tool"
)

// ToolChoice says whether the model may call tools on this request.
type ToolChoice string

const (
	// ToolChoiceAuto lets the model call tools or answer.
	ToolChoiceAuto ToolChoice = "auto"
	// ToolChoiceNone makes the model answer in text. The tools are still
	// sent, because some providers refuse earlier tool calls in the
	// conversation without their definitions.
	ToolChoiceNone ToolChoice = "none"
)

// ToolChatMessage is one message of a tool-calling conversation.
type ToolChatMessage struct {
	Role string
	Text string
	// ToolCalls are the calls an assistant message asked for.
	ToolCalls []ToolCall
	// ToolCallID and ToolName name the call a tool message answers.
	ToolCallID string
	ToolName   string
	// IsError marks a tool result that is an error.
	IsError bool
	// native is the provider's own form of an assistant message, sent back
	// unchanged on the next request (Gemini's thought signatures must be).
	native any
}

// ToolChatRequest is one model request of a tool-calling conversation.
type ToolChatRequest struct {
	System   string
	Messages []ToolChatMessage
	Tools    []ToolSpec
	// ToolChoice is ToolChoiceAuto when empty.
	ToolChoice ToolChoice
	// Temperature is the provider default when nil.
	Temperature *float64
}

// ToolChatResponse is the model's reply: text, tool calls, or both.
type ToolChatResponse struct {
	// AIResponse.Content is the reply text ("" when the model only calls
	// tools) and carries the usage of this request.
	AIResponse
	ToolCalls []ToolCall
	// Message is the assistant message to append to the conversation before
	// the results of ToolCalls.
	Message ToolChatMessage
}

// ToolCaller is a provider that can run a tool-calling conversation.
type ToolCaller interface {
	ChatWithTools(ctx context.Context, req ToolChatRequest) (ToolChatResponse, error)
}

var (
	_ ToolCaller = (*OpenAIProvider)(nil)
	_ ToolCaller = (*ClaudeProvider)(nil)
	_ ToolCaller = (*GeminiProvider)(nil)
)

// withToolRetry retries a tool-calling request like withRetry does.
func withToolRetry(ctx context.Context, provider string, fn func() (ToolChatResponse, error)) (ToolChatResponse, error) {
	var out ToolChatResponse
	_, err := withRetry(ctx, provider, func() (AIResponse, error) {
		resp, err := fn()
		if err == nil {
			out = resp
		}
		return resp.AIResponse, err
	})
	return out, err
}

// withTemperatureFallback sends req, and once more without a temperature
// when the model refuses the one set (some reasoning models only take their
// default).
func withTemperatureFallback(ctx context.Context, provider string, req ToolChatRequest,
	send func(ToolChatRequest) (ToolChatResponse, error)) (ToolChatResponse, error) {
	resp, err := withToolRetry(ctx, provider, func() (ToolChatResponse, error) { return send(req) })
	if err == nil || req.Temperature == nil || !temperatureRefused(err) {
		return resp, err
	}
	log.Printf("[%s] model refused temperature %.2f; sending again with the default: %v", provider, *req.Temperature, err)
	req.Temperature = nil
	return withToolRetry(ctx, provider, func() (ToolChatResponse, error) { return send(req) })
}

func temperatureRefused(err error) bool {
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "temperature") &&
		(strings.Contains(msg, "400") || strings.Contains(msg, "unsupported") || strings.Contains(msg, "invalid") ||
			strings.Contains(msg, "not support") || strings.Contains(msg, "deprecated"))
}

// toolArguments returns a call's arguments as a JSON object for providers
// that need one decoded: malformed arguments become an empty object.
func toolArguments(raw json.RawMessage) map[string]any {
	args := map[string]any{}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &args)
	}
	if args == nil {
		args = map[string]any{}
	}
	return args
}

// rawToolArguments encodes decoded arguments; nil or unencodable is "{}".
func rawToolArguments(args map[string]any) json.RawMessage {
	if len(args) == 0 {
		return json.RawMessage(`{}`)
	}
	b, err := json.Marshal(args)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return b
}

// generatedToolCallID prefixes ids made up for calls the model left without
// one (some OpenAI-compatible models, older Gemini models). Gemini gets such
// calls answered without an id, as it sent them.
const generatedToolCallID = "cqa_call_"

// fillToolCallIDs gives calls without an id one that is unique in the reply.
func fillToolCallIDs(calls []ToolCall) {
	for i := range calls {
		if strings.TrimSpace(calls[i].ID) == "" {
			calls[i].ID = fmt.Sprintf("%s%d", generatedToolCallID, i+1)
		}
	}
}

// providerToolCallID is the id to send back to the provider for a call.
func providerToolCallID(id string) string {
	if strings.HasPrefix(id, generatedToolCallID) {
		return ""
	}
	return id
}
