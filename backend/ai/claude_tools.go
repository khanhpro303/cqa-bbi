package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

// ChatWithTools runs one tool-calling request on the Messages API.
func (c *ClaudeProvider) ChatWithTools(ctx context.Context, req ToolChatRequest) (ToolChatResponse, error) {
	return withTemperatureFallback(ctx, "claude", req, func(req ToolChatRequest) (ToolChatResponse, error) {
		opts := []option.RequestOption{
			option.WithAPIKey(c.apiKey),
			option.WithHTTPClient(claudeHTTPClient),
		}
		if c.baseURL != "" {
			opts = append(opts, option.WithBaseURL(c.baseURL))
		}
		client := anthropic.NewClient(opts...)
		message, err := client.Messages.New(ctx, c.toolChatParams(req))
		if err != nil {
			return ToolChatResponse{}, fmt.Errorf("claude api error: %w", err)
		}
		return claudeToolResponse(message), nil
	})
}

func (c *ClaudeProvider) toolChatParams(req ToolChatRequest) anthropic.MessageNewParams {
	params := anthropic.MessageNewParams{
		Model:     anthropic.Model(c.model),
		MaxTokens: int64(c.maxTokens),
		Messages:  claudeToolMessages(req.Messages),
	}
	if strings.TrimSpace(req.System) != "" {
		params.System = []anthropic.TextBlockParam{{Text: req.System}}
	}
	if len(req.Tools) > 0 {
		tools := make([]anthropic.ToolUnionParam, 0, len(req.Tools))
		for _, t := range req.Tools {
			tool := anthropic.ToolParam{Name: t.Name, InputSchema: claudeInputSchema(t.Parameters)}
			if t.Description != "" {
				tool.Description = anthropic.String(t.Description)
			}
			tools = append(tools, anthropic.ToolUnionParam{OfTool: &tool})
		}
		params.Tools = tools
		if req.ToolChoice == ToolChoiceNone {
			params.ToolChoice = anthropic.ToolChoiceUnionParam{OfNone: &anthropic.ToolChoiceNoneParam{}}
		} else {
			params.ToolChoice = anthropic.ToolChoiceUnionParam{OfAuto: &anthropic.ToolChoiceAutoParam{}}
		}
	}
	if req.Temperature != nil {
		// Claude takes 0 to 1; agents may be set up to 2 for other vendors.
		t := *req.Temperature
		if t > 1 {
			t = 1
		}
		params.Temperature = anthropic.Float(t)
	}
	return params
}

// claudeToolMessages converts the conversation: tool results become
// tool_result blocks of a user message, and consecutive messages of one
// role are merged (all results of one turn must sit in one user message).
func claudeToolMessages(in []ToolChatMessage) []anthropic.MessageParam {
	out := make([]anthropic.MessageParam, 0, len(in))
	push := func(role anthropic.MessageParamRole, blocks []anthropic.ContentBlockParamUnion) {
		if len(blocks) == 0 {
			return
		}
		if n := len(out); n > 0 && out[n-1].Role == role {
			out[n-1].Content = append(out[n-1].Content, blocks...)
			return
		}
		out = append(out, anthropic.MessageParam{Role: role, Content: blocks})
	}
	for _, m := range in {
		switch m.Role {
		case RoleAssistant:
			var blocks []anthropic.ContentBlockParamUnion
			if strings.TrimSpace(m.Text) != "" {
				blocks = append(blocks, anthropic.NewTextBlock(m.Text))
			}
			for _, call := range m.ToolCalls {
				blocks = append(blocks, anthropic.NewToolUseBlock(call.ID, toolArguments(call.Arguments), call.Name))
			}
			push(anthropic.MessageParamRoleAssistant, blocks)
		case RoleTool:
			text := m.Text
			if strings.TrimSpace(text) == "" {
				text = "{}"
			}
			push(anthropic.MessageParamRoleUser, []anthropic.ContentBlockParamUnion{
				anthropic.NewToolResultBlock(m.ToolCallID, text, m.IsError),
			})
		default:
			if strings.TrimSpace(m.Text) != "" {
				push(anthropic.MessageParamRoleUser, []anthropic.ContentBlockParamUnion{anthropic.NewTextBlock(m.Text)})
			}
		}
	}
	return out
}

// claudeInputSchema maps a JSON schema onto the SDK's input schema: type is
// always "object", properties and required have their own fields, and the
// rest (additionalProperties...) rides along as extra fields.
func claudeInputSchema(schema map[string]any) anthropic.ToolInputSchemaParam {
	out := anthropic.ToolInputSchemaParam{Properties: map[string]any{}}
	for k, v := range schema {
		switch k {
		case "type":
		case "properties":
			if v != nil {
				out.Properties = v
			}
		case "required":
			switch req := v.(type) {
			case []string:
				out.Required = append([]string(nil), req...)
			case []any:
				for _, r := range req {
					if s, ok := r.(string); ok {
						out.Required = append(out.Required, s)
					}
				}
			}
		default:
			if out.ExtraFields == nil {
				out.ExtraFields = map[string]any{}
			}
			out.ExtraFields[k] = v
		}
	}
	return out
}

func claudeToolResponse(message *anthropic.Message) ToolChatResponse {
	var text strings.Builder
	var calls []ToolCall
	for _, block := range message.Content {
		switch block.Type {
		case "text":
			text.WriteString(block.Text)
		case "tool_use":
			args := block.Input
			if len(args) == 0 {
				args = json.RawMessage(`{}`)
			}
			calls = append(calls, ToolCall{ID: block.ID, Name: block.Name, Arguments: args})
		}
	}
	fillToolCallIDs(calls)
	content := text.String()
	return ToolChatResponse{
		AIResponse: AIResponse{
			Content:      content,
			InputTokens:  int(message.Usage.InputTokens),
			OutputTokens: int(message.Usage.OutputTokens),
			Model:        string(message.Model),
			Provider:     "claude",
		},
		ToolCalls: calls,
		Message:   ToolChatMessage{Role: RoleAssistant, Text: content, ToolCalls: calls},
	}
}
