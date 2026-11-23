package ai

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"google.golang.org/genai"
)

// ImageInput is one image sent to a vision model.
type ImageInput struct {
	MediaType string // image/jpeg, image/png, image/webp, image/gif
	Data      []byte
}

// VisionProvider is an optional capability: providers that can read images.
type VisionProvider interface {
	AnalyzeImages(ctx context.Context, systemPrompt, text string, images []ImageInput) (AIResponse, error)
}

var errNoImages = errors.New("no images to analyze")

// AnalyzeImages reads images with the Claude model.
func (c *ClaudeProvider) AnalyzeImages(ctx context.Context, systemPrompt, text string, images []ImageInput) (AIResponse, error) {
	if len(images) == 0 {
		return AIResponse{}, errNoImages
	}
	return withRetry(ctx, "claude", func() (AIResponse, error) {
		opts := []option.RequestOption{option.WithAPIKey(c.apiKey), option.WithHTTPClient(claudeHTTPClient)}
		if c.baseURL != "" {
			opts = append(opts, option.WithBaseURL(c.baseURL))
		}
		client := anthropic.NewClient(opts...)
		blocks := make([]anthropic.ContentBlockParamUnion, 0, len(images)+1)
		for _, im := range images {
			blocks = append(blocks, anthropic.NewImageBlockBase64(im.MediaType, base64.StdEncoding.EncodeToString(im.Data)))
		}
		blocks = append(blocks, anthropic.NewTextBlock(text))
		maxTokens := c.maxTokens
		if maxTokens > 1024 {
			maxTokens = 1024
		}
		message, err := client.Messages.New(ctx, anthropic.MessageNewParams{
			Model:     anthropic.Model(c.model),
			MaxTokens: int64(maxTokens),
			System:    []anthropic.TextBlockParam{{Text: systemPrompt}},
			Messages:  []anthropic.MessageParam{anthropic.NewUserMessage(blocks...)},
		})
		if err != nil {
			return AIResponse{}, fmt.Errorf("claude api error: %w", err)
		}
		var out string
		for _, block := range message.Content {
			if block.Type == "text" {
				out = block.Text
				break
			}
		}
		if out == "" {
			return AIResponse{}, fmt.Errorf("claude api returned empty content")
		}
		return AIResponse{Content: out, InputTokens: int(message.Usage.InputTokens), OutputTokens: int(message.Usage.OutputTokens), Model: string(message.Model), Provider: "claude"}, nil
	})
}

// AnalyzeImages reads images with the OpenAI model (Chat Completions, which
// every OpenAI-compatible proxy serves).
func (o *OpenAIProvider) AnalyzeImages(ctx context.Context, systemPrompt, text string, images []ImageInput) (AIResponse, error) {
	if len(images) == 0 {
		return AIResponse{}, errNoImages
	}
	return withRetry(ctx, "openai", func() (AIResponse, error) {
		content := make([]map[string]interface{}, 0, len(images)+1)
		content = append(content, map[string]interface{}{"type": "text", "text": text})
		for _, im := range images {
			content = append(content, map[string]interface{}{
				"type":      "image_url",
				"image_url": map[string]string{"url": "data:" + im.MediaType + ";base64," + base64.StdEncoding.EncodeToString(im.Data), "detail": "high"},
			})
		}
		reqBody := map[string]interface{}{
			"model": o.model,
			"messages": []map[string]interface{}{
				{"role": "system", "content": systemPrompt},
				{"role": "user", "content": content},
			},
		}
		body, err := o.doJSONPost(ctx, o.chatCompletionsURL(), reqBody)
		if err != nil {
			return AIResponse{}, err
		}
		return parseChatCompletion(body, o.model)
	})
}

// AnalyzeImages reads images with the Gemini model.
func (g *GeminiProvider) AnalyzeImages(ctx context.Context, systemPrompt, text string, images []ImageInput) (AIResponse, error) {
	if len(images) == 0 {
		return AIResponse{}, errNoImages
	}
	return withRetry(ctx, "gemini", func() (AIResponse, error) {
		clientCfg := &genai.ClientConfig{APIKey: g.apiKey, Backend: genai.BackendGeminiAPI, HTTPClient: NewHTTPClientWithTimeout()}
		if g.baseURL != "" {
			clientCfg.HTTPOptions = genai.HTTPOptions{BaseURL: g.baseURL}
		}
		client, err := genai.NewClient(ctx, clientCfg)
		if err != nil {
			return AIResponse{}, fmt.Errorf("gemini client error: %w", err)
		}
		parts := make([]*genai.Part, 0, len(images)+1)
		for _, im := range images {
			parts = append(parts, genai.NewPartFromBytes(im.Data, im.MediaType))
		}
		parts = append(parts, genai.NewPartFromText(text))
		result, err := client.Models.GenerateContent(ctx, g.model, []*genai.Content{genai.NewContentFromParts(parts, genai.RoleUser)}, &genai.GenerateContentConfig{
			SystemInstruction: genai.NewContentFromText(systemPrompt, "user"),
		})
		if err != nil {
			return AIResponse{}, fmt.Errorf("gemini api error: %w", err)
		}
		out := strings.TrimSpace(result.Text())
		if out == "" {
			return AIResponse{}, fmt.Errorf("gemini api returned empty content")
		}
		resp := AIResponse{Content: out, Model: g.model, Provider: "gemini"}
		if result.UsageMetadata != nil {
			resp.InputTokens = int(result.UsageMetadata.PromptTokenCount)
			resp.OutputTokens = int(result.UsageMetadata.CandidatesTokenCount)
		}
		return resp, nil
	})
}
