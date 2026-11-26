package ai

import (
	"math"
	"testing"
	"time"

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
			if math.Abs(got-tt.want) > 1e-9 {
				t.Fatalf("CalculateCostUSD() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCalculateCostUSDUsesNewOpenAIShortContextRates(t *testing.T) {
	tests := []struct {
		model string
		want  float64
	}{
		{"gpt-6.2-astra", 4.80},
		{"gpt-6.2-sol", 1.20},
		{"gpt-6-astra", 6.00},
		{"gpt-6.1-sol", 1.20},
		{"gpt-6.1-terra", 0.60},
		{"gpt-6-sol", 1.20},
		{"gpt-6-luna", 0.06},
		{"gpt-5.6-terra", 1.40},
		{"gpt-5.6-luna", 0.14},
		{"gpt-5.5-2026-04-23", 3.50},
		{"gpt-5.5-pro", 21.00},
	}

	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			got := CalculateCostUSD("openai", tt.model, 100_000, 100_000)
			if math.Abs(got-tt.want) > 1e-9 {
				t.Fatalf("CalculateCostUSD() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGPT56SolPromotionalRatesExpire(t *testing.T) {
	end := time.Date(2026, time.December, 1, 8, 0, 0, 0, time.UTC)
	for _, model := range []string{"gpt-5.6", "gpt-5.6-sol", "gpt-5.6-sol-2026-08-03"} {
		if rates, ok := gpt56SolPromotionalRates(model, end.Add(-time.Second)); !ok || rates != (tokenRates{4, 20}) {
			t.Fatalf("%s: promotional rate = %+v, known = %v", model, rates, ok)
		}
		if rates, ok := gpt56SolPromotionalRates(model, end); ok {
			t.Fatalf("%s: promotional rate is still active after expiry: %+v", model, rates)
		}
	}
}

func TestCalculateCostUSDAppliesLongContextPricing(t *testing.T) {
	tests := []struct {
		name  string
		model string
		input int
		want  float64
	}{
		{"threshold stays at short rate", "gpt-6.2-sol", 272_000, 10.544},
		{"GPT-6.2 long context", "gpt-6.2-sol", 1_000_000, 19.00},
		{"GPT-5.6 long context", "gpt-5.6-terra", 1_000_000, 22.00},
		{"GPT-5.5 Pro remains linear", "gpt-5.5-pro", 1_000_000, 210.00},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CalculateCostUSD("openai", tt.model, tt.input, 1_000_000)
			if math.Abs(got-tt.want) > 1e-9 {
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
