package engine

import (
	"fmt"

	"github.com/vietbui/chat-quality-agent/ai"
	"github.com/vietbui/chat-quality-agent/config"
	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/db/models"
	"github.com/vietbui/chat-quality-agent/pkg"
)

// TenantAIProvider is the tenant's configured AI provider for small internal
// LLM calls (intent classification, chat-memory summaries).
type TenantAIProvider struct {
	Client   ai.AIProvider
	Provider string // claude | gemini | openai
	Model    string
}

// ResolveTenantAIProvider builds the tenant's AI client from the AI settings
// (ai_provider, ai_api_key_*, ai_model, ai_base_url). It returns a nil Client
// with no error when no API key is configured, so callers can skip the call.
func ResolveTenantAIProvider(tenantID string, cfg *config.Config) (TenantAIProvider, error) {
	provider := "claude"
	var providerSetting models.AppSetting
	if err := db.DB.Where("tenant_id = ? AND setting_key = 'ai_provider'", tenantID).First(&providerSetting).Error; err == nil && providerSetting.ValuePlain != "" {
		provider = providerSetting.ValuePlain
	}
	out := TenantAIProvider{Provider: provider}

	var setting models.AppSetting
	keyFound := false
	for _, key := range ai.ProviderAPIKeySettingKeys(provider) {
		if err := db.DB.Where("tenant_id = ? AND setting_key = ?", tenantID, key).First(&setting).Error; err == nil {
			keyFound = true
			break
		}
	}

	apiKey := ""
	if keyFound {
		if len(setting.ValueEncrypted) > 0 {
			decrypted, err := pkg.Decrypt(setting.ValueEncrypted, cfg.EncryptionKey)
			if err != nil {
				return out, fmt.Errorf("failed to decrypt API key: %w", err)
			}
			apiKey = string(decrypted)
		} else {
			apiKey = setting.ValuePlain
		}
	}
	if apiKey == "" && provider == "openai" {
		apiKey = cfg.LangflowAPIKey
	}
	if apiKey == "" {
		return out, nil
	}

	model := "claude-haiku-4-5"
	if provider == "gemini" {
		model = "gemini-2.0-flash"
	} else if provider == "openai" {
		model = "gpt-5-mini"
	}
	var modelSetting models.AppSetting
	if err := db.DB.Where("tenant_id = ? AND setting_key = 'ai_model'", tenantID).First(&modelSetting).Error; err == nil && modelSetting.ValuePlain != "" {
		model = modelSetting.ValuePlain
	}
	out.Model = model

	var baseURL string
	var baseURLSetting models.AppSetting
	if err := db.DB.Where("tenant_id = ? AND setting_key = 'ai_base_url'", tenantID).First(&baseURLSetting).Error; err == nil {
		baseURL = baseURLSetting.ValuePlain
	}

	switch provider {
	case "claude":
		out.Client = ai.NewClaudeProvider(apiKey, model, cfg.AIMaxTokens, baseURL)
	case "gemini":
		out.Client = ai.NewGeminiProvider(apiKey, model, baseURL)
	case "openai":
		out.Client = ai.NewOpenAIProvider(apiKey, model, baseURL)
	default:
		return out, fmt.Errorf("unsupported AI provider: %s", provider)
	}
	return out, nil
}
