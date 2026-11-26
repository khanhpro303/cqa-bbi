package handlers

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/gin-gonic/gin"
	"github.com/vietbui/chat-quality-agent/config"
	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/db/models"
	"github.com/vietbui/chat-quality-agent/engine"
	"gorm.io/gorm"
)

const personalZaloTextLimit = 2000

var personalZaloUIDPattern = regexp.MustCompile(`^[0-9]{1,64}$`)

type personalZaloChatbotRequest struct {
	SchemaVersion     string    `json:"schema_version" binding:"required,eq=personal-zalo-chatbot-v1"`
	RequestID         string    `json:"request_id" binding:"required,max=64"`
	TenantID          string    `json:"tenant_id" binding:"required,max=36"`
	ChannelID         string    `json:"channel_id" binding:"required,max=36"`
	AccountExternalID string    `json:"account_external_id" binding:"required"`
	ThreadID          string    `json:"thread_id" binding:"required"`
	MessageID         string    `json:"message_id" binding:"required,max=128"`
	SentAt            time.Time `json:"sent_at" binding:"required"`
	Text              string    `json:"text" binding:"required"`
}

// NewPersonalZaloChatbotHandler receives live direct-message turns from the
// authenticated gateway. The gateway owns the Zalo session and sends the reply.
func NewPersonalZaloChatbotHandler(cfg *config.Config) gin.HandlerFunc {
	langflow := engine.NewLangflowClient(cfg)
	settingKeys := append([]string{"chatbot_active", "ai_engine_system_prompt"}, engine.LangflowSettingKeys...)

	return func(c *gin.Context) {
		var req personalZaloChatbotRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request"})
			return
		}
		if c.GetString("internal_request_id") != req.RequestID {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "request_id_mismatch"})
			return
		}
		req.Text = strings.TrimSpace(req.Text)
		age := time.Since(req.SentAt)
		if req.Text == "" || personalZaloTextBoundary(req.Text, personalZaloTextLimit) != len(req.Text) ||
			!personalZaloUIDPattern.MatchString(req.AccountExternalID) || !personalZaloUIDPattern.MatchString(req.ThreadID) ||
			req.ThreadID == req.AccountExternalID || age > 2*time.Minute || age < -30*time.Second {
			c.Status(http.StatusNoContent)
			return
		}

		ctx, cancel := context.WithTimeout(c.Request.Context(), 45*time.Second)
		defer cancel()
		enabled, err := personalZaloChatbotChannelEnabled(ctx, req)
		if err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "chatbot_settings_unavailable"})
			return
		}
		if !enabled {
			c.Status(http.StatusNoContent)
			return
		}
		settings, err := loadPersonalZaloChatbotSettings(ctx, req.TenantID, settingKeys...)
		if err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "chatbot_settings_unavailable"})
			return
		}
		flow := engine.ResolveTenantLangflowConfig(settings, cfg)
		if !personalZaloChatbotMasterEnabled(settings) || flow.APIURL == "" || flow.PublicFlowID == "" {
			c.Status(http.StatusNoContent)
			return
		}

		// Keep both semantic history and Langflow sessions isolated by tenant,
		// channel, Zalo account, and peer. Personal turns carry no ERP grants.
		digest := sha256.Sum256([]byte(strings.Join([]string{req.TenantID, req.ChannelID, req.AccountExternalID, req.ThreadID}, "\x00")))
		scope := "personal-zalo-" + hex.EncodeToString(digest[:])
		reply, err := langflow.RunFlowWithOverrides(ctx, scope, scope, req.Text, flow.APIURL, flow.APIKey, flow.PublicFlowID, settings["ai_engine_system_prompt"].ValuePlain)
		if err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": "chatbot_reply_failed"})
			return
		}
		reply = strings.TrimSpace(reply)
		if reply == "" {
			c.Status(http.StatusNoContent)
			return
		}

		// A channel or tenant can be paused while Langflow is generating.
		enabled, err = personalZaloChatbotChannelEnabled(ctx, req)
		if err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "chatbot_settings_unavailable"})
			return
		}
		if !enabled {
			c.Status(http.StatusNoContent)
			return
		}
		settings, err = loadPersonalZaloChatbotSettings(ctx, req.TenantID, "chatbot_active")
		if err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "chatbot_settings_unavailable"})
			return
		}
		if !personalZaloChatbotMasterEnabled(settings) {
			c.Status(http.StatusNoContent)
			return
		}

		if personalZaloTextBoundary(reply, personalZaloTextLimit) < len(reply) {
			reply = strings.TrimSpace(reply[:personalZaloTextBoundary(reply, personalZaloTextLimit-1)]) + "…"
		}
		c.JSON(http.StatusOK, gin.H{"reply": reply})
	}
}

func personalZaloChatbotChannelEnabled(ctx context.Context, req personalZaloChatbotRequest) (bool, error) {
	var channel models.Channel
	err := db.DB.WithContext(ctx).Select("external_id", "metadata").
		Where("id = ? AND tenant_id = ? AND channel_type = ? AND is_active = ? AND personal_zalo_auto_reply_enabled = ?",
			req.ChannelID, req.TenantID, "personal_zalo_import", true, true).
		Take(&channel).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if channel.ExternalID != "" && channel.ExternalID != req.AccountExternalID {
		return false, nil
	}
	metadata, err := decodeMetadataObject(channel.Metadata)
	return err == nil && metadata["sync_scope"] != "group", err
}

func loadPersonalZaloChatbotSettings(ctx context.Context, tenantID string, keys ...string) (map[string]models.AppSetting, error) {
	var rows []models.AppSetting
	err := db.DB.WithContext(ctx).Where("tenant_id = ? AND setting_key IN ?", tenantID, keys).Find(&rows).Error
	if err != nil {
		return nil, err
	}
	settings := make(map[string]models.AppSetting, len(rows))
	for _, row := range rows {
		settings[row.SettingKey] = row
	}
	return settings, nil
}

func personalZaloChatbotMasterEnabled(settings map[string]models.AppSetting) bool {
	setting, exists := settings["chatbot_active"]
	return !exists || setting.ValuePlain == "true"
}

func personalZaloTextBoundary(text string, limit int) int {
	for index, r := range text {
		limit -= utf16.RuneLen(r)
		if limit < 0 {
			return index
		}
	}
	return len(text)
}
