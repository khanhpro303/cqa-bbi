package ai

func ProviderBaseURLSettingKey(provider string) string {
	return "ai_base_url_" + provider
}

// ProviderAPIKeySettingKey returns the tenant setting key used to store API key per provider.
func ProviderAPIKeySettingKey(provider string) string {
	switch provider {
	case "claude":
		return "ai_api_key_claude"
	case "gemini":
		return "ai_api_key_gemini"
	case "openai":
		return "ai_api_key_openai"
	case "openrouter":
		return "ai_api_key_openrouter"
	case "9router":
		return "ai_api_key_9router"
	default:
		return "ai_api_key"
	}
}

// ProviderAPIKeySettingKeys returns lookup order for API key settings.
// Includes legacy ai_api_key for backward compatibility.
func ProviderAPIKeySettingKeys(provider string) []string {
	primary := ProviderAPIKeySettingKey(provider)
	if primary == "ai_api_key" {
		return []string{"ai_api_key"}
	}
	if provider == "openrouter" || provider == "9router" {
		// Routers need their own key. The legacy key may belong to another provider.
		return []string{primary}
	}
	return []string{primary, "ai_api_key"}
}
