// Package cloudifyorders notices new and changed Cloudify ERP orders and tells
// the customer on Zalo.
//
// Cloudify's REST API (saorders/search) has no webhook and no "modified at"
// field, so changes are found by diffing: every poll fetches the orders of a
// recent date window, hashes each row and compares the hash with the stored
// snapshot. Any source of order rows (the poller, the /webhooks/cloudify
// endpoint, or a future realtime listener) goes through the same Ingest step,
// which stores snapshots and writes one outbox event per change. Delivery
// reads the outbox, so a change is messaged at most once even when two
// sources see it.
package cloudifyorders

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
)

// Event kinds.
const (
	KindCreated       = "created"
	KindStatusChanged = "status_changed"
	KindUpdated       = "updated"
)

// Sources.
const (
	SourcePoll    = "poll"
	SourceWebhook = "webhook"
)

// statusNames maps BBI's TRANG_THAI codes to the label shown to customers.
// Keep in sync with api/handlers/erp_orders.go orderStatusName.
var statusNames = map[string]string{
	"0": "Hủy",
	"1": "Đang thực hiện",
	"2": "Hoàn thành",
	"3": "Đang giao",
}

// StatusName returns the customer-facing label of a TRANG_THAI code.
func StatusName(code string) string {
	if name, ok := statusNames[code]; ok {
		return name
	}
	if code == "" {
		return "Không xác định"
	}
	return "Khác (mã " + code + ")"
}

// Order is the part of a saorders/search row the watcher needs.
type Order struct {
	ID           int64
	Code         string
	CustomerCode string
	CustomerName string
	Status       string
	Date         string
	Total        float64
	Fingerprint  string
}

// ParseOrder reads one raw saorders/search row. It returns false for rows
// without a usable numeric id.
func ParseOrder(raw map[string]any) (Order, bool) {
	id, ok := asInt64(raw["id"])
	if !ok || id <= 0 {
		return Order{}, false
	}
	order := Order{
		ID:           id,
		Code:         firstString(raw, "MA_HOA_DON", "SO_DON_HANG", "name"),
		CustomerCode: customerCode(raw["MA_KHACH_HANG"]),
		CustomerName: firstString(raw, "TEN_KHACH_HANG"),
		Status:       firstString(raw, "TRANG_THAI", "TINH_TRANG"),
		Date:         firstString(raw, "THOI_GIAN_TAO", "NGAY_LAP"),
		Total:        orderTotal(raw),
		Fingerprint:  Fingerprint(raw),
	}
	return order, true
}

// Fingerprint hashes the whole row. encoding/json sorts map keys, so equal
// rows always hash equally.
func Fingerprint(raw map[string]any) string {
	data, err := json.Marshal(raw)
	if err != nil {
		data = []byte(fmt.Sprintf("%v", raw))
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// customerCode reads MA_KHACH_HANG, which Cloudify ships as [id, "CODE"] or
// [id, "CODE - Name"], and returns the bare code.
func customerCode(v any) string {
	var label string
	switch t := v.(type) {
	case []any:
		if len(t) >= 2 {
			label, _ = t[1].(string)
		}
	case string:
		label = t
	}
	label = strings.TrimSpace(label)
	if i := strings.Index(label, " - "); i >= 0 {
		label = strings.TrimSpace(label[:i])
	}
	return label
}

// orderTotal sums SO_LUONG × DON_GIA over the lines minus the order discount.
// saorders/search has no precomputed total.
func orderTotal(raw map[string]any) float64 {
	total := 0.0
	if lines, ok := raw["DON_DAT_HANG_CHI_TIET"].([]any); ok {
		for _, line := range lines {
			m, ok := line.(map[string]any)
			if !ok {
				continue
			}
			qty, _ := asFloat(m["SO_LUONG"])
			price, _ := asFloat(m["DON_GIA"])
			total += qty * price
		}
	}
	discount, _ := asFloat(raw["GIAM_GIA_HOA_DON"])
	total -= discount
	return math.Round(total)
}

func firstString(raw map[string]any, keys ...string) string {
	for _, key := range keys {
		switch v := raw[key].(type) {
		case string:
			if s := strings.TrimSpace(v); s != "" {
				return s
			}
		case float64:
			return strconv.FormatFloat(v, 'f', -1, 64)
		case json.Number:
			return v.String()
		}
	}
	return ""
}

func asInt64(v any) (int64, bool) {
	switch t := v.(type) {
	case float64:
		return int64(t), t == math.Trunc(t)
	case int:
		return int64(t), true
	case int64:
		return t, true
	case json.Number:
		n, err := t.Int64()
		return n, err == nil
	case string:
		n, err := strconv.ParseInt(strings.TrimSpace(t), 10, 64)
		return n, err == nil
	}
	return 0, false
}

func asFloat(v any) (float64, bool) {
	switch t := v.(type) {
	case float64:
		return t, true
	case int:
		return float64(t), true
	case int64:
		return float64(t), true
	case json.Number:
		f, err := t.Float64()
		return f, err == nil
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(t), 64)
		return f, err == nil
	}
	return 0, false
}
