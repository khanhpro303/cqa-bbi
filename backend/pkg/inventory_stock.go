package pkg

import (
	"fmt"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
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

// NormalizeWarehouseName folds a warehouse name to a comparison key so the
// configured "Kho Tổng" matches whatever the ERP actually returns. The BBI
// Cloudify response labels the total warehouse "KHO-TONG" (ASCII, hyphenated,
// no diacritics) inside TON_KHO_CHI_TIET, while the constant carries Vietnamese
// diacritics and a space. Folding strips diacritics (Tổng→TONG, đ→D), upper-cases
// letters, and drops every non-alphanumeric rune (spaces, hyphens, dots) so both
// reduce to "KHOTONG". Equality is still required after folding, so a different
// warehouse like "KHO-TONG-HN" ("KHOTONGHN") will NOT match.
func NormalizeWarehouseName(name string) string {
	var b strings.Builder
	for _, r := range norm.NFD.String(name) {
		switch {
		case unicode.Is(unicode.Mn, r): // combining diacritic mark from NFD
			continue
		case r == 'đ' || r == 'Đ': // đ does not decompose under NFD
			b.WriteRune('D')
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(unicode.ToUpper(r))
		}
	}
	return b.String()
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

// InventoryStockRequestBody builds the documented request body for an inventory
// endpoint. The official total-stock endpoint takes only {"MA_HANG": sku}; any
// other custom POST endpoint defaults to MA_HANG + limit.
func InventoryStockRequestBody(endpoint, sku string, limit int) map[string]interface{} {
	if endpoint == InventoryTotalStockEndpoint {
		return map[string]interface{}{"MA_HANG": sku}
	}
	return map[string]interface{}{"limit": limit, "MA_HANG": sku}
}

// InventoryStock returns live ERP stock for a single SKU from the given
// endpoint, honoring the tenant's Global HTTP Method config (endpoint + POST vs
// GET) instead of hardcoding one path. The official total-stock endpoint reads
// only the "Kho Tổng" warehouse row; any custom endpoint sums stock across the
// returned rows — identical semantics to the HTTP handler's per-SKU fetch. This
// is the shared path used by both the handler and the Zalo worker so line-level
// (theo dòng) and SKU-level flows read identical numbers. An ERP error is
// propagated, never swallowed into 0.
func (c *CloudifyClient) InventoryStock(endpoint string, usePost bool, sku string) (float64, error) {
	body := InventoryStockRequestBody(endpoint, sku, 100)

	var items []map[string]interface{}
	var err error
	if usePost {
		items, err = c.SearchCustomEndpointWithBody(endpoint, body)
	} else {
		params := make(map[string]string, len(body))
		for k, v := range body {
			params[k] = fmt.Sprintf("%v", v)
		}
		items, err = c.SearchCustomEndpoint(endpoint, params)
	}
	if err != nil {
		return 0, fmt.Errorf("inventory stock %s MA_HANG=%s: %w", endpoint, sku, err)
	}

	if endpoint == InventoryTotalStockEndpoint {
		return TotalStockFromInventoryItems(items), nil
	}
	// Custom endpoint: sum stock across the returned rows.
	var total float64
	for _, item := range items {
		total += inventoryMapFloat(item,
			"stock", "ton", "ton_kho",
			"SO_LUONG_TON_KHA_DUNG", "so_luong_ton_kha_dung",
			"SO_LUONG_TON_TONG", "so_luong_ton_tong")
	}
	return total, nil
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
