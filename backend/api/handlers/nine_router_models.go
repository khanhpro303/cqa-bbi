package handlers

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/vietbui/chat-quality-agent/ai"
	"github.com/vietbui/chat-quality-agent/api/middleware"
	"github.com/vietbui/chat-quality-agent/config"
	"github.com/vietbui/chat-quality-agent/pkg"
	"gorm.io/gorm"
)

// ListNineRouterModels reads the catalog using the tenant's saved connection.
func ListNineRouterModels(c *gin.Context) {
	ctx := c.Request.Context()
	setting, baseURL, err := loadNineRouterTestCredentials(ctx, middleware.GetTenantID(c))
	if errors.Is(err, gorm.ErrRecordNotFound) || (err == nil && baseURL == "") {
		c.JSON(http.StatusConflict, gin.H{"error": "9router_not_configured"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "ai_configuration_unavailable"})
		return
	}
	key := setting.ValuePlain
	if len(setting.ValueEncrypted) > 0 {
		cfg, err := config.Load()
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "ai_configuration_unavailable"})
			return
		}
		decrypted, err := pkg.Decrypt(setting.ValueEncrypted, cfg.EncryptionKey)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "decrypt_failed"})
			return
		}
		key = string(decrypted)
	}

	models, err := ai.FetchNineRouterModels(ctx, baseURL, key)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "9router_models_failed"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"models": models})
}
