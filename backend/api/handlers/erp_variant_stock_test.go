package handlers

import (
	"context"
	"testing"

	"github.com/vietbui/chat-quality-agent/engine"
)

func TestEnrichVariantRowsWithStock(t *testing.T) {
	client, calls := setupWarehouseFixture(t)
	rows := []map[string]any{{"ma": "SP457996"}, {"ma": "SP458003"}, {"ma": "zero-stock"}}
	for len(rows) < maxVariantStockEnrichment+1 {
		rows = append(rows, map[string]any{"ma": "SP457220_DK"})
	}
	ctx := engine.WithWarehouseStock(context.Background(), stockFixtureTenant)
	enrichVariantRowsWithStock(ctx, client, stockFixtureTenant, rows)

	for i, want := range []float64{32, 4, 0, 1} {
		if got := rows[i]["ton_kho"]; got != want {
			t.Errorf("row %d stock = %v, want %v", i, got, want)
		}
	}
	if _, has := rows[maxVariantStockEnrichment]["ton_kho"]; has {
		t.Error("row beyond limit has stock")
	}
	if *calls != 1 {
		t.Errorf("report requests = %d, want 1", *calls)
	}
}
