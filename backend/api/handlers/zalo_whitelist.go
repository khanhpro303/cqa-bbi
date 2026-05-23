package handlers

import (
	"math/rand"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/vietbui/chat-quality-agent/api/middleware"
	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/db/models"
)

type ZaloWhitelistResponse struct {
	ID          string    `json:"id"`
	TenantID    string    `json:"tenant_id"`
	ZaloUserID  string    `json:"zalo_user_id"`
	Name        string    `json:"name"`
	Avatar      string    `json:"avatar"`
	VerifyToken string    `json:"verify_token"`
	Status      string    `json:"status"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

func ListZaloWhitelist(c *gin.Context) {
	tenantID := middleware.GetTenantID(c)

	var list []models.ZaloWhitelist
	if err := db.DB.Where("tenant_id = ?", tenantID).Order("created_at DESC").Find(&list).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed_to_fetch_whitelist"})
		return
	}

	results := make([]ZaloWhitelistResponse, len(list))
	for i, item := range list {
		results[i] = ZaloWhitelistResponse{
			ID:          item.ID,
			TenantID:    item.TenantID,
			ZaloUserID:  item.ZaloUserID,
			Name:        item.Name,
			Avatar:      item.Avatar,
			VerifyToken: item.VerifyToken,
			Status:      item.Status,
			CreatedAt:   item.CreatedAt,
			UpdatedAt:   item.UpdatedAt,
		}
	}

	c.JSON(http.StatusOK, results)
}

func AddZaloWhitelist(c *gin.Context) {
	tenantID := middleware.GetTenantID(c)

	var req struct {
		ZaloUserID string `json:"zalo_user_id" binding:"required"`
		Name       string `json:"name" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request", "details": err.Error()})
		return
	}

	// Check if already whitelisted (active) for this tenant
	var existing models.ZaloWhitelist
	if err := db.DB.Where("tenant_id = ? AND zalo_user_id = ? AND status = ?", tenantID, req.ZaloUserID, "active").First(&existing).Error; err == nil {
		c.JSON(http.StatusConflict, gin.H{"error": "user_already_whitelisted"})
		return
	}

	now := time.Now()
	item := models.ZaloWhitelist{
		ID:         uuid.New().String(),
		TenantID:   tenantID,
		ZaloUserID: req.ZaloUserID,
		Name:       req.Name,
		Status:     "active",
		CreatedAt:  now,
		UpdatedAt:  now,
	}

	if err := db.DB.Create(&item).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed_to_add_whitelist"})
		return
	}

	c.JSON(http.StatusCreated, ZaloWhitelistResponse{
		ID:         item.ID,
		TenantID:   item.TenantID,
		ZaloUserID: item.ZaloUserID,
		Name:       item.Name,
		Status:     item.Status,
		CreatedAt:  item.CreatedAt,
	})
}

func InviteZaloWhitelist(c *gin.Context) {
	tenantID := middleware.GetTenantID(c)

	var req struct {
		Name string `json:"name" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request", "details": err.Error()})
		return
	}

	// Generate verification token (6 random uppercase alphanumeric characters)
	token := generateVerifyToken()

	now := time.Now()
	item := models.ZaloWhitelist{
		ID:          uuid.New().String(),
		TenantID:    tenantID,
		Name:        req.Name,
		VerifyToken: token,
		Status:      "pending",
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	if err := db.DB.Create(&item).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed_to_create_invite"})
		return
	}

	c.JSON(http.StatusCreated, ZaloWhitelistResponse{
		ID:          item.ID,
		TenantID:    item.TenantID,
		Name:        item.Name,
		VerifyToken: item.VerifyToken,
		Status:      item.Status,
		CreatedAt:   item.CreatedAt,
	})
}

func DeleteZaloWhitelist(c *gin.Context) {
	tenantID := middleware.GetTenantID(c)
	id := c.Param("id")

	result := db.DB.Where("id = ? AND tenant_id = ?", id, tenantID).Delete(&models.ZaloWhitelist{})
	if result.RowsAffected == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "record_not_found"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "deleted"})
}

func generateVerifyToken() string {
	const charset = "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	rand.Seed(time.Now().UnixNano())
	b := make([]byte, 6)
	for i := range b {
		b[i] = charset[rand.Intn(len(charset))]
	}
	return string(b)
}
