package handlers

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/db/models"
	"github.com/vietbui/chat-quality-agent/pkg"
)

// TestSearchProductsByWebNameFromCache_ExactSKUShortCircuits is the regression
// guard for the "Shiba đen bóng size XXL → SP461294" bug: when the agent already
// resolved a question to a specific child SKU code and passes it as the search
// keyword, the inventory resolver must surface that code as specificSKU (the
// direct-answer path) instead of fuzzy-expanding it to the parent line and
// firing the redundant dòng-vs-SKU picker.
func TestSearchProductsByWebNameFromCache_ExactSKUShortCircuits(t *testing.T) {
	dsn := os.Getenv("TEST_DB_DSN")
	if dsn == "" {
		dsn = "cqa:cqa_password@tcp(127.0.0.1:3306)/cqa?charset=utf8mb4&parseTime=True&loc=UTC"
	}
	if err := db.Connect(dsn, false); err != nil {
		t.Skip("Skipping TestSearchProductsByWebNameFromCache_ExactSKUShortCircuits: database not available")
		return
	}
	defer db.Close()

	if err := db.DB.AutoMigrate(&models.CachedProduct{}); err != nil {
		t.Skipf("Skipping: cannot migrate cached_products: %v", err)
		return
	}

	tenantID := "testten-" + pkg.NewUUID()[:8]
	const (
		webName    = "Shiba"
		parentCode = "MACHA-SHIBA"
		exactSKU   = "SP461294" // Shiba đen bóng XXL
	)

	// One product line (shared ma_cha) with several sibling SKUs. A bare web-name
	// LIKE on "Shiba" pulls all of them — that is what used to trigger the picker.
	now := time.Now()
	rows := []models.CachedProduct{
		{ID: pkg.NewUUID(), TenantID: tenantID, MA: "SP461290", TEN_DONG_BO_WEB: webName, TEN: "Mũ Shiba", THUOC_TINH_1: "Đen bóng", THUOC_TINH_2: "L", MA_CHA: parentCode, CreatedAt: now, UpdatedAt: now},
		{ID: pkg.NewUUID(), TenantID: tenantID, MA: "SP461292", TEN_DONG_BO_WEB: webName, TEN: "Mũ Shiba", THUOC_TINH_1: "Đen bóng", THUOC_TINH_2: "XL", MA_CHA: parentCode, CreatedAt: now, UpdatedAt: now},
		{ID: pkg.NewUUID(), TenantID: tenantID, MA: exactSKU, TEN_DONG_BO_WEB: webName, TEN: "Mũ Shiba", THUOC_TINH_1: "Đen bóng", THUOC_TINH_2: "XXL", MA_CHA: parentCode, CreatedAt: now, UpdatedAt: now},
	}
	for _, r := range rows {
		if err := db.DB.Create(&r).Error; err != nil {
			t.Fatalf("seed cached_product %s: %v", r.MA, err)
		}
	}
	defer db.DB.Exec("DELETE FROM cached_products WHERE tenant_id = ?", tenantID)

	ctx := context.Background()

	// Exact SKU code → must short-circuit to specificSKU, no family rows.
	products, specificSKU, err := searchProductsByWebNameFromCache(ctx, tenantID, exactSKU)
	if err != nil {
		t.Fatalf("exact-SKU search returned error: %v", err)
	}
	if specificSKU != exactSKU {
		t.Errorf("exact SKU %q: expected specificSKU=%q, got specificSKU=%q (rows=%d) — the dòng-vs-SKU picker would fire", exactSKU, exactSKU, specificSKU, len(products))
	}
	if len(products) != 0 {
		t.Errorf("exact SKU %q: expected 0 family rows on the direct-answer path, got %d", exactSKU, len(products))
	}

	// Bare web-name keyword still returns the family (unchanged behavior, so the
	// legitimate dòng-vs-SKU picker for an unresolved line keeps working).
	famRows, famSpecific, err := searchProductsByWebNameFromCache(ctx, tenantID, webName)
	if err != nil {
		t.Fatalf("web-name search returned error: %v", err)
	}
	if famSpecific != "" {
		t.Errorf("web-name %q: expected no specificSKU, got %q", webName, famSpecific)
	}
	if len(famRows) != len(rows) {
		t.Errorf("web-name %q: expected %d family rows, got %d", webName, len(rows), len(famRows))
	}
}
