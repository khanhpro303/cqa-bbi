package handlers

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/vietbui/chat-quality-agent/agents"
	"github.com/vietbui/chat-quality-agent/ai"
	"github.com/vietbui/chat-quality-agent/api/middleware"
	"github.com/vietbui/chat-quality-agent/config"
	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/db/models"
	"github.com/vietbui/chat-quality-agent/engine"
	"github.com/vietbui/chat-quality-agent/pkg"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// GetSettings returns all non-secret settings for the tenant
func GetSettings(c *gin.Context) {
	tenantID := middleware.GetTenantID(c)

	var settings []models.AppSetting
	db.DB.Where("tenant_id = ?", tenantID).Find(&settings)

	result := make(map[string]string)
	for _, s := range settings {
		if s.SettingKey == "erp_global_method_permissions" {
			continue
		}
		if s.ValuePlain != "" {
			result[s.SettingKey] = s.ValuePlain
		} else if len(s.ValueEncrypted) > 0 {
			// Return masked value for encrypted settings
			result[s.SettingKey] = "••••••••"
		}
	}
	result[ai.MessengerInsightsPromptSettingKey+"_default"] = ai.DefaultMessengerInsightsPrompt()
	if strings.TrimSpace(result[ai.MessengerInsightsPromptSettingKey]) == "" {
		result[ai.MessengerInsightsPromptSettingKey] = ai.DefaultMessengerInsightsPrompt()
	}

	cfg, _ := config.Load()
	if _, ok := result["ai_engine_langflow_url"]; !ok && cfg.LangflowAPIURL != "" {
		result["ai_engine_langflow_url"] = cfg.LangflowAPIURL
	}
	if _, ok := result["ai_engine_langflow_flow_id"]; !ok && cfg.LangflowFlowID != "" {
		result["ai_engine_langflow_flow_id"] = cfg.LangflowFlowID
	}
	if _, ok := result["ai_engine_langflow_public_flow_id"]; !ok && cfg.LangflowPublicFlowID != "" {
		result["ai_engine_langflow_public_flow_id"] = cfg.LangflowPublicFlowID
	}
	if _, ok := result["ai_engine_langflow_token"]; !ok && cfg.LangflowAPIKey != "" {
		result["ai_engine_langflow_token"] = "••••••••"
	}

	// Backward-compatible alias for current provider key (used by older frontend versions).
	currentProvider := getSettingValue(settings, "ai_provider", "claude")
	for _, key := range ai.ProviderAPIKeySettingKeys(currentProvider) {
		if v, ok := result[key]; ok && v != "" {
			result["ai_api_key"] = v
			break
		}
	}

	// Ensure ai_agent_erp_token_public and ai_agent_erp_token_private are generated
	if _, ok := result["ai_agent_erp_token_public"]; !ok {
		newToken := strings.ReplaceAll(pkg.NewUUID()+pkg.NewUUID(), "-", "")
		upsertSetting(tenantID, "ai_agent_erp_token_public", newToken, nil)
		result["ai_agent_erp_token_public"] = newToken
	}
	if _, ok := result["ai_agent_erp_token_private"]; !ok {
		newToken := strings.ReplaceAll(pkg.NewUUID()+pkg.NewUUID(), "-", "")
		upsertSetting(tenantID, "ai_agent_erp_token_private", newToken, nil)
		result["ai_agent_erp_token_private"] = newToken
	}

	// Also get tenant info
	var tenant models.Tenant
	db.DB.First(&tenant, "id = ?", tenantID)

	// Load private bot endpoints
	var privateEndpoints []models.ERPEndpoint
	db.DB.Where("tenant_id = ? AND group_id = ?", tenantID, "private_bot").Find(&privateEndpoints)

	c.JSON(http.StatusOK, gin.H{
		"settings":          result,
		"private_endpoints": privateEndpoints,
		"tenant": gin.H{
			"name":     tenant.Name,
			"timezone": getSettingValue(settings, "timezone", "Asia/Ho_Chi_Minh"),
			"language": getSettingValue(settings, "language", "vi"),
		},
	})
}

func getSettingValue(settings []models.AppSetting, key, defaultVal string) string {
	for _, s := range settings {
		if s.SettingKey == key && s.ValuePlain != "" {
			return s.ValuePlain
		}
	}
	return defaultVal
}

// SaveAISettings saves provider credentials and, optionally, the tenant default.
func SaveAISettings(c *gin.Context) {
	tenantID := middleware.GetTenantID(c)
	var req struct {
		Provider        string `json:"provider" binding:"required"`
		APIKey          string `json:"api_key" binding:"required_without=ModelOnly"`
		Model           string `json:"model"`
		BaseURL         string `json:"base_url"`
		BatchMode       string `json:"batch_mode"`
		BatchSize       string `json:"batch_size"`
		CredentialsOnly bool   `json:"credentials_only"`
		ModelOnly       bool   `json:"model_only"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request", "details": err.Error()})
		return
	}
	if !ai.ValidProvider(req.Provider) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_provider"})
		return
	}
	if req.ModelOnly {
		req.Model = strings.TrimSpace(req.Model)
		if req.Model == "" || req.CredentialsOnly {
			c.JSON(http.StatusBadRequest, gin.H{"error": "model_required"})
			return
		}
	} else if req.Provider == "9router" {
		if err := ai.ValidateNineRouterBaseURL(req.BaseURL); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_base_url", "details": err.Error()})
			return
		}
		req.Model = strings.TrimSpace(req.Model)
		if !req.CredentialsOnly && req.Model == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "model_required"})
			return
		}
	}
	cfg, err := config.Load()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "ai_configuration_unavailable"})
		return
	}

	providerKey := ai.ProviderAPIKeySettingKey(req.Provider)
	providerBaseURLKey := ai.ProviderBaseURLSettingKey(req.Provider)
	baseURL := strings.TrimSpace(req.BaseURL)
	apiKeyChanged := !req.ModelOnly && !isMaskedSecret(req.APIKey)
	var encrypted []byte
	if apiKeyChanged {
		encrypted, err = pkg.Encrypt([]byte(req.APIKey), cfg.EncryptionKey)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "encryption_failed"})
			return
		}
	}

	var oldProvider, oldModel string
	missingKey, nineRouterURLChanged := false, false
	var baseURLError error
	err = db.DB.WithContext(c.Request.Context()).Transaction(func(tx *gorm.DB) error {
		var existing []models.AppSetting
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("tenant_id = ? AND setting_key IN ?", tenantID,
				[]string{"ai_provider", "ai_model", "ai_base_url", providerKey, providerBaseURLKey, "ai_api_key"}).
			Find(&existing).Error; err != nil {
			return err
		}
		oldProvider = getSettingValue(existing, "ai_provider", "claude")
		oldModel = getSettingValue(existing, "ai_model", "")
		hasAPIKey := apiKeyChanged
		for _, row := range existing {
			legacyForProvider := row.SettingKey == "ai_api_key" && oldProvider == req.Provider && len(ai.ProviderAPIKeySettingKeys(req.Provider)) > 1
			if (row.SettingKey == providerKey || legacyForProvider) && (len(row.ValueEncrypted) > 0 || row.ValuePlain != "") {
				hasAPIKey = true
				break
			}
		}
		if !hasAPIKey {
			missingKey = true
			return gorm.ErrRecordNotFound
		}
		if req.ModelOnly {
			baseURL = ""
			if oldProvider == req.Provider && req.Provider != "9router" {
				baseURL = getSettingValue(existing, "ai_base_url", "")
			}
			for _, row := range existing {
				if row.SettingKey == providerBaseURLKey {
					baseURL = row.ValuePlain
					break
				}
			}
			if req.Provider == "9router" {
				baseURLError = ai.ValidateNineRouterBaseURL(baseURL)
				if baseURLError != nil {
					return gorm.ErrInvalidData
				}
			}
		}
		if req.Provider == "9router" && !req.ModelOnly && !apiKeyChanged && !sameNineRouterBaseURL(baseURL, getSettingValue(existing, providerBaseURLKey, "")) {
			nineRouterURLChanged = true
			return gorm.ErrInvalidData
		}
		rows := []models.AppSetting{}
		put := func(key, value string, secret []byte) {
			rows = append(rows, models.AppSetting{ID: pkg.NewUUID(), TenantID: tenantID, SettingKey: key, ValuePlain: value, ValueEncrypted: secret})
		}
		if apiKeyChanged {
			put(providerKey, "", encrypted)
		}
		if !req.ModelOnly {
			put(providerBaseURLKey, baseURL, nil)
		}
		if !req.CredentialsOnly || oldProvider == req.Provider {
			put("ai_base_url", baseURL, nil)
		}
		if !req.CredentialsOnly {
			if oldProvider != req.Provider && ai.ValidProvider(oldProvider) {
				// Preserve the old provider's endpoint for legacy tenants that
				// configured it before per-provider URLs were available.
				prior := models.AppSetting{ID: pkg.NewUUID(), TenantID: tenantID, SettingKey: ai.ProviderBaseURLSettingKey(oldProvider),
					ValuePlain: getSettingValue(existing, "ai_base_url", "")}
				if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&prior).Error; err != nil {
					return err
				}
				for _, row := range existing {
					if len(ai.ProviderAPIKeySettingKeys(oldProvider)) > 1 && row.SettingKey == "ai_api_key" && (len(row.ValueEncrypted) > 0 || row.ValuePlain != "") {
						priorKey := models.AppSetting{ID: pkg.NewUUID(), TenantID: tenantID, SettingKey: ai.ProviderAPIKeySettingKey(oldProvider),
							ValuePlain: row.ValuePlain, ValueEncrypted: row.ValueEncrypted}
						if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&priorKey).Error; err != nil {
							return err
						}
						break
					}
				}
			}
			put("ai_provider", req.Provider, nil)
			if req.Model != "" {
				put("ai_model", req.Model, nil)
			} else if oldProvider != req.Provider {
				put("ai_model", ai.DefaultModel(req.Provider), nil)
			}
			if req.BatchMode != "" {
				put("ai_batch_mode", req.BatchMode, nil)
			}
			if req.BatchSize != "" {
				put("ai_batch_size", req.BatchSize, nil)
			}
		}
		return tx.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "tenant_id"}, {Name: "setting_key"}},
			DoUpdates: clause.AssignmentColumns([]string{"value_plain", "value_encrypted", "updated_at"}),
		}).Create(&rows).Error
	})
	if err != nil {
		if missingKey {
			c.JSON(http.StatusBadRequest, gin.H{"error": "no_api_key_configured"})
		} else if baseURLError != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_base_url", "details": baseURLError.Error()})
		} else if nineRouterURLChanged {
			writeNineRouterURLChanged(c)
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "save_ai_settings_failed"})
		}
		return
	}

	uid, uemail, ip := middleware.GetUserID(c), middleware.GetUserEmail(c), c.ClientIP()
	if !req.CredentialsOnly && req.Provider != oldProvider {
		db.LogActivity(tenantID, uid, uemail, "ai.provider_changed", "settings", "ai_provider",
			fmt.Sprintf("AI provider: %s→%s", oldProvider, req.Provider), "", ip)
	}
	if !req.CredentialsOnly && req.Model != "" && req.Model != oldModel {
		db.LogActivity(tenantID, uid, uemail, "ai.model_changed", "settings", "ai_model",
			fmt.Sprintf("AI model: %s→%s", oldModel, req.Model), "", ip)
	}
	if apiKeyChanged {
		db.LogActivity(tenantID, uid, uemail, "ai.api_key_rotated", "settings", "ai_api_key",
			fmt.Sprintf("API key (provider %s) đã thay đổi", req.Provider), "", ip)
	}
	c.JSON(http.StatusOK, gin.H{"message": "saved"})
}

func sameNineRouterBaseURL(first, second string) bool {
	return strings.TrimRight(strings.TrimSpace(first), "/") == strings.TrimRight(strings.TrimSpace(second), "/")
}

func writeNineRouterURLChanged(c *gin.Context) {
	c.JSON(http.StatusBadRequest, gin.H{"error": "9router_endpoint_changed", "details": "Nhập lại API key 9router khi thay đổi Base URL."})
}

// SaveAnalysisSettings saves batch mode and batch size settings
func SaveAnalysisSettings(c *gin.Context) {
	tenantID := middleware.GetTenantID(c)
	var req struct {
		BatchMode string `json:"batch_mode" binding:"required"`
		BatchSize string `json:"batch_size"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request", "details": err.Error()})
		return
	}

	upsertSetting(tenantID, "ai_batch_mode", req.BatchMode, nil)
	if req.BatchSize != "" {
		upsertSetting(tenantID, "ai_batch_size", req.BatchSize, nil)
	}

	c.JSON(http.StatusOK, gin.H{"message": "saved"})
}

// SaveAIEnginesSettings saves Langflow configuration and other AI engines
func SaveAIEnginesSettings(c *gin.Context) {
	tenantID := middleware.GetTenantID(c)
	var req struct {
		LangflowBaseURL               string  `json:"langflow_base_url"`
		LangflowFlowID                string  `json:"langflow_flow_id"`
		LangflowPublicFlowID          string  `json:"langflow_public_flow_id"`
		LangflowToken                 string  `json:"langflow_token"`
		SystemPrompt                  string  `json:"system_prompt"`
		SystemPromptInternal          string  `json:"system_prompt_internal"`
		SystemPromptCrmAnalysis       string  `json:"system_prompt_crm_analysis"`
		SystemPromptMessengerInsights *string `json:"system_prompt_messenger_insights"`

		// Astra DB configuration
		AstraDBAPIEndpoint       string `json:"astradb_api_endpoint"`
		AstraDBToken             string `json:"astradb_token"`
		AstraDBKeyspace          string `json:"astradb_keyspace"`
		AstraDBProductCollection string `json:"astradb_product_collection"`
		AstraDBCollection        string `json:"astradb_collection"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request", "details": err.Error()})
		return
	}

	specialistAgentsEnabled, err := agents.Enabled(c.Request.Context(), tenantID)
	if err != nil {
		agentServerError(c, "get enabled setting", err)
		return
	}
	cfg, _ := config.Load()

	// Audit: hash prompt cũ để ghi cũ→mới (không bao giờ log nội dung prompt).
	oldPrompt := getPlainSetting(tenantID, "ai_engine_system_prompt")
	oldPromptInternal := getPlainSetting(tenantID, "ai_engine_system_prompt_internal")
	oldMessengerPrompt := getPlainSetting(tenantID, ai.MessengerInsightsPromptSettingKey)

	// Base URL
	if req.LangflowBaseURL != "" {
		upsertSetting(tenantID, "ai_engine_langflow_url", req.LangflowBaseURL, nil)
	} else {
		db.DB.Where("tenant_id = ? AND setting_key = ?", tenantID, "ai_engine_langflow_url").Delete(&models.AppSetting{})
	}

	// Flow ID
	if req.LangflowFlowID != "" {
		upsertSetting(tenantID, "ai_engine_langflow_flow_id", req.LangflowFlowID, nil)
	} else {
		db.DB.Where("tenant_id = ? AND setting_key = ?", tenantID, "ai_engine_langflow_flow_id").Delete(&models.AppSetting{})
	}

	// Public Flow ID
	if req.LangflowPublicFlowID != "" {
		upsertSetting(tenantID, "ai_engine_langflow_public_flow_id", req.LangflowPublicFlowID, nil)
	} else {
		db.DB.Where("tenant_id = ? AND setting_key = ?", tenantID, "ai_engine_langflow_public_flow_id").Delete(&models.AppSetting{})
	}

	// Once chats are answered by specialist agents, the two chatbot prompts
	// here are the copy agents were made from and the rollback if agents are
	// turned off: the AI Engine page shows them read-only and saving it must
	// not change or clear them.
	if specialistAgentsEnabled {
		req.SystemPrompt, req.SystemPromptInternal = oldPrompt, oldPromptInternal
	}

	// System prompt (plain text, optional). Empty clears the override so the
	// agent falls back to the SYSTEM_PROMPT global variable inside Langflow.
	// This applies to the public / OWN-scope flow.
	if req.SystemPrompt != "" {
		upsertSetting(tenantID, "ai_engine_system_prompt", req.SystemPrompt, nil)
	} else {
		db.DB.Where("tenant_id = ? AND setting_key = ?", tenantID, "ai_engine_system_prompt").Delete(&models.AppSetting{})
	}

	// Internal system prompt for the private / ALL-scope staff flow (plain text,
	// optional). Empty clears it so the private flow uses its own SYSTEM_PROMPT
	// global — it does NOT inherit the public prompt above.
	if req.SystemPromptInternal != "" {
		upsertSetting(tenantID, "ai_engine_system_prompt_internal", req.SystemPromptInternal, nil)
	} else {
		db.DB.Where("tenant_id = ? AND setting_key = ?", tenantID, "ai_engine_system_prompt_internal").Delete(&models.AppSetting{})
	}

	// CRM Customer Analysis system prompt
	if req.SystemPromptCrmAnalysis != "" {
		upsertSetting(tenantID, "ai_engine_system_prompt_crm_analysis", req.SystemPromptCrmAnalysis, nil)
	} else {
		db.DB.Where("tenant_id = ? AND setting_key = ?", tenantID, "ai_engine_system_prompt_crm_analysis").Delete(&models.AppSetting{})
	}

	// Omitted fields preserve the override for older clients. Empty or default
	// values clear it so subsequent runs use the built-in structured prompt.
	if req.SystemPromptMessengerInsights != nil {
		prompt := strings.TrimSpace(*req.SystemPromptMessengerInsights)
		var saveErr error
		if prompt == "" || prompt == strings.TrimSpace(ai.DefaultMessengerInsightsPrompt()) {
			prompt = ""
			saveErr = db.DB.WithContext(c.Request.Context()).Where("tenant_id = ? AND setting_key = ?", tenantID, ai.MessengerInsightsPromptSettingKey).Delete(&models.AppSetting{}).Error
		} else {
			now := time.Now()
			setting := models.AppSetting{ID: pkg.NewUUID(), TenantID: tenantID, SettingKey: ai.MessengerInsightsPromptSettingKey, ValuePlain: prompt, CreatedAt: now, UpdatedAt: now}
			saveErr = db.DB.WithContext(c.Request.Context()).Clauses(clause.OnConflict{
				Columns:   []clause.Column{{Name: "tenant_id"}, {Name: "setting_key"}},
				DoUpdates: clause.Assignments(map[string]interface{}{"value_plain": prompt, "value_encrypted": nil, "updated_at": now}),
			}).Create(&setting).Error
		}
		if saveErr != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "messenger_insights_prompt_save_failed"})
			return
		}
		if prompt != oldMessengerPrompt {
			db.LogActivity(tenantID, middleware.GetUserID(c), middleware.GetUserEmail(c), "ai.system_prompt_updated", "settings", ai.MessengerInsightsPromptSettingKey,
				fmt.Sprintf("System prompt (Messenger insights): hash %s→%s", shortHash(oldMessengerPrompt), shortHash(prompt)), "", c.ClientIP())
		}
	}

	// Token
	if req.LangflowToken != "" {
		if !isMaskedSecret(req.LangflowToken) {
			encrypted, err := pkg.Encrypt([]byte(req.LangflowToken), cfg.EncryptionKey)
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "encryption_failed"})
				return
			}
			upsertSetting(tenantID, "ai_engine_langflow_token", "", encrypted)
		}
	} else {
		db.DB.Where("tenant_id = ? AND setting_key = ?", tenantID, "ai_engine_langflow_token").Delete(&models.AppSetting{})
	}

	// Astra DB API Endpoint
	if req.AstraDBAPIEndpoint != "" {
		upsertSetting(tenantID, "astradb_api_endpoint", strings.TrimRight(req.AstraDBAPIEndpoint, "/"), nil)
	} else {
		db.DB.Where("tenant_id = ? AND setting_key = 'astradb_api_endpoint'", tenantID).Delete(&models.AppSetting{})
	}

	// Astra DB Keyspace
	if req.AstraDBKeyspace != "" {
		upsertSetting(tenantID, "astradb_keyspace", req.AstraDBKeyspace, nil)
	} else {
		db.DB.Where("tenant_id = ? AND setting_key = 'astradb_keyspace'", tenantID).Delete(&models.AppSetting{})
	}

	// Astra DB Product Collection
	if req.AstraDBProductCollection != "" {
		upsertSetting(tenantID, "astradb_product_collection", req.AstraDBProductCollection, nil)
	} else {
		db.DB.Where("tenant_id = ? AND setting_key = 'astradb_product_collection'", tenantID).Delete(&models.AppSetting{})
	}

	// Astra DB Chat History Collection
	if req.AstraDBCollection != "" {
		upsertSetting(tenantID, "astradb_collection", req.AstraDBCollection, nil)
	} else {
		db.DB.Where("tenant_id = ? AND setting_key = 'astradb_collection'", tenantID).Delete(&models.AppSetting{})
	}

	// Astra DB Token
	if req.AstraDBToken != "" {
		if !isMaskedSecret(req.AstraDBToken) {
			encrypted, err := pkg.Encrypt([]byte(req.AstraDBToken), cfg.EncryptionKey)
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "encryption_failed"})
				return
			}
			upsertSetting(tenantID, "astradb_token", "", encrypted)
		}
	} else {
		db.DB.Where("tenant_id = ? AND setting_key = 'astradb_token'", tenantID).Delete(&models.AppSetting{})
	}

	uid, uemail, ip := middleware.GetUserID(c), middleware.GetUserEmail(c), c.ClientIP()
	if req.SystemPrompt != oldPrompt {
		db.LogActivity(tenantID, uid, uemail, "ai.system_prompt_updated", "settings", "ai_engine_system_prompt",
			fmt.Sprintf("System prompt (public): hash %s→%s", shortHash(oldPrompt), shortHash(req.SystemPrompt)), "", ip)
	}
	if req.SystemPromptInternal != oldPromptInternal {
		db.LogActivity(tenantID, uid, uemail, "ai.system_prompt_updated", "settings", "ai_engine_system_prompt_internal",
			fmt.Sprintf("System prompt (internal): hash %s→%s", shortHash(oldPromptInternal), shortHash(req.SystemPromptInternal)), "", ip)
	}

	c.JSON(http.StatusOK, gin.H{"message": "saved"})
}

// TestLangflowConnection checks access to both flows without running either flow.
func TestLangflowConnection(c *gin.Context) {
	tenantID := middleware.GetTenantID(c)
	cfg, err := config.Load()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "invalid_server_config", "message": "Không thể đọc cấu hình máy chủ."})
		return
	}

	var req struct {
		LangflowBaseURL      string  `json:"langflow_base_url"`
		LangflowFlowID       string  `json:"langflow_flow_id"`
		LangflowPublicFlowID *string `json:"langflow_public_flow_id"`
		LangflowToken        string  `json:"langflow_token"`
	}
	body, err := io.ReadAll(http.MaxBytesReader(c.Writer, c.Request.Body, 64<<10))
	if err != nil || (strings.TrimSpace(string(body)) != "" && json.Unmarshal(body, &req) != nil) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request"})
		return
	}

	settings := engine.LoadLangflowTenantSettings(tenantID)
	if req.LangflowPublicFlowID != nil && strings.TrimSpace(*req.LangflowPublicFlowID) == "" {
		// Saving an empty field removes the tenant override and restores fallback.
		delete(settings, "ai_engine_langflow_public_flow_id")
	}
	effective := engine.ResolveTenantLangflowConfig(settings, cfg)
	if value := strings.TrimRight(strings.TrimSpace(req.LangflowBaseURL), "/"); value != "" {
		effective.APIURL, effective.APIURLSource = value, "form"
	}
	if value := strings.TrimSpace(req.LangflowFlowID); value != "" {
		effective.FlowID, effective.FlowIDSource = value, "form"
	}
	if req.LangflowPublicFlowID != nil {
		if value := strings.TrimSpace(*req.LangflowPublicFlowID); value != "" {
			effective.PublicFlowID, effective.PublicFlowIDSource = value, "form"
		}
	}
	if value := strings.TrimSpace(req.LangflowToken); value != "" && !isMaskedSecret(value) {
		effective.APIKey, effective.APIKeySource = value, "form"
		effective.APIKeyWarning = ""
	}
	if effective.APIURL == "" || effective.FlowID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing_config", "message": "Vui lòng cung cấp URL và Flow ID"})
		return
	}

	publicFlow, publicSource := effective.FlowFor("public")
	checks := []struct {
		Role   string `json:"role"`
		FlowID string `json:"flow_id"`
		Source string `json:"source"`
		OK     bool   `json:"ok"`
		Error  string `json:"error,omitempty"`
	}{
		{Role: "private", FlowID: effective.FlowID, Source: effective.FlowIDSource},
		{Role: "public", FlowID: publicFlow, Source: publicSource},
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 15*time.Second)
	defer cancel()
	probed := make(map[string]engine.FlowCheck, len(checks))
	var failures []string
	for i := range checks {
		check := &checks[i]
		probe, ok := probed[check.FlowID]
		if !ok {
			probe = engine.CheckLangflowFlow(ctx, effective.APIURL, effective.APIKey, check.FlowID)
			probed[check.FlowID] = probe
		}
		check.OK = probe.OK
		if check.OK {
			continue
		}
		switch probe.Problem {
		case "bad_key":
			check.Error = "Langflow từ chối API key. Kiểm tra lại API key ở AI Engine."
		case "flow_missing":
			check.Error = "Flow ID không tồn tại trên Langflow. Kiểm tra lại Flow ID."
		case "unreachable":
			check.Error = "Không kết nối được đến Langflow. Kiểm tra URL và kết nối mạng."
		default:
			check.Error = "Langflow trả về phản hồi không hợp lệ. Kiểm tra URL và trạng thái máy chủ."
		}
		role := "Nội bộ"
		if check.Role == "public" {
			role = "Khách hàng / nhóm"
		}
		failures = append(failures, role+": "+check.Error)
	}

	warnings := langflowTestChannelWarnings(tenantID)
	if effective.APIKeyWarning != "" {
		warnings = append(warnings, effective.APIKeyWarning)
	}
	response := gin.H{"checks": checks, "api_url_source": effective.APIURLSource, "api_key_source": effective.APIKeySource, "warnings": warnings}
	if len(failures) > 0 {
		response["error"] = "langflow_check_failed"
		response["message"] = strings.Join(append(failures, warnings...), " ")
		c.JSON(http.StatusBadRequest, response)
		return
	}
	response["status"] = "ok"
	response["message"] = strings.Join(append([]string{"Kết nối thành công cho flow nội bộ và flow khách hàng / nhóm."}, warnings...), " ")
	c.JSON(http.StatusOK, response)
}

func langflowTestChannelWarnings(tenantID string) []string {
	if db.DB == nil {
		return nil
	}
	var channels []models.Channel
	if err := db.DB.Select("metadata").Where("tenant_id = ? AND channel_type = ? AND is_active = true", tenantID, "zalo_oa").Find(&channels).Error; err != nil {
		return nil
	}
	for _, channel := range channels {
		var meta channelLangflowMeta
		if json.Unmarshal([]byte(channel.Metadata), &meta) == nil &&
			(meta.LangflowAPIURL != "" || meta.LangflowAPIKey != "" || meta.LangflowFlowID != "" || meta.LangflowPublicFlowID != "") {
			return []string{"Có kênh Zalo OA còn lưu cấu hình Langflow cũ. Xem kiểm tra sức khỏe để kiểm tra cấu hình đang dùng của từng kênh."}
		}
	}
	return nil
}

// TestAIKey tests the AI API key by making a simple request
func TestAIKey(c *gin.Context) {
	tenantID := middleware.GetTenantID(c)
	cfg, err := config.Load()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "ai_configuration_unavailable"})
		return
	}

	// Optional request payload so UI can test unsaved key/provider directly.
	var req struct {
		Provider string  `json:"provider"`
		APIKey   string  `json:"api_key"`
		Model    string  `json:"model"`
		BaseURL  *string `json:"base_url"`
	}
	if err := c.ShouldBindJSON(&req); err != nil && err != io.EOF {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request", "details": err.Error()})
		return
	}

	// Get provider
	var providerSetting models.AppSetting
	defaultProvider := "claude"
	if err := db.DB.Where("tenant_id = ? AND setting_key = ?", tenantID, "ai_provider").First(&providerSetting).Error; err == nil && providerSetting.ValuePlain != "" {
		defaultProvider = providerSetting.ValuePlain
	}
	provider := defaultProvider
	if req.Provider != "" {
		if !ai.ValidProvider(req.Provider) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_provider"})
			return
		}
		provider = req.Provider
	}
	var apiKey []byte
	if req.APIKey != "" && !isMaskedSecret(req.APIKey) {
		apiKey = []byte(req.APIKey)
	} else {
		var setting *models.AppSetting
		var err error
		if provider == "9router" {
			var savedURL string
			setting, savedURL, err = loadNineRouterTestCredentials(c.Request.Context(), tenantID)
			if err == nil && req.BaseURL != nil && !sameNineRouterBaseURL(*req.BaseURL, savedURL) {
				writeNineRouterURLChanged(c)
				return
			}
			req.BaseURL = &savedURL
		} else {
			keyNames := []string{ai.ProviderAPIKeySettingKey(provider)}
			if provider == defaultProvider {
				keyNames = ai.ProviderAPIKeySettingKeys(provider)
			}
			setting, err = getFirstSettingByKeys(tenantID, keyNames)
		}
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "no_api_key_configured"})
			return
		}
		if len(setting.ValueEncrypted) > 0 {
			apiKey, err = pkg.Decrypt(setting.ValueEncrypted, cfg.EncryptionKey)
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "decrypt_failed"})
				return
			}
		} else {
			apiKey = []byte(setting.ValuePlain)
		}
	}

	// Get model + base URL for provider test (allow request override before save)
	model := ""
	if req.Model != "" {
		model = req.Model
	} else if provider == defaultProvider {
		var modelSetting models.AppSetting
		if err := db.DB.Where("tenant_id = ? AND setting_key = ?", tenantID, "ai_model").First(&modelSetting).Error; err == nil {
			model = strings.TrimSpace(modelSetting.ValuePlain)
		}
	}
	baseURL := ""
	if req.BaseURL != nil {
		baseURL = strings.TrimSpace(*req.BaseURL)
	} else {
		var baseURLSetting models.AppSetting
		err := db.DB.Where("tenant_id = ? AND setting_key = ?", tenantID, ai.ProviderBaseURLSettingKey(provider)).Take(&baseURLSetting).Error
		if err == nil {
			baseURL = strings.TrimSpace(baseURLSetting.ValuePlain)
		} else if provider == defaultProvider {
			baseURL = strings.TrimSpace(getPlainSetting(tenantID, "ai_base_url"))
		}
	}

	providerClient, err := ai.NewProvider(provider, string(apiKey), model, baseURL, cfg.AIMaxTokens)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_ai_configuration", "details": err.Error()})
		return
	}

	testCtx, cancel := context.WithTimeout(c.Request.Context(), 35*time.Second)
	defer cancel()
	_, err = providerClient.AnalyzeChat(testCtx, "Respond with valid JSON only: {\"ok\": true}", "Ping")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "api_test_failed", "details": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "ok", "provider": provider, "message": "API key works"})
}

func loadNineRouterTestCredentials(ctx context.Context, tenantID string) (*models.AppSetting, string, error) {
	key := ai.ProviderAPIKeySettingKey("9router")
	urlKey := ai.ProviderBaseURLSettingKey("9router")
	var settings []models.AppSetting
	if err := db.DB.WithContext(ctx).Where("tenant_id = ? AND setting_key IN ?", tenantID, []string{key, urlKey}).Find(&settings).Error; err != nil {
		return nil, "", err
	}
	for _, setting := range settings {
		if setting.SettingKey == key && (len(setting.ValueEncrypted) > 0 || setting.ValuePlain != "") {
			return &setting, getSettingValue(settings, urlKey, ""), nil
		}
	}
	return nil, "", gorm.ErrRecordNotFound
}

// SaveGeneralSettings saves general tenant settings
func SaveGeneralSettings(c *gin.Context) {
	tenantID := middleware.GetTenantID(c)
	var req struct {
		CompanyName  string  `json:"company_name"`
		Timezone     string  `json:"timezone"`
		Language     string  `json:"language"`
		ExchangeRate float64 `json:"exchange_rate_vnd"`
		AppURL       string  `json:"app_url"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request", "details": err.Error()})
		return
	}

	// Update tenant name
	if req.CompanyName != "" {
		db.DB.Model(&models.Tenant{}).Where("id = ?", tenantID).Updates(map[string]interface{}{
			"name":       req.CompanyName,
			"updated_at": time.Now(),
		})
	}

	// Save timezone and language as settings
	if req.Timezone != "" {
		upsertSetting(tenantID, "timezone", req.Timezone, nil)
	}
	if req.Language != "" {
		upsertSetting(tenantID, "language", req.Language, nil)
	}
	if req.ExchangeRate > 0 {
		upsertSetting(tenantID, "exchange_rate_vnd", fmt.Sprintf("%.0f", req.ExchangeRate), nil)
	}

	// Strip trailing slash from app URL
	appURL := strings.TrimRight(req.AppURL, "/")
	upsertSetting(tenantID, "app_url", appURL, nil)

	c.JSON(http.StatusOK, gin.H{"message": "saved"})
}

// ChangePassword changes the user's password
func ChangePassword(c *gin.Context) {
	userID := middleware.GetUserID(c)
	var req struct {
		CurrentPassword string `json:"current_password" binding:"required"`
		NewPassword     string `json:"new_password" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request", "details": err.Error()})
		return
	}
	if err := validatePasswordComplexity(req.NewPassword); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "weak_password", "message": err.Error()})
		return
	}

	var user models.User
	if err := db.DB.First(&user, "id = ?", userID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "user_not_found"})
		return
	}

	// Verify current password
	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(req.CurrentPassword)); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "wrong_current_password"})
		return
	}

	// Hash new password
	hash, err := bcrypt.GenerateFromPassword([]byte(req.NewPassword), bcrypt.DefaultCost)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "hash_failed"})
		return
	}

	if err := db.DB.Model(&user).Updates(map[string]interface{}{
		"password_hash": string(hash),
		"updated_at":    time.Now(),
	}).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "update_failed"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "password_changed"})
}

// allowedSettingKeys is a whitelist of keys that can be set via the SaveSetting API.
// Sensitive keys like ai_api_key must be set through dedicated endpoints.
var allowedSettingKeys = map[string]bool{
	"onboarding_dismissed": true,
	"language":             true,
	"timezone":             true,
	"date_format":          true,
	"notification_enabled": true,
	"sync_interval":        true,
	"default_ai_provider":  true,
	"default_ai_model":     true,
	"chatbot_active":       true,
	"banner_offsets":       true,
}

// SaveSetting saves a single key-value setting
func SaveSetting(c *gin.Context) {
	tenantID := middleware.GetTenantID(c)
	var req struct {
		Key   string `json:"key" binding:"required"`
		Value string `json:"value" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request"})
		return
	}
	if !allowedSettingKeys[req.Key] {
		c.JSON(http.StatusBadRequest, gin.H{"error": "setting_key_not_allowed"})
		return
	}
	upsertSetting(tenantID, req.Key, req.Value, nil)
	c.JSON(http.StatusOK, gin.H{"message": "saved"})
}

func upsertSetting(tenantID, key, plainValue string, encryptedValue []byte) {
	var existing models.AppSetting
	result := db.DB.Where("tenant_id = ? AND setting_key = ?", tenantID, key).First(&existing)

	if result.Error == nil {
		// Update
		updates := map[string]interface{}{"updated_at": time.Now()}
		if plainValue != "" {
			updates["value_plain"] = plainValue
			updates["value_encrypted"] = nil
		}
		if encryptedValue != nil {
			updates["value_encrypted"] = encryptedValue
			updates["value_plain"] = ""
		}
		db.DB.Model(&existing).Updates(updates)
	} else {
		// Create
		setting := models.AppSetting{
			ID:             pkg.NewUUID(),
			TenantID:       tenantID,
			SettingKey:     key,
			ValuePlain:     plainValue,
			ValueEncrypted: encryptedValue,
			CreatedAt:      time.Now(),
			UpdatedAt:      time.Now(),
		}
		db.DB.Create(&setting)
	}
}

func isMaskedSecret(v string) bool {
	return strings.TrimSpace(v) == "••••••••"
}

// getPlainSetting trả về giá trị plain hiện tại của một setting (rỗng nếu chưa có).
func getPlainSetting(tenantID, key string) string {
	var s models.AppSetting
	if err := db.DB.Where("tenant_id = ? AND setting_key = ?", tenantID, key).First(&s).Error; err == nil {
		return s.ValuePlain
	}
	return ""
}

// shortHash trả về 8 ký tự đầu của SHA-256 để ghi audit prompt mà không lộ nội dung.
func shortHash(s string) string {
	if s == "" {
		return "(rỗng)"
	}
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])[:8]
}

func getFirstSettingByKeys(tenantID string, keys []string) (*models.AppSetting, error) {
	for _, key := range keys {
		var setting models.AppSetting
		if err := db.DB.Where("tenant_id = ? AND setting_key = ?", tenantID, key).First(&setting).Error; err == nil {
			return &setting, nil
		}
	}
	return nil, fmt.Errorf("setting not found")
}

// ListOpenRouterFreeModels lists OpenRouter's current free models for the
// AI settings page (the catalogue is public; no key is used).
func ListOpenRouterFreeModels(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 20*time.Second)
	defer cancel()
	models, err := ai.OpenRouterFreeModels(ctx, c.Query("refresh") == "1")
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "openrouter_models_failed", "details": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"models": models})
}
