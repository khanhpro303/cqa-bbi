package handlers

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode"

	"github.com/gin-gonic/gin"
	"github.com/vietbui/chat-quality-agent/api/middleware"
	"github.com/vietbui/chat-quality-agent/config"
	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/db/models"
	"github.com/vietbui/chat-quality-agent/knowledge"
	"github.com/vietbui/chat-quality-agent/pkg"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const firecrawlKeySetting = "knowledge_firecrawl_key"

func loadFirecrawlKey(ctx context.Context, tenantID string) (key string, hasKey bool, err error) {
	var row models.AppSetting
	err = db.DB.WithContext(ctx).Select("value_encrypted").
		Where("tenant_id = ? AND setting_key = ?", tenantID, firecrawlKeySetting).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) || (err == nil && len(row.ValueEncrypted) == 0) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	cfg, _ := config.Load()
	if cfg == nil || cfg.EncryptionKey == "" {
		return "", true, nil
	}
	plain, err := pkg.Decrypt(row.ValueEncrypted, cfg.EncryptionKey)
	if err != nil {
		return "", true, nil
	}
	return strings.TrimSpace(string(plain)), true, nil
}

func GetKnowledgeFirecrawl(c *gin.Context) {
	key, hasKey, err := loadFirecrawlKey(c.Request.Context(), middleware.GetTenantID(c))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "query_failed"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"has_key": hasKey, "enabled": key != ""})
}

func SaveKnowledgeFirecrawl(c *gin.Context) {
	tenantID := middleware.GetTenantID(c)
	ctx := c.Request.Context()
	var req struct {
		APIKey   string `json:"api_key"`
		ClearKey bool   `json:"clear_key"`
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 4<<10)
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_body"})
		return
	}

	key := strings.TrimSpace(req.APIKey)
	var saveErr error
	switch {
	case req.ClearKey:
		saveErr = db.DB.WithContext(ctx).Where("tenant_id = ? AND setting_key = ?", tenantID, firecrawlKeySetting).
			Delete(&models.AppSetting{}).Error
	case key == "" || isMaskedSecret(key):
		GetKnowledgeFirecrawl(c)
		return
	default:
		if len(key) > 1024 || strings.ContainsFunc(key, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_api_key"})
			return
		}
		cfg, _ := config.Load()
		if cfg == nil || cfg.EncryptionKey == "" {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "encryption_unavailable"})
			return
		}
		encrypted, err := pkg.Encrypt([]byte(key), cfg.EncryptionKey)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "encryption_failed"})
			return
		}
		row := models.AppSetting{
			ID: pkg.NewUUID(), TenantID: tenantID, SettingKey: firecrawlKeySetting, ValueEncrypted: encrypted,
		}
		saveErr = db.DB.WithContext(ctx).Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "tenant_id"}, {Name: "setting_key"}},
			DoUpdates: clause.AssignmentColumns([]string{"value_encrypted", "value_plain", "updated_at"}),
		}).Create(&row).Error
	}
	if saveErr != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "save_failed"})
		return
	}
	db.LogActivity(tenantID, middleware.GetUserID(c), middleware.GetUserEmail(c), "knowledge.firecrawl_updated", "settings", "knowledge_firecrawl", "", "", c.ClientIP())
	GetKnowledgeFirecrawl(c)
}

func TestKnowledgeFirecrawl(c *gin.Context) {
	client := configuredFirecrawlClient(c)
	if client == nil {
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 15*time.Second)
	defer cancel()
	if err := client.Check(ctx); err != nil {
		c.JSON(http.StatusOK, gin.H{"ok": false, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func CreateKnowledgeURL(c *gin.Context) {
	var req struct {
		URL      string `json:"url"`
		Title    string `json:"title"`
		Audience string `json:"audience"`
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 16<<10)
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_body"})
		return
	}
	source, err := knowledge.ParseSourceURL(req.URL)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_url"})
		return
	}
	client := configuredFirecrawlClient(c)
	if client == nil {
		return
	}
	tenantID := middleware.GetTenantID(c)
	doc, err := knowledge.Default().CreateFromURL(knowledge.NewDocumentInput{
		TenantID: tenantID, Title: req.Title, Audience: req.Audience, CreatedBy: middleware.GetUserEmail(c),
	}, source, client)
	if err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": "save_failed"})
		return
	}
	db.LogActivity(tenantID, middleware.GetUserID(c), middleware.GetUserEmail(c), "knowledge.document_url_imported", "knowledge_document", doc.ID,
		doc.Title, "", c.ClientIP())
	c.JSON(http.StatusAccepted, doc)
}

func configuredFirecrawlClient(c *gin.Context) *knowledge.FirecrawlClient {
	key, _, err := loadFirecrawlKey(c.Request.Context(), middleware.GetTenantID(c))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "query_failed"})
		return nil
	}
	if key == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "firecrawl_not_configured"})
		return nil
	}
	return knowledge.NewFirecrawlClient(key)
}
