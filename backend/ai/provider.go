package ai

import (
	"context"
	"strings"
)

// AIResponse contains the AI response text and usage metrics.
type AIResponse struct {
	Content         string
	InputTokens     int
	OutputTokens    int
	Model           string
	Provider        string // "claude" | "gemini" | "openai" | "openrouter"
	ReportedCostUSD *float64
}

// UsageCostUSD prefers a billed amount supplied by the provider, including zero.
func (r AIResponse) UsageCostUSD() float64 {
	if r.ReportedCostUSD != nil && *r.ReportedCostUSD >= 0 {
		return *r.ReportedCostUSD
	}
	return CalculateCostUSD(r.Provider, r.Model, r.InputTokens, r.OutputTokens)
}

// BatchItem represents one conversation in a batch request.
type BatchItem struct {
	ConversationID string
	Transcript     string
}

// AIProvider defines the interface for AI chat analysis.
type AIProvider interface {
	// AnalyzeChat sends a system prompt + chat transcript to the AI and returns the response with usage.
	AnalyzeChat(ctx context.Context, systemPrompt string, chatTranscript string) (AIResponse, error)

	// AnalyzeChatBatch sends multiple conversations in one prompt and returns a combined response.
	// The response Content will be a JSON array of results, one per conversation (in order).
	AnalyzeChatBatch(ctx context.Context, systemPrompt string, items []BatchItem) (AIResponse, error)
}

// JSONProvider is an optional capability for providers that can ask the model
// to return a JSON object at the transport level. Callers must still validate
// the returned object's application-specific schema.
type JSONProvider interface {
	AnalyzeJSON(ctx context.Context, systemPrompt string, input string) (AIResponse, error)
}

type JSONSchemaProvider interface {
	AnalyzeJSONSchema(ctx context.Context, systemPrompt, input string, schema map[string]interface{}) (AIResponse, error)
}

// CalculateCostUSD returns estimated cost in USD based on provider, model, and token counts.
func CalculateCostUSD(provider, model string, inputTokens, outputTokens int) float64 {
	rates, ok := lookupTokenRates(provider, model)
	if !ok {
		return 0
	}
	if NormalizeProvider(provider) == "openai" && inputTokens > 272_000 &&
		(modelMatchesRate(strings.ToLower(strings.TrimSpace(model)), "gpt-5.4") ||
			modelMatchesRate(strings.ToLower(strings.TrimSpace(model)), "gpt-5.4-pro")) {
		rates.input *= 2
		rates.output *= 1.5
	}
	return (float64(inputTokens) * rates.input / 1_000_000) + (float64(outputTokens) * rates.output / 1_000_000)
}

type tokenRates struct {
	input  float64
	output float64
}

var modelTokenRates = map[string][]struct {
	model string
	rates tokenRates
}{
	"claude": {
		{"claude-haiku-4-5", tokenRates{1.00, 5.00}},
		{"claude-sonnet-4-6", tokenRates{3.00, 15.00}},
		{"claude-sonnet-4-5", tokenRates{3.00, 15.00}},
		{"claude-sonnet-4", tokenRates{3.00, 15.00}},
		{"claude-opus-4-6", tokenRates{5.00, 25.00}},
		{"claude-opus-4-5", tokenRates{5.00, 25.00}},
		{"claude-opus-4-1", tokenRates{15.00, 75.00}},
		{"claude-opus-4", tokenRates{15.00, 75.00}},
		{"claude-3-5-haiku", tokenRates{0.80, 4.00}},
		{"claude-3-5-sonnet", tokenRates{3.00, 15.00}},
		{"claude-3-opus", tokenRates{15.00, 75.00}},
	},
	"gemini": {
		{"gemini-2.5-flash-lite", tokenRates{0.10, 0.40}},
		{"gemini-2.5-flash", tokenRates{0.30, 2.50}},
		{"gemini-2.5-pro", tokenRates{1.25, 10.00}},
		{"gemini-2.0-flash", tokenRates{0.10, 0.40}},
		{"gemini-2.0-flash-001", tokenRates{0.10, 0.40}},
	},
	"openai": {
		{"gpt-5.4-nano", tokenRates{0.20, 1.25}},
		{"gpt-5.4-mini", tokenRates{0.75, 4.50}},
		{"gpt-5.4-pro", tokenRates{30.00, 180.00}},
		{"gpt-5.4", tokenRates{2.50, 15.00}},
		{"gpt-5.3-codex", tokenRates{1.75, 14.00}},
		{"gpt-5.3-chat-latest", tokenRates{1.75, 14.00}},
		{"gpt-5.2", tokenRates{1.75, 14.00}},
		{"gpt-5-mini", tokenRates{0.25, 2.00}},
		{"gpt-5-nano", tokenRates{0.05, 0.40}},
		{"gpt-5", tokenRates{1.25, 10.00}},
		{"gpt-4o-mini", tokenRates{0.15, 0.60}},
		{"gpt-4o-2024-05-13", tokenRates{5.00, 15.00}},
		{"gpt-4o", tokenRates{2.50, 10.00}},
		{"chatgpt-4o-latest", tokenRates{5.00, 15.00}},
	},
}

func lookupTokenRates(provider, model string) (tokenRates, bool) {
	provider = NormalizeProvider(provider)
	model = strings.ToLower(strings.TrimSpace(model))
	for _, pricedModel := range modelTokenRates[provider] {
		if modelMatchesRate(model, pricedModel.model) {
			return pricedModel.rates, true
		}
	}
	return tokenRates{}, false
}

func modelMatchesRate(model, base string) bool {
	if model == base || model == base+"-latest" {
		return true
	}
	date, ok := strings.CutPrefix(model, base+"-")
	if !ok || !strings.HasPrefix(date, "20") {
		return false
	}
	if len(date) == 10 && date[4] == '-' && date[7] == '-' {
		date = date[:4] + date[5:7] + date[8:]
	}
	return len(date) == 8 && strings.Trim(date, "0123456789") == ""
}

func NormalizeProvider(provider string) string {
	provider = strings.ToLower(strings.TrimSpace(provider))
	switch provider {
	case "anthropic":
		return "claude"
	case "google", "google generative ai", "google genai":
		return "gemini"
	default:
		return provider
	}
}

// ProviderForModel infers a provider from native model IDs when metadata is missing.
func ProviderForModel(model string) string {
	model = strings.ToLower(strings.TrimSpace(model))
	switch {
	case strings.HasPrefix(model, "gpt-"), strings.HasPrefix(model, "chatgpt-"):
		return "openai"
	case strings.HasPrefix(model, "claude-"):
		return "claude"
	case strings.HasPrefix(model, "gemini-"):
		return "gemini"
	default:
		return ""
	}
}

// EstimateTokens approximates the token count of a piece of text when the real
// usage figures are unavailable (e.g. the LLM ran inside Langflow and the
// response carried no usage metadata). It uses the common ~4-chars-per-token
// heuristic over runes so multibyte Vietnamese text is not over-counted.
//
// This is intentionally a lower-bound estimate for chatbot turns: the actual
// prompt sent to the model also includes conversation history and RAG context
// injected inside Langflow, which the Go backend never sees.
func EstimateTokens(text string) int {
	if text == "" {
		return 0
	}
	tokens := len([]rune(text)) / 4
	if tokens < 1 {
		return 1
	}
	return tokens
}
