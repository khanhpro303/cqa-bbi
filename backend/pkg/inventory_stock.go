package pkg

import (
	"fmt"
	"strings"
)

// InventoryTotalStockEndpoint is the official Cloudify endpoint that returns a
// product's stock. Each data item carries product fields plus a nested
// TON_KHO_CHI_TIET array of per-warehouse rows (TEN_KHO/SO_LUONG_TON). Only the
// "Kho Tổng" warehouse row is treated as reportable stock — never the
// SO_LUONG_TON_TONG aggregate. The documented request body is {"MA_HANG": sku}.
const InventoryTotalStockEndpoint = "danhmucvattuhanghoa/lay_ton_kho_san_pham"

// InventoryTotalWarehouseName is the warehouse whose SO_LUONG_TON is treated as
// the reportable stock. Matching is case- and space-insensitive.
const InventoryTotalWarehouseName = "Kho Tổng"

// NormalizeWarehouseName upper-cases and strips spaces so warehouse names match
// regardless of casing or incidental spacing (e.g. "1. KHO-TONG").
func NormalizeWarehouseName(name string) string {
	return strings.ReplaceAll(strings.ToUpper(strings.TrimSpace(name)), " ", "")
}

// TotalStockFromInventoryItems sums SO_LUONG_TON across the "Kho Tổng" warehouse
// rows in a lay_ton_kho_san_pham response. Warehouse rows live in the nested
// TON_KHO_CHI_TIET array; older/flat responses expose the warehouse on the item
// itself. The SO_LUONG_TON_TONG aggregate and every other warehouse are ignored.
// Returns 0 when no matching warehouse row is found.
func TotalStockFromInventoryItems(items []map[string]interface{}) float64 {
	target := NormalizeWarehouseName(InventoryTotalWarehouseName)
	var total float64

	addIfMatch := func(row map[string]interface{}) {
		if NormalizeWarehouseName(inventoryMapString(row, "TEN_KHO", "ten_kho")) == target {
			total += inventoryMapFloat(row, "SO_LUONG_TON", "so_luong_ton")
		}
	}

	for _, item := range items {
		if details, ok := item["TON_KHO_CHI_TIET"].([]interface{}); ok {
			for _, d := range details {
				if row, ok := d.(map[string]interface{}); ok {
					addIfMatch(row)
				}
			}
			continue
		}
		// Flat shape: the item itself is a warehouse row.
		addIfMatch(item)
	}
	return total
}

// TotalStockForSKU returns the live "Kho Tổng" stock for a single SKU from the
// official inventory endpoint. It is the shared path used by both the HTTP
// handler and the Zalo worker so the line-level (theo dòng) and SKU-level flows
// read identical numbers. An ERP error is propagated, never swallowed into 0.
func (c *CloudifyClient) TotalStockForSKU(sku string) (float64, error) {
	items, err := c.SearchCustomEndpointWithBody(
		InventoryTotalStockEndpoint,
		map[string]interface{}{"MA_HANG": sku},
	)
	if err != nil {
		return 0, fmt.Errorf("lay_ton_kho_san_pham MA_HANG=%s: %w", sku, err)
	}
	return TotalStockFromInventoryItems(items), nil
}

// inventoryMapString returns the first non-empty string value among keys.
func inventoryMapString(m map[string]interface{}, keys ...string) string {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			if s, ok := v.(string); ok && s != "" {
				return s
			}
		}
	}
	return ""
}

// inventoryMapFloat returns the first numeric value among keys, coercing common
// JSON shapes (float64, json.Number-as-string via fmt, int).
func inventoryMapFloat(m map[string]interface{}, keys ...string) float64 {
	for _, k := range keys {
		v, ok := m[k]
		if !ok {
			continue
		}
		switch n := v.(type) {
		case float64:
			return n
		case int:
			return float64(n)
		case int64:
			return float64(n)
		case string:
			var f float64
			if _, err := fmt.Sscanf(n, "%f", &f); err == nil {
				return f
			}
		}
	}
	return 0
}
