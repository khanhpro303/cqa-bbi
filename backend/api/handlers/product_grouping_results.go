package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/vietbui/chat-quality-agent/ai"
	"github.com/vietbui/chat-quality-agent/api/middleware"
	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/db/models"
	"github.com/vietbui/chat-quality-agent/servicequality"
	"gorm.io/gorm"
)

type productGroupingResultsReport struct {
	From        time.Time             `json:"from"`
	To          time.Time             `json:"to"`
	GeneratedAt time.Time             `json:"generated_at"`
	ChannelID   string                `json:"channel_id,omitempty"`
	Pages       []servicequality.Page `json:"pages"`
}

type productGroupingResultSource struct {
	ConversationID  string                          `json:"conversation_id"`
	CustomerName    string                          `json:"customer_name"`
	ChannelID       string                          `json:"channel_id"`
	ChannelName     string                          `json:"channel_name"`
	InsightJobID    string                          `json:"insight_job_id,omitempty"`
	MatchedProducts []ai.ProductInsight             `json:"matched_products"`
	InsightAt       *time.Time                      `json:"insight_at,omitempty"`
	LeadQuality     ai.LeadQualityInsight           `json:"lead_quality"`
	Summary         string                          `json:"summary,omitempty"`
	QualityAnalysis *servicequality.QualityAnalysis `json:"quality_analysis,omitempty"`
	QualityStale    bool                            `json:"quality_analysis_stale"`
}

type productGroupingResultGroup struct {
	Name    string                        `json:"name"`
	Members []string                      `json:"members"`
	Count   int                           `json:"count"`
	Sources []productGroupingResultSource `json:"sources"`
}

type productGroupingResultsResponse struct {
	Status               string                       `json:"status"`
	GroupingReady        bool                         `json:"grouping_ready"`
	CacheUpdatedAt       *time.Time                   `json:"cache_updated_at,omitempty"`
	Report               productGroupingResultsReport `json:"report"`
	ProductNames         []string                     `json:"product_names"`
	ExcludedProductNames []string                     `json:"excluded_product_names"`
	Groups               []productGroupingResultGroup `json:"groups"`
}

type productGroupingInsight struct {
	Summary     string                `json:"summary"`
	Products    []ai.ProductInsight   `json:"products"`
	LeadQuality ai.LeadQualityInsight `json:"lead_quality"`
}

// GetProductGroupingResults returns the exact cached grouping for the current
// job prompt and current service-quality report labels. It never resolves a
// cache miss, starts a job run, or calls an AI provider.
func GetProductGroupingResults(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID := middleware.GetTenantID(c)
	jobID := c.Param("jobId")

	var job models.Job
	if err := db.DB.WithContext(ctx).Where("id = ? AND tenant_id = ? AND job_type = ?", jobID, tenantID, productGroupingJobType).First(&job).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "job_not_found"})
			return
		}
		serviceQualityError(c, err)
		return
	}

	policy, err := servicequality.LoadPolicy(db.DB.WithContext(ctx), tenantID)
	if err != nil {
		serviceQualityError(c, err)
		return
	}
	now := time.Now()
	from, to, err := servicequality.DateWindow(c.Query("from"), c.Query("to"), now, policy)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	report, err := servicequality.Build(ctx, db.DB, tenantID, c.Query("channel_id"), now, from, to, policy)
	if errors.Is(err, servicequality.ErrReportTooLarge) && report != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": err.Error(), "pages": report.Pages})
		return
	}
	if err != nil {
		serviceQualityError(c, err)
		return
	}

	productNames := productNamesForGrouping(report)
	response := pendingProductGroupingResultsResponse(report, c.Query("channel_id"), productNames)
	if len(productNames) == 0 {
		response.Status = "ready"
		response.GroupingReady = true
		c.JSON(http.StatusOK, response)
		return
	}

	inputHash := productGroupingInputHash(productNames, productGroupingPrompt(job)+job.ID)
	encoded, updatedAt, found, err := readProductGroupingResultCache(ctx, db.DB, tenantID, inputHash)
	if err != nil {
		serviceQualityError(c, err)
		return
	}
	if !found {
		c.JSON(http.StatusOK, response)
		return
	}
	if err := applyCachedProductGroupingResults(&response, report, encoded, updatedAt); err != nil {
		log.Printf("[service-quality] cached product grouping is invalid tenant=%s job=%s hash=%s: %v", tenantID, job.ID, inputHash, err)
		c.JSON(http.StatusOK, response)
		return
	}
	c.JSON(http.StatusOK, response)
}

func pendingProductGroupingResultsResponse(report *servicequality.Report, channelID string, productNames []string) productGroupingResultsResponse {
	return productGroupingResultsResponse{
		Status: "pending", GroupingReady: false,
		Report:       productGroupingResultsReport{From: report.From, To: report.To, GeneratedAt: report.GeneratedAt, ChannelID: channelID, Pages: report.Pages},
		ProductNames: productNames, ExcludedProductNames: []string{}, Groups: []productGroupingResultGroup{},
	}
}

func readProductGroupingResultCache(ctx context.Context, database *gorm.DB, tenantID, inputHash string) (string, *time.Time, bool, error) {
	var setting models.AppSetting
	err := database.WithContext(ctx).
		Select("value_plain, updated_at").
		Where("tenant_id = ? AND setting_key = ?", tenantID, productGroupingCacheSettingKey(inputHash)).
		First(&setting).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return "", nil, false, nil
	}
	if err != nil {
		return "", nil, false, err
	}
	updatedAt := setting.UpdatedAt
	return setting.ValuePlain, &updatedAt, true, nil
}

func applyCachedProductGroupingResults(response *productGroupingResultsResponse, report *servicequality.Report, encoded string, updatedAt *time.Time) error {
	groups, err := decodeProductGroupingCache(encoded)
	if err != nil {
		return err
	}
	// Same rule as the job's own cache read: accept labels it isolated verbatim.
	groups, err = validateStoredProductGroups(response.ProductNames, groups)
	if err != nil {
		return err
	}
	response.Status = "ready"
	response.GroupingReady = true
	response.CacheUpdatedAt = updatedAt
	response.Groups, response.ExcludedProductNames = buildProductGroupingResultGroups(report, groups)
	return nil
}

func buildProductGroupingResultGroups(report *servicequality.Report, groups []serviceQualityProductGroup) ([]productGroupingResultGroup, []string) {
	if report == nil {
		return []productGroupingResultGroup{}, []string{}
	}
	memberToGroup := make(map[string]int)
	result := make([]productGroupingResultGroup, 0, len(groups))
	excluded := make([]string, 0)
	for _, group := range groups {
		if strings.TrimSpace(group.Name) == "" {
			excluded = append(excluded, group.Members...)
		}
		index := len(result)
		members := append([]string(nil), group.Members...)
		result = append(result, productGroupingResultGroup{Name: group.Name, Members: members, Sources: []productGroupingResultSource{}})
		for _, member := range members {
			memberToGroup[member] = index
		}
	}

	for _, row := range report.Rows {
		if row.InsightStale || row.InsightAt == nil || row.InsightAt.Before(report.From) || !row.InsightAt.Before(report.To) {
			continue
		}
		var insight productGroupingInsight
		if json.Unmarshal(row.Insight, &insight) != nil {
			continue
		}
		insight.LeadQuality.Level = strings.ToLower(strings.TrimSpace(insight.LeadQuality.Level))
		switch insight.LeadQuality.Level {
		case "high", "medium", "low":
		default:
			continue
		}
		matched := make(map[int][]ai.ProductInsight)
		for _, product := range insight.Products {
			name := strings.TrimSpace(product.Name)
			if index, ok := memberToGroup[name]; ok {
				product.Name = name
				matched[index] = append(matched[index], product)
			}
		}
		for index, products := range matched {
			result[index].Sources = append(result[index].Sources, productGroupingResultSource{
				ConversationID: row.ConversationID, CustomerName: row.CustomerName,
				ChannelID: row.ChannelID, ChannelName: row.ChannelName, InsightJobID: row.InsightJobID,
				MatchedProducts: products, InsightAt: row.InsightAt,
				LeadQuality: insight.LeadQuality, Summary: insight.Summary,
				QualityAnalysis: row.QualityAnalysis, QualityStale: row.QualityAnalysisStale,
			})
		}
	}
	for i := range result {
		sort.SliceStable(result[i].Sources, func(a, b int) bool {
			return result[i].Sources[a].ConversationID < result[i].Sources[b].ConversationID
		})
		result[i].Count = len(result[i].Sources)
	}
	sort.Strings(excluded)
	return result, excluded
}
