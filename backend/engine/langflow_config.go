package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/vietbui/chat-quality-agent/config"
	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/db/models"
	"github.com/vietbui/chat-quality-agent/pkg"
)

// Langflow config sources, in precedence order.
const (
	LangflowSourceChannel = "channel_metadata" // per-channel override (Kênh → Langflow)
	LangflowSourceTenant  = "tenant_setting"   // AI Engine settings (recommended)
	LangflowSourceEnv     = "env"              // .env fallback
	LangflowSourceNone    = "none"
)

// LangflowConfig is the effective Langflow connection for one chat turn, with
// the source of every field so a stale value can be traced in logs and in the
// chatbot health check.
type LangflowConfig struct {
	APIURL       string `json:"api_url"`
	APIKey       string `json:"-"`
	FlowID       string `json:"flow_id"`        // private/internal staff flow
	PublicFlowID string `json:"public_flow_id"` // public/customer/group flow

	APIURLSource       string `json:"api_url_source"`
	APIKeySource       string `json:"api_key_source"`
	FlowIDSource       string `json:"flow_id_source"`
	PublicFlowIDSource string `json:"public_flow_id_source"`
}

// LangflowOverrides is the optional per-channel override stored in
// channels.metadata. Empty fields mean "use the AI Engine settings".
type LangflowOverrides struct {
	APIURL       string
	APIKey       string
	FlowID       string
	PublicFlowID string
}

// IsEmpty reports whether the channel carries no Langflow override at all.
func (o LangflowOverrides) IsEmpty() bool {
	return o.APIURL == "" && o.APIKey == "" && o.FlowID == "" && o.PublicFlowID == ""
}

// LangflowSettingKeys are the app_settings keys the resolver reads.
var LangflowSettingKeys = []string{
	"ai_engine_langflow_url",
	"ai_engine_langflow_token",
	"ai_engine_langflow_flow_id",
	"ai_engine_langflow_public_flow_id",
}

// LoadLangflowTenantSettings fetches the tenant's Langflow settings in one query.
func LoadLangflowTenantSettings(tenantID string) map[string]models.AppSetting {
	out := map[string]models.AppSetting{}
	if db.DB == nil {
		return out
	}
	var rows []models.AppSetting
	if err := db.DB.Where("tenant_id = ? AND setting_key IN ?", tenantID, LangflowSettingKeys).Find(&rows).Error; err != nil {
		return out
	}
	for _, r := range rows {
		out[r.SettingKey] = r
	}
	return out
}

// ResolveTenantLangflowConfig resolves the AI Engine settings → .env chain,
// ignoring any channel override. This is the "known good" config the worker
// falls back to when a channel override is rejected by Langflow.
func ResolveTenantLangflowConfig(settings map[string]models.AppSetting, cfg *config.Config) LangflowConfig {
	return ResolveLangflowConfig(LangflowOverrides{}, settings, cfg)
}

// ResolveLangflowConfig applies the precedence channel override → AI Engine
// settings → .env for every field independently and records each source.
func ResolveLangflowConfig(ov LangflowOverrides, settings map[string]models.AppSetting, cfg *config.Config) LangflowConfig {
	var out LangflowConfig
	plain := func(key string) string {
		if s, ok := settings[key]; ok {
			return strings.TrimSpace(s.ValuePlain)
		}
		return ""
	}

	out.APIURL, out.APIURLSource = pick(ov.APIURL, plain("ai_engine_langflow_url"), cfg.LangflowAPIURL)
	out.APIURL = strings.TrimRight(out.APIURL, "/")
	out.FlowID, out.FlowIDSource = pick(ov.FlowID, plain("ai_engine_langflow_flow_id"), cfg.LangflowFlowID)
	out.PublicFlowID, out.PublicFlowIDSource = pick(ov.PublicFlowID, plain("ai_engine_langflow_public_flow_id"), cfg.LangflowPublicFlowID)

	tenantKey := ""
	if s, ok := settings["ai_engine_langflow_token"]; ok {
		if len(s.ValueEncrypted) > 0 && cfg.EncryptionKey != "" {
			if dec, err := pkg.Decrypt(s.ValueEncrypted, cfg.EncryptionKey); err == nil {
				tenantKey = strings.TrimSpace(string(dec))
			}
		} else {
			tenantKey = strings.TrimSpace(s.ValuePlain)
		}
	}
	out.APIKey, out.APIKeySource = pick(ov.APIKey, tenantKey, cfg.LangflowAPIKey)
	return out
}

func pick(channel, tenant, env string) (string, string) {
	if v := strings.TrimSpace(channel); v != "" {
		return v, LangflowSourceChannel
	}
	if tenant != "" {
		return tenant, LangflowSourceTenant
	}
	if v := strings.TrimSpace(env); v != "" {
		return v, LangflowSourceEnv
	}
	return "", LangflowSourceNone
}

// FlowFor returns the flow ID to run for an agent type ("private" or "public")
// and its source. Public falls back to the private flow when unset (matching
// the worker's historical behaviour).
func (c LangflowConfig) FlowFor(agentType string) (string, string) {
	if agentType == "private" {
		return c.FlowID, c.FlowIDSource
	}
	if c.PublicFlowID != "" {
		return c.PublicFlowID, c.PublicFlowIDSource
	}
	return c.FlowID, c.FlowIDSource
}

// UsesChannelOverride reports whether any field came from the channel override.
func (c LangflowConfig) UsesChannelOverride() bool {
	return c.APIURLSource == LangflowSourceChannel || c.APIKeySource == LangflowSourceChannel ||
		c.FlowIDSource == LangflowSourceChannel || c.PublicFlowIDSource == LangflowSourceChannel
}

// Equivalent reports whether two configs would hit the same Langflow flow with
// the same credentials (so retrying with the other one is pointless).
func (c LangflowConfig) Equivalent(o LangflowConfig, agentType string) bool {
	f1, _ := c.FlowFor(agentType)
	f2, _ := o.FlowFor(agentType)
	return c.APIURL == o.APIURL && c.APIKey == o.APIKey && f1 == f2
}

// IsLangflowConfigError reports a Langflow error no retry can fix: an invalid or
// missing API key (401/403) or a flow that does not exist (404).
func IsLangflowConfigError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	for _, code := range []string{"(status 401)", "(status 403)", "(status 404)"} {
		if strings.Contains(msg, code) {
			return true
		}
	}
	return false
}

// FlowCheck is the outcome of probing one Langflow flow.
type FlowCheck struct {
	OK      bool   `json:"ok"`
	Status  int    `json:"status,omitempty"`
	Problem string `json:"problem,omitempty"` // unreachable | bad_key | flow_missing | not_configured | error
	Detail  string `json:"detail,omitempty"`
	Name    string `json:"name,omitempty"`
}

// CheckLangflowFlow verifies, without running the flow (no LLM cost), that the
// Langflow server is reachable, the API key is accepted and the flow exists.
func CheckLangflowFlow(ctx context.Context, apiURL, apiKey, flowID string) FlowCheck {
	if apiURL == "" || flowID == "" {
		return FlowCheck{Problem: "not_configured", Detail: "Thiếu Langflow URL hoặc Flow ID"}
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("%s/api/v1/flows/%s", strings.TrimRight(apiURL, "/"), flowID), nil)
	if err != nil {
		return FlowCheck{Problem: "error", Detail: err.Error()}
	}
	req.Header.Set("Accept", "application/json")
	if apiKey != "" {
		req.Header.Set("x-api-key", apiKey)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return FlowCheck{Problem: "unreachable", Detail: err.Error()}
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	switch {
	case resp.StatusCode == http.StatusOK:
		return FlowCheck{OK: true, Status: resp.StatusCode, Name: extractFlowName(body)}
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return FlowCheck{Status: resp.StatusCode, Problem: "bad_key", Detail: "Langflow từ chối API key"}
	case resp.StatusCode == http.StatusNotFound:
		return FlowCheck{Status: resp.StatusCode, Problem: "flow_missing", Detail: "Flow ID không tồn tại trên Langflow"}
	default:
		return FlowCheck{Status: resp.StatusCode, Problem: "error", Detail: truncate(string(body), 200)}
	}
}

func extractFlowName(body []byte) string {
	var f struct {
		Name string `json:"name"`
	}
	if json.Unmarshal(body, &f) != nil {
		return ""
	}
	return f.Name
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
