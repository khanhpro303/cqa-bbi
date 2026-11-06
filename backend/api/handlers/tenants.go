package handlers

import (
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/vietbui/chat-quality-agent/api/middleware"
	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/db/models"
	"github.com/vietbui/chat-quality-agent/pkg"
	"gorm.io/gorm"
)

type CreateTenantRequest struct {
	Name string `json:"name" binding:"required,min=2,max=255"`
	Slug string `json:"slug" binding:"required,min=2,max=100"`
}

type TenantResponse struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Slug          string `json:"slug"`
	ChannelsCount int64  `json:"channels_count"`
	JobsCount     int64  `json:"jobs_count"`
}

var slugRegex = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*[a-z0-9]$`)

func ListTenants(c *gin.Context) {
	userID := middleware.GetUserID(c)

	var userTenants []models.UserTenant
	if err := db.DB.Where("user_id = ?", userID).Find(&userTenants).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "fetch_tenants_failed"})
		return
	}

	tenantIDs := make([]string, len(userTenants))
	for i, ut := range userTenants {
		tenantIDs[i] = ut.TenantID
	}

	if len(tenantIDs) == 0 {
		c.JSON(http.StatusOK, []TenantResponse{})
		return
	}

	var tenants []models.Tenant
	db.DB.Where("id IN ?", tenantIDs).Find(&tenants)

	results := make([]TenantResponse, len(tenants))
	for i, t := range tenants {
		var channelsCount, jobsCount int64
		db.DB.Model(&models.Channel{}).Where("tenant_id = ?", t.ID).Count(&channelsCount)
		db.DB.Model(&models.Job{}).Where("tenant_id = ?", t.ID).Count(&jobsCount)
		results[i] = TenantResponse{
			ID:            t.ID,
			Name:          t.Name,
			Slug:          t.Slug,
			ChannelsCount: channelsCount,
			JobsCount:     jobsCount,
		}
	}

	c.JSON(http.StatusOK, results)
}

func CreateTenant(c *gin.Context) {
	var req CreateTenantRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request", "details": err.Error()})
		return
	}

	if !slugRegex.MatchString(req.Slug) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_slug", "details": "slug must be lowercase alphanumeric with hyphens"})
		return
	}

	// Check slug uniqueness
	var count int64
	db.DB.Model(&models.Tenant{}).Where("slug = ?", req.Slug).Count(&count)
	if count > 0 {
		c.JSON(http.StatusConflict, gin.H{"error": "slug_already_exists"})
		return
	}

	now := time.Now()
	tenant := models.Tenant{
		ID:        pkg.NewUUID(),
		Name:      req.Name,
		Slug:      req.Slug,
		Settings:  "{}",
		CreatedAt: now,
		UpdatedAt: now,
	}

	if err := db.DB.Create(&tenant).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "create_tenant_failed"})
		return
	}

	// Add creator as owner
	userID := middleware.GetUserID(c)
	ut := models.UserTenant{
		UserID:   userID,
		TenantID: tenant.ID,
		Role:     "owner",
	}
	db.DB.Create(&ut)

	c.JSON(http.StatusCreated, TenantResponse{
		ID:   tenant.ID,
		Name: tenant.Name,
		Slug: tenant.Slug,
	})
}

func GetTenant(c *gin.Context) {
	tenantID := middleware.GetTenantID(c)

	var tenant models.Tenant
	if err := db.DB.First(&tenant, "id = ?", tenantID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "tenant_not_found"})
		return
	}

	var channelsCount, jobsCount int64
	db.DB.Model(&models.Channel{}).Where("tenant_id = ?", tenantID).Count(&channelsCount)
	db.DB.Model(&models.Job{}).Where("tenant_id = ?", tenantID).Count(&jobsCount)

	c.JSON(http.StatusOK, TenantResponse{
		ID:            tenant.ID,
		Name:          tenant.Name,
		Slug:          tenant.Slug,
		ChannelsCount: channelsCount,
		JobsCount:     jobsCount,
	})
}

func UpdateTenant(c *gin.Context) {
	tenantID := middleware.GetTenantID(c)

	var req struct {
		Name string `json:"name" binding:"required,min=2,max=255"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request", "details": err.Error()})
		return
	}

	result := db.DB.Model(&models.Tenant{}).Where("id = ?", tenantID).Updates(map[string]interface{}{
		"name":       req.Name,
		"updated_at": time.Now(),
	})
	if result.Error != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "update_tenant_failed"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "updated"})
}

func DeleteTenant(c *gin.Context) {
	tenantID := middleware.GetTenantID(c)
	var convIDs []string
	err := db.DB.Transaction(func(tx *gorm.DB) error {
		channelIDs, err := tenantJourneyChannelIDs(tx, tenantID)
		if err != nil {
			return err
		}
		for _, channelID := range channelIDs {
			if err := eraseChannelJourneySubjects(tx, tenantID, channelID, "cqa_tenant_delete"); err != nil {
				return err
			}
		}
		if err := tx.Where("tenant_id = ?", tenantID).Delete(&models.MessengerIntakeAttribution{}).Error; err != nil {
			return err
		}
		if err := tx.Model(&models.Conversation{}).Where("tenant_id = ?", tenantID).Pluck("id", &convIDs).Error; err != nil {
			return err
		}
		if len(convIDs) > 0 {
			if err := tx.Where("conversation_id IN ?", convIDs).Delete(&models.Message{}).Error; err != nil {
				return err
			}
		}
		if err := tx.Where("tenant_id = ?", tenantID).Delete(&models.Conversation{}).Error; err != nil {
			return err
		}
		var runIDs []string
		if err := tx.Model(&models.JobRun{}).Where("tenant_id = ?", tenantID).Pluck("id", &runIDs).Error; err != nil {
			return err
		}
		if len(runIDs) > 0 {
			if err := tx.Where("job_run_id IN ?", runIDs).Delete(&models.JobResult{}).Error; err != nil {
				return err
			}
		}
		for _, model := range []interface{}{&models.JobRun{}, &models.AIUsageLog{}, &models.NotificationLog{}, &models.ActivityLog{}, &models.Job{}, &models.AppSetting{}, &models.Channel{}, &models.UserTenant{}} {
			if err := tx.Where("tenant_id = ?", tenantID).Delete(model).Error; err != nil {
				return err
			}
		}
		return tx.Where("id = ?", tenantID).Delete(&models.Tenant{}).Error
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "tenant_delete_failed"})
		return
	}
	_ = os.RemoveAll(filepath.Join("/var/lib/cqa/files", tenantID))

	c.JSON(http.StatusOK, gin.H{"message": "deleted"})
}
