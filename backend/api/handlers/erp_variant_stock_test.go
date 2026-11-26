package handlers

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/vietbui/chat-quality-agent/engine"
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

func TestEnrichVariantRowsWithStockVisibility_PublicHidesNumbers(t *testing.T) {
	client, _ := setupWarehouseFixture(t)
	rows := []map[string]any{{"ma": "SP457996"}, {"ma": "ZERO-STOCK"}, {"ma": "MISSING-STOCK"}}
	enrichVariantRowsWithStockVisibility(context.Background(), client, stockFixtureTenant, rows, true)

	if rows[0]["in_stock"] != true || rows[0]["stock_status"] != "còn hàng" {
		t.Errorf("in-stock row = %#v", rows[0])
	}
	if rows[1]["in_stock"] != false || rows[1]["stock_status"] != "hết hàng" {
		t.Errorf("zero-stock row = %#v", rows[1])
	}
	if _, has := rows[2]["in_stock"]; has {
		t.Errorf("missing-stock row should omit availability: %#v", rows[2])
	}
	for i, row := range rows {
		for _, key := range []string{"ton_kho", "TON_KHO", "stock"} {
			if _, has := row[key]; has {
				t.Errorf("row %d leaked numeric field %q: %#v", i, key, row)
			}
		}
	}
}

func TestRespondWithMockDataV2InventoryVisibility(t *testing.T) {
	gin.SetMode(gin.TestMode)
	run := func(agentType string) string {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		respondWithMockDataV2(c, "inventory", "SP001", 10, nil, "all", "", "t", nil, "u", agentType)
		return rec.Body.String()
	}

	pub := run("public")
	if !strings.Contains(pub, `"in_stock":true`) || !strings.Contains(pub, `"stock_status":"còn hàng"`) ||
		!strings.Contains(pub, `"stock_visibility":"status_only"`) || strings.Contains(pub, `"stock":`) {
		t.Errorf("public mock inventory leaked stock count: %s", pub)
	}

	priv := run("private")
	if !strings.Contains(priv, `"stock":45.5`) || strings.Contains(priv, `"stock_visibility":"status_only"`) {
		t.Errorf("private mock inventory must keep numeric stock: %s", priv)
	}
}

func TestApplyStockVisibility(t *testing.T) {
	pubRow := map[string]interface{}{"TON_KHO": 9.0, "ton_kho": 9.0, "stock": 9.0}
	applyStockVisibility(pubRow, 4, true, true)
	if pubRow["in_stock"] != true || pubRow["stock_status"] != "còn hàng" {
		t.Fatalf("unexpected public row: %#v", pubRow)
	}
	for _, k := range []string{"TON_KHO", "ton_kho", "stock"} {
		if _, ok := pubRow[k]; ok {
			t.Fatalf("public row kept %q: %#v", k, pubRow)
		}
	}
	if hideStockQuantity(&engine.GroupPermissionContext{AgentType: "private"}) {
		t.Fatal("private agent must see numeric stock")
	}
	if !hideStockQuantity(&engine.GroupPermissionContext{AgentType: "public"}) || !hideStockQuantity(nil) {
		t.Fatal("public/nil permission must hide numeric stock")
	}
}
