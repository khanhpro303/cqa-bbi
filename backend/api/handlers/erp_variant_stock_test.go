package handlers

import (
	"context"
	"testing"
	"time"

	"github.com/vietbui/chat-quality-agent/engine"
)

// TestEnrichVariantRowsWithStock_CacheHitAttachesTonKho verifies the colour-only
// stock enrichment: every variant row in range gets a ton_kho field resolved from
// the (pre-seeded) cache, so a nil Cloudify client is never dialed.
func TestEnrichVariantRowsWithStock_CacheHitAttachesTonKho(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	cache := engine.NewInventoryStockCache(time.Minute)
	cache.Set(ctx, "tenant-1", "SKU-S", 3)
	cache.Set(ctx, "tenant-1", "SKU-M", 0)
	cache.Set(ctx, "tenant-1", "SKU-L", 11)

	rows := []map[string]interface{}{
		{"ma": "SKU-S", "size": "S"},
		{"ma": "SKU-M", "size": "M"},
		{"ma": "SKU-L", "size": "L"},
	}

	// nil client is safe because every SKU is a cache hit.
	enrichVariantRowsWithStock(ctx, nil, cache, "tenant-1", "inventory_receipt/search", false, rows)

	want := map[string]float64{"SKU-S": 3, "SKU-M": 0, "SKU-L": 11}
	for _, row := range rows {
		sku, _ := row["ma"].(string)
		got, ok := row["ton_kho"].(float64)
		if !ok {
			t.Fatalf("row %s missing ton_kho", sku)
		}
		if got != want[sku] {
			t.Errorf("row %s ton_kho = %v, want %v", sku, got, want[sku])
		}
	}
}

// TestEnrichVariantRowsWithStock_RespectsFanOutCap verifies rows beyond the
// safety cap are left untouched (no lookup attempted), which is also what keeps a
// nil client safe for the un-seeded overflow rows here.
func TestEnrichVariantRowsWithStock_RespectsFanOutCap(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	cache := engine.NewInventoryStockCache(time.Minute)

	rows := make([]map[string]interface{}, 0, maxVariantStockEnrichment+2)
	for i := 0; i < maxVariantStockEnrichment+2; i++ {
		sku := "SKU-" + string(rune('A'+i))
		// Seed only the in-cap SKUs; overflow rows must never be looked up.
		if i < maxVariantStockEnrichment {
			cache.Set(ctx, "tenant-1", sku, float64(i))
		}
		rows = append(rows, map[string]interface{}{"ma": sku})
	}

	enrichVariantRowsWithStock(ctx, nil, cache, "tenant-1", "inventory_receipt/search", false, rows)

	for i, row := range rows {
		_, has := row["ton_kho"]
		if i < maxVariantStockEnrichment && !has {
			t.Errorf("row %d (in cap) should have ton_kho", i)
		}
		if i >= maxVariantStockEnrichment && has {
			t.Errorf("row %d (beyond cap) must NOT have ton_kho", i)
		}
	}
}
