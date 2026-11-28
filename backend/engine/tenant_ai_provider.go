package engine

import (
	"errors"
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
	byKey, err := loadTenantAISettings(tenantID, taskKey)
	if err != nil {
		return TenantAIProvider{}, err
	}
	override, err := ParseAITaskModel(byKey[taskKey].ValuePlain)
	if err != nil {
		return TenantAIProvider{}, fmt.Errorf("AI task %s: %w", task, err)
	}
	return buildTenantAIProvider(byKey, cfg, "AI task "+task, override, fallback, useLegacyOpenAIKey)
}

// ResolveTenantAIModel builds a client for an explicit provider and model
// with the tenant's key for that provider, exactly as a task override would
// pick it (the tenant's default gateway URL only for the default provider).
// The native agent runtime uses it for each agent's own model. A missing key
// is an error.
func ResolveTenantAIModel(tenantID string, cfg *config.Config, provider, model string) (TenantAIProvider, error) {
	override, err := NormalizeAITaskModel(AITaskModel{Provider: provider, Model: model})
	if err != nil {
		return TenantAIProvider{}, err
	}
	if override.Provider == "" {
		return TenantAIProvider{}, fmt.Errorf("AI provider not set")
	}
	byKey, err := loadTenantAISettings(tenantID)
	if err != nil {
		return TenantAIProvider{}, err
	}
	return buildTenantAIProvider(byKey, cfg, "agent model", override, AITaskModel{Provider: "claude"}, false)
}

// ErrLoadTenantAISettings wraps a failed read of the tenant's AI settings:
// a database problem, unlike a missing key, that may pass.
var ErrLoadTenantAISettings = errors.New("load tenant AI settings")

// loadTenantAISettings reads the tenant's AI provider settings (and extra
// keys) by key.
func loadTenantAISettings(tenantID string, extra ...string) (map[string]models.AppSetting, error) {
	var settings []models.AppSetting
	keys := append([]string{"ai_provider", "ai_model", "ai_base_url", "ai_api_key"}, extra...)
	for _, provider := range ai.Providers {
		keys = append(keys, ai.ProviderAPIKeySettingKey(provider), ai.ProviderBaseURLSettingKey(provider))
	}
	if err := db.DB.Where("tenant_id = ? AND setting_key IN ?", tenantID, keys).Find(&settings).Error; err != nil {
		return nil, fmt.Errorf("%w: %w", ErrLoadTenantAISettings, err)
	}
	byKey := make(map[string]models.AppSetting, len(settings))
	for _, setting := range settings {
		byKey[setting.SettingKey] = setting
	}
	return byKey, nil
}

// buildTenantAIProvider picks the model, URL and key from the tenant's
// settings: override when set, else the tenant default (fallback when that
// is unset). label names the caller in errors.
func buildTenantAIProvider(byKey map[string]models.AppSetting, cfg *config.Config, label string, override, fallback AITaskModel, useLegacyOpenAIKey bool) (TenantAIProvider, error) {
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
			return out, fmt.Errorf("API key not configured for %s (%s)", label, selection.Provider)
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
