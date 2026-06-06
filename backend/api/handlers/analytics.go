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
		// Group by the stable sender_external_id (not the display name) so that
		// turns whose name wasn't cached at log time still collapse into one bar,
		// then resolve a friendly name afterwards from zalo_customers / whitelist.
		type empRow struct {
			SenderExternalID string  `gorm:"column:sender_external_id"`
			SenderName       string  `gorm:"column:sender_name"`
			TotalCost        float64 `gorm:"column:total_cost"`
			InputTokens      int64   `gorm:"column:input_tokens"`
			OutputTokens     int64   `gorm:"column:output_tokens"`
			CallCount        int64   `gorm:"column:call_count"`
		}
		var empRows []empRow
		base().
			Where("ai_usage_logs.source = ?", "chatbot").
			Select("ai_usage_logs.sender_external_id as sender_external_id, " +
				"MAX(NULLIF(ai_usage_logs.sender_name, '')) as sender_name, " + aggCols).
			Group("ai_usage_logs.sender_external_id").
			Order("total_cost DESC").
			Limit(20).
			Scan(&empRows)

		ids := make([]string, 0, len(empRows))
		for _, er := range empRows {
			if er.SenderName == "" && er.SenderExternalID != "" {
				ids = append(ids, er.SenderExternalID)
			}
		}
		nameByID := resolveEmployeeNames(tenantID, ids)
		rows = make([]aiCostRow, 0, len(empRows))
		for _, er := range empRows {
			label := er.SenderName
			if label == "" {
				label = nameByID[er.SenderExternalID]
			}
			if label == "" {
				if er.SenderExternalID != "" {
					label = er.SenderExternalID
				} else {
					label = "Không xác định"
				}
			}
			rows = append(rows, aiCostRow{
				Label:        label,
				TotalCost:    er.TotalCost,
				InputTokens:  er.InputTokens,
				OutputTokens: er.OutputTokens,
				CallCount:    er.CallCount,
			})
		}
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

// resolveEmployeeNames maps Zalo user IDs to display names for the employee
// cost breakdown, covering both private NV (zalo_whitelist) and public GMF
// (zalo_customers) senders whose name wasn't cached on the usage log. Lookups
// are tenant-scoped and done in two small batched queries to avoid the row
// fan-out (and double-counted SUMs) that a SQL JOIN on per-channel whitelist
// rows would cause.
func resolveEmployeeNames(tenantID string, ids []string) map[string]string {
	out := make(map[string]string)
	if len(ids) == 0 {
		return out
	}

	type idName struct {
		ZaloUserID string `gorm:"column:zalo_user_id"`
		Name       string `gorm:"column:name"`
	}

	// Customers first; whitelist (employee) names override when both exist.
	var customers []idName
	db.DB.Model(&models.ZaloCustomer{}).
		Select("zalo_user_id, name").
		Where("tenant_id = ? AND zalo_user_id IN ? AND name <> ''", tenantID, ids).
		Scan(&customers)
	for _, c := range customers {
		out[c.ZaloUserID] = c.Name
	}

	var employees []idName
	db.DB.Model(&models.ZaloWhitelist{}).
		Select("zalo_user_id, name").
		Where("tenant_id = ? AND zalo_user_id IN ? AND name <> ''", tenantID, ids).
		Scan(&employees)
	for _, e := range employees {
		out[e.ZaloUserID] = e.Name
	}

	// Last resort: recover the cached display name from past messages for any id
	// still unresolved. This survives deletion of the zalo_customers/whitelist row
	// (messages are kept), so a removed customer still shows their name, not a raw
	// Zalo ID. Only queried for the leftover ids, so it stays off the hot path.
	var unresolved []string
	for _, id := range ids {
		if _, ok := out[id]; !ok {
			unresolved = append(unresolved, id)
		}
	}
	if len(unresolved) > 0 {
		type msgName struct {
			SenderExternalID string `gorm:"column:sender_external_id"`
			Name             string `gorm:"column:name"`
		}
		var msgs []msgName
		db.DB.Model(&models.Message{}).
			Select("sender_external_id, MAX(NULLIF(sender_name, '')) as name").
			Where("tenant_id = ? AND sender_external_id IN ?", tenantID, unresolved).
			Group("sender_external_id").
			Scan(&msgs)
		for _, m := range msgs {
			if m.Name != "" {
				out[m.SenderExternalID] = m.Name
			}
		}
	}

	return out
}
