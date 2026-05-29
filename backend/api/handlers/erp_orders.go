package handlers

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
)

// orderCodeRe matches an ERP order code: the "ĐH"/"DH" prefix followed by
// digits (e.g. "ĐH000016"). The (?i) flag folds Unicode case, so it also
// matches "đh"/"dh"/"DH" variants. Used to route a customer message that
// carries a concrete order code into the single-order lookup branch.
var orderCodeRe = regexp.MustCompile(`(?i)[ĐD]H\d+`)

// extractOrderCode returns the first order code found anywhere in the search
// string (so "đơn ĐH000016 sao rồi" still resolves), or "" when none is
// present — letting the caller fall through to the generic / date-window
// branches.
func extractOrderCode(search string) string {
	return strings.TrimSpace(orderCodeRe.FindString(search))
}

// isOrderAuthorized reports whether an order with customer code itemCustCode
// is visible under the given scope. own → only the verified customer's own
// code; assigned → any code in allowedCodes (compared on the bare code);
// all → always visible (internal staff). Any other scope denies by default.
func isOrderAuthorized(itemCustCode, scopeType, ownCode string, allowedCodes []string) bool {
	switch scopeType {
	case "own":
		return strings.EqualFold(itemCustCode, ownCode)
	case "assigned":
		for _, ac := range allowedCodes {
			if strings.EqualFold(leadingCustomerCode(ac), itemCustCode) {
				return true
			}
		}
		return false
	case "all":
		return true
	default:
		return false
	}
}

// normalizeOrderRecord maps a raw saorders/search row onto the canonical
// fields the LLM consumes. order_id now also reads SO_DON_HANG (the order
// code customers quote), and status_name carries the Vietnamese label so a
// single-order reply (and enumerated orders[]) can render status without the
// summary. saorders/search has no precomputed total — derive it from the
// line items via computeOrderTotal.
func normalizeOrderRecord(item map[string]interface{}) map[string]interface{} {
	status := getMapString(item, "TRANG_THAI", "trang_thai", "status")
	return map[string]interface{}{
		"order_id":              getMapString(item, "SO_DON_HANG", "so_don_hang", "MA_HOA_DON", "ma_hoa_don", "MA_SO", "ma_so", "MA", "ma", "order_id", "name", "id"),
		"customer_name":         getMapString(item, "TEN_KHACH_HANG", "ten_khach_hang", "TEN_KH", "ten_kh", "TEN_DT", "ten_dt", "customer_name"),
		"customer_code":         orderCustomerCode(item),
		"status":                status,
		"trang_thai":            getMapString(item, "TRANG_THAI", "trang_thai"),
		"status_name":           orderStatusDisplayName(status),
		"ghi_chu":               getMapString(item, "GHI_CHU", "ghi_chu"),
		"don_dat_hang_chi_tiet": item["DON_DAT_HANG_CHI_TIET"],
		"total":                 computeOrderTotal(item),
		"date":                  getMapString(item, "THOI_GIAN_TAO", "thoi_gian_tao", "NGAY_LAP", "ngay_lap", "date"),
	}
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
	keys := []string{"date", "create_date", "ngay_lap", "ngay_ct", "write_date", "NGAY_LAP", "NGAY_CT", "THOI_GIAN_TAO", "thoi_gian_tao"}
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

// leadingCustomerCode strips the "<code> - <name>" label Cloudify ships in
// MA_KHACH_HANG / customer codes down to the bare code (e.g.
// "S052 - Phượt 4P" → "S052"). A bare code passes through unchanged.
func leadingCustomerCode(s string) string {
	s = strings.TrimSpace(s)
	if idx := strings.Index(s, " - "); idx >= 0 {
		return strings.TrimSpace(s[:idx])
	}
	return s
}

// orderCustomerCode extracts the customer code from a raw saorders/search
// row. The official endpoint ships MA_KHACH_HANG as a 2-element array
// [id, "CODE - Name"]; older/flat payloads use a plain string or the legacy
// MA_KH / MA_DT aliases. Returns the bare code so it can be compared against
// the verified customer's CustomerCode.
func orderCustomerCode(item map[string]interface{}) string {
	if raw, ok := item["MA_KHACH_HANG"]; ok && raw != nil {
		switch v := raw.(type) {
		case string:
			if code := leadingCustomerCode(v); code != "" {
				return code
			}
		case []interface{}:
			// Prefer the labelled element ([1] = "CODE - Name"); fall back to
			// the numeric id ([0]) when no label is present.
			if len(v) >= 2 {
				if s, ok := v[1].(string); ok {
					if code := leadingCustomerCode(s); code != "" {
						return code
					}
				}
			}
			if len(v) >= 1 && v[0] != nil {
				return strings.TrimSpace(fmt.Sprintf("%v", v[0]))
			}
		}
	}
	// Legacy flat keys from older ERP shapes.
	return getMapString(item, "MA_KH", "ma_kh", "MA_DT", "ma_dt", "customer_code", "partner_code")
}

// computeOrderTotal derives an order's value from its line items
// (Σ SO_LUONG × DON_GIA) minus the invoice-level discount GIAM_GIA_HOA_DON.
// saorders/search carries no precomputed total field, so summing the lines
// is the only correct source. Clamps to 0 so a discount that exceeds the
// line total never yields a negative value.
func computeOrderTotal(item map[string]interface{}) float64 {
	raw, ok := item["DON_DAT_HANG_CHI_TIET"]
	if !ok || raw == nil {
		raw = item["don_dat_hang_chi_tiet"]
	}
	var total float64
	if lines, ok := raw.([]interface{}); ok {
		for _, line := range lines {
			m, ok := line.(map[string]interface{})
			if !ok {
				continue
			}
			qty := getMapFloat(m, "SO_LUONG", "so_luong", "quantity", "SL", "sl")
			price := getMapFloat(m, "DON_GIA", "don_gia", "price", "gia")
			total += qty * price
		}
	}
	total -= getMapFloat(item, "GIAM_GIA_HOA_DON", "giam_gia_hoa_don")
	if total < 0 {
		total = 0
	}
	return total
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
