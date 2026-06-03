package handlers

import (
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/vietbui/chat-quality-agent/api/middleware"
	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/db/models"
	"gorm.io/gorm"
)

// aiCostRow is one bar/point in an AI-cost breakdown.
type aiCostRow struct {
	Label        string  `json:"label"`
	TotalCost    float64 `json:"total_cost"`
	InputTokens  int64   `json:"input_tokens"`
	OutputTokens int64   `json:"output_tokens"`
	CallCount    int64   `json:"call_count"`
}

// GetAICostAnalytics returns AI spend grouped by day, OA channel, or employee,
// with optional source (job|chatbot|all) and segment (public|private|all)
// filters. It powers the multi-chart carousel on the dashboard home page.
//
// GET /tenants/:tenantId/analytics/ai-cost
//
//	?group_by=day|channel|employee  (default day)
//	&source=job|chatbot|all         (default all)
//	&segment=public|private|all     (default all)
//	&from=YYYY-MM-DD&to=YYYY-MM-DD
func GetAICostAnalytics(c *gin.Context) {
	tenantID := middleware.GetTenantID(c)

	groupBy := c.DefaultQuery("group_by", "day")
	switch groupBy {
	case "day", "channel", "employee":
	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid group_by (day|channel|employee)"})
		return
	}

	source := c.DefaultQuery("source", "all")
	switch source {
	case "all", "job", "chatbot":
	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid source (job|chatbot|all)"})
		return
	}

	segment := c.DefaultQuery("segment", "all")
	switch segment {
	case "all", "public", "private":
	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid segment (public|private|all)"})
		return
	}

	// Date window (default: last 30 days). from/to are inclusive day boundaries.
	now := time.Now()
	from := now.Add(-30 * 24 * time.Hour).Truncate(24 * time.Hour)
	to := now
	if f := c.Query("from"); f != "" {
		if t, err := time.Parse("2006-01-02", f); err == nil {
			from = t
		}
	}
	if t := c.Query("to"); t != "" {
		if parsed, err := time.Parse("2006-01-02", t); err == nil {
			to = parsed.Add(24*time.Hour - time.Second)
		}
	}

	// base applies the filters shared by every grouping. Always tenant-scoped.
	base := func() *gorm.DB {
		q := db.DB.Model(&models.AIUsageLog{}).
			Where("ai_usage_logs.tenant_id = ? AND ai_usage_logs.created_at BETWEEN ? AND ?", tenantID, from, to)
		if source != "all" {
			q = q.Where("ai_usage_logs.source = ?", source)
		}
		if segment != "all" {
			q = q.Where("ai_usage_logs.agent_type = ?", segment)
		}
		return q
	}

	const aggCols = "SUM(ai_usage_logs.cost_usd) as total_cost, " +
		"SUM(ai_usage_logs.input_tokens) as input_tokens, " +
		"SUM(ai_usage_logs.output_tokens) as output_tokens, " +
		"COUNT(*) as call_count"

	var rows []aiCostRow
	switch groupBy {
	case "day":
		base().
			Select("DATE(ai_usage_logs.created_at) as label, " + aggCols).
			Group("DATE(ai_usage_logs.created_at)").
			Order("label ASC").
			Scan(&rows)
	case "channel":
		base().
			Joins("LEFT JOIN channels ON channels.id = ai_usage_logs.channel_id").
			Select("COALESCE(NULLIF(channels.name, ''), 'Hệ thống / Job') as label, " + aggCols).
			Group("label").
			Order("total_cost DESC").
			Scan(&rows)
	case "employee":
		// Employee breakdown only applies to chatbot spend (jobs have no sender).
		base().
			Where("ai_usage_logs.source = ?", "chatbot").
			Select("COALESCE(NULLIF(ai_usage_logs.sender_name, ''), NULLIF(ai_usage_logs.sender_external_id, ''), 'Không xác định') as label, " + aggCols).
			Group("label").
			Order("total_cost DESC").
			Limit(20).
			Scan(&rows)
	}

	if rows == nil {
		rows = []aiCostRow{}
	}

	// Exchange rate for USD -> VND display, mirroring GetDashboard.
	exchangeRate := 26000.0
	var rateSetting models.AppSetting
	if db.DB.Where("tenant_id = ? AND setting_key = ?", tenantID, "exchange_rate_vnd").First(&rateSetting).Error == nil && rateSetting.ValuePlain != "" {
		if r, err := strconv.ParseFloat(rateSetting.ValuePlain, 64); err == nil && r > 0 {
			exchangeRate = r
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"group_by":      groupBy,
		"source":        source,
		"segment":       segment,
		"rows":          rows,
		"exchange_rate": exchangeRate,
	})
}
