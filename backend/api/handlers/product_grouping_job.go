package handlers

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/vietbui/chat-quality-agent/ai"
	"github.com/vietbui/chat-quality-agent/api/middleware"
	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/db/models"
	"github.com/vietbui/chat-quality-agent/pkg"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const productGroupingJobType = "messenger_product_groups"
const maxProductGroupingPromptBytes = 60 << 10

func isProductGroupingAdmin(c *gin.Context) bool {
	role := middleware.GetTenantRole(c)
	return role == "owner" || role == "admin"
}

func productGroupingPrompt(job models.Job) string {
	if strings.TrimSpace(job.RulesContent) != "" && job.RulesContent != ai.LegacyMessengerProductGroupingPrompt && job.RulesContent != ai.LegacyMessengerProductGroupingIDsPrompt {
		return job.RulesContent
	}
	return ai.MessengerProductGroupingPrompt
}

func findProductGroupingJob(ctx context.Context, database *gorm.DB, tenantID string) (models.Job, error) {
	var job models.Job
	err := database.WithContext(ctx).Where("tenant_id = ? AND job_type = ? AND is_active = ?", tenantID, productGroupingJobType, true).First(&job).Error
	return job, err
}

// This fixed task has no wizard, schedule, channels, or notification configuration.
func CreateProductGroupingJob(c *gin.Context) {
	if !isProductGroupingAdmin(c) {
		c.JSON(http.StatusForbidden, gin.H{"error": "insufficient_role"})
		return
	}
	tenantID := middleware.GetTenantID(c)
	var job models.Job
	created := false
	err := db.DB.WithContext(c.Request.Context()).Transaction(func(tx *gorm.DB) error {
		// Serialize creation across servers without introducing another table/index.
		var tenant models.Tenant
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", tenantID).First(&tenant).Error; err != nil {
			return err
		}
		err := tx.Where("tenant_id = ? AND job_type = ?", tenantID, productGroupingJobType).First(&job).Error
		if err == nil {
			return nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		now := time.Now()
		job = models.Job{ID: pkg.NewUUID(), TenantID: tenantID, Name: "Tổng hợp sản phẩm CSKH", Description: "AI gom tên sản phẩm cho biểu đồ treemap trong Chất lượng CSKH.", JobType: productGroupingJobType,
			InputChannelIDs: "[]", RulesContent: ai.MessengerProductGroupingPrompt, RulesConfig: "{}", Outputs: "[]", OutputSchedule: "none", ScheduleType: "manual", IsActive: true, CreatedAt: now, UpdatedAt: now}
		created = true
		return tx.Create(&job).Error
	})
	if err != nil {
		serviceQualityError(c, err)
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
		db.LogActivity(tenantID, middleware.GetUserID(c), middleware.GetUserEmail(c), "job.create", "job", job.ID, "Tạo tác vụ Tổng hợp sản phẩm CSKH", "", c.ClientIP())
	}
	c.JSON(status, productGroupingJobResponse(job))
}

func productGroupingJobResponse(job models.Job) interface{} {
	return struct {
		models.Job
		SystemPrompt string `json:"system_prompt"`
	}{Job: job, SystemPrompt: productGroupingPrompt(job)}
}

func SaveProductGroupingPrompt(c *gin.Context) {
	if !isProductGroupingAdmin(c) {
		c.JSON(http.StatusForbidden, gin.H{"error": "insufficient_role"})
		return
	}
	var req struct {
		SystemPrompt string `json:"system_prompt"`
	}
	if c.ShouldBindJSON(&req) != nil || strings.TrimSpace(req.SystemPrompt) == "" || len(req.SystemPrompt) > maxProductGroupingPromptBytes {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Prompt không được để trống và tối đa 60 KiB."})
		return
	}
	tenantID := middleware.GetTenantID(c)
	var job models.Job
	result := db.DB.WithContext(c.Request.Context()).Where("id = ? AND tenant_id = ? AND job_type = ?", c.Param("jobId"), tenantID, productGroupingJobType).First(&job)
	if result.Error != nil {
		serviceQualityError(c, result.Error)
		return
	}
	result = db.DB.WithContext(c.Request.Context()).Model(&models.Job{}).Where("id = ? AND tenant_id = ? AND job_type = ?", job.ID, tenantID, productGroupingJobType).Updates(map[string]interface{}{"rules_content": req.SystemPrompt, "updated_at": time.Now()})
	if result.Error != nil {
		serviceQualityError(c, result.Error)
		return
	}
	if result.RowsAffected == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "job_not_found"})
		return
	}
	job.RulesContent = req.SystemPrompt
	db.LogActivity(tenantID, middleware.GetUserID(c), middleware.GetUserEmail(c), "job.update_prompt", "job", job.ID, "Cập nhật prompt tổng hợp sản phẩm CSKH", "", c.ClientIP())
	c.JSON(http.StatusOK, productGroupingJobResponse(job))
}

// Locking the task also serializes deletion against short run/usage writes.
func withProductGroupingJob(ctx context.Context, database *gorm.DB, job models.Job, write func(*gorm.DB) error) error {
	return database.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var current models.Job
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND tenant_id = ? AND job_type = ? AND is_active = ?", job.ID, job.TenantID, productGroupingJobType, true).First(&current).Error; err != nil {
			return err
		}
		return write(tx)
	})
}

func deleteProductGroupingJob(ctx context.Context, database *gorm.DB, job models.Job) error {
	return withProductGroupingJob(ctx, database, job, func(tx *gorm.DB) error {
		for _, model := range []interface{}{&models.JobRun{}, &models.AIUsageLog{}, &models.NotificationLog{}} {
			if err := tx.Where("job_id = ? AND tenant_id = ?", job.ID, job.TenantID).Delete(model).Error; err != nil {
				return err
			}
		}
		return tx.Where("id = ? AND tenant_id = ?", job.ID, job.TenantID).Delete(&models.Job{}).Error
	})
}
