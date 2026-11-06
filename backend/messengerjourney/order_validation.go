package messengerjourney

import (
	"encoding/json"
	"math"
	"strings"
	"time"
)

// ValidateOrderPayload checks the compact shared order contract before an
// imported outcome can enter the canonical feed. It does not invoke AI or infer
// missing fields. Insufficient evidence must be represented as review by caller.
func ValidateOrderPayload(payload json.RawMessage, observedAt time.Time) error {
	var value struct {
		OrderID    string   `json:"order_id"`
		OrderRef   string   `json:"order_ref"`
		ValueVND   *int64   `json:"value_vnd"`
		Status     string   `json:"status"`
		OrderedAt  string   `json:"ordered_at"`
		Evidence   []string `json:"evidence_message_ids"`
		Confidence *float64 `json:"confidence"`
	}
	if json.Unmarshal(payload, &value) != nil {
		return ErrInvalidInput
	}
	bounded := func(s string, max int) bool { return strings.TrimSpace(s) != "" && len(s) <= max }
	if !bounded(value.OrderID, 128) || !bounded(value.OrderRef, 160) || value.Confidence == nil || math.IsNaN(*value.Confidence) || math.IsInf(*value.Confidence, 0) || *value.Confidence < 0 || *value.Confidence > 1 {
		return ErrInvalidInput
	}
	switch value.Status {
	case "confirmed", "cancelled", "refunded", "review":
	default:
		return ErrInvalidInput
	}
	if value.ValueVND != nil && (*value.ValueVND < 0 || *value.ValueVND > 1_000_000_000_000) {
		return ErrInvalidInput
	}
	if value.Status == "confirmed" && value.ValueVND == nil {
		return ErrInvalidInput
	}
	ordered, err := time.Parse(time.RFC3339Nano, value.OrderedAt)
	if err != nil || observedAt.IsZero() || ordered.After(observedAt) {
		return ErrInvalidInput
	}
	if len(value.Evidence) > 20 {
		return ErrInvalidInput
	}
	for _, id := range value.Evidence {
		if !bounded(id, 200) {
			return ErrInvalidInput
		}
	}
	return nil
}
