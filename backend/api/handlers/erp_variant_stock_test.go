package handlers

import (
	"context"
	"testing"
)

func TestEnrichVariantRowsWithStock(t *testing.T) {
	client, calls := setupWarehouseFixture(t)
	rows := []map[string]any{{"ma": "SP457996"}, {"ma": "SP458003"}, {"ma": "ZERO-STOCK"}, {"ma": "MISSING-STOCK"}, {"ma": "MISSING-STOCK"}}
	for len(rows) < maxVariantStockEnrichment+1 {
		rows = append(rows, map[string]any{"ma": "SP457220_DK"})
	}
	enrichVariantRowsWithStock(context.Background(), client, stockFixtureTenant, rows)

	for i, row := range rows {
		got, has := row["ton_kho"]
		if i == maxVariantStockEnrichment || row["ma"] == "MISSING-STOCK" {
			if has {
				t.Errorf("row %d should have no stock value, got %v", i, got)
			}
			continue
		}
		want := map[string]float64{"SP457996": 32, "SP458003": 4, "ZERO-STOCK": 0, "SP457220_DK": 1}[row["ma"].(string)]
		if !has || got != want {
			t.Errorf("row %d stock = %v, want %v", i, got, want)
		}
	}
	if got := calls.Load(); got != 5 {
		t.Errorf("warehouse requests = %d, want one per distinct SKU (5)", got)
	}
}
