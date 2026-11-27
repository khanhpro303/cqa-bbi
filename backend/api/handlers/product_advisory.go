package handlers

import (
	"context"
	"errors"
	"io"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/vietbui/chat-quality-agent/api/middleware"
	"github.com/vietbui/chat-quality-agent/config"
	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/db/models"
	"github.com/vietbui/chat-quality-agent/engine"
	"github.com/vietbui/chat-quality-agent/knowledge"
	"github.com/vietbui/chat-quality-agent/pkg"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	maxProductAdvisoryImages        = 12
	maxProductAdvisoryUploadFiles   = 8
	maxProductAdvisoryImageBytes    = 5 << 20
	maxProductAdvisoryUploadRequest = 12 << 20 // nginx client_max_body_size
)

const productParentCodeExpr = "COALESCE(NULLIF(TRIM(ma_cha), ''), TRIM(ma))"
const productCatalogGroupSelect = productParentCodeExpr + " AS parent_code, COALESCE(MAX(NULLIF(TRIM(ten_dong_bo_web), '')), MAX(NULLIF(TRIM(ten), '')), MAX(" + productParentCodeExpr + ")) AS web_name, COUNT(DISTINCT ma) AS variant_count, COALESCE(MAX(nhan_hieu_name), '') AS brand, COALESCE(MAX(NULLIF(TRIM(link_anh), '')), '') AS default_image_url"

type productCatalogGroup struct {
	ParentCode      string `json:"parent_code" gorm:"column:parent_code"`
	WebName         string `json:"web_name" gorm:"column:web_name"`
	VariantCount    int    `json:"variant_count" gorm:"column:variant_count"`
	Brand           string `json:"brand" gorm:"column:brand"`
	DefaultImageURL string `json:"default_image_url" gorm:"column:default_image_url"`
}

type productAdvisoryImageDTO struct {
	ID        string `json:"id"`
	URL       string `json:"url"`
	Source    string `json:"source"`
	Caption   string `json:"caption"`
	Enabled   bool   `json:"enabled"`
	SortOrder int    `json:"sort_order"`
}

type productAdvisoryListItem struct {
	productCatalogGroup
	HasContent   bool       `json:"has_content"`
	ThumbnailURL string     `json:"thumbnail_url"`
	UpdatedAt    *time.Time `json:"updated_at,omitempty"`
}

type productAdvisoryListRow struct {
	ParentCode      string     `gorm:"column:parent_code"`
	WebName         string     `gorm:"column:web_name"`
	VariantCount    int        `gorm:"column:variant_count"`
	Brand           string     `gorm:"column:brand"`
	DefaultImageURL string     `gorm:"column:default_image_url"`
	ProfileID       string     `gorm:"column:profile_id"`
	HasContent      bool       `gorm:"column:has_content"`
	UpdatedAt       *time.Time `gorm:"column:updated_at"`
}

type productAdvisoryDetail struct {
	productCatalogGroup
	Notes          string                    `json:"notes"`
	SourceURL      string                    `json:"source_url"`
	SourceTitle    string                    `json:"source_title"`
	ScrapedContent string                    `json:"scraped_content"`
	SourceSummary  string                    `json:"source_summary"`
	SummaryError   string                    `json:"summary_error,omitempty"`
	Images         []productAdvisoryImageDTO `json:"images"`
	UpdatedAt      *time.Time                `json:"updated_at,omitempty"`
}

func ListProductAdvisories(c *gin.Context) {
	tenantID := middleware.GetTenantID(c)
	page := boundedPositiveInt(c.Query("page"), 1, 1, 1_000_000)
	perPage := boundedPositiveInt(c.Query("per_page"), 50, 1, 100)
	catalog := db.DB.WithContext(c.Request.Context()).Model(&models.CachedProduct{}).
		Select(productCatalogGroupSelect).
		Where("tenant_id = ? AND TRIM(ma) <> ''", tenantID).Scopes(engine.ExcludeDKCopies).Group(productParentCodeExpr)
	base := db.DB.WithContext(c.Request.Context()).Table("(?) AS catalog", catalog).
		Joins("LEFT JOIN product_advisories pa ON pa.tenant_id = ? AND pa.parent_code = catalog.parent_code", tenantID)
	query := strings.TrimSpace(c.Query("q"))
	if query != "" {
		like := "%" + query + "%"
		base = base.Where("catalog.parent_code LIKE ? OR catalog.web_name LIKE ? OR catalog.brand LIKE ?", like, like, like)
	}
	hasContentSQL := "(COALESCE(TRIM(pa.notes), '') <> '' OR COALESCE(TRIM(pa.scraped_content), '') <> '' OR EXISTS (SELECT 1 FROM product_advisory_images AS ai WHERE ai.tenant_id = pa.tenant_id AND ai.product_advisory_id = pa.id AND ai.enabled = TRUE))"
	switch strings.TrimSpace(c.DefaultQuery("configured", "all")) {
	case "yes":
		base = base.Where(hasContentSQL)
	case "no":
		base = base.Where("NOT " + hasContentSQL)
	}
	var total int64
	if err := base.Count(&total).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "query_failed"})
		return
	}
	var rows []productAdvisoryListRow
	selectSQL := "catalog.parent_code, catalog.web_name, catalog.variant_count, catalog.brand, catalog.default_image_url, COALESCE(pa.id, '') AS profile_id, " + hasContentSQL + " AS has_content, pa.updated_at"
	if err := base.Select(selectSQL).Order("catalog.web_name, catalog.parent_code").Offset((page - 1) * perPage).Limit(perPage).Scan(&rows).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "query_failed"})
		return
	}
	profileIDs := make([]string, 0, len(rows))
	for _, row := range rows {
		if row.ProfileID != "" {
			profileIDs = append(profileIDs, row.ProfileID)
		}
	}
	thumbnailByProfile := make(map[string]models.ProductAdvisoryImage)
	if len(profileIDs) > 0 {
		var images []models.ProductAdvisoryImage
		if err := db.DB.WithContext(c.Request.Context()).Select("id, product_advisory_id, url, source, enabled, sort_order").
			Where("tenant_id = ? AND product_advisory_id IN ? AND enabled = ?", tenantID, profileIDs, true).
			Order("sort_order, created_at, id").Find(&images).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "query_failed"})
			return
		}
		for _, image := range images {
			if _, exists := thumbnailByProfile[image.ProductAdvisoryID]; !exists {
				thumbnailByProfile[image.ProductAdvisoryID] = image
			}
		}
	}

	items := make([]productAdvisoryListItem, 0, len(rows))
	for _, row := range rows {
		group := productCatalogGroup{ParentCode: row.ParentCode, WebName: row.WebName, VariantCount: row.VariantCount, Brand: row.Brand, DefaultImageURL: row.DefaultImageURL}
		item := productAdvisoryListItem{productCatalogGroup: group, HasContent: row.HasContent, UpdatedAt: row.UpdatedAt}
		if image, ok := thumbnailByProfile[row.ProfileID]; ok {
			item.ThumbnailURL = productAdvisoryImageURL(tenantID, group.ParentCode, image)
		}
		if item.ThumbnailURL == "" {
			item.ThumbnailURL = group.DefaultImageURL
		}
		items = append(items, item)
	}
	c.JSON(http.StatusOK, gin.H{"items": items, "total": total, "page": page, "per_page": perPage})
}

func boundedPositiveInt(raw string, fallback, minValue, maxValue int) int {
	value, err := strconv.Atoi(raw)
	if err != nil || value < minValue {
		return fallback
	}
	if value > maxValue {
		return maxValue
	}
	return value
}

func GetProductAdvisory(c *gin.Context) {
	detail, status, err := loadProductAdvisoryDetail(c.Request.Context(), middleware.GetTenantID(c), c.Param("parentCode"))
	if err != nil {
		c.JSON(status, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, detail)
}

func SaveProductAdvisory(c *gin.Context) {
	var req struct {
		Notes     string `json:"notes"`
		SourceURL string `json:"source_url"`
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 1<<20)
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_body"})
		return
	}
	if len(req.Notes) > 100_000 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "notes_too_long"})
		return
	}
	req.SourceURL = strings.TrimSpace(req.SourceURL)
	if req.SourceURL != "" {
		source, err := knowledge.ParseSourceURL(req.SourceURL)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_url"})
			return
		}
		req.SourceURL = source.String()
	}
	tenantID := middleware.GetTenantID(c)
	group, status, err := loadProductCatalogGroup(c.Request.Context(), tenantID, c.Param("parentCode"))
	if err != nil {
		c.JSON(status, gin.H{"error": err.Error()})
		return
	}
	if err := db.DB.WithContext(c.Request.Context()).Transaction(func(tx *gorm.DB) error {
		profile, err := ensureProductAdvisory(tx, tenantID, group)
		if err != nil {
			return err
		}
		sourceChanged := profile.SourceURL != req.SourceURL
		updates := map[string]any{"web_name": group.WebName, "notes": strings.TrimSpace(req.Notes), "source_url": req.SourceURL, "updated_at": time.Now()}
		if sourceChanged {
			updates["source_title"] = ""
			updates["scraped_content"] = ""
			updates["source_summary"] = ""
			if err := tx.Where("tenant_id = ? AND product_advisory_id = ? AND source = ?", tenantID, profile.ID, "scraped").Delete(&models.ProductAdvisoryImage{}).Error; err != nil {
				return err
			}
		}
		return tx.Model(&profile).Updates(updates).Error
	}); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "save_failed"})
		return
	}
	GetProductAdvisory(c)
}

func ScrapeProductAdvisory(c *gin.Context, cfg *config.Config) {
	var req struct {
		URL string `json:"url"`
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 16<<10)
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_body"})
		return
	}
	source, err := knowledge.ParseSourceURL(req.URL)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_url"})
		return
	}
	tenantID := middleware.GetTenantID(c)
	group, status, err := loadProductCatalogGroup(c.Request.Context(), tenantID, c.Param("parentCode"))
	if err != nil {
		c.JSON(status, gin.H{"error": err.Error()})
		return
	}
	client := configuredFirecrawlClient(c)
	if client == nil {
		return
	}
	page, err := client.ScrapePage(c.Request.Context(), source.String())
	if err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": "scrape_failed", "message": err.Error()})
		return
	}
	// Keep the Firecrawl and AI calls within the browser request budget.
	summaryCtx, cancel := context.WithTimeout(c.Request.Context(), 35*time.Second)
	summary, summaryErr := knowledge.SummarizeProductPage(summaryCtx, tenantID, cfg, group.WebName, page)
	cancel()
	if summaryErr != nil && !errors.Is(summaryErr, knowledge.ErrSummaryNoAI) && !errors.Is(summaryErr, knowledge.ErrSummaryNoText) {
		log.Printf("[product-advisory] summarize page for tenant %s, product %s: %v", tenantID, group.ParentCode, summaryErr)
	}
	if err := saveProductAdvisoryScrape(c.Request.Context(), tenantID, group, source.String(), page, summary); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "save_failed"})
		return
	}
	detail, status, err := loadProductAdvisoryDetail(c.Request.Context(), tenantID, group.ParentCode)
	if err != nil {
		c.JSON(status, gin.H{"error": err.Error()})
		return
	}
	if detail.SourceSummary == "" {
		switch {
		case errors.Is(summaryErr, knowledge.ErrSummaryNoAI):
			detail.SummaryError = "summary_no_ai"
		case errors.Is(summaryErr, knowledge.ErrSummaryNoText):
			detail.SummaryError = "summary_no_text"
		case summaryErr != nil:
			detail.SummaryError = "summary_failed"
		}
	}
	c.JSON(http.StatusOK, detail)
}

func saveProductAdvisoryScrape(ctx context.Context, tenantID string, group productCatalogGroup, sourceURL string, page knowledge.ScrapedPage, summary string) error {
	return db.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		_, err := persistProductAdvisoryScrape(tx, tenantID, group, sourceURL, page, summary, false)
		return err
	})
}

// persistProductAdvisoryScrape returns the newly created images so an import
// can record which images belong to its review in the same transaction.
func persistProductAdvisoryScrape(tx *gorm.DB, tenantID string, group productCatalogGroup, sourceURL string, page knowledge.ScrapedPage, summary string, forceOverwrite bool) ([]models.ProductAdvisoryImage, error) {
	profile, err := ensureProductAdvisory(tx, tenantID, group)
	if err != nil {
		return nil, err
	}
	sameSource := !forceOverwrite && profile.SourceURL == sourceURL
	updates := map[string]any{
		"web_name": group.WebName, "source_url": sourceURL, "source_title": page.Title,
		"scraped_content": page.Text, "updated_at": time.Now(),
	}
	if summary != "" || !sameSource || profile.SourceTitle != page.Title || profile.ScrapedContent != page.Text {
		updates["source_summary"] = summary
	}
	if err := tx.Model(&profile).Updates(updates).Error; err != nil {
		return nil, err
	}
	var previous []models.ProductAdvisoryImage
	imageFilter := "tenant_id = ? AND product_advisory_id = ?"
	if err := tx.Select("id", "url", "source").Where(imageFilter, tenantID, profile.ID).Find(&previous).Error; err != nil {
		return nil, err
	}
	oldScraped := make(map[string]models.ProductAdvisoryImage)
	remainingSlots := maxProductAdvisoryImages
	for _, image := range previous {
		if image.Source == "scraped" {
			oldScraped[image.URL] = image
		} else {
			remainingSlots--
		}
	}
	var keepIDs []string
	images := make([]models.ProductAdvisoryImage, 0, len(page.Images))
	for i, imageURL := range page.Images {
		if len(images)+len(keepIDs) >= remainingSlots {
			break
		}
		if old, exists := oldScraped[imageURL]; exists && sameSource {
			keepIDs = append(keepIDs, old.ID)
			continue
		}
		images = append(images, models.ProductAdvisoryImage{ID: pkg.NewUUID(), ProductAdvisoryID: profile.ID, TenantID: tenantID, URL: imageURL, Source: "scraped", SortOrder: i})
	}
	obsolete := tx.Where(imageFilter+" AND source = ?", tenantID, profile.ID, "scraped")
	if len(keepIDs) != 0 {
		obsolete = obsolete.Where("id NOT IN ?", keepIDs)
	}
	if err := obsolete.Delete(&models.ProductAdvisoryImage{}).Error; err != nil {
		return nil, err
	}
	if len(images) > 0 {
		if err := tx.Create(&images).Error; err != nil {
			return nil, err
		}
	}
	return images, nil
}

func UploadProductAdvisoryImages(c *gin.Context) {
	tenantID := middleware.GetTenantID(c)
	group, status, err := loadProductCatalogGroup(c.Request.Context(), tenantID, c.Param("parentCode"))
	if err != nil {
		c.JSON(status, gin.H{"error": err.Error()})
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxProductAdvisoryUploadRequest)
	form, err := c.MultipartForm()
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_multipart_form"})
		return
	}
	defer form.RemoveAll()
	files := form.File["images"]
	if len(files) == 0 || len(files) > maxProductAdvisoryUploadFiles {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_image_count"})
		return
	}
	type upload struct {
		data               []byte
		mediaType, caption string
	}
	uploads := make([]upload, 0, len(files))
	for _, file := range files {
		if file.Size <= 0 || file.Size > maxProductAdvisoryImageBytes {
			c.JSON(http.StatusBadRequest, gin.H{"error": "image_too_large", "filename": file.Filename})
			return
		}
		opened, err := file.Open()
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_image"})
			return
		}
		data, readErr := io.ReadAll(io.LimitReader(opened, maxProductAdvisoryImageBytes+1))
		opened.Close()
		mediaType := http.DetectContentType(data)
		if readErr != nil || len(data) > maxProductAdvisoryImageBytes || !allowedProductAdvisoryImageType(mediaType) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "unsupported_image_type", "filename": file.Filename})
			return
		}
		uploads = append(uploads, upload{data: data, mediaType: mediaType, caption: strings.TrimSpace(file.Filename)})
	}
	var created []models.ProductAdvisoryImage
	err = db.DB.WithContext(c.Request.Context()).Transaction(func(tx *gorm.DB) error {
		profile, err := ensureProductAdvisory(tx, tenantID, group)
		if err != nil {
			return err
		}
		var count int64
		if err := tx.Model(&models.ProductAdvisoryImage{}).Where("tenant_id = ? AND product_advisory_id = ?", tenantID, profile.ID).Count(&count).Error; err != nil {
			return err
		}
		if count+int64(len(uploads)) > maxProductAdvisoryImages {
			return errTooManyProductAdvisoryImages
		}
		for i, upload := range uploads {
			created = append(created, models.ProductAdvisoryImage{ID: pkg.NewUUID(), ProductAdvisoryID: profile.ID, TenantID: tenantID, Source: "upload", Caption: upload.caption, MediaType: upload.mediaType, Data: upload.data, Enabled: true, SortOrder: int(count) + i})
		}
		return tx.CreateInBatches(&created, 1).Error
	})
	if errors.Is(err, errTooManyProductAdvisoryImages) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "too_many_images"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "save_failed"})
		return
	}
	images := make([]productAdvisoryImageDTO, 0, len(created))
	for _, image := range created {
		images = append(images, productAdvisoryImageDTOFromModel(tenantID, group.ParentCode, image))
	}
	c.JSON(http.StatusCreated, gin.H{"images": images})
}

var errTooManyProductAdvisoryImages = errors.New("too many product advisory images")

func UpdateProductAdvisoryImage(c *gin.Context) {
	var req struct {
		Enabled   *bool   `json:"enabled"`
		SortOrder *int    `json:"sort_order"`
		Caption   *string `json:"caption"`
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 8<<10)
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_body"})
		return
	}
	updates := map[string]any{"updated_at": time.Now()}
	if req.Enabled != nil {
		updates["enabled"] = *req.Enabled
	}
	if req.SortOrder != nil && *req.SortOrder >= 0 {
		updates["sort_order"] = *req.SortOrder
	}
	if req.Caption != nil {
		caption := strings.TrimSpace(*req.Caption)
		if len(caption) > 500 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "caption_too_long"})
			return
		}
		updates["caption"] = caption
	}
	if len(updates) == 1 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "empty_update"})
		return
	}
	image, status, err := findProductAdvisoryImage(c.Request.Context(), middleware.GetTenantID(c), c.Param("parentCode"), c.Param("imageId"))
	if err != nil {
		c.JSON(status, gin.H{"error": err.Error()})
		return
	}
	if err := db.DB.WithContext(c.Request.Context()).Model(&image).Updates(updates).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "save_failed"})
		return
	}
	if err := db.DB.WithContext(c.Request.Context()).First(&image, "id = ?", image.ID).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "query_failed"})
		return
	}
	c.JSON(http.StatusOK, productAdvisoryImageDTOFromModel(middleware.GetTenantID(c), strings.TrimSpace(c.Param("parentCode")), image))
}

func DeleteProductAdvisoryImage(c *gin.Context) {
	image, status, err := findProductAdvisoryImage(c.Request.Context(), middleware.GetTenantID(c), c.Param("parentCode"), c.Param("imageId"))
	if err != nil {
		c.JSON(status, gin.H{"error": err.Error()})
		return
	}
	if err := db.DB.WithContext(c.Request.Context()).Where("id = ? AND tenant_id = ?", image.ID, middleware.GetTenantID(c)).Delete(&models.ProductAdvisoryImage{}).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "delete_failed"})
		return
	}
	c.Status(http.StatusNoContent)
}

func GetProductAdvisoryImageContent(c *gin.Context) {
	image, status, err := findProductAdvisoryImage(c.Request.Context(), middleware.GetTenantID(c), c.Param("parentCode"), c.Param("imageId"))
	if err != nil {
		c.JSON(status, gin.H{"error": err.Error()})
		return
	}
	if image.Source != "upload" || len(image.Data) == 0 || !allowedProductAdvisoryImageType(image.MediaType) {
		c.JSON(http.StatusNotFound, gin.H{"error": "image_not_found"})
		return
	}
	c.Header("Cache-Control", "private, max-age=86400")
	c.Data(http.StatusOK, image.MediaType, image.Data)
}

func loadProductCatalogGroup(ctx context.Context, tenantID, rawParentCode string) (productCatalogGroup, int, error) {
	parentCode := strings.TrimSpace(rawParentCode)
	var group productCatalogGroup
	result := db.DB.WithContext(ctx).Model(&models.CachedProduct{}).Select(productCatalogGroupSelect).
		Where("tenant_id = ? AND TRIM(ma) <> '' AND "+productParentCodeExpr+" = ?", tenantID, parentCode).
		Scopes(engine.ExcludeDKCopies).Group(productParentCodeExpr).Scan(&group)
	if result.Error != nil {
		return productCatalogGroup{}, http.StatusInternalServerError, errors.New("query_failed")
	}
	if result.RowsAffected != 0 {
		return group, http.StatusOK, nil
	}
	return productCatalogGroup{}, http.StatusNotFound, errors.New("product_not_found")
}

func loadProductAdvisoryDetail(ctx context.Context, tenantID, rawParentCode string) (productAdvisoryDetail, int, error) {
	group, status, err := loadProductCatalogGroup(ctx, tenantID, rawParentCode)
	if err != nil {
		return productAdvisoryDetail{}, status, err
	}
	detail := productAdvisoryDetail{productCatalogGroup: group, Images: []productAdvisoryImageDTO{}}
	var profile models.ProductAdvisory
	err = db.DB.WithContext(ctx).Where("tenant_id = ? AND parent_code = ?", tenantID, group.ParentCode).Take(&profile).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return detail, http.StatusOK, nil
	}
	if err != nil {
		return productAdvisoryDetail{}, http.StatusInternalServerError, errors.New("query_failed")
	}
	detail.Notes, detail.SourceURL, detail.SourceTitle, detail.ScrapedContent = profile.Notes, profile.SourceURL, profile.SourceTitle, profile.ScrapedContent
	detail.SourceSummary = profile.SourceSummary
	detail.UpdatedAt = &profile.UpdatedAt
	var images []models.ProductAdvisoryImage
	if err := db.DB.WithContext(ctx).Select("id, product_advisory_id, tenant_id, url, source, caption, media_type, enabled, sort_order, created_at, updated_at").
		Where("tenant_id = ? AND product_advisory_id = ?", tenantID, profile.ID).Order("sort_order, created_at, id").Find(&images).Error; err != nil {
		return productAdvisoryDetail{}, http.StatusInternalServerError, errors.New("query_failed")
	}
	for _, image := range images {
		detail.Images = append(detail.Images, productAdvisoryImageDTOFromModel(tenantID, group.ParentCode, image))
	}
	return detail, http.StatusOK, nil
}

func ensureProductAdvisory(tx *gorm.DB, tenantID string, group productCatalogGroup) (models.ProductAdvisory, error) {
	profile := models.ProductAdvisory{ID: pkg.NewUUID(), TenantID: tenantID, ParentCode: group.ParentCode, WebName: group.WebName}
	if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "tenant_id"}, {Name: "parent_code"}}, DoUpdates: clause.AssignmentColumns([]string{"web_name", "updated_at"})}).Create(&profile).Error; err != nil {
		return profile, err
	}
	var stored models.ProductAdvisory
	err := tx.Where("tenant_id = ? AND parent_code = ?", tenantID, group.ParentCode).Take(&stored).Error
	return stored, err
}

func findProductAdvisoryImage(ctx context.Context, tenantID, rawParentCode, imageID string) (models.ProductAdvisoryImage, int, error) {
	var image models.ProductAdvisoryImage
	err := db.DB.WithContext(ctx).Model(&image).Joins("JOIN product_advisories pa ON pa.id = product_advisory_images.product_advisory_id AND pa.tenant_id = product_advisory_images.tenant_id").
		Where("product_advisory_images.id = ? AND product_advisory_images.tenant_id = ? AND pa.parent_code = ?", imageID, tenantID, strings.TrimSpace(rawParentCode)).Take(&image).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return image, http.StatusNotFound, errors.New("image_not_found")
	}
	if err != nil {
		return image, http.StatusInternalServerError, errors.New("query_failed")
	}
	return image, http.StatusOK, nil
}

func productAdvisoryImageDTOFromModel(tenantID, parentCode string, image models.ProductAdvisoryImage) productAdvisoryImageDTO {
	return productAdvisoryImageDTO{ID: image.ID, URL: productAdvisoryImageURL(tenantID, parentCode, image), Source: image.Source, Caption: image.Caption, Enabled: image.Enabled, SortOrder: image.SortOrder}
}

func productAdvisoryImageURL(tenantID, parentCode string, image models.ProductAdvisoryImage) string {
	if image.Source == "scraped" {
		return image.URL
	}
	return "/api/v1/tenants/" + url.PathEscape(tenantID) + "/product-advisory/" + url.PathEscape(parentCode) + "/images/" + url.PathEscape(image.ID) + "/content"
}

func allowedProductAdvisoryImageType(mediaType string) bool {
	switch mediaType {
	case "image/jpeg", "image/png", "image/gif", "image/webp":
		return true
	default:
		return false
	}
}
