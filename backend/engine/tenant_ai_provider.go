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
	Provider string // claude | gemini | openai | openrouter
	Model    string
}

// ResolveTenantAIProvider uses a task override when present. It returns a nil
// Client when the inherited settings have no key, so callers can skip the call.
func ResolveTenantAIProvider(tenantID string, cfg *config.Config, task string) (TenantAIProvider, error) {
	return resolveTenantAIProvider(tenantID, cfg, task, AITaskModel{Provider: "claude"}, true)
}

// ResolveTenantAIProviderWithTenantKey resolves task settings using credentials
// configured by the tenant, including for tenant-configured provider URLs.
func ResolveTenantAIProviderWithTenantKey(tenantID string, cfg *config.Config, task string) (TenantAIProvider, error) {
	return resolveTenantAIProvider(tenantID, cfg, task, AITaskModel{Provider: "claude"}, false)
}

func resolveTenantAIProvider(tenantID string, cfg *config.Config, task string, fallback AITaskModel, useLegacyOpenAIKey bool) (TenantAIProvider, error) {
	taskKey := AITaskModelSettingPrefix + task
	var settings []models.AppSetting
	keys := []string{"ai_provider", "ai_model", "ai_base_url", "ai_api_key", taskKey}
	for _, provider := range ai.Providers {
		keys = append(keys, ai.ProviderAPIKeySettingKey(provider), ai.ProviderBaseURLSettingKey(provider))
	}
	if err := db.DB.Where("tenant_id = ? AND setting_key IN ?", tenantID, keys).Find(&settings).Error; err != nil {
		return TenantAIProvider{}, fmt.Errorf("load tenant AI settings: %w", err)
	}
	byKey := make(map[string]models.AppSetting, len(settings))
	for _, setting := range settings {
		byKey[setting.SettingKey] = setting
	}
	override, err := ParseAITaskModel(byKey[taskKey].ValuePlain)
	if err != nil {
		return TenantAIProvider{}, fmt.Errorf("AI task %s: %w", task, err)
	}

	selection, baseURL, keyNames := selectTaskAIModel(override, fallback,
		AITaskModel{Provider: byKey["ai_provider"].ValuePlain, Model: byKey["ai_model"].ValuePlain},
		byKey["ai_base_url"].ValuePlain)
	if scopedURL, ok := byKey[ai.ProviderBaseURLSettingKey(selection.Provider)]; ok {
		baseURL = scopedURL.ValuePlain
	}
	out := TenantAIProvider{Provider: selection.Provider, Model: selection.Model}
	apiKey := ""
	for _, key := range keyNames {
		setting, ok := byKey[key]
		if !ok {
			continue
		}
		if len(setting.ValueEncrypted) > 0 {
			decrypted, err := pkg.Decrypt(setting.ValueEncrypted, cfg.EncryptionKey)
			if err != nil {
				return out, fmt.Errorf("failed to decrypt API key: %w", err)
			}
			apiKey = string(decrypted)
		} else {
			apiKey = setting.ValuePlain
		}
		if apiKey != "" {
			break
		}
	}
	if apiKey == "" && selection.Provider == "openai" && override == (AITaskModel{}) && useLegacyOpenAIKey {
		apiKey = cfg.LangflowAPIKey
	}
	if apiKey == "" {
		if override != (AITaskModel{}) {
			return out, fmt.Errorf("API key not configured for AI task %s (%s)", task, selection.Provider)
		}
		return out, nil
	}

	client, err := ai.NewProvider(selection.Provider, apiKey, selection.Model, baseURL, cfg.AIMaxTokens)
	if err != nil {
		return out, err
	}
	out.Client = client
	return out, nil
}

func selectTaskAIModel(override, fallback, tenantDefault AITaskModel, baseURL string) (AITaskModel, string, []string) {
	if tenantDefault.Provider == "" {
		tenantDefault.Provider = fallback.Provider
	}
	if tenantDefault.Provider == "" {
		tenantDefault.Provider = "claude"
	}
	if tenantDefault.Model == "" && tenantDefault.Provider == fallback.Provider {
		tenantDefault.Model = fallback.Model
	}
	if tenantDefault.Model == "" {
		tenantDefault.Model = ai.DefaultModel(tenantDefault.Provider)
	}
	if override.Provider == "" {
		return tenantDefault, baseURL, ai.ProviderAPIKeySettingKeys(tenantDefault.Provider)
	}
	if override.Provider == tenantDefault.Provider {
		return override, baseURL, ai.ProviderAPIKeySettingKeys(override.Provider)
	}
	// A legacy key or a custom gateway for the default provider may be valid
	// only with that provider. A different vendor uses its own key and URL.
	return override, "", []string{ai.ProviderAPIKeySettingKey(override.Provider)}
}
