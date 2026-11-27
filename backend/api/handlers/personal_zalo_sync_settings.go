package handlers

import (
	"context"
	"errors"
	"log"
	"math"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/vietbui/chat-quality-agent/config"
	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/db/models"
	"gorm.io/gorm"
)

func personalZaloSyncInterval(metadata map[string]interface{}) int {
	minutes, ok := metadata["sync_interval"].(float64)
	if !ok || minutes < 1 || minutes > 1440 || math.Trunc(minutes) != minutes {
		return 0
	}
	return int(minutes)
}

func validPersonalZaloSyncInterval(metadata map[string]interface{}) bool {
	return metadata["sync_interval"] == nil || personalZaloSyncInterval(metadata) > 0
}

// GetPersonalZaloSyncSettings lets the gateway read the current schedule for
// its own channel without claiming the Zalo account or starting an import.
func GetPersonalZaloSyncSettings(c *gin.Context) {
	var req struct {
		RequestID         string `json:"request_id" binding:"required,max=64"`
		TenantID          string `json:"tenant_id" binding:"required,max=64"`
		ChannelID         string `json:"channel_id" binding:"required,max=64"`
		AccountExternalID string `json:"account_external_id" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || !personalZaloUIDPattern.MatchString(req.AccountExternalID) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request"})
		return
	}
	if req.RequestID != c.GetString("internal_request_id") {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "request_id_mismatch"})
		return
	}

	var channel models.Channel
	err := db.DB.WithContext(c.Request.Context()).Select("is_active", "external_id", "metadata").
		Where("id = ? AND tenant_id = ? AND channel_type = ?", req.ChannelID, req.TenantID, "personal_zalo_import").First(&channel).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": "channel_not_found"})
		return
	}
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "personal_zalo_sync_settings_unavailable"})
		return
	}
	if channel.ExternalID != "" && channel.ExternalID != req.AccountExternalID {
		c.JSON(http.StatusConflict, gin.H{"error": "channel_account_mismatch"})
		return
	}

	metadata, _ := decodeMetadataObject(channel.Metadata)
	c.JSON(http.StatusOK, struct {
		Active              bool `json:"active"`
		SyncIntervalMinutes int  `json:"sync_interval_minutes,omitempty"`
	}{channel.IsActive, personalZaloSyncInterval(metadata)})
}

func testPersonalZaloGatewayConnection(c *gin.Context, channel models.Channel) {
	cfg, err := config.Load()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "config_load_failed"})
		return
	}
	if cfg.PersonalZaloGatewayBaseURL == "" {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "gateway_not_configured"})
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), channelHealthCheckTimeout)
	defer cancel()
	accounts, err := listPersonalZaloGatewayAccountsContext(ctx, cfg.PersonalZaloGatewayBaseURL)
	if err != nil {
		log.Printf("[channels] personal Zalo gateway connection check failed for channel %s: %v", channel.ID, err)
		c.JSON(http.StatusBadGateway, gin.H{"error": "gateway_request_failed"})
		return
	}
	account := findPersonalZaloGatewayAccount(accounts, channel.TenantID, channel.ID)
	if account == nil || account.ID == "" {
		c.JSON(http.StatusConflict, gin.H{"error": "gateway_account_not_found"})
		return
	}
	if account.Status != "connected" || !personalZaloUIDPattern.MatchString(account.ZaloUID) {
		c.JSON(http.StatusConflict, gin.H{"error": "gateway_account_not_connected"})
		return
	}
	if (account.AccountExternalID != "" && account.AccountExternalID != account.ZaloUID) ||
		(channel.ExternalID != "" && channel.ExternalID != account.ZaloUID) {
		c.JSON(http.StatusConflict, gin.H{"error": "channel_account_mismatch"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok", "message": "connection_successful"})
}
