package handlers

import (
	"fmt"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/vietbui/chat-quality-agent/api/middleware"
	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/db/models"
)

func ListActivityLogs(c *gin.Context) {
	tenantID := middleware.GetTenantID(c)

	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	perPage, _ := strconv.Atoi(c.DefaultQuery("per_page", "50"))
	action := c.Query("action")

	if page < 1 {
		page = 1
	}
	if perPage > 100 {
		perPage = 100
	}

	query := db.DB.Where("tenant_id = ?", tenantID)
	if action != "" {
		query = query.Where("action LIKE ?", action+"%")
	}

	var total int64
	query.Model(&models.ActivityLog{}).Count(&total)

	var logs []models.ActivityLog
	query.Order("created_at DESC").
		Offset((page - 1) * perPage).
		Limit(perPage).
		Find(&logs)

	c.JSON(http.StatusOK, gin.H{
		"data":     logs,
		"total":    total,
		"page":     page,
		"per_page": perPage,
	})
}

// DeleteActivityLogs bulk-deletes activity logs for the tenant within an optional
// date range. Query params (YYYY-MM-DD): "from" (created_at >= from 00:00:00) and
// "to" (created_at <= to 23:59:59). With neither param, all tenant logs are deleted.
// Owner/admin only (gated at the router). Returns the number of rows deleted.
func DeleteActivityLogs(c *gin.Context) {
	tenantID := middleware.GetTenantID(c)
	from := c.Query("from")
	to := c.Query("to")

	query := db.DB.Where("tenant_id = ?", tenantID)
	if from != "" {
		query = query.Where("created_at >= ?", from+" 00:00:00")
	}
	if to != "" {
		query = query.Where("created_at <= ?", to+" 23:59:59")
	}

	result := query.Delete(&models.ActivityLog{})
	if result.Error != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed_to_delete_logs"})
		return
	}

	detail := fmt.Sprintf("Cleared %d activity logs (from=%q, to=%q)", result.RowsAffected, from, to)
	db.LogActivity(tenantID, middleware.GetUserID(c), middleware.GetUserEmail(c), "activity_logs.clear", "activity_logs", "", detail, "", c.ClientIP())

	c.JSON(http.StatusOK, gin.H{"deleted": result.RowsAffected})
}
