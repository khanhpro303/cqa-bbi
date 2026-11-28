package ai

import (
	"context"
	"fmt"
	"strings"

	"google.golang.org/genai"
)

// ChatWithTools runs one tool-calling request on GenerateContent.
func (g *GeminiProvider) ChatWithTools(ctx context.Context, req ToolChatRequest) (ToolChatResponse, error) {
	return withTemperatureFallback(ctx, "gemini", req, func(req ToolChatRequest) (ToolChatResponse, error) {
		clientCfg := &genai.ClientConfig{
			APIKey:     g.apiKey,
			Backend:    genai.BackendGeminiAPI,
			HTTPClient: NewHTTPClientWithTimeout(),
		}
		if g.baseURL != "" {
			clientCfg.HTTPOptions = genai.HTTPOptions{BaseURL: g.baseURL}
		}
		client, err := genai.NewClient(ctx, clientCfg)
		if err != nil {
			return ToolChatResponse{}, fmt.Errorf("gemini client error: %w", err)
		}
		result, err := client.Models.GenerateContent(ctx, g.model, geminiToolContents(req.Messages), geminiToolConfig(req))
		if err != nil {
			return ToolChatResponse{}, fmt.Errorf("gemini api error: %w", err)
		}
		return geminiToolResponse(result, g.model), nil
	})
}

func geminiToolConfig(req ToolChatRequest) *genai.GenerateContentConfig {
	cfg := &genai.GenerateContentConfig{}
	if strings.TrimSpace(req.System) != "" {
		cfg.SystemInstruction = genai.NewContentFromText(req.System, genai.RoleUser)
	}
	if len(req.Tools) > 0 {
		decls := make([]*genai.FunctionDeclaration, 0, len(req.Tools))
		for _, t := range req.Tools {
			decls = append(decls, &genai.FunctionDeclaration{
				Name: t.Name, Description: t.Description, ParametersJsonSchema: toolSchema(t.Parameters),
			})
		}
		cfg.Tools = []*genai.Tool{{FunctionDeclarations: decls}}
		mode := genai.FunctionCallingConfigModeAuto
		if req.ToolChoice == ToolChoiceNone {
			mode = genai.FunctionCallingConfigModeNone
		}
		cfg.ToolConfig = &genai.ToolConfig{FunctionCallingConfig: &genai.FunctionCallingConfig{Mode: mode}}
	}
	if req.Temperature != nil {
		cfg.Temperature = genai.Ptr(float32(*req.Temperature))
	}
	return cfg
}

// geminiToolContents converts the conversation. A model message the
// provider returned is sent back as it came (with its thought signatures);
// the results of one turn's calls go in one user content.
func geminiToolContents(in []ToolChatMessage) []*genai.Content {
	out := make([]*genai.Content, 0, len(in))
	push := func(role genai.Role, parts []*genai.Part) {
		if len(parts) == 0 {
			return
		}
		if n := len(out); n > 0 && out[n-1].Role == string(role) {
			out[n-1].Parts = append(out[n-1].Parts, parts...)
			return
		}
		out = append(out, &genai.Content{Role: string(role), Parts: parts})
	}
	for _, m := range in {
		switch m.Role {
		case RoleAssistant:
			if native, ok := m.native.(*genai.Content); ok && native != nil {
				out = append(out, native)
				continue
			}
			var parts []*genai.Part
			if strings.TrimSpace(m.Text) != "" {
				parts = append(parts, genai.NewPartFromText(m.Text))
			}
			for _, call := range m.ToolCalls {
				parts = append(parts, &genai.Part{FunctionCall: &genai.FunctionCall{
					ID: providerToolCallID(call.ID), Name: call.Name, Args: toolArguments(call.Arguments),
				}})
			}
			push(genai.RoleModel, parts)
		case RoleTool:
			key := "output"
			if m.IsError {
				key = "error"
			}
			push(genai.RoleUser, []*genai.Part{{FunctionResponse: &genai.FunctionResponse{
				ID: providerToolCallID(m.ToolCallID), Name: m.ToolName, Response: map[string]any{key: m.Text},
			}}})
		default:
			if strings.TrimSpace(m.Text) != "" {
				push(genai.RoleUser, []*genai.Part{genai.NewPartFromText(m.Text)})
			}
		}
	}
	return out
}

func geminiToolResponse(result *genai.GenerateContentResponse, configuredModel string) ToolChatResponse {
	model := result.ModelVersion
	if model == "" {
		model = configuredModel
	}
	resp := ToolChatResponse{AIResponse: AIResponse{Model: model, Provider: "gemini"}}
	if usage := result.UsageMetadata; usage != nil {
		resp.InputTokens = int(usage.PromptTokenCount)
		resp.OutputTokens = int(usage.CandidatesTokenCount) + int(usage.ThoughtsTokenCount)
	}
	if len(result.Candidates) == 0 || result.Candidates[0].Content == nil {
		resp.Message = ToolChatMessage{Role: RoleAssistant}
		return resp
	}
	content := result.Candidates[0].Content
	var text strings.Builder
	var calls []ToolCall
	for _, part := range content.Parts {
		switch {
		case part == nil:
		case part.FunctionCall != nil:
			calls = append(calls, ToolCall{
				ID: part.FunctionCall.ID, Name: part.FunctionCall.Name, Arguments: rawToolArguments(part.FunctionCall.Args),
			})
		case part.Text != "" && !part.Thought:
			text.WriteString(part.Text)
		}
	}
	fillToolCallIDs(calls)
	if content.Role == "" {
		content.Role = genai.RoleModel
	}
	resp.Content = text.String()
	resp.ToolCalls = calls
	resp.Message = ToolChatMessage{Role: RoleAssistant, Text: resp.Content, ToolCalls: calls, native: content}
	return resp
}
