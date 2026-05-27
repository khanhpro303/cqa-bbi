package handlers

import (
	"bytes"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/vietbui/chat-quality-agent/api/middleware"
	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/db/models"
	"github.com/vietbui/chat-quality-agent/engine"
	"github.com/vietbui/chat-quality-agent/pkg"
	"github.com/xuri/excelize/v2"
	"gorm.io/gorm"
)

// ListParentSKUs returns every distinct parent SKU (MA_CHA) from the raw ERP
// crawl table, annotated with the current exclusion flag and a count of child
// SKUs in the raw data. The "Loại trừ dòng SP" tab uses this to drive both the
// summary view and the manual tick/untick modal.
func ListParentSKUs(c *gin.Context) {
	tenantID := middleware.GetTenantID(c)

	type parentRow struct {
		ParentSKU  string `json:"parent_sku"`
		ChildCount int    `json:"child_count"`
		NhanHieu   string `json:"nhan_hieu"`
	}
	var rows []parentRow
	err := db.DB.Table("erp_raw_products").
		Select("ma_cha AS parent_sku, COUNT(*) AS child_count, MAX(nhan_hieu_name) AS nhan_hieu").
		Where("tenant_id = ? AND ma_cha <> ''", tenantID).
		Group("ma_cha").
		Order("ma_cha ASC").
		Scan(&rows).Error
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "list_parents_failed", "message": err.Error()})
		return
	}

	var exclusions []models.ERPParentSKUExclusion
	if err := db.DB.Where("tenant_id = ?", tenantID).Find(&exclusions).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "load_exclusions_failed", "message": err.Error()})
		return
	}
	excludedAt := make(map[string]time.Time, len(exclusions))
	for _, ex := range exclusions {
		excludedAt[strings.TrimSpace(ex.ParentSKU)] = ex.UpdatedAt
	}

	type respItem struct {
		ParentSKU  string    `json:"parent_sku"`
		ChildCount int       `json:"child_count"`
		NhanHieu   string    `json:"nhan_hieu"`
		IsExcluded bool      `json:"is_excluded"`
		UpdatedAt  time.Time `json:"updated_at,omitempty"`
	}
	items := make([]respItem, 0, len(rows))
	for _, r := range rows {
		key := strings.TrimSpace(r.ParentSKU)
		updated, excluded := excludedAt[key]
		items = append(items, respItem{
			ParentSKU:  r.ParentSKU,
			ChildCount: r.ChildCount,
			NhanHieu:   r.NhanHieu,
			IsExcluded: excluded,
			UpdatedAt:  updated,
		})
	}

	// Some exclusions may reference parents that no longer exist in raw (e.g.,
	// ERP removed them). Surface them so admins can still see/clean up.
	known := make(map[string]struct{}, len(rows))
	for _, r := range rows {
		known[strings.TrimSpace(r.ParentSKU)] = struct{}{}
	}
	for _, ex := range exclusions {
		key := strings.TrimSpace(ex.ParentSKU)
		if _, ok := known[key]; ok {
			continue
		}
		items = append(items, respItem{
			ParentSKU:  ex.ParentSKU,
			ChildCount: 0,
			NhanHieu:   "",
			IsExcluded: true,
			UpdatedAt:  ex.UpdatedAt,
		})
	}

	c.JSON(http.StatusOK, gin.H{
		"items":          items,
		"total":          len(items),
		"excluded_count": len(exclusions),
	})
}

// DownloadParentSKUTemplate returns an Excel file pre-filled with every parent
// SKU currently in the raw ERP table and its T/F exclusion flag, so admins can
// edit and re-upload it.
func DownloadParentSKUTemplate(c *gin.Context) {
	tenantID := middleware.GetTenantID(c)

	type parentRow struct {
		ParentSKU string
		NhanHieu  string
	}
	var rows []parentRow
	err := db.DB.Table("erp_raw_products").
		Select("ma_cha AS parent_sku, MAX(nhan_hieu_name) AS nhan_hieu").
		Where("tenant_id = ? AND ma_cha <> ''", tenantID).
		Group("ma_cha").
		Order("ma_cha ASC").
		Scan(&rows).Error
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "list_parents_failed", "message": err.Error()})
		return
	}

	var exclusions []models.ERPParentSKUExclusion
	if err := db.DB.Where("tenant_id = ?", tenantID).Find(&exclusions).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "load_exclusions_failed", "message": err.Error()})
		return
	}
	excluded := make(map[string]struct{}, len(exclusions))
	for _, ex := range exclusions {
		excluded[strings.TrimSpace(ex.ParentSKU)] = struct{}{}
	}

	f := excelize.NewFile()
	defer func() { _ = f.Close() }()
	sheet := "Sheet1"
	_ = f.SetCellValue(sheet, "A1", "SKU_CHA")
	_ = f.SetCellValue(sheet, "B1", "LOAI_TRU")
	_ = f.SetCellValue(sheet, "C1", "NHAN_HIEU")
	_ = f.SetCellValue(sheet, "D1", "GHI_CHU")
	_ = f.SetCellValue(sheet, "D2", "T = loại ra, F = giữ lại")

	rowIdx := 2
	for _, r := range rows {
		flag := "F"
		if _, ok := excluded[strings.TrimSpace(r.ParentSKU)]; ok {
			flag = "T"
		}
		_ = f.SetCellValue(sheet, fmt.Sprintf("A%d", rowIdx), r.ParentSKU)
		_ = f.SetCellValue(sheet, fmt.Sprintf("B%d", rowIdx), flag)
		_ = f.SetCellValue(sheet, fmt.Sprintf("C%d", rowIdx), r.NhanHieu)
		rowIdx++
	}

	known := make(map[string]struct{}, len(rows))
	for _, r := range rows {
		known[strings.TrimSpace(r.ParentSKU)] = struct{}{}
	}
	for _, ex := range exclusions {
		if _, ok := known[strings.TrimSpace(ex.ParentSKU)]; ok {
			continue
		}
		_ = f.SetCellValue(sheet, fmt.Sprintf("A%d", rowIdx), ex.ParentSKU)
		_ = f.SetCellValue(sheet, fmt.Sprintf("B%d", rowIdx), "T")
		rowIdx++
	}

	buf := new(bytes.Buffer)
	if err := f.Write(buf); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "excel_write_failed", "message": err.Error()})
		return
	}
	filename := fmt.Sprintf("parent-sku-exclusions-%s.xlsx", time.Now().Format("20060102-150405"))
	c.Header("Content-Disposition", "attachment; filename="+filename)
	c.DataFromReader(http.StatusOK,
		int64(buf.Len()),
		"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
		bytes.NewReader(buf.Bytes()),
		map[string]string{},
	)
}

// ImportParentSKUExclusions accepts a multipart .xlsx upload with two columns:
// A=SKU_CHA, B=T/F flag. T rows are upserted into the exclusion table,
// F rows are removed. Cache is rebuilt synchronously after the transaction.
func ImportParentSKUExclusions(c *gin.Context) {
	tenantID := middleware.GetTenantID(c)
	userID := middleware.GetUserID(c)

	fileHeader, err := c.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "file_required", "message": err.Error()})
		return
	}
	if fileHeader.Size > 10*1024*1024 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "file_too_large", "message": "tối đa 10MB"})
		return
	}

	src, err := fileHeader.Open()
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "open_file_failed", "message": err.Error()})
		return
	}
	defer func() { _ = src.Close() }()

	xf, err := excelize.OpenReader(src)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "excel_parse_failed", "message": err.Error()})
		return
	}
	defer func() { _ = xf.Close() }()

	sheets := xf.GetSheetList()
	if len(sheets) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "empty_workbook"})
		return
	}
	rows, err := xf.GetRows(sheets[0])
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "excel_read_rows_failed", "message": err.Error()})
		return
	}

	added := 0
	removed := 0
	skipped := 0

	err = db.DB.Transaction(func(tx *gorm.DB) error {
		for i, row := range rows {
			if i == 0 {
				continue // header
			}
			if len(row) < 2 {
				skipped++
				continue
			}
			parent := strings.TrimSpace(row[0])
			flag := normalizeExclusionFlag(row[1])
			if parent == "" || flag == "" {
				skipped++
				continue
			}

			if flag == "T" {
				var existing models.ERPParentSKUExclusion
				findErr := tx.Where("tenant_id = ? AND parent_sku = ?", tenantID, parent).First(&existing).Error
				if findErr == nil {
					if err := tx.Model(&existing).Updates(map[string]interface{}{
						"updated_at":         time.Now(),
						"updated_by_user_id": userID,
					}).Error; err != nil {
						return fmt.Errorf("update exclusion %s: %w", parent, err)
					}
					continue
				}
				now := time.Now()
				if err := tx.Create(&models.ERPParentSKUExclusion{
					ID:              pkg.NewUUID(),
					TenantID:        tenantID,
					ParentSKU:       parent,
					UpdatedByUserID: userID,
					CreatedAt:       now,
					UpdatedAt:       now,
				}).Error; err != nil {
					return fmt.Errorf("insert exclusion %s: %w", parent, err)
				}
				added++
				continue
			}

			// F → remove if exists
			res := tx.Where("tenant_id = ? AND parent_sku = ?", tenantID, parent).
				Delete(&models.ERPParentSKUExclusion{})
			if res.Error != nil {
				return fmt.Errorf("delete exclusion %s: %w", parent, res.Error)
			}
			if res.RowsAffected > 0 {
				removed++
			}
		}
		return nil
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "import_failed", "message": err.Error()})
		return
	}

	cacheRows, rebuildErr := engine.RebuildCachedProductsFromRaw(tenantID)
	if rebuildErr != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "rebuild_failed",
			"message": rebuildErr.Error(),
			"added":   added,
			"removed": removed,
			"skipped": skipped,
		})
		return
	}

	db.LogActivity(tenantID, userID, middleware.GetUserEmail(c),
		"erp.exclusion.imported", "erp_parent_sku_exclusion", "",
		fmt.Sprintf("Imported exclusions: added=%d removed=%d skipped=%d cache_rows=%d",
			added, removed, skipped, cacheRows),
		"", c.ClientIP())

	c.JSON(http.StatusOK, gin.H{
		"added":      added,
		"removed":    removed,
		"skipped":    skipped,
		"cache_rows": cacheRows,
	})
}

// PutParentSKUExclusions replaces the entire exclusion list for a tenant with
// the provided array of parent SKUs (sent from the manual config modal).
func PutParentSKUExclusions(c *gin.Context) {
	tenantID := middleware.GetTenantID(c)
	userID := middleware.GetUserID(c)

	var body struct {
		ExcludedParentSKUs []string `json:"excluded_parent_skus"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_body", "message": err.Error()})
		return
	}

	uniq := make(map[string]struct{}, len(body.ExcludedParentSKUs))
	cleaned := make([]string, 0, len(body.ExcludedParentSKUs))
	for _, s := range body.ExcludedParentSKUs {
		p := strings.TrimSpace(s)
		if p == "" {
			continue
		}
		if _, ok := uniq[p]; ok {
			continue
		}
		uniq[p] = struct{}{}
		cleaned = append(cleaned, p)
	}

	err := db.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("tenant_id = ?", tenantID).
			Delete(&models.ERPParentSKUExclusion{}).Error; err != nil {
			return fmt.Errorf("clear exclusions: %w", err)
		}
		if len(cleaned) == 0 {
			return nil
		}
		now := time.Now()
		batch := make([]models.ERPParentSKUExclusion, 0, len(cleaned))
		for _, p := range cleaned {
			batch = append(batch, models.ERPParentSKUExclusion{
				ID:              pkg.NewUUID(),
				TenantID:        tenantID,
				ParentSKU:       p,
				UpdatedByUserID: userID,
				CreatedAt:       now,
				UpdatedAt:       now,
			})
		}
		return tx.Create(&batch).Error
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "save_failed", "message": err.Error()})
		return
	}

	cacheRows, rebuildErr := engine.RebuildCachedProductsFromRaw(tenantID)
	if rebuildErr != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":          "rebuild_failed",
			"message":        rebuildErr.Error(),
			"excluded_count": len(cleaned),
		})
		return
	}

	db.LogActivity(tenantID, userID, middleware.GetUserEmail(c),
		"erp.exclusion.updated", "erp_parent_sku_exclusion", "",
		fmt.Sprintf("Updated exclusions: count=%d cache_rows=%d", len(cleaned), cacheRows),
		"", c.ClientIP())

	c.JSON(http.StatusOK, gin.H{
		"excluded_count": len(cleaned),
		"cache_rows":     cacheRows,
	})
}

// normalizeExclusionFlag accepts T/F/true/false/1/0 (case-insensitive) and
// returns "T", "F", or "" for invalid values. Empty/blank returns "".
func normalizeExclusionFlag(raw string) string {
	v := strings.ToLower(strings.TrimSpace(raw))
	switch v {
	case "t", "true", "1", "yes", "y", "loai", "loại":
		return "T"
	case "f", "false", "0", "no", "n", "giu", "giữ":
		return "F"
	}
	return ""
}
