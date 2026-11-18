package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/vietbui/chat-quality-agent/api/middleware"
	"github.com/vietbui/chat-quality-agent/config"
	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/db/models"
	"github.com/vietbui/chat-quality-agent/engine"
	"github.com/vietbui/chat-quality-agent/pkg"
)

const langflowCheckTimeout = 15 * time.Second

// langflowTestRequest carries optional unsaved values from the AI Engines form.
type langflowTestRequest struct {
	LangflowBaseURL      string `json:"langflow_base_url"`
	LangflowFlowID       string `json:"langflow_flow_id"`
	LangflowPublicFlowID string `json:"langflow_public_flow_id"`
	LangflowToken        string `json:"langflow_token"`
}

type langflowFlowCheck struct {
	Role   string `json:"role"` // "private" (staff) | "public" (customers/groups)
	FlowID string `json:"flow_id"`
	OK     bool   `json:"ok"`
	Error  string `json:"error,omitempty"`
}

// channelLangflowMeta is the Langflow subset of a channel's metadata JSON.
type channelLangflowMeta struct {
	LangflowAPIURL       string `json:"langflow_api_url"`
	LangflowAPIKey       string `json:"langflow_api_key"`
	LangflowFlowID       string `json:"langflow_flow_id"`
	LangflowPublicFlowID string `json:"langflow_public_flow_id"`
}

// TestLangflowConnection checks the Langflow config the chatbot will actually
// use: the same resolver as the worker (tenant settings → channel → .env),
// with any unsaved form values applied on top. It verifies BOTH the private
// (staff) and public flows with a cheap GET /flows/{id} — no LLM call — and
// reports stale per-channel overrides that used to silently win.
func TestLangflowConnection(c *gin.Context) {
	tenantID := middleware.GetTenantID(c)
	cfg, _ := config.Load()

	var req langflowTestRequest
	body, _ := io.ReadAll(c.Request.Body)
	if len(strings.TrimSpace(string(body))) > 0 {
		if err := json.Unmarshal(body, &req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request"})
			return
		}
	}

	lfCfg := resolveTenantLangflowConfig(tenantID, cfg)
	applyLangflowFormOverrides(&lfCfg, req)
	if lfCfg.APIURL == "" || lfCfg.PrivateFlowID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing_config", "message": "Vui lòng cung cấp URL và Flow ID"})
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), langflowCheckTimeout)
	defer cancel()
	checks := runLangflowFlowChecks(ctx, engine.NewLangflowClient(cfg), lfCfg)
	warnings := append(lfCfg.Warnings, channelLangflowWarnings(tenantID, cfg, lfCfg)...)

	failed := failedFlowChecks(checks)
	resp := gin.H{"checks": checks, "warnings": warnings, "config": describeLangflowSources(lfCfg)}
	if len(failed) > 0 {
		resp["error"] = "langflow_check_failed"
		resp["message"] = "Kết nối Langflow lỗi: " + strings.Join(failed, "; ")
		c.JSON(http.StatusBadRequest, resp)
		return
	}
	resp["status"] = "ok"
	resp["message"] = "Kết nối thành công"
	if len(warnings) > 0 {
		resp["message"] = fmt.Sprintf("Kết nối thành công (có %d cảnh báo cấu hình: %s)", len(warnings), strings.Join(warnings, "; "))
	}
	c.JSON(http.StatusOK, resp)
}

func resolveTenantLangflowConfig(tenantID string, cfg *config.Config) engine.LangflowConfig {
	return engine.ResolveLangflowConfig(
		engine.DBSettingLookup(tenantID),
		func(b []byte) ([]byte, error) { return pkg.Decrypt(b, cfg.EncryptionKey) },
		engine.LangflowValues{},
		envLangflowValues(cfg),
	)
}

func envLangflowValues(cfg *config.Config) engine.LangflowValues {
	return engine.LangflowValues{
		APIURL:       cfg.LangflowAPIURL,
		APIKey:       cfg.LangflowAPIKey,
		FlowID:       cfg.LangflowFlowID,
		PublicFlowID: cfg.LangflowPublicFlowID,
	}
}

// applyLangflowFormOverrides lets the admin test values before saving them.
// A masked token ("****…") means "keep the saved one".
func applyLangflowFormOverrides(lfCfg *engine.LangflowConfig, req langflowTestRequest) {
	if v := strings.TrimSpace(req.LangflowBaseURL); v != "" {
		lfCfg.APIURL, lfCfg.Sources["url"] = v, "form"
	}
	if v := strings.TrimSpace(req.LangflowFlowID); v != "" {
		lfCfg.PrivateFlowID, lfCfg.Sources["flow_id"] = v, "form"
	}
	if v := strings.TrimSpace(req.LangflowPublicFlowID); v != "" {
		lfCfg.PublicFlowID, lfCfg.Sources["public_flow_id"] = v, "form"
	}
	if v := strings.TrimSpace(req.LangflowToken); v != "" && !isMaskedSecret(v) {
		lfCfg.APIKey, lfCfg.Sources["api_key"] = v, "form"
	}
}

func runLangflowFlowChecks(ctx context.Context, client *engine.LangflowClient, lfCfg engine.LangflowConfig) []langflowFlowCheck {
	targets := []langflowFlowCheck{{Role: "private", FlowID: lfCfg.PrivateFlowID}}
	if lfCfg.PublicFlowID != "" && lfCfg.PublicFlowID != lfCfg.PrivateFlowID {
		targets = append(targets, langflowFlowCheck{Role: "public", FlowID: lfCfg.PublicFlowID})
	}
	for i := range targets {
		if err := client.CheckFlow(ctx, lfCfg.APIURL, lfCfg.APIKey, targets[i].FlowID); err != nil {
			targets[i].Error = explainLangflowError(err)
			continue
		}
		targets[i].OK = true
	}
	return targets
}

func failedFlowChecks(checks []langflowFlowCheck) []string {
	var failed []string
	for _, chk := range checks {
		if !chk.OK {
			failed = append(failed, fmt.Sprintf("flow %s (%s): %s", chk.Role, chk.FlowID, chk.Error))
		}
	}
	return failed
}

// explainLangflowError turns Langflow status codes into actionable Vietnamese.
func explainLangflowError(err error) string {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "(status 401)"), strings.Contains(msg, "(status 403)"):
		return "API key Langflow không hợp lệ hoặc đã bị xoá"
	case strings.Contains(msg, "(status 404)"):
		return "không tìm thấy flow này trên Langflow (sai Flow ID hoặc flow đã bị xoá)"
	}
	return msg
}

// channelLangflowWarnings reports per-channel Langflow overrides that differ
// from the tenant config (they are ignored now, but should be cleaned up).
func channelLangflowWarnings(tenantID string, cfg *config.Config, tenantCfg engine.LangflowConfig) []string {
	if db.DB == nil {
		return nil
	}
	var chans []models.Channel
	if err := db.DB.Where("tenant_id = ? AND channel_type = ?", tenantID, "zalo_oa").Find(&chans).Error; err != nil {
		return nil
	}
	var warnings []string
	for _, ch := range chans {
		var meta channelLangflowMeta
		if ch.Metadata == "" || json.Unmarshal([]byte(ch.Metadata), &meta) != nil {
			continue
		}
		resolved := engine.ResolveLangflowConfig(
			engine.DBSettingLookup(tenantID),
			func(b []byte) ([]byte, error) { return pkg.Decrypt(b, cfg.EncryptionKey) },
			engine.LangflowValues{APIURL: meta.LangflowAPIURL, APIKey: meta.LangflowAPIKey, FlowID: meta.LangflowFlowID, PublicFlowID: meta.LangflowPublicFlowID},
			envLangflowValues(cfg),
		)
		for _, w := range resolved.Warnings {
			if !containsString(tenantCfg.Warnings, w) {
				warnings = append(warnings, fmt.Sprintf("kênh %q: %s", ch.Name, w))
			}
		}
	}
	return warnings
}

func describeLangflowSources(lfCfg engine.LangflowConfig) gin.H {
	return gin.H{
		"url":            lfCfg.APIURL,
		"api_key":        engine.MaskLangflowSecret(lfCfg.APIKey),
		"flow_id":        lfCfg.PrivateFlowID,
		"public_flow_id": lfCfg.PublicFlowID,
		"sources":        lfCfg.Sources,
	}
}
