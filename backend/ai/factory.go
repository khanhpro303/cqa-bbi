package ai

import "fmt"

// Provider names stored in the tenant's ai_provider setting.
const (
	ProviderClaude     = "claude"
	ProviderGemini     = "gemini"
	ProviderOpenAI     = "openai"
	ProviderOpenRouter = "openrouter"
)

// SupportedProviders lists every provider a tenant can choose.
var SupportedProviders = []string{ProviderClaude, ProviderGemini, ProviderOpenAI, ProviderOpenRouter}

// IsSupportedProvider reports whether name is a provider NewProviderClient builds.
func IsSupportedProvider(name string) bool {
	for _, p := range SupportedProviders {
		if p == name {
			return true
		}
	}
	return false
}

// DefaultModel is the model used for small internal calls when the tenant
// has not chosen one.
func DefaultModel(provider string) string {
	switch provider {
	case ProviderGemini:
		return "gemini-2.0-flash"
	case ProviderOpenAI:
		return "gpt-5-mini"
	case ProviderOpenRouter:
		return "openrouter/auto"
	default:
		return "claude-haiku-4-5"
	}
}

// NewProviderClient builds the AI client for a provider. An empty model
// lets the provider pick its own default.
func NewProviderClient(provider, apiKey, model, baseURL string, maxTokens int) (AIProvider, error) {
	switch provider {
	case ProviderClaude:
		return NewClaudeProvider(apiKey, model, maxTokens, baseURL), nil
	case ProviderGemini:
		return NewGeminiProvider(apiKey, model, baseURL), nil
	case ProviderOpenAI:
		return NewOpenAIProvider(apiKey, model, baseURL), nil
	case ProviderOpenRouter:
		return NewOpenRouterProvider(apiKey, model, baseURL), nil
	default:
		return nil, fmt.Errorf("unsupported AI provider: %s", provider)
	}
}
