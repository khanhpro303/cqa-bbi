package handlers

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/db/models"
	"github.com/vietbui/chat-quality-agent/engine"
	"github.com/vietbui/chat-quality-agent/pkg"
)

func respondWithWarehouseStock(c *gin.Context, client *pkg.CloudifyClient, permissions *engine.GroupPermissionContext, scope string, groups []string, term string, byLine bool) {
	ctx := c.Request.Context()
	tenantID := permissions.TenantID
	term = strings.TrimSpace(term)
	query := db.DB.WithContext(ctx).Model(&models.CachedProduct{}).Where("tenant_id = ?", tenantID)
	if byLine {
		parents := db.DB.WithContext(ctx).Model(&models.CachedProduct{}).
			Where("tenant_id = ? AND ten_dong_bo_web = ? AND ma_cha <> ''", tenantID, term).Distinct("ma_cha")
		query = query.Where("ma_cha IN (?)", parents)
	} else {
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
