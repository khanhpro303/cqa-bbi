package workers

import (
	"testing"

	"github.com/vietbui/chat-quality-agent/engine"
)

func TestResolveChatbotPricingUsesResponseThenActualFlow(t *testing.T) {
	tests := []struct {
		name, flowProvider, flowModel, wantProvider, wantModel string
		result                                                 engine.LangflowResult
	}{
		{"usage-less inherits actual graph", "OpenAI", "gpt-5.4", "openai", "gpt-5.4", engine.LangflowResult{}},
		{"reported model wins without counters", "OpenAI", "gpt-5.4", "openai", "gpt-4o-mini-2024-07-18", engine.LangflowResult{Model: "gpt-4o-mini-2024-07-18"}},
		{"missing provider inferred from actual model", "OpenAI", "gpt-5-mini", "claude", "claude-haiku-4-5", engine.LangflowResult{Model: "claude-haiku-4-5", HasUsage: true}},
		{"OpenRouter flow keeps gateway label", "OpenRouter", "gpt-4o-mini", "openrouter", "gpt-4o-mini-2024-07-18", engine.LangflowResult{Model: "gpt-4o-mini-2024-07-18"}},
		{"missing graph and identity stays unknown", "", "", "", "", engine.LangflowResult{HasUsage: true}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider, model := resolveChatbotPricing(tt.result, tt.flowProvider, tt.flowModel)
			if provider != tt.wantProvider || model != tt.wantModel {
				t.Fatalf("pricing labels = %s/%s, want %s/%s", provider, model, tt.wantProvider, tt.wantModel)
			}
		})
	}
}
