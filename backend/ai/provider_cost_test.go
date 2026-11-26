package ai

import (
	"testing"

	"google.golang.org/genai"
)

func TestCalculateCostUSDUsesActualModelRates(t *testing.T) {
	tests := []struct {
		name     string
		provider string
		model    string
		want     float64
	}{
		{"OpenAI configured model", "openai", "gpt-5-mini", 2.25},
		{"OpenAI actual legacy model", "openai", "gpt-4o-mini-2024-07-18", 0.75},
		{"OpenAI legacy GPT-4o snapshot", "OpenAI", "gpt-4o-2024-05-13", 20.00},
		{"OpenAI newer GPT-4o snapshot", "openai", "gpt-4o-2024-11-20", 12.50},
		{"OpenAI snapshot", "openai", "gpt-5.4-mini-2026-03-17", 5.25},
		{"Claude snapshot", "claude", "claude-haiku-4-5-20251001", 6.00},
		{"Claude current Opus", "claude", "claude-opus-4-6", 30.00},
		{"Claude legacy Opus", "claude", "claude-opus-4-20250514", 90.00},
		{"Claude legacy alias", "claude", "claude-3-5-sonnet-latest", 18.00},
		{"Gemini Flash", "gemini", "gemini-2.5-flash", 2.80},
		{"Gemini Flash Lite", "gemini", "gemini-2.5-flash-lite", 0.50},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CalculateCostUSD(tt.provider, tt.model, 1_000_000, 1_000_000)
			if got != tt.want {
				t.Fatalf("CalculateCostUSD() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestModelUsageCostPrefersOpenRouterReportedCost(t *testing.T) {
	response, err := parseChatCompletion([]byte(`{"model":"anthropic/claude-haiku-4-5","choices":[{"message":{"content":"ok"}}],"usage":{"prompt_tokens":100,"completion_tokens":10,"cost":0.002}}`), "")
	if err != nil {
		t.Fatal(err)
	}
	response.Provider = "openrouter"
	if cost := response.UsageCostUSD(); cost != 0.002 {
		t.Fatalf("reported usage cost = %v, want 0.002", cost)
	}
	free := 0.0
	response.ReportedCostUSD = &free
	if cost := response.UsageCostUSD(); cost != 0 {
		t.Fatalf("reported zero cost = %v, want 0", cost)
	}
}

func TestModelUsageGeminiIncludesThinkingTokens(t *testing.T) {
	response := geminiResponse("ok", "gemini-2.0-flash", &genai.GenerateContentResponse{
		ModelVersion: "gemini-2.5-flash",
		UsageMetadata: &genai.GenerateContentResponseUsageMetadata{
			PromptTokenCount: 100, CandidatesTokenCount: 10, ThoughtsTokenCount: 50,
		},
	})
	if response.Model != "gemini-2.5-flash" || response.InputTokens != 100 || response.OutputTokens != 60 {
		t.Fatalf("unexpected Gemini model/usage: %#v", response)
	}
}

func TestCalculateCostUSDDoesNotGuessUnknownModelPrice(t *testing.T) {
	if got := CalculateCostUSD("openai", "future-model", 1_000_000, 1_000_000); got != 0 {
		t.Fatalf("unknown model cost = %v, want 0", got)
	}
	if got := CalculateCostUSD("unknown-provider", "gpt-5-mini", 1_000_000, 1_000_000); got != 0 {
		t.Fatalf("unknown provider cost = %v, want 0", got)
	}
}
