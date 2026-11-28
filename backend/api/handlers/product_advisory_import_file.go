package handlers

import (
	"bytes"
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/vietbui/chat-quality-agent/api/middleware"
	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/db/models"
	"github.com/vietbui/chat-quality-agent/engine"
	"github.com/vietbui/chat-quality-agent/knowledge"
	"github.com/xuri/excelize/v2"
	"golang.org/x/text/unicode/norm"
)

const (
	maxProductAdvisoryImportBytes = 10 << 20
	maxProductAdvisoryImportRows  = 2000
	maxProductImportUnzipBytes    = 64 << 20
)

var (
	errProductImportFileRequired    = errors.New("file_required")
	errProductImportFileTooLarge    = errors.New("file_too_large")
	errProductImportUnsupportedType = errors.New("unsupported_file_type")
	errProductImportParseFailed     = errors.New("file_parse_failed")
	errProductImportEmptyWorkbook   = errors.New("empty_workbook")
	errProductImportInvalidHeader   = errors.New("invalid_header")
	errProductImportTooManyRows     = errors.New("too_many_rows")
	errProductImportEmpty           = errors.New("empty_import")
)

type productAdvisoryImportRow struct {
	RowNumber       int
	InputWebName    string
	InputParentCode string
	InputCategory   string
	SourceURL       string
}

type productAdvisoryTemplateRow struct {
	ParentCode      string `gorm:"column:parent_code"`
	WebName         string `gorm:"column:web_name"`
	ProductCategory string `gorm:"column:product_category"`
	SourceURL       string `gorm:"column:source_url"`
}

// DownloadProductAdvisoryLinkTemplate returns the tenant catalog as an XLSX
// whose existing links can be retained to deliberately re-read those pages.
func DownloadProductAdvisoryLinkTemplate(c *gin.Context) {
	tenantID := middleware.GetTenantID(c)
	tx := db.DB.WithContext(c.Request.Context())
	catalog := productAdvisoryCatalog(tx, tenantID)

	var rows []productAdvisoryTemplateRow
	if err := tx.Table("(?) AS catalog", catalog).
		Select("catalog.parent_code, catalog.web_name, catalog.product_category, COALESCE(pa.source_url, '') AS source_url").
		Joins("LEFT JOIN product_advisories pa ON pa.tenant_id = ? AND pa.group_key = catalog.group_key", tenantID).
		Order("catalog.web_name, catalog.parent_code, catalog.group_key").Scan(&rows).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "query_failed"})
		return
	}

	f := excelize.NewFile()
	defer func() { _ = f.Close() }()
	const productSheet = "San pham"
	_ = f.SetSheetName("Sheet1", productSheet)
	_, _ = f.NewSheet("Huong dan")
	for column, value := range map[string]string{
		"A1": "ten_dong_bo_web", "B1": "ma_cha", "C1": "link", "D1": "nhom_vthh",
	} {
		_ = f.SetCellValue(productSheet, column, value)
	}
	for index, row := range rows {
		excelRow := index + 2
		_ = f.SetCellValue(productSheet, fmt.Sprintf("A%d", excelRow), row.WebName)
		_ = f.SetCellValue(productSheet, fmt.Sprintf("B%d", excelRow), row.ParentCode)
		_ = f.SetCellValue(productSheet, fmt.Sprintf("C%d", excelRow), row.SourceURL)
		_ = f.SetCellValue(productSheet, fmt.Sprintf("D%d", excelRow), row.ProductCategory)
	}
	_ = f.SetColWidth(productSheet, "A", "A", 36)
	_ = f.SetColWidth(productSheet, "B", "B", 20)
	_ = f.SetColWidth(productSheet, "C", "C", 72)
	_ = f.SetColWidth(productSheet, "D", "D", 32)
	_ = f.SetPanes(productSheet, &excelize.Panes{Freeze: true, Split: false, YSplit: 1, TopLeftCell: "A2", ActivePane: "bottomLeft"})
	textStyle, _ := f.NewStyle(&excelize.Style{NumFmt: 49})
	_ = f.SetColStyle(productSheet, "A:D", textStyle)
	headerStyle, _ := f.NewStyle(&excelize.Style{Font: &excelize.Font{Bold: true, Color: "FFFFFF"}, Fill: excelize.Fill{Type: "pattern", Color: []string{"2563EB"}, Pattern: 1}})
	_ = f.SetCellStyle(productSheet, "A1", "D1", headerStyle)

	instructions := []string{
		"Điền link sản phẩm vào cột link. Hệ thống chấp nhận mã cha, SKU riêng biệt hoặc tên đồng bộ web.",
		"Nếu nhiều dòng dùng chung mã cha, hãy giữ tên đồng bộ web và nhóm VTHH trong file mẫu hoặc dùng SKU riêng biệt trong cột ma_cha.",
		"Dòng không có link sẽ được bỏ qua. Link phải dùng http hoặc https.",
		"Các link đã có sẵn trong file mẫu sẽ được đọc lại và ghi đè dữ liệu scrape cũ nếu bạn giữ nguyên chúng khi import.",
		"Ảnh scrape mới sẽ chờ duyệt trước khi chatbot sử dụng.",
		"Mỗi file được import tối đa 2.000 dòng sản phẩm. Nếu danh mục lớn hơn, hãy chia thành nhiều file tối đa 2.000 dòng.",
	}
	_ = f.SetCellValue("Huong dan", "A1", "HƯỚNG DẪN IMPORT LINK SẢN PHẨM")
	for i, instruction := range instructions {
		_ = f.SetCellValue("Huong dan", fmt.Sprintf("A%d", i+3), fmt.Sprintf("%d. %s", i+1, instruction))
	}
	_ = f.SetColWidth("Huong dan", "A", "A", 110)
	_ = f.SetCellStyle("Huong dan", "A1", "A1", headerStyle)

	buf := new(bytes.Buffer)
	if err := f.Write(buf); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "excel_write_failed"})
		return
	}
	filename := fmt.Sprintf("product-links-template-%s.xlsx", time.Now().Format("20060102-150405"))
	c.Header("Content-Disposition", "attachment; filename="+filename)
	c.DataFromReader(http.StatusOK, int64(buf.Len()), "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", bytes.NewReader(buf.Bytes()), nil)
}

func readProductAdvisoryImportFile(c *gin.Context) (string, []productAdvisoryImportRow, error) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxProductAdvisoryImportBytes+(1<<20))
	fileHeader, err := c.FormFile("file")
	if err != nil {
		var sizeErr *http.MaxBytesError
		if errors.As(err, &sizeErr) {
			return "", nil, errProductImportFileTooLarge
		}
		return "", nil, errProductImportFileRequired
	}
	if fileHeader.Size <= 0 {
		return "", nil, errProductImportEmpty
	}
	if fileHeader.Size > maxProductAdvisoryImportBytes {
		return "", nil, errProductImportFileTooLarge
	}
	fileName := filepath.Base(strings.TrimSpace(fileHeader.Filename))
	extension := strings.ToLower(filepath.Ext(fileName))
	if extension != ".xlsx" && extension != ".csv" {
		return fileName, nil, errProductImportUnsupportedType
	}
	fileName = clipProductImportText(strings.TrimSuffix(fileName, filepath.Ext(fileName)), 255-len(extension)) + extension
	file, err := fileHeader.Open()
	if err != nil {
		return fileName, nil, errProductImportParseFailed
	}
	defer func() { _ = file.Close() }()
	limited := io.LimitReader(file, maxProductAdvisoryImportBytes+1)
	var rawRows [][]string
	if extension == ".csv" {
		rawRows, err = readProductImportCSV(limited)
	} else {
		rawRows, err = readProductImportXLSX(limited)
	}
	if err != nil {
		return fileName, nil, err
	}
	rows, err := parseProductAdvisoryImportRows(rawRows)
	return fileName, rows, err
}

func readProductImportCSV(reader io.Reader) ([][]string, error) {
	data, err := io.ReadAll(reader)
	if err != nil || len(data) > maxProductAdvisoryImportBytes {
		return nil, errProductImportFileTooLarge
	}
	data = bytes.TrimPrefix(data, []byte{0xEF, 0xBB, 0xBF})
	if !utf8.Valid(data) {
		return nil, errProductImportParseFailed
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return nil, errProductImportEmpty
	}
	firstLine := data
	if newline := bytes.IndexByte(data, '\n'); newline >= 0 {
		firstLine = data[:newline]
	}
	delimiter := ','
	if bytes.Count(firstLine, []byte(";")) > bytes.Count(firstLine, []byte(",")) {
		delimiter = ';'
	}
	parser := csv.NewReader(bytes.NewReader(data))
	parser.Comma = rune(delimiter)
	parser.FieldsPerRecord = -1
	parser.TrimLeadingSpace = true
	var rows [][]string
	for {
		record, err := parser.Read()
		if errors.Is(err, io.EOF) {
			return rows, nil
		}
		if err != nil {
			return nil, errProductImportParseFailed
		}
		line, _ := parser.FieldPos(0)
		if line > maxProductAdvisoryImportRows+1 {
			return nil, errProductImportTooManyRows
		}
		for len(rows) < line-1 {
			rows = append(rows, nil)
		}
		rows = append(rows, record)
	}
}

func readProductImportXLSX(reader io.Reader) ([][]string, error) {
	f, err := excelize.OpenReader(reader, excelize.Options{UnzipSizeLimit: maxProductImportUnzipBytes, UnzipXMLSizeLimit: maxProductImportUnzipBytes})
	if err != nil {
		return nil, errProductImportParseFailed
	}
	defer func() { _ = f.Close() }()
	sheets := f.GetSheetList()
	if len(sheets) == 0 {
		return nil, errProductImportEmptyWorkbook
	}
	iter, err := f.Rows(sheets[0])
	if err != nil {
		return nil, errProductImportParseFailed
	}
	defer func() { _ = iter.Close() }()
	var rows [][]string
	for iter.Next() {
		if len(rows) == maxProductAdvisoryImportRows+1 {
			return nil, errProductImportTooManyRows
		}
		values, err := iter.Columns()
		if err != nil {
			return nil, errProductImportParseFailed
		}
		rows = append(rows, values)
	}
	if iter.Error() != nil {
		return nil, errProductImportParseFailed
	}
	return rows, nil
}

func parseProductAdvisoryImportRows(rawRows [][]string) ([]productAdvisoryImportRow, error) {
	if len(rawRows) == 0 {
		return nil, errProductImportEmpty
	}
	columns := map[string]int{"web_name": -1, "parent_code": -1, "category": -1, "link": -1}
	for index, value := range rawRows[0] {
		kind := productImportHeaderKind(value)
		if kind == "" {
			continue
		}
		if columns[kind] >= 0 {
			return nil, errProductImportInvalidHeader
		}
		columns[kind] = index
	}
	if columns["link"] < 0 || (columns["web_name"] < 0 && columns["parent_code"] < 0) {
		return nil, errProductImportInvalidHeader
	}
	if len(rawRows)-1 > maxProductAdvisoryImportRows {
		return nil, errProductImportTooManyRows
	}
	rows := make([]productAdvisoryImportRow, 0, len(rawRows)-1)
	for rowIndex, values := range rawRows[1:] {
		row := productAdvisoryImportRow{
			RowNumber:       rowIndex + 2,
			InputWebName:    productImportCell(values, columns["web_name"]),
			InputParentCode: productImportCell(values, columns["parent_code"]),
			InputCategory:   productImportCell(values, columns["category"]),
			SourceURL:       productImportCell(values, columns["link"]),
		}
		if row.InputWebName == "" && row.InputParentCode == "" && row.SourceURL == "" {
			continue
		}
		rows = append(rows, row)
	}
	if len(rows) == 0 {
		return nil, errProductImportEmpty
	}
	return rows, nil
}

func productImportCell(row []string, index int) string {
	if index < 0 || index >= len(row) {
		return ""
	}
	return strings.TrimSpace(row[index])
}

func clipProductImportText(value string, maxRunes int) string {
	value = strings.TrimSpace(value)
	runes := []rune(value)
	if len(runes) > maxRunes {
		return string(runes[:maxRunes])
	}
	return value
}

func productImportHeaderKind(header string) string {
	switch normalizeProductImportKey(header) {
	case "tendongboweb", "webname", "productname", "tensanpham":
		return "web_name"
	case "macha", "maskumacha", "sku", "parentsku", "parentcode":
		return "parent_code"
	case "nhomvthh", "listtennhomvthh", "listvthh", "productcategory":
		return "category"
	case "link", "linksanpham", "url", "producturl", "sourceurl":
		return "link"
	default:
		return ""
	}
}

func normalizeProductImportKey(value string) string {
	value = strings.ReplaceAll(strings.ToLower(strings.TrimSpace(value)), "đ", "d")
	var result strings.Builder
	for _, r := range norm.NFD.String(value) {
		if unicode.Is(unicode.Mn, r) {
			continue
		}
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			result.WriteRune(r)
		}
	}
	return result.String()
}

func normalizeProductImportName(value string) string {
	value = strings.ReplaceAll(strings.ToLower(value), "đ", "d")
	var result strings.Builder
	for _, r := range norm.NFD.String(value) {
		if !unicode.Is(unicode.Mn, r) {
			result.WriteRune(r)
		}
	}
	return strings.Join(strings.Fields(result.String()), " ")
}

func mapProductAdvisoryImportRows(ctx context.Context, tenantID string, rows []productAdvisoryImportRow) ([]models.ProductAdvisoryImportItem, error) {
	tx := db.DB.WithContext(ctx)
	var groups []productCatalogGroup
	if err := productAdvisoryCatalog(tx, tenantID).Scan(&groups).Error; err != nil {
		return nil, err
	}
	type childProduct struct {
		SKU      string `gorm:"column:sku"`
		GroupKey string `gorm:"column:group_key"`
	}
	var children []childProduct
	if err := tx.Model(&models.CachedProduct{}).
		Select("TRIM(ma) AS sku, "+models.ProductAdvisoryGroupKeySQL+" AS group_key").Distinct().
		Where("tenant_id = ? AND TRIM(ma) <> ''", tenantID).
		Scopes(engine.ExcludeDKCopies).Scan(&children).Error; err != nil {
		return nil, err
	}
	var profiles []models.ProductAdvisory
	if err := tx.Select("group_key").Where("tenant_id = ? AND group_key IS NOT NULL", tenantID).Find(&profiles).Error; err != nil {
		return nil, err
	}
	overwrite := make(map[string]bool, len(profiles))
	for _, profile := range profiles {
		overwrite[profile.GroupKey] = true
	}
	byKey := make(map[string]productCatalogGroup, len(groups))
	byParent := make(map[string][]productCatalogGroup, len(groups))
	byName := make(map[string][]productCatalogGroup)
	for _, group := range groups {
		byKey[group.GroupKey] = group
		parent := strings.ToLower(group.ParentCode)
		byParent[parent] = append(byParent[parent], group)
		name := normalizeProductImportName(group.WebName)
		if name != "" {
			byName[name] = append(byName[name], group)
		}
	}
	byChild := make(map[string][]productCatalogGroup)
	for _, child := range children {
		if group, ok := byKey[child.GroupKey]; ok {
			code := strings.ToLower(child.SKU)
			byChild[code] = append(byChild[code], group)
		}
	}

	items := make([]models.ProductAdvisoryImportItem, 0, len(rows))
	latestPending := make(map[string]int)
	for _, row := range rows {
		item := models.ProductAdvisoryImportItem{
			RowNumber: row.RowNumber, InputWebName: clipProductImportText(row.InputWebName, 255),
			InputParentCode: clipProductImportText(row.InputParentCode, 100), SourceURL: clipProductImportText(row.SourceURL, 2048),
		}
		if row.SourceURL == "" {
			item.Status, item.ErrorCode = "skipped", "empty_link"
			items = append(items, item)
			continue
		}
		source, err := knowledge.ParseSourceURL(row.SourceURL)
		if err != nil {
			item.Status, item.ErrorCode = "failed", "invalid_url"
			items = append(items, item)
			continue
		}
		if utf8.RuneCountInString(row.InputParentCode) > 100 || utf8.RuneCountInString(row.InputCategory) > 500 || utf8.RuneCountInString(row.InputWebName) > 255 {
			item.Status, item.ErrorCode = "unmatched", "identifier_too_long"
			items = append(items, item)
			continue
		}
		item.SourceURL = source.String()
		group, matchedBy, candidates := matchProductImportGroup(row, byParent, byChild, byName)
		if group.ParentCode == "" {
			item.Status = "unmatched"
			if row.InputParentCode == "" && row.InputWebName == "" {
				item.ErrorCode = "missing_product_identifier"
			} else if len(candidates) > 1 {
				item.ErrorCode = "ambiguous_web_name"
				if row.InputParentCode != "" {
					item.ErrorCode = "ambiguous_parent_code"
				}
				sort.Strings(candidates)
				item.ErrorMessage = clipProductImportText(strings.Join(candidates, ", "), 500)
			} else {
				item.ErrorCode = "product_not_found"
			}
			items = append(items, item)
			continue
		}
		item.ParentCode, item.GroupKey, item.WebName, item.MatchedBy = group.ParentCode, group.GroupKey, group.WebName, matchedBy
		item.ProductCategory = group.ProductCategory
		item.Overwrite = overwrite[group.GroupKey]
		item.Status = "pending"
		key := group.GroupKey
		if previous, exists := latestPending[key]; exists {
			items[previous].Status = "skipped"
			items[previous].ErrorCode = "duplicate_in_file"
		}
		latestPending[key] = len(items)
		items = append(items, item)
	}
	return items, nil
}

func matchProductImportGroup(row productAdvisoryImportRow, byParent, byChild, byName map[string][]productCatalogGroup) (productCatalogGroup, string, []string) {
	matchedBy := "web_name"
	matches := byName[normalizeProductImportName(row.InputWebName)]
	if code := strings.ToLower(strings.TrimSpace(row.InputParentCode)); code != "" {
		matchedBy, matches = "parent_code", byParent[code]
		if len(matches) == 0 {
			matchedBy, matches = "child_sku", byChild[code]
		}
	}
	if row.InputCategory != "" {
		var categoryMatches []productCatalogGroup
		for _, match := range matches {
			if strings.EqualFold(match.ProductCategory, row.InputCategory) {
				categoryMatches = append(categoryMatches, match)
			}
		}
		matches = categoryMatches
		if row.InputWebName != "" && matchedBy == "parent_code" {
			var nameMatches []productCatalogGroup
			for _, match := range matches {
				if normalizeProductImportName(match.WebName) == normalizeProductImportName(row.InputWebName) {
					nameMatches = append(nameMatches, match)
				}
			}
			matches = nameMatches
		}
	}
	if len(matches) == 1 {
		return matches[0], matchedBy, nil
	}
	// Prefer the literal web name to the accent-insensitive lookup when
	// several catalog groups have similar names.
	var exact []productCatalogGroup
	for _, match := range matches {
		if strings.EqualFold(match.WebName, strings.TrimSpace(row.InputWebName)) {
			exact = append(exact, match)
		}
	}
	if len(exact) == 1 {
		return exact[0], matchedBy, nil
	}
	candidates := make([]string, 0, len(matches))
	for _, match := range matches {
		candidate := match.ParentCode + " / " + match.WebName
		if match.ProductCategory != "" {
			candidate += " / " + match.ProductCategory
		}
		candidates = append(candidates, candidate)
	}
	return productCatalogGroup{}, "", candidates
}
