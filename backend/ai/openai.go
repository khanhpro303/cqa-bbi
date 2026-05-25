package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

var openAIHTTPClient = NewHTTPClientWithTimeout()

type OpenAIProvider struct {
	apiKey  string
	model   string
	baseURL string
}

func NewOpenAIProvider(apiKey, model, baseURL string) *OpenAIProvider {
	if model == "" {
		model = "gpt-5.4-mini"
	}
	mappedModel := model
	switch model {
	case "gpt-5.4-mini", "gpt-5-mini", "gpt-5-nano":
		mappedModel = "gpt-4o-mini"
	case "gpt-5", "gpt-5.3-codex", "gpt-5.3-chat-latest":
		mappedModel = "gpt-4o"
	}
	return &OpenAIProvider{
		apiKey:  apiKey,
		model:   mappedModel,
		baseURL: strings.TrimRight(baseURL, "/"),
	}
}

func (o *OpenAIProvider) AnalyzeChat(ctx context.Context, systemPrompt string, chatTranscript string) (AIResponse, error) {
	return withRetry(ctx, "openai", func() (AIResponse, error) {
		// Prefer Responses API for modern OpenAI models, then fallback to Chat Completions
		// for compatible proxies/older deployments.
		resp, err := o.analyzeWithResponses(ctx, systemPrompt, chatTranscript)
		if err == nil {
			return resp, nil
		}

		if !shouldFallbackToChatCompletions(err) {
			return AIResponse{}, err
		}

		resp, chatErr := o.analyzeWithChatCompletions(ctx, systemPrompt, chatTranscript)
		if chatErr == nil {
			return resp, nil
		}

		return AIResponse{}, fmt.Errorf("openai responses failed (%v); chat_completions failed (%v)", err, chatErr)
	})
}

func (o *OpenAIProvider) AnalyzeChatBatch(ctx context.Context, systemPrompt string, items []BatchItem) (AIResponse, error) {
	batchPrompt := WrapBatchPrompt(systemPrompt, len(items))
	batchTranscript := FormatBatchTranscript(items)
	return o.AnalyzeChat(ctx, batchPrompt, batchTranscript)
}

func (o *OpenAIProvider) chatCompletionsURL() string {
	base := o.baseURL
	if base == "" {
		base = "https://api.openai.com/v1"
	}
	if strings.HasSuffix(base, "/chat/completions") {
		return base
	}
	return base + "/chat/completions"
}

func (o *OpenAIProvider) responsesURL() string {
	base := o.baseURL
	if base == "" {
		base = "https://api.openai.com/v1"
	}
	if strings.HasSuffix(base, "/responses") {
		return base
	}
	return base + "/responses"
}

func (o *OpenAIProvider) analyzeWithResponses(ctx context.Context, systemPrompt string, chatTranscript string) (AIResponse, error) {
	reqBody := map[string]interface{}{
		"model": o.model,
		"input": []map[string]interface{}{
			{
				"role": "system",
				"content": []map[string]string{
					{"type": "input_text", "text": systemPrompt},
				},
			},
			{
				"role": "user",
				"content": []map[string]string{
					{"type": "input_text", "text": chatTranscript},
				},
			},
		},
	}

	body, err := o.doJSONPost(ctx, o.responsesURL(), reqBody)
	if err != nil {
		return AIResponse{}, err
	}

	var parsed struct {
		Model      string `json:"model"`
		OutputText string `json:"output_text"`
		Output     []struct {
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"output"`
		Usage struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return AIResponse{}, fmt.Errorf("openai parse responses response failed: %w", err)
	}

	content := strings.TrimSpace(parsed.OutputText)
	if content == "" {
		for _, out := range parsed.Output {
			for _, c := range out.Content {
				if c.Type == "output_text" || c.Type == "text" {
					if strings.TrimSpace(c.Text) != "" {
						content = c.Text
						break
					}
				}
			}
			if content != "" {
				break
			}
		}
	}
	if content == "" {
		return AIResponse{}, fmt.Errorf("openai responses api returned empty content")
	}

	model := parsed.Model
	if model == "" {
		model = o.model
	}

	return AIResponse{
		Content:      content,
		InputTokens:  parsed.Usage.InputTokens,
		OutputTokens: parsed.Usage.OutputTokens,
		Model:        model,
		Provider:     "openai",
	}, nil
}

func (o *OpenAIProvider) analyzeWithChatCompletions(ctx context.Context, systemPrompt string, chatTranscript string) (AIResponse, error) {
	reqBody := map[string]interface{}{
		"model": o.model,
		"messages": []map[string]string{
			{"role": "system", "content": systemPrompt},
			{"role": "user", "content": chatTranscript},
		},
	}

	body, err := o.doJSONPost(ctx, o.chatCompletionsURL(), reqBody)
	if err != nil {
		return AIResponse{}, err
	}

	var parsed struct {
		Model   string `json:"model"`
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return AIResponse{}, fmt.Errorf("openai parse chat_completions response failed: %w", err)
	}
	if len(parsed.Choices) == 0 || strings.TrimSpace(parsed.Choices[0].Message.Content) == "" {
		return AIResponse{}, fmt.Errorf("openai chat_completions api returned empty content")
	}

	model := parsed.Model
	if model == "" {
		model = o.model
	}

	return AIResponse{
		Content:      parsed.Choices[0].Message.Content,
		InputTokens:  parsed.Usage.PromptTokens,
		OutputTokens: parsed.Usage.CompletionTokens,
		Model:        model,
		Provider:     "openai",
	}, nil
}

func (o *OpenAIProvider) doJSONPost(ctx context.Context, url string, reqBody interface{}) ([]byte, error) {
	payload, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("openai marshal request failed: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("openai create request failed: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+o.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := openAIHTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("openai request failed: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("openai api error %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	return body, nil
}

func shouldFallbackToChatCompletions(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "api error 401") || strings.Contains(msg, "api error 403") {
		return false
	}
	if strings.Contains(msg, "api error 404") || strings.Contains(msg, "api error 405") ||
		strings.Contains(msg, "api error 415") || strings.Contains(msg, "api error 422") {
		return true
	}
	return strings.Contains(msg, "unsupported") ||
		strings.Contains(msg, "not found") ||
		strings.Contains(msg, "unknown endpoint") ||
		strings.Contains(msg, "invalid_request_error")
}
