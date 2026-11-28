package handlers

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

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

const (
	productAdvisoryImportWorkers      = 3
	productAdvisoryImportMaxAttempts  = 3
	productAdvisoryImportClaimTTL     = 5 * time.Minute
	productAdvisoryImportPollInterval = 2 * time.Second
)

type productAdvisoryImportJobDTO struct {
	models.ProductAdvisoryImportJob
	ImageCount        int64 `json:"image_count"`
	PendingImageCount int64 `json:"pending_image_count"`
}

type productAdvisoryImportItemDTO struct {
	models.ProductAdvisoryImportItem
	ImageCount        int64 `json:"image_count"`
	PendingImageCount int64 `json:"pending_image_count"`
}

// ImportProductAdvisoryLinks validates, maps, and durably queues an uploaded
// file. A tenant row lock serializes the active-job check across API replicas.
func ImportProductAdvisoryLinks(c *gin.Context) {
	if configuredFirecrawlClient(c) == nil {
		return
	}
	fileName, rows, err := readProductAdvisoryImportFile(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	tenantID := middleware.GetTenantID(c)
	items, err := mapProductAdvisoryImportRows(c.Request.Context(), tenantID, rows)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "query_failed"})
		return
	}

	now := time.Now()
	job := models.ProductAdvisoryImportJob{
		ID: pkg.NewUUID(), TenantID: tenantID, CreatedBy: middleware.GetUserEmail(c),
		FileName: fileName, Status: "running", TotalRows: len(items), StartedAt: now,
	}
	for i := range items {
		items[i].ID = pkg.NewUUID()
		items[i].JobID = job.ID
		items[i].TenantID = tenantID
		switch items[i].Status {
		case "pending":
			job.MatchedCount++
		case "skipped":
			job.SkippedCount++
		case "unmatched":
			job.UnmatchedCount++
		case "failed":
			job.FailedCount++
		}
	}
	if job.MatchedCount == 0 {
		job.Status = "completed"
		job.FinishedAt = &now
	}

	err = db.DB.WithContext(c.Request.Context()).Transaction(func(tx *gorm.DB) error {
		var tenant models.Tenant
		if err := tx.Select("id").Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ?", tenantID).Take(&tenant).Error; err != nil {
			return err
		}
		var active int64
		if err := tx.Model(&models.ProductAdvisoryImportJob{}).
			Where("tenant_id = ? AND status = ?", tenantID, "running").Count(&active).Error; err != nil {
			return err
		}
		if active != 0 {
			return errProductAdvisoryImportActive
		}
		if err := tx.Create(&job).Error; err != nil {
			return err
		}
		if len(items) > 0 {
			return tx.CreateInBatches(&items, 200).Error
		}
		return nil
	})
	if errors.Is(err, errProductAdvisoryImportActive) {
		c.JSON(http.StatusConflict, gin.H{"error": "import_already_running"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "save_failed"})
		return
	}
	db.LogActivity(tenantID, middleware.GetUserID(c), middleware.GetUserEmail(c),
		"product_advisory.links_imported", "product_advisory_import", job.ID, fileName, "", c.ClientIP())
	status := http.StatusAccepted
	if job.Status == "completed" {
		status = http.StatusOK
	}
	itemDTOs := make([]productAdvisoryImportItemDTO, len(items))
	for i, item := range items {
		itemDTOs[i] = productAdvisoryImportItemDTO{ProductAdvisoryImportItem: item}
	}
	c.JSON(status, gin.H{"job": productAdvisoryImportJobDTO{ProductAdvisoryImportJob: job}, "items": itemDTOs})
}

var errProductAdvisoryImportActive = errors.New("product advisory import already running")

func GetLatestProductAdvisoryImportJob(c *gin.Context) {
	tenantID := middleware.GetTenantID(c)
	var job models.ProductAdvisoryImportJob
	query := db.DB.WithContext(c.Request.Context()).Where("tenant_id = ?", tenantID)
	if c.Query("review_queue") == "1" {
		// Keep earlier batches reachable until their remaining images are reviewed.
		query = query.Order(`CASE WHEN status = 'running' THEN 0 WHEN EXISTS (
			SELECT 1 FROM product_advisory_import_images AS link
			JOIN product_advisory_images AS img ON img.id = link.image_id AND img.tenant_id = link.tenant_id
			WHERE link.tenant_id = product_advisory_import_jobs.tenant_id AND link.job_id = product_advisory_import_jobs.id
				AND img.source = 'scraped' AND img.enabled = FALSE
		) THEN 1 ELSE 2 END`)
	}
	err := query.Order("created_at DESC, id DESC").Take(&job).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		c.JSON(http.StatusOK, gin.H{"job": nil, "items": []productAdvisoryImportItemDTO{}})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "query_failed"})
		return
	}
	respondProductAdvisoryImport(c, tenantID, job.ID)
}

func GetProductAdvisoryImportJob(c *gin.Context) {
	respondProductAdvisoryImport(c, middleware.GetTenantID(c), c.Param("jobId"))
}

func respondProductAdvisoryImport(c *gin.Context, tenantID, jobID string) {
	includeItems := c.Query("summary") != "1"
	job, items, err := loadProductAdvisoryImport(c.Request.Context(), tenantID, jobID, includeItems)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": "import_not_found"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "query_failed"})
		return
	}
	if !includeItems {
		items = []productAdvisoryImportItemDTO{}
	}
	c.JSON(http.StatusOK, gin.H{"job": job, "items": items})
}

func loadProductAdvisoryImport(ctx context.Context, tenantID, jobID string, includeItems bool) (productAdvisoryImportJobDTO, []productAdvisoryImportItemDTO, error) {
	var job models.ProductAdvisoryImportJob
	if err := db.DB.WithContext(ctx).Where("id = ? AND tenant_id = ?", jobID, tenantID).Take(&job).Error; err != nil {
		return productAdvisoryImportJobDTO{}, nil, err
	}
	jobDTO := productAdvisoryImportJobDTO{ProductAdvisoryImportJob: job}
	if err := scanProductAdvisoryImportImageCounts(ctx, tenantID, jobID, &jobDTO.ImageCount, &jobDTO.PendingImageCount); err != nil {
		return productAdvisoryImportJobDTO{}, nil, err
	}
	if !includeItems {
		return jobDTO, nil, nil
	}
	type itemRow struct {
		models.ProductAdvisoryImportItem
		ImageCount        int64 `gorm:"column:image_count"`
		PendingImageCount int64 `gorm:"column:pending_image_count"`
	}
	var rows []itemRow
	err := db.DB.WithContext(ctx).Table("product_advisory_import_items AS item").
		Select("item.*, COUNT(img.id) AS image_count, COALESCE(SUM(CASE WHEN img.id IS NOT NULL AND img.enabled = FALSE THEN 1 ELSE 0 END), 0) AS pending_image_count").
		Joins("LEFT JOIN product_advisory_import_images AS link ON link.item_id = item.id AND link.job_id = item.job_id AND link.tenant_id = item.tenant_id").
		Joins("LEFT JOIN product_advisory_images AS img ON img.id = link.image_id AND img.tenant_id = link.tenant_id AND img.source = ?", "scraped").
		Where("item.job_id = ? AND item.tenant_id = ?", jobID, tenantID).
		Group("item.id").Order("item.row_number, item.id").Scan(&rows).Error
	if err != nil {
		return productAdvisoryImportJobDTO{}, nil, err
	}
	items := make([]productAdvisoryImportItemDTO, 0, len(rows))
	for _, row := range rows {
		items = append(items, productAdvisoryImportItemDTO{
			ProductAdvisoryImportItem: row.ProductAdvisoryImportItem,
			ImageCount:                row.ImageCount, PendingImageCount: row.PendingImageCount,
		})
	}
	return jobDTO, items, nil
}

func scanProductAdvisoryImportImageCounts(ctx context.Context, tenantID, jobID string, total, pending *int64) error {
	type counts struct {
		Total   int64 `gorm:"column:total"`
		Pending int64 `gorm:"column:pending"`
	}
	var row counts
	query := db.DB.WithContext(ctx).Table("product_advisory_import_images AS link").
		Select("COUNT(img.id) AS total, COALESCE(SUM(CASE WHEN img.id IS NOT NULL AND img.enabled = FALSE THEN 1 ELSE 0 END), 0) AS pending").
		Joins("JOIN product_advisory_images AS img ON img.id = link.image_id AND img.tenant_id = link.tenant_id AND img.source = ?", "scraped").
		Where("link.tenant_id = ? AND link.job_id = ?", tenantID, jobID)
	if err := query.Scan(&row).Error; err != nil {
		return err
	}
	*total, *pending = row.Total, row.Pending
	return nil
}

// RunProductAdvisoryImportWorker starts a process-local bounded pool. Claims
// live in MySQL, so multiple API replicas and restarts share the same queue.
func RunProductAdvisoryImportWorker(ctx context.Context, cfg *config.Config) {
	var group sync.WaitGroup
	group.Add(productAdvisoryImportWorkers)
	for i := 0; i < productAdvisoryImportWorkers; i++ {
		go func() {
			defer group.Done()
			runProductAdvisoryImportWorker(ctx, cfg)
		}()
	}
	<-ctx.Done()
	group.Wait()
}

func runProductAdvisoryImportWorker(ctx context.Context, cfg *config.Config) {
	ticker := time.NewTicker(productAdvisoryImportPollInterval)
	defer ticker.Stop()
	for {
		worked, err := processNextProductAdvisoryImportItem(ctx, cfg)
		if err != nil && !errors.Is(err, context.Canceled) {
			log.Printf("[product-advisory-import] worker: %v", err)
		}
		if worked {
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func processNextProductAdvisoryImportItem(ctx context.Context, cfg *config.Config) (bool, error) {
	item, claimed, err := claimProductAdvisoryImportItem(ctx)
	if err != nil || !claimed {
		return false, err
	}
	key, _, err := loadFirecrawlKey(ctx, item.TenantID)
	if err != nil {
		return true, releaseProductAdvisoryImportItem(ctx, item, "query_failed", "Could not load Firecrawl settings")
	}
	if key == "" {
		return true, releaseProductAdvisoryImportItem(ctx, item, "firecrawl_not_configured", "Firecrawl is not configured")
	}
	return true, processClaimedProductAdvisoryImportItem(ctx, cfg, item, knowledge.NewFirecrawlClient(key).ScrapePage)
}

func processClaimedProductAdvisoryImportItem(ctx context.Context, cfg *config.Config, item models.ProductAdvisoryImportItem, scrape func(context.Context, string) (knowledge.ScrapedPage, error)) error {
	scrapeCtx, cancel := context.WithTimeout(ctx, 70*time.Second)
	page, scrapeErr := scrape(scrapeCtx, item.SourceURL)
	cancel()
	if scrapeErr != nil {
		return releaseProductAdvisoryImportItem(ctx, item, "scrape_failed", "Could not read the product page")
	}

	summaryCtx, cancel := context.WithTimeout(ctx, 35*time.Second)
	summary, summaryErr := knowledge.SummarizeProductPage(summaryCtx, item.TenantID, cfg, item.WebName, page)
	cancel()
	summaryCode := productAdvisorySummaryErrorCode(summaryErr)
	if summaryErr != nil && summaryCode == "summary_failed" {
		log.Printf("[product-advisory-import] summarize tenant=%s item=%s: %v", item.TenantID, item.ID, summaryErr)
	}
	if err := completeProductAdvisoryImportItem(ctx, item, page, summary, summaryCode); err != nil {
		if errors.Is(err, errProductAdvisoryImportClaimLost) {
			return nil
		}
		if errors.Is(err, errProductAdvisoryImportGroupMissing) {
			return releaseProductAdvisoryImportItem(ctx, item, "product_not_found", "The product line could no longer be identified in the catalog")
		}
		log.Printf("[product-advisory-import] save tenant=%s item=%s: %v", item.TenantID, item.ID, err)
		return releaseProductAdvisoryImportItem(ctx, item, "save_failed", "Could not save product data")
	}
	return nil
}

func productAdvisorySummaryErrorCode(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, knowledge.ErrSummaryNoAI):
		return "summary_no_ai"
	case errors.Is(err, knowledge.ErrSummaryNoText):
		return "summary_no_text"
	case errors.Is(err, knowledge.ErrSummarySize):
		return "summary_too_large"
	case errors.Is(err, knowledge.ErrSummaryBusy):
		return "summary_busy"
	default:
		return "summary_failed"
	}
}

func claimProductAdvisoryImportItem(ctx context.Context) (models.ProductAdvisoryImportItem, bool, error) {
	var claimed models.ProductAdvisoryImportItem
	err := db.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		now := time.Now()
		err := tx.Table("product_advisory_import_items AS item").Select("item.*").
			Joins("JOIN product_advisory_import_jobs AS job ON job.id = item.job_id AND job.tenant_id = item.tenant_id").
			Where("job.status = ? AND (item.status = ? OR (item.status = ? AND item.claimed_until < ?))", "running", "pending", "processing", now).
			Order("job.created_at, item.row_number, item.id").
			Clauses(clause.Locking{Strength: "UPDATE", Table: clause.Table{Name: "item"}, Options: "SKIP LOCKED"}).Take(&claimed).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		if claimed.Attempts >= productAdvisoryImportMaxAttempts {
			if err := tx.Model(&models.ProductAdvisoryImportItem{}).Where("id = ? AND tenant_id = ?", claimed.ID, claimed.TenantID).
				Updates(map[string]any{"status": "failed", "error_code": "retry_exhausted", "error_message": "Processing did not finish after several attempts", "claim_token": "", "claimed_until": nil}).Error; err != nil {
				return err
			}
			if err := advanceProductAdvisoryImportJob(tx, claimed.JobID, claimed.TenantID, "failed_count"); err != nil {
				return err
			}
			claimed = models.ProductAdvisoryImportItem{}
			return nil
		}
		token := pkg.NewUUID()
		until := now.Add(productAdvisoryImportClaimTTL)
		result := tx.Model(&models.ProductAdvisoryImportItem{}).
			Where("id = ? AND tenant_id = ? AND (status = ? OR (status = ? AND claimed_until < ?))", claimed.ID, claimed.TenantID, "pending", "processing", now).
			Updates(map[string]any{"status": "processing", "claim_token": token, "claimed_until": until, "attempts": gorm.Expr("attempts + 1"), "error_code": "", "error_message": ""})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return errProductAdvisoryImportClaimLost
		}
		claimed.Status, claimed.ClaimToken, claimed.ClaimedUntil = "processing", token, &until
		claimed.Attempts++
		return nil
	}, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return models.ProductAdvisoryImportItem{}, false, err
	}
	return claimed, claimed.ClaimToken != "", nil
}

var errProductAdvisoryImportClaimLost = errors.New("product advisory import claim lost")
var errProductAdvisoryImportGroupMissing = errors.New("product advisory import group missing or ambiguous")

func completeProductAdvisoryImportItem(ctx context.Context, item models.ProductAdvisoryImportItem, page knowledge.ScrapedPage, summary, summaryError string) error {
	return db.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var current models.ProductAdvisoryImportItem
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ? AND tenant_id = ? AND status = ? AND claim_token = ? AND claimed_until > ?", item.ID, item.TenantID, "processing", item.ClaimToken, time.Now()).
			Take(&current).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return errProductAdvisoryImportClaimLost
			}
			return err
		}
		catalog := productAdvisoryCatalog(tx, item.TenantID)
		if current.GroupKey != "" {
			catalog = catalog.Where(models.ProductAdvisoryGroupKeySQL+" = ?", current.GroupKey)
		} else {
			// An import queued before the split has a saved parent and name.
			catalog = catalog.Where(models.ProductAdvisoryParentSQL+" = ?", current.ParentCode).
				Having("LOWER("+models.ProductAdvisoryWebNameSQL+") = LOWER(?)", current.WebName)
		}
		var groups []productCatalogGroup
		if err := catalog.Limit(2).Scan(&groups).Error; err != nil {
			return err
		}
		if len(groups) != 1 {
			return errProductAdvisoryImportGroupMissing
		}
		page.Title = clipProductImportText(page.Title, 500)
		images, err := persistProductAdvisoryScrape(tx, item.TenantID, groups[0], item.SourceURL, page, summary, true)
		if err != nil {
			return err
		}
		links := make([]models.ProductAdvisoryImportImage, 0, len(images))
		for _, image := range images {
			links = append(links, models.ProductAdvisoryImportImage{
				ID: pkg.NewUUID(), JobID: item.JobID, ItemID: item.ID, TenantID: item.TenantID, ImageID: image.ID,
			})
		}
		if len(links) > 0 {
			if err := tx.Create(&links).Error; err != nil {
				return err
			}
		}
		if err := tx.Model(&models.ProductAdvisoryImportItem{}).
			Where("id = ? AND tenant_id = ? AND claim_token = ?", item.ID, item.TenantID, item.ClaimToken).
			Updates(map[string]any{"status": "succeeded", "group_key": groups[0].GroupKey, "product_category": groups[0].ProductCategory, "summary_error": summaryError, "claim_token": "", "claimed_until": nil}).Error; err != nil {
			return err
		}
		return advanceProductAdvisoryImportJob(tx, item.JobID, item.TenantID, "succeeded_count")
	})
}

func releaseProductAdvisoryImportItem(ctx context.Context, item models.ProductAdvisoryImportItem, code, message string) error {
	return db.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&models.ProductAdvisoryImportItem{}).
			Where("id = ? AND tenant_id = ? AND status = ? AND claim_token = ?", item.ID, item.TenantID, "processing", item.ClaimToken).
			Updates(map[string]any{"status": "failed", "error_code": code, "error_message": message, "claim_token": "", "claimed_until": nil})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return errProductAdvisoryImportClaimLost
		}
		return advanceProductAdvisoryImportJob(tx, item.JobID, item.TenantID, "failed_count")
	})
}

func advanceProductAdvisoryImportJob(tx *gorm.DB, jobID, tenantID, counter string) error {
	job := tx.Model(&models.ProductAdvisoryImportJob{}).Where("id = ? AND tenant_id = ?", jobID, tenantID)
	if err := job.Session(&gorm.Session{}).Update(counter, gorm.Expr("? + 1", clause.Column{Name: counter})).Error; err != nil {
		return err
	}
	return job.Where("status = ? AND succeeded_count + failed_count + skipped_count + unmatched_count = total_rows", "running").
		Updates(map[string]any{"status": "completed", "finished_at": time.Now()}).Error
}

func GetProductAdvisoryImportImages(c *gin.Context) {
	tenantID, jobID := middleware.GetTenantID(c), c.Param("jobId")
	if err := requireTerminalProductAdvisoryImport(c.Request.Context(), tenantID, jobID); err != nil {
		respondProductAdvisoryImportLookupError(c, err)
		return
	}
	type imageRow struct {
		ID        string `json:"id"`
		ItemID    string `json:"item_id"`
		URL       string `json:"url"`
		Source    string `json:"source"`
		Caption   string `json:"caption"`
		Enabled   bool   `json:"enabled"`
		SortOrder int    `json:"sort_order"`
	}
	var images []imageRow
	query := db.DB.WithContext(c.Request.Context()).Table("product_advisory_import_images AS link").
		Select("img.id, link.item_id, img.url, img.source, img.caption, img.enabled, img.sort_order").
		Joins("JOIN product_advisory_images AS img ON img.id = link.image_id AND img.tenant_id = link.tenant_id AND img.source = ?", "scraped").
		Where("link.tenant_id = ? AND link.job_id = ?", tenantID, jobID)
	itemID := strings.TrimSpace(c.Query("item_id"))
	if itemID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "item_required"})
		return
	}
	if err := query.Where("link.item_id = ?", itemID).Order("img.sort_order, img.id").Scan(&images).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "query_failed"})
		return
	}
	if images == nil {
		images = []imageRow{}
	}
	c.JSON(http.StatusOK, gin.H{"images": images})
}

func ApproveProductAdvisoryImportImages(c *gin.Context) {
	tenantID, jobID := middleware.GetTenantID(c), c.Param("jobId")
	if err := requireTerminalProductAdvisoryImport(c.Request.Context(), tenantID, jobID); err != nil {
		respondProductAdvisoryImportLookupError(c, err)
		return
	}
	var req struct {
		ImageID string `json:"image_id"`
		All     bool   `json:"all"`
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 4<<10)
	if err := c.ShouldBindJSON(&req); err != nil || (req.All == (strings.TrimSpace(req.ImageID) != "")) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_body"})
		return
	}
	query := `UPDATE product_advisory_images AS img
		JOIN product_advisory_import_images AS link
		  ON link.image_id = img.id AND link.tenant_id = img.tenant_id
		SET img.enabled = TRUE, img.updated_at = ?
		WHERE link.tenant_id = ? AND link.job_id = ? AND img.source = ? AND img.enabled = FALSE`
	args := []any{time.Now(), tenantID, jobID, "scraped"}
	if !req.All {
		query += " AND img.id = ?"
		args = append(args, strings.TrimSpace(req.ImageID))
	}
	result := db.DB.WithContext(c.Request.Context()).Exec(query, args...)
	if result.Error != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "save_failed"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"approved_count": result.RowsAffected})
}

func requireTerminalProductAdvisoryImport(ctx context.Context, tenantID, jobID string) error {
	var job models.ProductAdvisoryImportJob
	if err := db.DB.WithContext(ctx).Select("id", "status").
		Where("id = ? AND tenant_id = ?", jobID, tenantID).Take(&job).Error; err != nil {
		return err
	}
	if job.Status == "running" {
		return errProductAdvisoryImportRunning
	}
	return nil
}

var errProductAdvisoryImportRunning = errors.New("product advisory import is still running")

func respondProductAdvisoryImportLookupError(c *gin.Context, err error) {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": "import_not_found"})
		return
	}
	if errors.Is(err, errProductAdvisoryImportRunning) {
		c.JSON(http.StatusConflict, gin.H{"error": "import_still_running"})
		return
	}
	c.JSON(http.StatusInternalServerError, gin.H{"error": "query_failed"})
}
