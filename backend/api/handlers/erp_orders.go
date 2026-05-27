package handlers

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// isGenericOrderSearch reports whether the search string is one of the
// catch-all phrases the bot treats as "show me my orders" (with no
// specific order code). Matching is accent-insensitive on the common
// Vietnamese variants ERP customers type into Zalo.
func isGenericOrderSearch(search string) bool {
	s := strings.ToLower(strings.TrimSpace(search))
	if s == "" || s == "đơn hàng" || s == "don hang" || s == "xem đơn hàng" || s == "xem don hang" || s == "check đơn hàng" || s == "check don hang" || s == "tra cứu đơn hàng" || s == "tra cuu don hang" || s == "đơn đặt hàng" || s == "don dat hang" {
		return true
	}
	return false
}

// parseDaysFromSearch extracts a recent-window in days when the customer
// includes "3 ngày", "7 ngày", "1 tuần" or accent-stripped equivalents in
// the search query. Returns 0 when nothing matches so the caller can fall
// back to a default window.
func parseDaysFromSearch(search string) int {
	s := strings.ToLower(search)
	if strings.Contains(s, "3 ngày") || strings.Contains(s, "3 ngay") {
		return 3
	}
	if strings.Contains(s, "5 ngày") || strings.Contains(s, "5 ngay") {
		return 5
	}
	if strings.Contains(s, "7 ngày") || strings.Contains(s, "7 ngay") || strings.Contains(s, "1 tuần") || strings.Contains(s, "1 tuan") {
		return 7
	}
	return 0
}

// formatDate renders a date in DD/MM/YYYY for customer-facing replies.
func formatDate(t time.Time) string {
	return t.Format("02/01/2006")
}

// parseOrderDate pulls a date out of the heterogeneous keys ERP rows ship
// with (date / create_date / ngay_lap / ngay_ct / write_date plus
// uppercase variants), trying the most common Cloudify formats in turn.
// Returns ok=false when nothing parses, so callers can decide whether to
// hide the row or surface it without a date.
func parseOrderDate(item map[string]interface{}) (time.Time, bool) {
	keys := []string{"date", "create_date", "ngay_lap", "ngay_ct", "write_date", "NGAY_LAP", "NGAY_CT"}
	for _, k := range keys {
		if val, ok := item[k]; ok && val != nil {
			if str, ok := val.(string); ok && str != "" {
				// Try parsing different formats
				// Format 1: "2006-01-02 15:04:05"
				if t, err := time.Parse("2006-01-02 15:04:05", str); err == nil {
					return t, true
				}
				// Format 2: "2006-01-02T15:04:05Z"
				if t, err := time.Parse(time.RFC3339, str); err == nil {
					return t, true
				}
				// Format 3: "2006-01-02"
				if t, err := time.Parse("2006-01-02", str); err == nil {
					return t, true
				}
				// Format 4: "02/01/2006"
				if t, err := time.Parse("02/01/2006", str); err == nil {
					return t, true
				}
			}
		}
	}
	return time.Time{}, false
}

// OrdersStatusBucket is a per-TRANG_THAI breakdown of orders in the summary.
type OrdersStatusBucket struct {
	Status     string  `json:"status"`
	StatusName string  `json:"status_name"`
	Count      int     `json:"count"`
	Quantity   float64 `json:"quantity"`
	Value      float64 `json:"value"`
}

// OrdersSummary is the aggregate payload the LLM uses to reply to the
// customer instead of re-counting raw rows.
type OrdersSummary struct {
	TotalOrders   int                  `json:"total_orders"`
	TotalValue    float64              `json:"total_value"`
	TotalQuantity float64              `json:"total_quantity"`
	ByStatus      []OrdersStatusBucket `json:"by_status"`
}

// orderStatusName maps BBI ERP TRANG_THAI codes to the Vietnamese label
// surfaced to customers.
var orderStatusName = map[string]string{
	"0": "Hủy",
	"1": "Đang thực hiện",
	"2": "Hoàn thành",
	"3": "Đang giao",
}

// orderStatusReportOrder is the canonical order ByStatus is emitted in:
// in-flight first, finished, cancelled last. Customer-friendly framing.
var orderStatusReportOrder = []string{"3", "1", "2", "0"}

// orderStatusDisplayName resolves a TRANG_THAI code to its display label,
// falling back to "Không xác định" for empty and "Khác (mã X)" for any
// code outside the canonical set.
func orderStatusDisplayName(code string) string {
	if name, ok := orderStatusName[code]; ok {
		return name
	}
	if code == "" {
		return "Không xác định"
	}
	return fmt.Sprintf("Khác (mã %s)", code)
}

// sumOrderLineQuantity walks DON_DAT_HANG_CHI_TIET and totals SO_LUONG.
// Returns 0 when the field is missing or malformed (defensive — ERP rows
// are passthrough JSON).
func sumOrderLineQuantity(item map[string]interface{}) float64 {
	raw, ok := item["don_dat_hang_chi_tiet"]
	if !ok || raw == nil {
		raw = item["DON_DAT_HANG_CHI_TIET"]
	}
	if raw == nil {
		return 0
	}
	lines, ok := raw.([]interface{})
	if !ok {
		return 0
	}
	var total float64
	for _, line := range lines {
		m, ok := line.(map[string]interface{})
		if !ok {
			continue
		}
		total += getMapFloat(m, "SO_LUONG", "so_luong", "quantity", "SL", "sl")
	}
	return total
}

// buildOrdersSummary aggregates customer-scoped orders into per-status
// buckets plus overall totals. Buckets are ordered canonically (Đang giao →
// Đang thực hiện → Hoàn thành → Hủy) with any unknown statuses appended
// after, so the LLM's reply order is stable across runs.
func buildOrdersSummary(items []map[string]interface{}) OrdersSummary {
	summary := OrdersSummary{}
	buckets := map[string]*OrdersStatusBucket{}
	for _, item := range items {
		status := strings.TrimSpace(getMapString(item, "trang_thai", "TRANG_THAI", "status"))
		value := getMapFloat(item, "total", "TONG_TIEN", "tong_tien")
		qty := sumOrderLineQuantity(item)

		summary.TotalOrders++
		summary.TotalValue += value
		summary.TotalQuantity += qty

		bucket, exists := buckets[status]
		if !exists {
			bucket = &OrdersStatusBucket{
				Status:     status,
				StatusName: orderStatusDisplayName(status),
			}
			buckets[status] = bucket
		}
		bucket.Count++
		bucket.Quantity += qty
		bucket.Value += value
	}

	emitted := map[string]bool{}
	for _, s := range orderStatusReportOrder {
		if b, ok := buckets[s]; ok {
			summary.ByStatus = append(summary.ByStatus, *b)
			emitted[s] = true
		}
	}
	var leftovers []string
	for s := range buckets {
		if !emitted[s] {
			leftovers = append(leftovers, s)
		}
	}
	sort.Strings(leftovers)
	for _, s := range leftovers {
		summary.ByStatus = append(summary.ByStatus, *buckets[s])
	}
	return summary
}

// trimOrdersForLLM sorts items by date desc (newest first) and caps to max.
// Keeps a small recent slice so the LLM can disambiguate if the customer
// asks for a specific order, without flooding the prompt.
func trimOrdersForLLM(items []map[string]interface{}, max int) []map[string]interface{} {
	sorted := make([]map[string]interface{}, len(items))
	copy(sorted, items)
	sort.SliceStable(sorted, func(i, j int) bool {
		ti, oki := parseOrderDate(sorted[i])
		tj, okj := parseOrderDate(sorted[j])
		if !oki {
			return false
		}
		if !okj {
			return true
		}
		return ti.After(tj)
	})
	if max > 0 && len(sorted) > max {
		return sorted[:max]
	}
	return sorted
}
