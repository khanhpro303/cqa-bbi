package handlers

import (
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/vietbui/chat-quality-agent/api/middleware"
	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/db/models"
	"github.com/vietbui/chat-quality-agent/pkg"
)

// dashboardDateRange interprets calendar dates in the tenant's timezone.
// The upper bound is exclusive so fractional seconds on the last day are included.
func dashboardDateRange(now time.Time, fromDate, toDate string, loc *time.Location) (time.Time, time.Time, error) {
	now = now.In(loc)
	from := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	to := from.AddDate(0, 0, 1)
	var err error
	if fromDate != "" {
		from, err = time.ParseInLocation("2006-01-02", fromDate, loc)
		if err != nil {
			return time.Time{}, time.Time{}, err
		}
	}
	if toDate != "" {
		to, err = time.ParseInLocation("2006-01-02", toDate, loc)
		if err != nil {
			return time.Time{}, time.Time{}, err
		}
		to = to.AddDate(0, 0, 1)
	}
	if !from.Before(to) {
		return time.Time{}, time.Time{}, fmt.Errorf("invalid date range")
	}
	return from.UTC(), to.UTC(), nil
}

func dashboardQCResults(database *gorm.DB, tenantID string, from, to time.Time) *gorm.DB {
	return database.Model(&models.JobResult{}).
		Where("tenant_id = ? AND result_type = ? AND created_at >= ? AND created_at < ?", tenantID, "qc_violation", from, to)
}

func GetDashboard(c *gin.Context) {
	tenantID := middleware.GetTenantID(c)

	loc := pkg.VNLocation
	var timezone models.AppSetting
	if db.DB.Where("tenant_id = ? AND setting_key = ?", tenantID, "timezone").First(&timezone).Error == nil {
		if configured, err := time.LoadLocation(timezone.ValuePlain); err == nil {
			loc = configured
		}
	}
	now := time.Now().In(loc)
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	from, to, err := dashboardDateRange(now, c.Query("from"), c.Query("to"), loc)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_date_range"})
		return
	}

	// Static stats (not time-dependent)
	var activeChannels, activeJobs int64
	db.DB.Model(&models.Channel{}).Where("tenant_id = ? AND is_active = true", tenantID).Count(&activeChannels)
	db.DB.Model(&models.Job{}).Where("tenant_id = ? AND is_active = true", tenantID).Count(&activeJobs)

	// Time-dependent stats
	var totalConversations, issuesToday int64
	db.DB.Model(&models.Conversation{}).Where("tenant_id = ? AND last_message_at >= ? AND last_message_at < ?", tenantID, from, to).Count(&totalConversations)
	if err := dashboardQCResults(db.DB, tenantID, from, to).Count(&issuesToday).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "dashboard_query_failed"})
		return
	}

	// Conversations by channel type
	type ChannelCount struct {
		ChannelType string `json:"channel_type"`
		Count       int64  `json:"count"`
	}
	var channelCounts []ChannelCount
	db.DB.Model(&models.Conversation{}).
		Joins("JOIN channels ON channels.id = conversations.channel_id").
		Where("conversations.tenant_id = ? AND conversations.last_message_at >= ? AND conversations.last_message_at < ?", tenantID, from, to).
		Select("channels.channel_type, COUNT(*) as count").
		Group("channels.channel_type").
		Scan(&channelCounts)

	// QC Alerts: only qc_violation (real quality issues)
	var qcAlerts []models.JobResult
	dashboardQCResults(db.DB, tenantID, from, to).
		Order("created_at DESC").Limit(5).Find(&qcAlerts)

	// Classification recent: only classification_tag
	type ClassificationItem struct {
		models.JobResult
		CustomerName string `json:"customer_name"`
	}
	var classRecent []ClassificationItem
	db.DB.Model(&models.JobResult{}).
		Select("job_results.*, conversations.customer_name").
		Joins("LEFT JOIN conversations ON conversations.id = job_results.conversation_id").
		Where("job_results.tenant_id = ? AND job_results.result_type = 'classification_tag' AND job_results.created_at >= ? AND job_results.created_at < ?", tenantID, from, to).
		Order("job_results.created_at DESC").Limit(10).Find(&classRecent)

	// AI cost
	var costPeriod float64
	db.DB.Model(&models.AIUsageLog{}).Where("tenant_id = ? AND created_at >= ? AND created_at < ?", tenantID, from, to).
		Select("COALESCE(SUM(cost_usd), 0)").Scan(&costPeriod)

	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
	var costMonth float64
	db.DB.Model(&models.AIUsageLog{}).Where("tenant_id = ? AND created_at >= ?", tenantID, monthStart).
		Select("COALESCE(SUM(cost_usd), 0)").Scan(&costMonth)

	// Cost by day
	type DayCost struct {
		Date         string  `json:"date"`
		TotalCost    float64 `json:"total_cost"`
		InputTokens  int64   `json:"input_tokens"`
		OutputTokens int64   `json:"output_tokens"`
		CallCount    int64   `json:"call_count"`
	}
	var costByDay []DayCost
	thirtyDaysAgo := today.Add(-30 * 24 * time.Hour)
	db.DB.Model(&models.AIUsageLog{}).
		Where("tenant_id = ? AND created_at >= ?", tenantID, thirtyDaysAgo).
		Select("DATE(created_at) as date, SUM(cost_usd) as total_cost, SUM(input_tokens) as input_tokens, SUM(output_tokens) as output_tokens, COUNT(*) as call_count").
		Group("DATE(created_at)").
		Order("date DESC").
		Scan(&costByDay)

	// Messages by day (with chat count + reply count)
	type DayMessages struct {
		Date       string `json:"date"`
		Count      int64  `json:"count"`
		ChatCount  int64  `json:"chat_count"`  // distinct conversations with customer messages
		ReplyCount int64  `json:"reply_count"` // agent replies
	}
	var messagesByDay []DayMessages
	db.DB.Model(&models.Message{}).
		Where("tenant_id = ? AND sent_at >= ?", tenantID, thirtyDaysAgo).
		Select(`DATE(sent_at) as date,
			COUNT(*) as count,
			COUNT(DISTINCT CASE WHEN sender_type = 'customer' THEN conversation_id END) as chat_count,
			SUM(CASE WHEN sender_type = 'agent' THEN 1 ELSE 0 END) as reply_count`).
		Group("DATE(sent_at)").
		Order("date ASC").
		Scan(&messagesByDay)

	// Exchange rate from tenant settings
	exchangeRate := 26000.0
	var rateSetting models.AppSetting
	if db.DB.Where("tenant_id = ? AND setting_key = ?", tenantID, "exchange_rate_vnd").First(&rateSetting).Error == nil && rateSetting.ValuePlain != "" {
		if r, err := strconv.ParseFloat(rateSetting.ValuePlain, 64); err == nil && r > 0 {
			exchangeRate = r
		}
	}

	// Banner carousel vertical focal points (shared across all viewers). Stored as
	// a JSON array of percentages, e.g. "[50,30,70]". Empty when never customized.
	bannerOffsets := ""
	var bannerSetting models.AppSetting
	if db.DB.Where("tenant_id = ? AND setting_key = ?", tenantID, "banner_offsets").First(&bannerSetting).Error == nil {
		bannerOffsets = bannerSetting.ValuePlain
	}

	c.JSON(http.StatusOK, gin.H{
		"total_conversations":      totalConversations,
		"active_channels":          activeChannels,
		"active_jobs":              activeJobs,
		"issues_today":             issuesToday,
		"conversations_by_channel": channelCounts,
		"qc_alerts":                qcAlerts,
		"classification_recent":    classRecent,
		"cost_today":               costPeriod,
		"cost_this_month":          costMonth,
		"cost_by_day":              costByDay,
		"messages_by_day":          messagesByDay,
		"exchange_rate":            exchangeRate,
		"banner_offsets":           bannerOffsets,
	})
}

func GetOnboardingStatus(c *gin.Context) {
	tenantID := middleware.GetTenantID(c)

	// Step 1: Has channels?
	var channelCount int64
	db.DB.Model(&models.Channel{}).Where("tenant_id = ?", tenantID).Count(&channelCount)

	// Step 2: Has conversations (synced)?
	var convCount int64
	db.DB.Model(&models.Conversation{}).Where("tenant_id = ?", tenantID).Count(&convCount)

	// Step 3: AI configured?
	var aiSetting models.AppSetting
	aiConfigured := db.DB.Where("tenant_id = ? AND setting_key = ? AND value_plain != ''", tenantID, "ai_provider").First(&aiSetting).Error == nil

	// Step 4: Has jobs?
	var jobCount int64
	db.DB.Model(&models.Job{}).Where("tenant_id = ?", tenantID).Count(&jobCount)

	// Step 5: Has job runs?
	var runCount int64
	db.DB.Model(&models.JobRun{}).Where("tenant_id = ?", tenantID).Count(&runCount)

	// Check if dismissed
	var dismissSetting models.AppSetting
	dismissed := false
	if db.DB.Where("tenant_id = ? AND setting_key = ?", tenantID, "onboarding_dismissed").First(&dismissSetting).Error == nil {
		dismissed = dismissSetting.ValuePlain == "true"
	}

	c.JSON(http.StatusOK, gin.H{
		"dismissed": dismissed,
		"steps": []gin.H{
			{"key": "channel", "title": "Kết nối kênh chat", "done": channelCount > 0, "link": "channels"},
			{"key": "sync", "title": "Đồng bộ tin nhắn", "done": convCount > 0, "link": "messages"},
			{"key": "ai", "title": "Cấu hình AI Provider", "done": aiConfigured, "link": "settings"},
			{"key": "job", "title": "Tạo công việc phân tích", "done": jobCount > 0, "link": "jobs/create"},
			{"key": "run", "title": "Chạy thử phân tích", "done": runCount > 0, "link": "jobs"},
		},
	})
}

func ListNotificationLogs(c *gin.Context) {
	tenantID := middleware.GetTenantID(c)

	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	perPage, _ := strconv.Atoi(c.DefaultQuery("per_page", "20"))
	if page < 1 {
		page = 1
	}
	if perPage > 100 {
		perPage = 100
	}

	// Optional origin filter: "job" (AI task outputs) or "campaign" (CRM campaign
	// failure alerts). Any other value is ignored so the list stays unfiltered.
	// scope is applied to a fresh builder for both the count and page queries so
	// GORM never carries statement state between them.
	source := c.Query("source")
	scope := func(q *gorm.DB) *gorm.DB {
		q = q.Where("tenant_id = ?", tenantID)
		if source == "job" || source == "campaign" {
			q = q.Where("source = ?", source)
		}
		return q
	}

	var total int64
	scope(db.DB.Model(&models.NotificationLog{})).Count(&total)

	var logs []models.NotificationLog
	scope(db.DB.Model(&models.NotificationLog{})).Order("sent_at DESC").
		Offset((page - 1) * perPage).Limit(perPage).Find(&logs)

	c.JSON(http.StatusOK, gin.H{
		"data":     logs,
		"total":    total,
		"page":     page,
		"per_page": perPage,
	})
}

// DeleteNotificationLogs bulk-deletes notification logs for the tenant within an
// optional date range. Query params (YYYY-MM-DD): "from" (created_at >= from
// 00:00:00) and "to" (created_at <= to 23:59:59). With neither param, all tenant
// logs are deleted. Owner/admin only (gated at the router). Returns rows deleted.
func DeleteNotificationLogs(c *gin.Context) {
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

	result := query.Delete(&models.NotificationLog{})
	if result.Error != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed_to_delete_logs"})
		return
	}

	detail := fmt.Sprintf("Cleared %d notification logs (from=%q, to=%q)", result.RowsAffected, from, to)
	db.LogActivity(tenantID, middleware.GetUserID(c), middleware.GetUserEmail(c), "notification_logs.clear", "notification_logs", "", detail, "", c.ClientIP())

	c.JSON(http.StatusOK, gin.H{"deleted": result.RowsAffected})
}
