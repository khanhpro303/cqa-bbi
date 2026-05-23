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
	"github.com/vietbui/chat-quality-agent/ai"
	"github.com/vietbui/chat-quality-agent/api/middleware"
	"github.com/vietbui/chat-quality-agent/config"
	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/db/models"
	"github.com/vietbui/chat-quality-agent/engine"
	"github.com/vietbui/chat-quality-agent/pkg"
	"golang.org/x/crypto/bcrypt"
)

// GetSettings returns all non-secret settings for the tenant
func GetSettings(c *gin.Context) {
	tenantID := middleware.GetTenantID(c)

	var settings []models.AppSetting
	db.DB.Where("tenant_id = ?", tenantID).Find(&settings)

	result := make(map[string]string)
	for _, s := range settings {
		if s.ValuePlain != "" {
			result[s.SettingKey] = s.ValuePlain
		} else if len(s.ValueEncrypted) > 0 {
			// Return masked value for encrypted settings
			result[s.SettingKey] = "••••••••"
		}
	}

	cfg, _ := config.Load()
	if _, ok := result["ai_engine_langflow_url"]; !ok && cfg.LangflowAPIURL != "" {
		result["ai_engine_langflow_url"] = cfg.LangflowAPIURL
	}
	if _, ok := result["ai_engine_langflow_flow_id"]; !ok && cfg.LangflowFlowID != "" {
		result["ai_engine_langflow_flow_id"] = cfg.LangflowFlowID
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

	// Also get tenant info
	var tenant models.Tenant
	db.DB.First(&tenant, "id = ?", tenantID)

	c.JSON(http.StatusOK, gin.H{
		"settings": result,
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

// SaveAISettings saves AI provider and API key
func SaveAISettings(c *gin.Context) {
	tenantID := middleware.GetTenantID(c)
	var req struct {
		Provider  string `json:"provider" binding:"required,oneof=claude gemini openai"`
		APIKey    string `json:"api_key" binding:"required"`
		Model     string `json:"model"`
		BaseURL   string `json:"base_url"`
		BatchMode string `json:"batch_mode"`
		BatchSize string `json:"batch_size"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request", "details": err.Error()})
		return
	}

	cfg, _ := config.Load()

	// Save model (plain)
	if req.Model != "" {
		upsertSetting(tenantID, "ai_model", req.Model, nil)
	}

	// Save base URL (plain, optional — empty string clears it)
	if req.BaseURL != "" {
		upsertSetting(tenantID, "ai_base_url", req.BaseURL, nil)
	} else {
		// Explicitly clear: delete the setting if empty
		db.DB.Where("tenant_id = ? AND setting_key = ?", tenantID, "ai_base_url").Delete(&models.AppSetting{})
	}

	// Save API key (encrypted, per provider). Keep existing key if masked value is sent.
	currentProvider := req.Provider
	var currentProviderSetting models.AppSetting
	if err := db.DB.Where("tenant_id = ? AND setting_key = ?", tenantID, "ai_provider").First(&currentProviderSetting).Error; err == nil && currentProviderSetting.ValuePlain != "" {
		currentProvider = currentProviderSetting.ValuePlain
	}

	providerKey := ai.ProviderAPIKeySettingKey(req.Provider)
	if isMaskedSecret(req.APIKey) {
		if _, err := getFirstSettingByKeys(tenantID, []string{providerKey}); err != nil {
			// Backward compatibility: allow legacy key only when provider is unchanged.
			if req.Provider != currentProvider {
				c.JSON(http.StatusBadRequest, gin.H{"error": "no_api_key_configured"})
				return
			}
			if _, legacyErr := getFirstSettingByKeys(tenantID, []string{"ai_api_key"}); legacyErr != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": "no_api_key_configured"})
				return
			}
		}
	} else {
		encrypted, err := pkg.Encrypt([]byte(req.APIKey), cfg.EncryptionKey)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "encryption_failed"})
			return
		}
		upsertSetting(tenantID, providerKey, "", encrypted)
	}

	// Save provider (plain)
	upsertSetting(tenantID, "ai_provider", req.Provider, nil)

	// Save batch settings
	if req.BatchMode != "" {
		upsertSetting(tenantID, "ai_batch_mode", req.BatchMode, nil)
	}
	if req.BatchSize != "" {
		upsertSetting(tenantID, "ai_batch_size", req.BatchSize, nil)
	}

	c.JSON(http.StatusOK, gin.H{"message": "saved"})
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
		LangflowBaseURL string `json:"langflow_base_url"`
		LangflowFlowID  string `json:"langflow_flow_id"`
		LangflowToken   string `json:"langflow_token"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request", "details": err.Error()})
		return
	}

	cfg, _ := config.Load()

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

	c.JSON(http.StatusOK, gin.H{"message": "saved"})
}

// TestLangflowConnection pings Langflow to check if configuration works
func TestLangflowConnection(c *gin.Context) {
	tenantID := middleware.GetTenantID(c)
	cfg, _ := config.Load()

	var req struct {
		LangflowBaseURL string `json:"langflow_base_url"`
		LangflowFlowID  string `json:"langflow_flow_id"`
		LangflowToken   string `json:"langflow_token"`
	}
	body, _ := io.ReadAll(c.Request.Body)
	if len(strings.TrimSpace(string(body))) > 0 {
		if err := json.Unmarshal(body, &req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request"})
			return
		}
	}

	// Resolve params (fallback to DB, then .env if not provided)
	baseURL := req.LangflowBaseURL
	if baseURL == "" {
		var setting models.AppSetting
		if err := db.DB.Where("tenant_id = ? AND setting_key = ?", tenantID, "ai_engine_langflow_url").First(&setting).Error; err == nil {
			baseURL = setting.ValuePlain
		} else {
			baseURL = cfg.LangflowAPIURL
		}
	}
	flowID := req.LangflowFlowID
	if flowID == "" {
		var setting models.AppSetting
		if err := db.DB.Where("tenant_id = ? AND setting_key = ?", tenantID, "ai_engine_langflow_flow_id").First(&setting).Error; err == nil {
			flowID = setting.ValuePlain
		} else {
			flowID = cfg.LangflowFlowID
		}
	}
	token := ""
	if req.LangflowToken != "" && !isMaskedSecret(req.LangflowToken) {
		token = req.LangflowToken
	} else {
		var setting models.AppSetting
		if err := db.DB.Where("tenant_id = ? AND setting_key = ?", tenantID, "ai_engine_langflow_token").First(&setting).Error; err == nil {
			if len(setting.ValueEncrypted) > 0 {
				decrypted, _ := pkg.Decrypt(setting.ValueEncrypted, cfg.EncryptionKey)
				token = string(decrypted)
			} else {
				token = setting.ValuePlain
			}
		} else {
			token = cfg.LangflowAPIKey
		}
	}

	if baseURL == "" || flowID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing_config", "message": "Vui lòng cung cấp URL và Flow ID"})
		return
	}

	// Try calling run flow with a ping using the actual LangflowClient logic
	lfClient := engine.NewLangflowClient(cfg)
	
	_, err := lfClient.RunFlowWithOverrides(c.Request.Context(), "ping_test_session", "ping", baseURL, token, flowID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "ok", "message": "Kết nối thành công"})
}

// TestAIKey tests the AI API key by making a simple request
func TestAIKey(c *gin.Context) {
	tenantID := middleware.GetTenantID(c)
	cfg, _ := config.Load()

	// Optional request payload so UI can test unsaved key/provider directly.
	var req struct {
		Provider string `json:"provider"`
		APIKey   string `json:"api_key"`
		Model    string `json:"model"`
		BaseURL  string `json:"base_url"`
	}
	body, _ := io.ReadAll(c.Request.Body)
	if len(strings.TrimSpace(string(body))) > 0 {
		if err := json.Unmarshal(body, &req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request"})
			return
		}
	}

	// Get provider
	var providerSetting models.AppSetting
	provider := "claude"
	if err := db.DB.Where("tenant_id = ? AND setting_key = ?", tenantID, "ai_provider").First(&providerSetting).Error; err == nil {
		provider = providerSetting.ValuePlain
	}
	if req.Provider != "" {
		switch req.Provider {
		case "claude", "gemini", "openai":
			provider = req.Provider
		default:
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_provider"})
			return
		}
	}

	var apiKey []byte
	if req.APIKey != "" && !isMaskedSecret(req.APIKey) {
		apiKey = []byte(req.APIKey)
	} else {
		// Get API key by provider (fallback to legacy ai_api_key)
		setting, err := getFirstSettingByKeys(tenantID, ai.ProviderAPIKeySettingKeys(provider))
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

	_ = apiKey

	// Get model + base URL for provider test (allow request override before save)
	model := ""
	if req.Model != "" {
		model = req.Model
	} else {
		var modelSetting models.AppSetting
		if err := db.DB.Where("tenant_id = ? AND setting_key = ?", tenantID, "ai_model").First(&modelSetting).Error; err == nil {
			model = strings.TrimSpace(modelSetting.ValuePlain)
		}
	}
	baseURL := ""
	if req.BaseURL != "" {
		baseURL = strings.TrimSpace(req.BaseURL)
	} else {
		var baseURLSetting models.AppSetting
		if err := db.DB.Where("tenant_id = ? AND setting_key = ?", tenantID, "ai_base_url").First(&baseURLSetting).Error; err == nil {
			baseURL = strings.TrimSpace(baseURLSetting.ValuePlain)
		}
	}

	var providerClient ai.AIProvider
	switch provider {
	case "claude":
		providerClient = ai.NewClaudeProvider(string(apiKey), model, cfg.AIMaxTokens, baseURL)
	case "gemini":
		providerClient = ai.NewGeminiProvider(string(apiKey), model, baseURL)
	case "openai":
		providerClient = ai.NewOpenAIProvider(string(apiKey), model, baseURL)
	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_provider"})
		return
	}

	testCtx, cancel := context.WithTimeout(c.Request.Context(), 35*time.Second)
	defer cancel()
	_, err := providerClient.AnalyzeChat(testCtx, "Respond with valid JSON only: {\"ok\": true}", "Ping")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "api_test_failed", "details": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "ok", "provider": provider, "message": "API key works"})
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

func getFirstSettingByKeys(tenantID string, keys []string) (*models.AppSetting, error) {
	for _, key := range keys {
		var setting models.AppSetting
		if err := db.DB.Where("tenant_id = ? AND setting_key = ?", tenantID, key).First(&setting).Error; err == nil {
			return &setting, nil
		}
	}
	return nil, fmt.Errorf("setting not found")
}
