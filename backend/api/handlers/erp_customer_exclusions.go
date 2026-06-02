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

// gmfLinkedCustomerCodes returns the set of customer codes (trimmed) that are
// linked to a CRM/GMF group for this tenant. These codes are protected: the
// customer cache feeds GMF "own scope" lookups, so excluding a linked code would
// break that group's data access. The exclusion handlers refuse to exclude them.
func gmfLinkedCustomerCodes(tenantID string) map[string]struct{} {
	var codes []string
	set := make(map[string]struct{})
	if err := db.DB.Model(&models.CRMGroup{}).
		Where("tenant_id = ? AND customer_code <> ''", tenantID).
		Distinct().
		Pluck("customer_code", &codes).Error; err != nil {
		return set
	}
	for _, code := range codes {
		if t := strings.TrimSpace(code); t != "" {
			set[t] = struct{}{}
		}
	}
	return set
}

// cleanCustomerCodes trims, drops blanks, and de-duplicates a raw list of
// customer codes while preserving first-seen order.
func cleanCustomerCodes(raw []string) []string {
	uniq := make(map[string]struct{}, len(raw))
	cleaned := make([]string, 0, len(raw))
	for _, s := range raw {
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
	return cleaned
}

// findGMFBlockedCodes returns, in input order, the codes that are linked to a GMF
// group and therefore cannot be excluded. An empty result means the request is
// safe to apply.
func findGMFBlockedCodes(codes []string, gmfLinked map[string]struct{}) []string {
	blocked := make([]string, 0)
	for _, code := range codes {
		if _, linked := gmfLinked[strings.TrimSpace(code)]; linked {
			blocked = append(blocked, code)
		}
	}
	return blocked
}

// ListExcludableCustomerCodes returns the candidate customer codes for the exclusion modal:
// every code currently in cached_customers, plus any already-excluded codes that
// are no longer in the cache (orphans). Each item is annotated with the current
// exclusion flag and whether the code is locked because it is linked to a GMF group.
func ListExcludableCustomerCodes(c *gin.Context) {
	tenantID := middleware.GetTenantID(c)

	type custRow struct {
		CustomerCode string `json:"customer_code"`
		HoVaTen      string `json:"ho_va_ten"`
		DienThoai    string `json:"dien_thoai"`
	}
	var rows []custRow
	err := db.DB.Table("cached_customers").
		Select("ma AS customer_code, MAX(ho_va_ten) AS ho_va_ten, MAX(dien_thoai) AS dien_thoai").
		Where("tenant_id = ? AND ma <> ''", tenantID).
		Group("ma").
		Order("ma ASC").
		Scan(&rows).Error
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "list_customers_failed", "message": err.Error()})
		return
	}

	var exclusions []models.ERPCustomerCodeExclusion
	if err := db.DB.Where("tenant_id = ?", tenantID).Find(&exclusions).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "load_exclusions_failed", "message": err.Error()})
		return
	}
	excludedAt := make(map[string]time.Time, len(exclusions))
	for _, ex := range exclusions {
		excludedAt[strings.TrimSpace(ex.CustomerCode)] = ex.UpdatedAt
	}

	gmfLinked := gmfLinkedCustomerCodes(tenantID)

	type respItem struct {
		CustomerCode string    `json:"customer_code"`
		HoVaTen      string    `json:"ho_va_ten"`
		DienThoai    string    `json:"dien_thoai"`
		IsExcluded   bool      `json:"is_excluded"`
		IsGMFLinked  bool      `json:"is_gmf_linked"`
		UpdatedAt    time.Time `json:"updated_at,omitempty"`
	}
	items := make([]respItem, 0, len(rows))
	known := make(map[string]struct{}, len(rows))
	for _, r := range rows {
		key := strings.TrimSpace(r.CustomerCode)
		known[key] = struct{}{}
		updated, excluded := excludedAt[key]
		_, linked := gmfLinked[key]
		items = append(items, respItem{
			CustomerCode: r.CustomerCode,
			HoVaTen:      r.HoVaTen,
			DienThoai:    r.DienThoai,
			IsExcluded:   excluded,
			IsGMFLinked:  linked,
			UpdatedAt:    updated,
		})
	}

	// Surface orphaned exclusions (excluded codes no longer present in the cache)
	// so admins can still un-exclude them.
	for _, ex := range exclusions {
		key := strings.TrimSpace(ex.CustomerCode)
		if _, ok := known[key]; ok {
			continue
		}
		_, linked := gmfLinked[key]
		items = append(items, respItem{
			CustomerCode: ex.CustomerCode,
			IsExcluded:   true,
			IsGMFLinked:  linked,
			UpdatedAt:    ex.UpdatedAt,
		})
	}

	c.JSON(http.StatusOK, gin.H{
		"items":          items,
		"total":          len(items),
		"excluded_count": len(exclusions),
	})
}

// DownloadCustomerCodeTemplate returns an Excel file pre-filled with every
// customer code currently in the cache and its T/F exclusion flag. GMF-linked
// codes are forced to F and annotated, since they cannot be excluded.
func DownloadCustomerCodeTemplate(c *gin.Context) {
	tenantID := middleware.GetTenantID(c)

	type custRow struct {
		CustomerCode string
		HoVaTen      string
	}
	var rows []custRow
	err := db.DB.Table("cached_customers").
		Select("ma AS customer_code, MAX(ho_va_ten) AS ho_va_ten").
		Where("tenant_id = ? AND ma <> ''", tenantID).
		Group("ma").
		Order("ma ASC").
		Scan(&rows).Error
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "list_customers_failed", "message": err.Error()})
		return
	}

	var exclusions []models.ERPCustomerCodeExclusion
	if err := db.DB.Where("tenant_id = ?", tenantID).Find(&exclusions).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "load_exclusions_failed", "message": err.Error()})
		return
	}
	excluded := make(map[string]struct{}, len(exclusions))
	for _, ex := range exclusions {
		excluded[strings.TrimSpace(ex.CustomerCode)] = struct{}{}
	}
	gmfLinked := gmfLinkedCustomerCodes(tenantID)

	f := excelize.NewFile()
	defer func() { _ = f.Close() }()
	sheet := "Sheet1"
	_ = f.SetCellValue(sheet, "A1", "MA_KH")
	_ = f.SetCellValue(sheet, "B1", "LOAI_TRU")
	_ = f.SetCellValue(sheet, "C1", "HO_TEN")
	_ = f.SetCellValue(sheet, "D1", "GHI_CHU")
	_ = f.SetCellValue(sheet, "D2", "T = loại ra, F = giữ lại")

	rowIdx := 2
	writeRow := func(code, name, flag, note string) {
		_ = f.SetCellValue(sheet, fmt.Sprintf("A%d", rowIdx), code)
		_ = f.SetCellValue(sheet, fmt.Sprintf("B%d", rowIdx), flag)
		_ = f.SetCellValue(sheet, fmt.Sprintf("C%d", rowIdx), name)
		if note != "" {
			_ = f.SetCellValue(sheet, fmt.Sprintf("D%d", rowIdx), note)
		}
		rowIdx++
	}

	known := make(map[string]struct{}, len(rows))
	for _, r := range rows {
		key := strings.TrimSpace(r.CustomerCode)
		known[key] = struct{}{}
		flag := "F"
		note := ""
		if _, locked := gmfLinked[key]; locked {
			flag = "F"
			note = "Đã link group GMF — không thể loại trừ"
		} else if _, ok := excluded[key]; ok {
			flag = "T"
		}
		writeRow(r.CustomerCode, r.HoVaTen, flag, note)
	}
	for _, ex := range exclusions {
		key := strings.TrimSpace(ex.CustomerCode)
		if _, ok := known[key]; ok {
			continue
		}
		writeRow(ex.CustomerCode, "", "T", "")
	}

	buf := new(bytes.Buffer)
	if err := f.Write(buf); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "excel_write_failed", "message": err.Error()})
		return
	}
	filename := fmt.Sprintf("customer-code-exclusions-%s.xlsx", time.Now().Format("20060102-150405"))
	c.Header("Content-Disposition", "attachment; filename="+filename)
	c.DataFromReader(http.StatusOK,
		int64(buf.Len()),
		"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
		bytes.NewReader(buf.Bytes()),
		map[string]string{},
	)
}

// ImportCustomerCodeExclusions accepts a multipart .xlsx upload with two columns:
// A=MA_KH, B=T/F flag. Before touching the database it rejects the whole import
// if any code flagged for exclusion (T) is linked to a GMF group. T rows are
// upserted, F rows removed, then the cache is pruned synchronously.
func ImportCustomerCodeExclusions(c *gin.Context) {
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

	// Pre-scan: collect codes flagged T and reject the whole import if any is
	// GMF-linked, so nothing is partially applied.
	gmfLinked := gmfLinkedCustomerCodes(tenantID)
	flaggedForExclusion := make([]string, 0)
	for i, row := range rows {
		if i == 0 || len(row) < 2 {
			continue
		}
		code := strings.TrimSpace(row[0])
		if code == "" || normalizeExclusionFlag(row[1]) != "T" {
			continue
		}
		flaggedForExclusion = append(flaggedForExclusion, code)
	}
	if blocked := findGMFBlockedCodes(flaggedForExclusion, gmfLinked); len(blocked) > 0 {
		c.JSON(http.StatusConflict, gin.H{
			"error":   "gmf_linked_blocked",
			"message": "Một số mã khách hàng đã link group GMF nên không thể loại trừ",
			"codes":   blocked,
		})
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
			code := strings.TrimSpace(row[0])
			flag := normalizeExclusionFlag(row[1])
			if code == "" || flag == "" {
				skipped++
				continue
			}

			if flag == "T" {
				var existing models.ERPCustomerCodeExclusion
				findErr := tx.Where("tenant_id = ? AND customer_code = ?", tenantID, code).First(&existing).Error
				if findErr == nil {
					if err := tx.Model(&existing).Updates(map[string]interface{}{
						"updated_at":         time.Now(),
						"updated_by_user_id": userID,
					}).Error; err != nil {
						return fmt.Errorf("update exclusion %s: %w", code, err)
					}
					continue
				}
				now := time.Now()
				if err := tx.Create(&models.ERPCustomerCodeExclusion{
					ID:              pkg.NewUUID(),
					TenantID:        tenantID,
					CustomerCode:    code,
					UpdatedByUserID: userID,
					CreatedAt:       now,
					UpdatedAt:       now,
				}).Error; err != nil {
					return fmt.Errorf("insert exclusion %s: %w", code, err)
				}
				added++
				continue
			}

			// F → remove if exists
			res := tx.Where("tenant_id = ? AND customer_code = ?", tenantID, code).
				Delete(&models.ERPCustomerCodeExclusion{})
			if res.Error != nil {
				return fmt.Errorf("delete exclusion %s: %w", code, res.Error)
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

	cacheRows, applyErr := engine.ApplyCustomerCodeExclusions(tenantID)
	if applyErr != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "apply_failed",
			"message": applyErr.Error(),
			"added":   added,
			"removed": removed,
			"skipped": skipped,
		})
		return
	}

	db.LogActivity(tenantID, userID, middleware.GetUserEmail(c),
		"erp.customer_exclusion.imported", "erp_customer_code_exclusion", "",
		fmt.Sprintf("Imported customer exclusions: added=%d removed=%d skipped=%d cache_rows=%d",
			added, removed, skipped, cacheRows),
		"", c.ClientIP())

	c.JSON(http.StatusOK, gin.H{
		"added":      added,
		"removed":    removed,
		"skipped":    skipped,
		"cache_rows": cacheRows,
	})
}

// PutCustomerCodeExclusions replaces the entire customer-code exclusion list for
// a tenant with the provided array (sent from the manual config modal). It blocks
// the whole save if any requested code is linked to a GMF group.
func PutCustomerCodeExclusions(c *gin.Context) {
	tenantID := middleware.GetTenantID(c)
	userID := middleware.GetUserID(c)

	var body struct {
		ExcludedCustomerCodes []string `json:"excluded_customer_codes"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_body", "message": err.Error()})
		return
	}

	cleaned := cleanCustomerCodes(body.ExcludedCustomerCodes)

	// Guard: refuse to exclude any GMF-linked code. Block the whole save.
	gmfLinked := gmfLinkedCustomerCodes(tenantID)
	if blocked := findGMFBlockedCodes(cleaned, gmfLinked); len(blocked) > 0 {
		c.JSON(http.StatusConflict, gin.H{
			"error":   "gmf_linked_blocked",
			"message": "Một số mã khách hàng đã link group GMF nên không thể loại trừ",
			"codes":   blocked,
		})
		return
	}

	err := db.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("tenant_id = ?", tenantID).
			Delete(&models.ERPCustomerCodeExclusion{}).Error; err != nil {
			return fmt.Errorf("clear exclusions: %w", err)
		}
		if len(cleaned) == 0 {
			return nil
		}
		now := time.Now()
		batch := make([]models.ERPCustomerCodeExclusion, 0, len(cleaned))
		for _, p := range cleaned {
			batch = append(batch, models.ERPCustomerCodeExclusion{
				ID:              pkg.NewUUID(),
				TenantID:        tenantID,
				CustomerCode:    p,
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

	cacheRows, applyErr := engine.ApplyCustomerCodeExclusions(tenantID)
	if applyErr != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":          "apply_failed",
			"message":        applyErr.Error(),
			"excluded_count": len(cleaned),
		})
		return
	}

	db.LogActivity(tenantID, userID, middleware.GetUserEmail(c),
		"erp.customer_exclusion.updated", "erp_customer_code_exclusion", "",
		fmt.Sprintf("Updated customer exclusions: count=%d cache_rows=%d", len(cleaned), cacheRows),
		"", c.ClientIP())

	c.JSON(http.StatusOK, gin.H{
		"excluded_count": len(cleaned),
		"cache_rows":     cacheRows,
	})
}
