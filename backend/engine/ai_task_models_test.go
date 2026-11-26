package engine

import (
	"slices"
	"testing"
)

func TestSelectTaskAIModelRespectsInheritanceAndCredentialScope(t *testing.T) {
	fallback := AITaskModel{Provider: "claude", Model: "claude-haiku-4-5"}
	defaultModel := AITaskModel{Provider: "openai", Model: "gpt-5-mini"}
	const proxy = "https://example.com/v1"
	tests := []struct {
		name                          string
		override, tenantDefault, want AITaskModel
		wantURL                       string
		wantKeys                      []string
	}{
		{"inherits tenant settings", AITaskModel{}, defaultModel, defaultModel, proxy, []string{"ai_api_key_openai", "ai_api_key"}},
		{"inherits legacy job settings", AITaskModel{}, AITaskModel{}, fallback, proxy, []string{"ai_api_key_claude", "ai_api_key"}},
		{"same provider keeps custom endpoint", AITaskModel{"openai", "gpt-4o-mini"}, defaultModel, AITaskModel{"openai", "gpt-4o-mini"}, proxy, []string{"ai_api_key_openai", "ai_api_key"}},
		{"different vendor uses its own credentials", AITaskModel{"gemini", "gemini-2.5-flash"}, defaultModel, AITaskModel{"gemini", "gemini-2.5-flash"}, "", []string{"ai_api_key_gemini"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, baseURL, keys := selectTaskAIModel(tt.override, fallback, tt.tenantDefault, proxy)
			if got != tt.want || baseURL != tt.wantURL || !slices.Equal(keys, tt.wantKeys) {
				t.Fatalf("selection = %+v, URL = %q, keys = %v", got, baseURL, keys)
			}
		})
	}
}
