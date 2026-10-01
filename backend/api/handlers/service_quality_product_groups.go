package handlers

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
	"github.com/vietbui/chat-quality-agent/pkg"
	"github.com/vietbui/chat-quality-agent/servicequality"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	messengerProductGroupsCacheKeyPrefix = "messenger_service_product_groups_cache:"
	maxProductGroupingCacheEntries       = 20
	maxProductGroupingCacheEncodedBytes  = 60 << 10
	maxProductGroupingCacheDecodedBytes  = 4 << 20
	maxProductGroupingLabels             = 500
	maxProductGroupingInputBytes         = 64 << 10
	productGroupingAITimeout             = 60 * time.Second
)

var (
	errInvalidProductGrouping          = errors.New("AI returned an invalid product grouping")
	errProductGroupingInputTooLarge    = errors.New("product grouping input exceeds the configured limit")
	errProductGroupingCacheTooLarge    = errors.New("encoded product grouping cache exceeds TEXT safety limit")
	errProductGroupingSnapshotConflict = errors.New("product grouping snapshot no longer matches report")
)

type serviceQualityProductGroupsRequest struct {
	From         string   `json:"from"`
	To           string   `json:"to"`
	ChannelID    string   `json:"channel_id"`
	ProductNames []string `json:"product_names,omitempty"`
}

type serviceQualityProductGroup struct {
	Name    string   `json:"name"`
	Members []string `json:"members"`
}

type serviceQualityProductGroupsResponse struct {
	Enabled bool                         `json:"enabled"`
	Groups  []serviceQualityProductGroup `json:"groups"`
}

type productGroupingDependencies struct {
	prompt     string
	cacheScope string
	startRun   func(context.Context) error
	finishRun  func(error)
	loadCache  func(context.Context, string, string) ([]serviceQualityProductGroup, bool, error)
	saveCache  func(context.Context, string, string, []serviceQualityProductGroup) error
	aiClient   func(context.Context, string) (ai.AIProvider, error)
	logUsage   func(context.Context, string, ai.AIResponse)
}

func GroupServiceQualityProducts(c *gin.Context) {
	var req serviceQualityProductGroupsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "bộ lọc báo cáo không hợp lệ"})
		return
	}

	ctx := c.Request.Context()
	tenantID := middleware.GetTenantID(c)
	job, err := findProductGroupingJob(ctx, db.DB, tenantID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		c.JSON(http.StatusOK, serviceQualityProductGroupsResponse{Groups: []serviceQualityProductGroup{}})
		return
	}
	if err != nil {
		serviceQualityError(c, err)
		return
	}
	policy, err := servicequality.LoadPolicy(db.DB.WithContext(ctx), tenantID)
	if err != nil {
		serviceQualityError(c, err)
		return
	}
	now := time.Now()
	from, to, err := servicequality.DateWindow(req.From, req.To, now, policy)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	report, err := servicequality.Build(ctx, db.DB, tenantID, req.ChannelID, now, from, to, policy)
	if errors.Is(err, servicequality.ErrReportTooLarge) && report != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": err.Error(), "pages": report.Pages})
		return
	}
	if err != nil {
		serviceQualityError(c, err)
		return
	}

	names := productNamesForGrouping(report)
	if err := validateProductGroupingSnapshot(req.ProductNames, names); err != nil {
		c.JSON(http.StatusConflict, gin.H{"error": "dữ liệu sản phẩm đã thay đổi; hãy tải lại báo cáo"})
		return
	}
	if len(names) == 0 {
		c.JSON(http.StatusOK, serviceQualityProductGroupsResponse{Enabled: true, Groups: []serviceQualityProductGroup{}})
		return
	}
	groups, err := resolveProductGroups(ctx, tenantID, names, defaultProductGroupingDependencies(db.DB, job))
	current, lookupErr := findProductGroupingJob(ctx, db.DB, tenantID)
	if errors.Is(lookupErr, gorm.ErrRecordNotFound) || (lookupErr == nil && current.ID != job.ID) {
		c.JSON(http.StatusOK, serviceQualityProductGroupsResponse{Groups: []serviceQualityProductGroup{}})
		return
	}
	if lookupErr != nil {
		serviceQualityError(c, lookupErr)
		return
	}
	if productGroupingPrompt(current) != productGroupingPrompt(job) {
		c.JSON(http.StatusConflict, gin.H{"error": "Prompt đã thay đổi; hãy tải lại báo cáo."})
		return
	}
	if errors.Is(err, errProductGroupingInputTooLarge) {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": "quá nhiều tên sản phẩm để gom nhóm; hãy thu hẹp khoảng ngày hoặc chọn một Fanpage"})
		return
	}
	if errors.Is(err, errInvalidProductGrouping) {
		c.JSON(http.StatusBadGateway, gin.H{"error": "AI trả về nhóm sản phẩm không hợp lệ; vui lòng thử lại"})
		return
	}
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "không thể gom nhóm sản phẩm; vui lòng thử lại"})
		return
	}
	c.JSON(http.StatusOK, serviceQualityProductGroupsResponse{Enabled: true, Groups: groups})
}

func productNamesForGrouping(report *servicequality.Report) []string {
	if report == nil {
		return []string{}
	}
	seen := make(map[string]bool)
	for _, row := range report.Rows {
		if row.InsightStale || row.InsightAt == nil || row.InsightAt.Before(report.From) || !row.InsightAt.Before(report.To) || len(row.Insight) == 0 {
			continue
		}
		var insight struct {
			Products []struct {
				Name string `json:"name"`
			} `json:"products"`
			LeadQuality struct {
				Level string `json:"level"`
			} `json:"lead_quality"`
		}
		if json.Unmarshal(row.Insight, &insight) != nil {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(insight.LeadQuality.Level)) {
		case "high", "medium", "low":
		default:
			continue
		}
		for _, product := range insight.Products {
			name := strings.TrimSpace(product.Name)
			if name != "" {
				seen[name] = true
			}
		}
	}
	names := make([]string, 0, len(seen))
	for name := range seen {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func sameProductNameSet(snapshot, current []string) bool {
	if len(snapshot) != len(current) {
		return false
	}
	seen := make(map[string]bool, len(snapshot))
	for _, name := range snapshot {
		if seen[name] {
			return false
		}
		seen[name] = true
	}
	for _, name := range current {
		if !seen[name] {
			return false
		}
	}
	return true
}

func validateProductGroupingSnapshot(snapshot, current []string) error {
	if snapshot == nil || sameProductNameSet(snapshot, current) {
		return nil
	}
	return errProductGroupingSnapshotConflict
}

func resolveProductGroups(ctx context.Context, tenantID string, names []string, deps productGroupingDependencies) (result []serviceQualityProductGroup, runErr error) {
	input, err := productGroupingInput(names)
	if err != nil {
		return nil, err
	}
	prompt := deps.prompt
	if prompt == "" {
		prompt = ai.MessengerProductGroupingPrompt
	}
	inputHash := productGroupingInputHash(names, prompt+deps.cacheScope)
	if cached, ok, err := deps.loadCache(ctx, tenantID, inputHash); err != nil {
		log.Printf("[service-quality] product grouping cache read failed tenant=%s hash=%s: %v", tenantID, inputHash, err)
	} else if ok {
		groups, err := validateProductGroups(names, cached)
		if err == nil {
			return groups, nil
		}
	}

	if deps.startRun != nil {
		if err := deps.startRun(ctx); err != nil {
			return nil, err
		}
	}
	if deps.finishRun != nil {
		defer func() { deps.finishRun(runErr) }()
	}
	provider, err := deps.aiClient(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	aiCtx, cancel := context.WithTimeout(ctx, productGroupingAITimeout)
	defer cancel()
	response, err := provider.AnalyzeChat(aiCtx, prompt, string(input))
	if err != nil {
		return nil, err
	}
	deps.logUsage(ctx, tenantID, response)
	groups, err := parseAndValidateProductGroups(response.Content, names)
	if err != nil {
		return nil, err
	}
	if err := deps.saveCache(ctx, tenantID, inputHash, groups); err != nil {
		if errors.Is(err, errProductGroupingCacheTooLarge) {
			log.Printf("[service-quality] product grouping cache skipped tenant=%s hash=%s: %v", tenantID, inputHash, err)
		} else {
			log.Printf("[service-quality] product grouping cache save/prune failed tenant=%s hash=%s: %v", tenantID, inputHash, err)
		}
	}
	return groups, nil
}

func productGroupingInput(names []string) ([]byte, error) {
	if len(names) > maxProductGroupingLabels {
		return nil, errProductGroupingInputTooLarge
	}
	input, err := json.Marshal(map[string][]string{"product_names": names})
	if err != nil {
		return nil, err
	}
	if len(input) > maxProductGroupingInputBytes {
		return nil, errProductGroupingInputTooLarge
	}
	return input, nil
}

func parseAndValidateProductGroups(content string, names []string) ([]serviceQualityProductGroup, error) {
	content = strings.TrimSpace(content)
	if strings.HasPrefix(content, "```") {
		if newline := strings.Index(content, "\n"); newline >= 0 {
			content = content[newline+1:]
		}
		if end := strings.LastIndex(content, "```"); end >= 0 {
			content = content[:end]
		}
		content = strings.TrimSpace(content)
	}
	var response serviceQualityProductGroupsResponse
	if json.Unmarshal([]byte(content), &response) != nil {
		return nil, errInvalidProductGrouping
	}
	return validateProductGroups(names, response.Groups)
}

func validateProductGroups(names []string, groups []serviceQualityProductGroup) ([]serviceQualityProductGroup, error) {
	allowed := make(map[string]bool, len(names))
	for _, name := range names {
		allowed[name] = true
	}
	seen := make(map[string]bool, len(names))
	for i := range groups {
		groups[i].Name = strings.TrimSpace(groups[i].Name)
		if len(groups[i].Members) == 0 {
			return nil, errInvalidProductGrouping
		}
		for _, member := range groups[i].Members {
			if !allowed[member] || seen[member] {
				return nil, errInvalidProductGrouping
			}
			seen[member] = true
		}
	}
	if len(seen) != len(allowed) {
		return nil, errInvalidProductGrouping
	}
	return groups, nil
}

func productGroupingInputHash(names []string, prompts ...string) string {
	prompt := ai.MessengerProductGroupingPrompt
	if len(prompts) > 0 {
		prompt = prompts[0]
	}
	payload, _ := json.Marshal(struct {
		Version string   `json:"version"`
		Prompt  string   `json:"prompt"`
		Names   []string `json:"names"`
	}{Version: ai.MessengerProductGroupingPromptVersion, Names: names, Prompt: prompt})
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

func productGroupingCacheSettingKey(inputHash string) string {
	return messengerProductGroupsCacheKeyPrefix + inputHash
}

func encodeProductGroupingCache(groups []serviceQualityProductGroup) (string, error) {
	raw, err := json.Marshal(serviceQualityProductGroupsResponse{Enabled: true, Groups: groups})
	if err != nil {
		return "", err
	}
	var compressed bytes.Buffer
	writer := gzip.NewWriter(&compressed)
	if _, err := writer.Write(raw); err != nil {
		return "", err
	}
	if err := writer.Close(); err != nil {
		return "", err
	}
	encoded := base64.StdEncoding.EncodeToString(compressed.Bytes())
	if len(encoded) > maxProductGroupingCacheEncodedBytes {
		return "", errProductGroupingCacheTooLarge
	}
	return encoded, nil
}

func decodeProductGroupingCache(encoded string) ([]serviceQualityProductGroup, error) {
	compressed, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, err
	}
	reader, err := gzip.NewReader(bytes.NewReader(compressed))
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	raw, err := io.ReadAll(io.LimitReader(reader, maxProductGroupingCacheDecodedBytes+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > maxProductGroupingCacheDecodedBytes {
		return nil, fmt.Errorf("decoded product grouping cache exceeds limit")
	}
	var response serviceQualityProductGroupsResponse
	if err := json.Unmarshal(raw, &response); err != nil {
		return nil, err
	}
	return response.Groups, nil
}

func pruneProductGroupingCache(ctx context.Context, database *gorm.DB, tenantID string) error {
	var settings []models.AppSetting
	if err := database.WithContext(ctx).
		Select("id, setting_key, updated_at").
		Where("tenant_id = ? AND LEFT(setting_key, ?) = ?", tenantID, len(messengerProductGroupsCacheKeyPrefix), messengerProductGroupsCacheKeyPrefix).
		Order("updated_at DESC, setting_key ASC").
		Find(&settings).Error; err != nil {
		return err
	}
	keys := productGroupingCacheKeysToPrune(settings)
	if len(keys) == 0 {
		return nil
	}
	return database.WithContext(ctx).
		Where("tenant_id = ? AND setting_key IN ?", tenantID, keys).
		Delete(&models.AppSetting{}).Error
}

func productGroupingCacheKeysToPrune(settings []models.AppSetting) []string {
	if len(settings) <= maxProductGroupingCacheEntries {
		return nil
	}
	keys := make([]string, 0, len(settings)-maxProductGroupingCacheEntries)
	for _, setting := range settings[maxProductGroupingCacheEntries:] {
		keys = append(keys, setting.SettingKey)
	}
	return keys
}

func defaultProductGroupingDependencies(database *gorm.DB, jobs ...models.Job) productGroupingDependencies {
	deps := productGroupingDependencies{
		loadCache: func(ctx context.Context, tenantID, inputHash string) ([]serviceQualityProductGroup, bool, error) {
			var setting models.AppSetting
			err := database.WithContext(ctx).Where("tenant_id = ? AND setting_key = ?", tenantID, productGroupingCacheSettingKey(inputHash)).First(&setting).Error
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil, false, nil
			}
			if err != nil {
				return nil, false, err
			}
			groups, err := decodeProductGroupingCache(setting.ValuePlain)
			if err != nil {
				return nil, false, err
			}
			return groups, true, nil
		},
		saveCache: func(ctx context.Context, tenantID, inputHash string, groups []serviceQualityProductGroup) error {
			value, err := encodeProductGroupingCache(groups)
			if err != nil {
				return err
			}
			now := time.Now()
			setting := models.AppSetting{ID: pkg.NewUUID(), TenantID: tenantID, SettingKey: productGroupingCacheSettingKey(inputHash), ValuePlain: value, CreatedAt: now, UpdatedAt: now}
			if err := database.WithContext(ctx).Clauses(clause.OnConflict{
				Columns:   []clause.Column{{Name: "tenant_id"}, {Name: "setting_key"}},
				DoUpdates: clause.Assignments(map[string]interface{}{"value_plain": setting.ValuePlain, "updated_at": now}),
			}).Create(&setting).Error; err != nil {
				return err
			}
			return pruneProductGroupingCache(ctx, database, tenantID)
		},
		aiClient: getAIClientForTenant,
		logUsage: func(ctx context.Context, tenantID string, response ai.AIResponse) {
			_ = database.WithContext(ctx).Create(&models.AIUsageLog{
				ID: pkg.NewUUID(), TenantID: tenantID, Source: "job", Provider: response.Provider, Model: response.Model,
				InputTokens: response.InputTokens, OutputTokens: response.OutputTokens,
				CostUSD: ai.CalculateCostUSD(response.Provider, response.Model, response.InputTokens, response.OutputTokens), CreatedAt: time.Now(),
			}).Error
		},
	}
	if len(jobs) == 0 {
		return deps
	}
	job := jobs[0]
	deps.prompt = productGroupingPrompt(job)
	deps.cacheScope = job.ID
	var run models.JobRun
	deps.startRun = func(ctx context.Context) error {
		now := time.Now()
		run = models.JobRun{ID: pkg.NewUUID(), JobID: job.ID, TenantID: job.TenantID, StartedAt: now, Status: "running", Summary: "{}", CreatedAt: now}
		return withProductGroupingJob(ctx, database, job, func(tx *gorm.DB) error { return tx.Create(&run).Error })
	}
	deps.logUsage = func(ctx context.Context, tenantID string, response ai.AIResponse) {
		err := withProductGroupingJob(ctx, database, job, func(tx *gorm.DB) error {
			return tx.Create(&models.AIUsageLog{ID: pkg.NewUUID(), TenantID: tenantID, JobID: job.ID, JobRunID: run.ID, Source: "job", Provider: response.Provider, Model: response.Model, InputTokens: response.InputTokens, OutputTokens: response.OutputTokens, CostUSD: ai.CalculateCostUSD(response.Provider, response.Model, response.InputTokens, response.OutputTokens), CreatedAt: time.Now()}).Error
		})
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			log.Printf("[service-quality] grouping usage write failed: %v", err)
		}
	}
	deps.finishRun = func(runErr error) {
		// Persist completion even if the browser disconnects, but never recreate a deleted task/run.
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		now := time.Now()
		status, message := "success", ""
		if runErr != nil {
			status, message = "error", "Không thể gom nhóm sản phẩm bằng AI."
		}
		err := withProductGroupingJob(ctx, database, job, func(tx *gorm.DB) error {
			if err := tx.Model(&models.JobRun{}).Where("id = ? AND tenant_id = ?", run.ID, job.TenantID).Updates(map[string]interface{}{"status": status, "finished_at": now, "error_message": message}).Error; err != nil {
				return err
			}
			return tx.Model(&models.Job{}).Where("id = ? AND tenant_id = ?", job.ID, job.TenantID).Updates(map[string]interface{}{"last_run_at": run.StartedAt, "last_run_status": status}).Error
		})
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			log.Printf("[service-quality] grouping run completion failed: %v", err)
		}
	}
	return deps
}
