package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/vietbui/chat-quality-agent/ai"
	"github.com/vietbui/chat-quality-agent/api/middleware"
	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/db/models"
	"github.com/vietbui/chat-quality-agent/engine"
	"github.com/vietbui/chat-quality-agent/pkg"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func ListAITaskModels(c *gin.Context) {
	tenantID := middleware.GetTenantID(c)
	tasks := engine.AITaskModelOptions()
	keys := []string{"ai_provider", "ai_model"}
	for _, task := range tasks {
		keys = append(keys, engine.AITaskModelSettingPrefix+task.Key)
	}
	var settings []models.AppSetting
	if err := db.DB.WithContext(c.Request.Context()).Select("setting_key", "value_plain").
		Where("tenant_id = ? AND setting_key IN ?", tenantID, keys).Find(&settings).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "load_ai_task_models_failed"})
		return
	}
	provider := getSettingValue(settings, "ai_provider", "claude")
	model := getSettingValue(settings, "ai_model", ai.DefaultModel(provider))
	for i, task := range tasks {
		var err error
		tasks[i].AITaskModel, err = engine.ParseAITaskModel(getSettingValue(settings, engine.AITaskModelSettingPrefix+task.Key, ""))
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "invalid_stored_ai_task_model", "task": task.Key})
			return
		}
	}
	c.JSON(http.StatusOK, gin.H{"tasks": tasks, "default_provider": provider, "default_model": model})
}

func SaveAITaskModels(c *gin.Context) {
	tenantID := middleware.GetTenantID(c)
	var req struct {
		Tasks []engine.AITaskModelSetting `json:"tasks" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request"})
		return
	}
	tasks, invalidKey, err := validateAITaskModelChanges(req.Tasks)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_ai_task_model", "task": invalidKey, "details": err.Error()})
		return
	}

	database := db.DB.WithContext(c.Request.Context())
	var settingRows []models.AppSetting
	keys := []string{"ai_provider", "ai_api_key"}
	for _, provider := range ai.Providers {
		keys = append(keys, ai.ProviderAPIKeySettingKey(provider))
	}
	if err := database.Where("tenant_id = ? AND setting_key IN ?", tenantID, keys).
		Find(&settingRows).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "load_ai_task_models_failed"})
		return
	}
	defaultProvider := getSettingValue(settingRows, "ai_provider", "claude")
	hasKey := make(map[string]bool, len(settingRows))
	for _, setting := range settingRows {
		hasKey[setting.SettingKey] = len(setting.ValueEncrypted) > 0 || setting.ValuePlain != ""
	}
	for _, task := range tasks {
		if task.Provider == "" {
			continue
		}
		canUseLegacyKey := task.Provider == defaultProvider && len(ai.ProviderAPIKeySettingKeys(task.Provider)) > 1 && hasKey["ai_api_key"]
		if !hasKey[ai.ProviderAPIKeySettingKey(task.Provider)] && !canUseLegacyKey {
			c.JSON(http.StatusBadRequest, gin.H{"error": "no_api_key_configured", "task": task.Key, "provider": task.Provider,
				"details": "Hãy lưu khóa API cho nhà cung cấp đã chọn trước khi lưu tác vụ."})
			return
		}
	}

	err = database.Transaction(func(tx *gorm.DB) error {
		for _, task := range tasks {
			key := engine.AITaskModelSettingPrefix + task.Key
			if task.Provider == "" {
				if err := tx.Where("tenant_id = ? AND setting_key = ?", tenantID, key).Delete(&models.AppSetting{}).Error; err != nil {
					return err
				}
				continue
			}
			value, err := json.Marshal(task.AITaskModel)
			if err != nil {
				return err
			}
			setting := models.AppSetting{ID: pkg.NewUUID(), TenantID: tenantID, SettingKey: key, ValuePlain: string(value)}
			if err := tx.Clauses(clause.OnConflict{
				Columns:   []clause.Column{{Name: "tenant_id"}, {Name: "setting_key"}},
				DoUpdates: clause.AssignmentColumns([]string{"value_plain", "updated_at"}),
			}).Create(&setting).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "save_ai_task_models_failed"})
		return
	}
	for _, task := range tasks {
		detail := "Dùng cấu hình AI chung"
		if task.Provider != "" {
			detail = task.Provider + "/" + task.Model
		}
		db.LogActivity(tenantID, middleware.GetUserID(c), middleware.GetUserEmail(c), "ai.task_model_changed", "settings",
			task.Key, detail, "", c.ClientIP())
	}
	ListAITaskModels(c)
}

func validateAITaskModelChanges(changes []engine.AITaskModelSetting) ([]engine.AITaskModelSetting, string, error) {
	options := engine.AITaskModelOptions()
	known := make(map[string]bool, len(options))
	for _, option := range options {
		known[option.Key] = true
	}
	for i, change := range changes {
		if !known[change.Key] {
			return nil, change.Key, errors.New("tác vụ không hợp lệ hoặc được gửi lặp")
		}
		delete(known, change.Key)
		value, err := engine.NormalizeAITaskModel(change.AITaskModel)
		if err != nil {
			return nil, change.Key, err
		}
		changes[i].AITaskModel = value
	}
	slices.SortFunc(changes, func(a, b engine.AITaskModelSetting) int { return strings.Compare(a.Key, b.Key) })
	return changes, "", nil
}
