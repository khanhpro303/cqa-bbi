package ai

import "context"

// AIResponse contains the AI response text and usage metrics.
type AIResponse struct {
	Content      string
	InputTokens  int
	OutputTokens int
	Model        string
	Provider     string // "claude" | "gemini" | "openai"
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

// CalculateCostUSD returns estimated cost in USD based on provider, model, and token counts.
func CalculateCostUSD(provider, model string, inputTokens, outputTokens int) float64 {
	var inputRate, outputRate float64 // per million tokens

	switch provider {
	case "claude":
		switch model {
		case "claude-haiku-4-5-20251001", "claude-haiku-4-5":
			inputRate, outputRate = 0.80, 4.00
		case "claude-sonnet-4-6", "claude-sonnet-4-20250514", "claude-sonnet-4-5-20250929":
			inputRate, outputRate = 3.00, 15.00
		case "claude-opus-4", "claude-opus-4-6":
			inputRate, outputRate = 15.00, 75.00
		default:
			inputRate, outputRate = 3.00, 15.00 // default sonnet pricing
		}
	case "gemini":
		switch model {
		case "gemini-2.0-flash":
			inputRate, outputRate = 0.075, 0.30
		case "gemini-2.5-pro":
			inputRate, outputRate = 1.25, 10.00
		default:
			inputRate, outputRate = 0.075, 0.30 // default flash pricing
		}
	case "openai":
		switch model {
		case "gpt-5.4-mini":
			inputRate, outputRate = 0.75, 4.50
		case "gpt-5.3-codex", "gpt-5.3-chat-latest":
			inputRate, outputRate = 1.75, 14.00
		case "gpt-5":
			inputRate, outputRate = 1.25, 10.00
		case "gpt-5-mini":
			inputRate, outputRate = 0.25, 2.00
		case "gpt-5-nano":
			inputRate, outputRate = 0.05, 0.40
		default:
			inputRate, outputRate = 0.75, 4.50 // default gpt-5.4-mini pricing
		}
	default:
		return 0
	}

	return (float64(inputTokens) * inputRate / 1_000_000) + (float64(outputTokens) * outputRate / 1_000_000)
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
