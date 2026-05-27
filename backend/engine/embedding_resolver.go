package engine

import (
	"log"

	"github.com/vietbui/chat-quality-agent/ai"
	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/db/models"
	"github.com/vietbui/chat-quality-agent/pkg"
)

// EmbeddingConfig is the subset of settings needed to build an OpenAI-compatible
// EmbeddingsClient. encryptionKey is the AES-256-GCM key used to decrypt
// per-tenant settings stored via the CQA UI.
type EmbeddingConfig struct {
	FallbackAPIKey string // env-derived, used when no tenant key is configured
	Model          string // e.g. "text-embedding-ada-002"
	BaseURL        string // empty => api.openai.com
	EncryptionKey  string
}

// BuildTenantEmbedder returns an EmbeddingsClient using, in order:
//  1. the tenant's `ai_api_key_openai` setting (stored from CQA Settings UI),
//     decrypted with EncryptionKey when ValueEncrypted is present;
//  2. the legacy `ai_api_key` setting (same lookup order as
//     ai.ProviderAPIKeySettingKeys("openai"));
//  3. EmbeddingConfig.FallbackAPIKey (env LANGFLOW_EMBEDDING_API_KEY /
//     OPENAI_API_KEY).
//
// Returns nil when none yield a non-empty key. Callers should treat nil as
// "skip $vector" (degraded write) rather than failing.
func BuildTenantEmbedder(tenantID string, cfg EmbeddingConfig) *ai.EmbeddingsClient {
	apiKey := lookupTenantOpenAIKey(tenantID, cfg.EncryptionKey)
	if apiKey == "" {
		apiKey = cfg.FallbackAPIKey
	}
	if apiKey == "" {
		return nil
	}
	return ai.NewEmbeddingsClient(apiKey, cfg.Model, cfg.BaseURL)
}

func lookupTenantOpenAIKey(tenantID, encryptionKey string) string {
	if tenantID == "" || db.DB == nil {
		return ""
	}
	for _, key := range ai.ProviderAPIKeySettingKeys("openai") {
		var s models.AppSetting
		if err := db.DB.Where("tenant_id = ? AND setting_key = ?", tenantID, key).First(&s).Error; err != nil {
			continue
		}
		if len(s.ValueEncrypted) > 0 {
			if encryptionKey == "" {
				log.Printf("[embed] tenant %s has encrypted key but no encryption key; skipping", tenantID)
				continue
			}
			plain, err := pkg.Decrypt(s.ValueEncrypted, encryptionKey)
			if err != nil {
				log.Printf("[embed] tenant %s decrypt %s failed: %v", tenantID, key, err)
				continue
			}
			if v := string(plain); v != "" {
				return v
			}
			continue
		}
		if s.ValuePlain != "" {
			return s.ValuePlain
		}
	}
	return ""
}
