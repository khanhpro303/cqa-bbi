package handlers

import (
	"context"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/db/models"
	"github.com/vietbui/chat-quality-agent/engine"
	"github.com/vietbui/chat-quality-agent/pkg"
)

// warehouseStockMode selects which SKUs respondWithWarehouseStock sums.
type warehouseStockMode int

const (
	// warehouseBySKU reads one exact SKU.
	warehouseBySKU warehouseStockMode = iota
	// warehouseByLine sums every SKU under the parent codes of a web line name.
	warehouseByLine
	// warehouseByParent sums every SKU under one parent code (ma_cha).
	warehouseByParent
)

// isParentProductCode reports whether code is the parent (ma_cha) of more
// than one cached SKU. Cloudify often reuses the first variant's SKU as the
// parent code (LS2 FF818: ma_cha SP462693 is also the SKU of P.White M), so an
// exact-SKU match alone must not decide that the agent asked for one variant.
func isParentProductCode(ctx context.Context, tenantID, code string) bool {
	code = strings.TrimSpace(code)
	if code == "" {
		return false
	}
	// Its "_DK" copy parent's SKUs belong to the same line: a parent with one
	// regular SKU plus copies is still a line, not a single variant.
	var count int64
	db.DB.WithContext(ctx).Model(&models.CachedProduct{}).
		Where("tenant_id = ? AND ma_cha IN ?", tenantID, []string{code, engine.DKCopyParentOf(code)}).Count(&count)
	return count > 1
}

func respondWithWarehouseStock(c *gin.Context, client *pkg.CloudifyClient, permissions *engine.GroupPermissionContext, scope string, groups []string, term string, mode warehouseStockMode) {
	ctx := c.Request.Context()
	tenantID := permissions.TenantID
	term = strings.TrimSpace(term)
	byLine := mode != warehouseBySKU
	query := db.DB.WithContext(ctx).Model(&models.CachedProduct{}).Where("tenant_id = ?", tenantID)
	switch mode {
	case warehouseByLine:
		parents := db.DB.WithContext(ctx).Model(&models.CachedProduct{}).
			Where("tenant_id = ? AND ten_dong_bo_web = ? AND ma_cha <> ''", tenantID, term).Distinct("ma_cha")
		query = query.Where("ma_cha IN (?)", parents)
	case warehouseByParent:
		// A parent's line also owns the stock of its "_DK" copy parent, which
		// the catalog no longer lists separately.
		query = query.Where("ma_cha IN ?", []string{term, engine.DKCopyParentOf(term)})
	default:
		query = query.Where("ma = ?", term)
	}
	var catalog []models.CachedProduct
	if err := query.Find(&catalog).Error; err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "catalog_unavailable"})
		return
	}

	brands, allBrands := permissions.ResolveBrandFilter("inventory")
	products := filterWarehouseProducts(cachedProductsToMaps(catalog), groups, brands, allBrands)
	if len(products) == 0 {
		writeAuditLog(tenantID, permissions, "inventory", scope, groups, term, http.StatusOK, 0, c.ClientIP())
		c.JSON(http.StatusOK, gin.H{"status": "success", "data": []gin.H{}, "count": 0, "warehouse": engine.MainWarehouseCode})
		return
	}

	totals, err := engine.MainWarehouseStock(ctx, tenantID, client)
	if err != nil {
		writeAuditLog(tenantID, permissions, "inventory", scope, groups, term, http.StatusBadGateway, 0, c.ClientIP())
		c.JSON(http.StatusBadGateway, gin.H{"error": "warehouse_stock_unavailable"})
		return
	}
	seen := make(map[string]struct{}, len(products))
	var total float64
	for _, product := range products {
		code := getMapString(product, "MA", "ma")
		if _, duplicate := seen[code]; duplicate || code == "" {
			continue
		}
		seen[code] = struct{}{}
		total += totals[code]
	}

	item := gin.H{"name": term, "ton_kho": total}
	if byLine {
		item["line_name"] = term
		item["sku_count"] = len(seen)
		if mode == warehouseByParent {
			item["parent_code"] = term
			if web := getMapString(products[0], "TEN_DONG_BO_WEB", "ten_dong_bo_web"); web != "" {
				item["name"], item["line_name"] = web, web
			}
		}
	} else {
		item["ma"] = getMapString(products[0], "MA", "ma")
		item["ten"] = getMapString(products[0], "TEN", "ten")
	}
	writeAuditLog(tenantID, permissions, "inventory", scope, groups, term, http.StatusOK, 1, c.ClientIP())
	c.JSON(http.StatusOK, gin.H{"status": "success", "data": []gin.H{item}, "count": 1, "warehouse": engine.MainWarehouseCode})
}

func filterWarehouseProducts(products []map[string]any, groups, brands []string, allBrands bool) []map[string]any {
	filtered := make([]map[string]any, 0, len(products))
	for _, product := range products {
		group := getMapString(product, "LIST_TEN_NHOM_VTHH", "list_ten_nhom_vthh", "group")
		brand := getMapString(product, "NHAN_HIEU_NAME", "nhan_hieu_name", "brand")
		if engine.WarehouseProductAllowed(group, brand, groups, brands, allBrands) {
			filtered = append(filtered, product)
		}
	}
	return filtered
}
