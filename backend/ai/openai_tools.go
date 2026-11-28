package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// ChatWithTools runs one tool-calling request on Chat Completions, for both
// OpenAI and OpenRouter (OpenRouter only serves Chat Completions; OpenAI
// serves function tools on it for every chat model).
func (o *OpenAIProvider) ChatWithTools(ctx context.Context, req ToolChatRequest) (ToolChatResponse, error) {
	return withTemperatureFallback(ctx, o.providerName(), req, func(req ToolChatRequest) (ToolChatResponse, error) {
		body, err := o.doJSONPost(ctx, o.chatCompletionsURL(), o.toolChatBody(req))
		if err != nil {
			return ToolChatResponse{}, err
		}
		resp, err := parseToolChatCompletion(body, o.model)
		resp.Provider = o.providerName()
		return resp, err
	})
}

type openAIToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

func (o *OpenAIProvider) toolChatBody(req ToolChatRequest) map[string]any {
	messages := make([]map[string]any, 0, len(req.Messages)+1)
	if strings.TrimSpace(req.System) != "" {
		messages = append(messages, map[string]any{"role": "system", "content": req.System})
	}
	for _, m := range req.Messages {
		switch m.Role {
		case RoleAssistant:
			msg := map[string]any{"role": "assistant"}
			if m.Text != "" || len(m.ToolCalls) == 0 {
				msg["content"] = m.Text
			} else {
				msg["content"] = nil
			}
			if len(m.ToolCalls) > 0 {
				calls := make([]map[string]any, 0, len(m.ToolCalls))
				for _, c := range m.ToolCalls {
					args := string(c.Arguments)
					if strings.TrimSpace(args) == "" {
						args = "{}"
					}
					calls = append(calls, map[string]any{
						"id": c.ID, "type": "function",
						"function": map[string]any{"name": c.Name, "arguments": args},
					})
				}
				msg["tool_calls"] = calls
			}
			messages = append(messages, msg)
		case RoleTool:
			messages = append(messages, map[string]any{"role": "tool", "tool_call_id": m.ToolCallID, "content": m.Text})
		default:
			messages = append(messages, map[string]any{"role": "user", "content": m.Text})
		}
	}
	body := map[string]any{"model": o.model, "messages": messages}
	if len(req.Tools) > 0 {
		tools := make([]map[string]any, 0, len(req.Tools))
		for _, t := range req.Tools {
			fn := map[string]any{"name": t.Name, "parameters": toolSchema(t.Parameters)}
			if t.Description != "" {
				fn["description"] = t.Description
			}
			tools = append(tools, map[string]any{"type": "function", "function": fn})
		}
		body["tools"] = tools
		choice := req.ToolChoice
		if choice == "" {
			choice = ToolChoiceAuto
		}
		body["tool_choice"] = string(choice)
	}
	if req.Temperature != nil {
		body["temperature"] = *req.Temperature
	}
	return body
}

// toolSchema returns a usable "object" schema for a tool's parameters.
func toolSchema(schema map[string]any) map[string]any {
	if len(schema) == 0 {
		return map[string]any{"type": "object", "properties": map[string]any{}}
	}
	if _, ok := schema["type"]; !ok {
		out := make(map[string]any, len(schema)+1)
		for k, v := range schema {
			out[k] = v
		}
		out["type"] = "object"
		return out
	}
	return schema
}

// parseToolChatCompletion reads a Chat Completions reply that may hold tool
// calls instead of (or with) text.
func parseToolChatCompletion(body []byte, fallbackModel string) (ToolChatResponse, error) {
	var parsed struct {
		Model   string `json:"model"`
		Choices []struct {
			Message struct {
				Content   *string          `json:"content"`
				ToolCalls []openAIToolCall `json:"tool_calls"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     int      `json:"prompt_tokens"`
			CompletionTokens int      `json:"completion_tokens"`
			Cost             *float64 `json:"cost"`
		} `json:"usage"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return ToolChatResponse{}, fmt.Errorf("openai parse chat_completions response failed: %w", err)
	}
	if parsed.Error != nil && len(parsed.Choices) == 0 {
		// OpenRouter reports some upstream failures in a 200 body.
		return ToolChatResponse{}, fmt.Errorf("openai chat_completions error: %s", parsed.Error.Message)
	}
	if len(parsed.Choices) == 0 {
		return ToolChatResponse{}, fmt.Errorf("openai chat_completions api returned no choices")
	}
	model := parsed.Model
	if model == "" {
		model = fallbackModel
	}
	msg := parsed.Choices[0].Message
	text := ""
	if msg.Content != nil {
		text = *msg.Content
	}
	calls := make([]ToolCall, 0, len(msg.ToolCalls))
	for _, c := range msg.ToolCalls {
		if c.Function.Name == "" {
			continue
		}
		args := strings.TrimSpace(c.Function.Arguments)
		if args == "" {
			args = "{}"
		}
		calls = append(calls, ToolCall{ID: c.ID, Name: c.Function.Name, Arguments: json.RawMessage(args)})
	}
	fillToolCallIDs(calls)
	return ToolChatResponse{
		AIResponse: AIResponse{
			Content:         text,
			InputTokens:     parsed.Usage.PromptTokens,
			OutputTokens:    parsed.Usage.CompletionTokens,
			ReportedCostUSD: parsed.Usage.Cost,
			Model:           model,
			Provider:        "openai",
		},
		ToolCalls: calls,
		Message:   ToolChatMessage{Role: RoleAssistant, Text: text, ToolCalls: calls},
	}, nil
}
