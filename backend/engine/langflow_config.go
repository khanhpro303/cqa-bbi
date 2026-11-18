package engine

import (
	"fmt"

	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/db/models"
)

// Tenant AI Engine setting keys (admin UI → AI Engines → Langflow).
const (
	SettingLangflowURL          = "ai_engine_langflow_url"
	SettingLangflowToken        = "ai_engine_langflow_token"
	SettingLangflowFlowID       = "ai_engine_langflow_flow_id"
	SettingLangflowPublicFlowID = "ai_engine_langflow_public_flow_id"
)

// Where a resolved Langflow value came from.
const (
	LangflowSourceTenant  = "tenant_settings"
	LangflowSourceChannel = "channel_metadata"
	LangflowSourceEnv     = "env"
	LangflowSourceNone    = "unset"
)

// LangflowConfig is the effective Langflow configuration for one channel.
type LangflowConfig struct {
	APIURL        string
	APIKey        string
	PrivateFlowID string
	PublicFlowID  string

	// Sources maps each field ("url", "api_key", "flow_id", "public_flow_id")
	// to the LangflowSource* it was taken from.
	Sources map[string]string
	// Warnings lists ignored stale overrides and undecryptable settings.
	Warnings []string
}

// LangflowValues is a bag of the four Langflow fields, used for both the
// legacy per-channel overrides and the .env defaults.
type LangflowValues struct {
	APIURL       string
	APIKey       string
	FlowID       string
	PublicFlowID string
}

// SettingLookup returns a tenant setting's plain and encrypted values.
type SettingLookup func(key string) (plain string, encrypted []byte, found bool)

// Decryptor decrypts an encrypted tenant setting.
type Decryptor func(ciphertext []byte) ([]byte, error)

// ResolveLangflowConfig computes the effective Langflow config with ONE
// precedence for every field:
//
//  1. tenant AI Engine settings (the single place admins are told to edit)
//  2. legacy per-channel metadata overrides (only when the tenant value is empty)
//  3. .env defaults
//
// Historically channel metadata won over tenant settings, so a stale key/flow
// typed into the channel form silently overrode the correct AI Engine config —
// the admin "Test" button (which only read tenant settings) said OK while every
// chatbot turn failed with 403. A channel value that differs from a non-empty
// tenant value is now ignored and reported in Warnings so the operator can
// clean it up.
func ResolveLangflowConfig(lookup SettingLookup, decrypt Decryptor, channel, env LangflowValues) LangflowConfig {
	cfg := LangflowConfig{Sources: map[string]string{}}

	plainTenant := func(key string) string {
		if lookup == nil {
			return ""
		}
		plain, _, found := lookup(key)
		if !found {
			return ""
		}
		return plain
	}

	tenantToken := func() string {
		if lookup == nil {
			return ""
		}
		plain, encrypted, found := lookup(SettingLangflowToken)
		if !found {
			return ""
		}
		if len(encrypted) == 0 {
			return plain
		}
		if decrypt == nil {
			cfg.Warnings = append(cfg.Warnings, "tenant Langflow token is encrypted but no decryptor is configured")
			return ""
		}
		decrypted, err := decrypt(encrypted)
		if err != nil {
			cfg.Warnings = append(cfg.Warnings, fmt.Sprintf("tenant Langflow token could not be decrypted (check ENCRYPTION_KEY): %v", err))
			return ""
		}
		return string(decrypted)
	}

	cfg.APIURL = cfg.pick("url", plainTenant(SettingLangflowURL), channel.APIURL, env.APIURL, false)
	cfg.APIKey = cfg.pick("api_key", tenantToken(), channel.APIKey, env.APIKey, true)
	cfg.PrivateFlowID = cfg.pick("flow_id", plainTenant(SettingLangflowFlowID), channel.FlowID, env.FlowID, false)
	cfg.PublicFlowID = cfg.pick("public_flow_id", plainTenant(SettingLangflowPublicFlowID), channel.PublicFlowID, env.PublicFlowID, false)
	return cfg
}

func (c *LangflowConfig) pick(field, tenant, channel, env string, secret bool) string {
	if tenant != "" {
		if channel != "" && channel != tenant {
			shown := channel
			if secret {
				shown = MaskLangflowSecret(channel)
			}
			c.Warnings = append(c.Warnings, fmt.Sprintf(
				"channel metadata %s=%s is ignored because AI Engine settings define it; clear it in the channel form", field, shown))
		}
		c.Sources[field] = LangflowSourceTenant
		return tenant
	}
	if channel != "" {
		c.Sources[field] = LangflowSourceChannel
		return channel
	}
	if env != "" {
		c.Sources[field] = LangflowSourceEnv
		return env
	}
	c.Sources[field] = LangflowSourceNone
	return ""
}

// FlowFor returns the flow ID to run for a private (whitelisted staff, 1:1)
// or public conversation. Public falls back to the private flow when no
// public flow is configured, matching the historical behaviour.
func (c LangflowConfig) FlowFor(private bool) string {
	if private || c.PublicFlowID == "" {
		return c.PrivateFlowID
	}
	return c.PublicFlowID
}

// Describe renders the config for logs without leaking the API key.
func (c LangflowConfig) Describe() string {
	return fmt.Sprintf("url=%s(%s) key=%s(%s) flow=%s(%s) public_flow=%s(%s)",
		c.APIURL, c.Sources["url"],
		MaskLangflowSecret(c.APIKey), c.Sources["api_key"],
		c.PrivateFlowID, c.Sources["flow_id"],
		c.PublicFlowID, c.Sources["public_flow_id"])
}

// MaskLangflowSecret shows only enough of a key to tell two keys apart.
func MaskLangflowSecret(s string) string {
	if s == "" {
		return "<empty>"
	}
	if len(s) <= 8 {
		return fmt.Sprintf("***(%d)", len(s))
	}
	return fmt.Sprintf("%s…%s(%d)", s[:3], s[len(s)-4:], len(s))
}

// DBSettingLookup reads tenant settings from the app database.
func DBSettingLookup(tenantID string) SettingLookup {
	return func(key string) (string, []byte, bool) {
		if db.DB == nil {
			return "", nil, false
		}
		var setting models.AppSetting
		if err := db.DB.Where("tenant_id = ? AND setting_key = ?", tenantID, key).First(&setting).Error; err != nil {
			return "", nil, false
		}
		return setting.ValuePlain, setting.ValueEncrypted, true
	}
}
