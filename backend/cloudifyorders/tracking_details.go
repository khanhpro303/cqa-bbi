package cloudifyorders

import (
	"context"
	"errors"
	"math"
	"strings"

	"github.com/vietbui/chat-quality-agent/db/models"
	"gorm.io/gorm"
)

var ErrTrackedOrderNotFound = errors.New("tracked order was not found in Cloudify")

type TrackedOrderLine struct {
	SKU       string  `json:"sku"`
	Name      string  `json:"name"`
	Unit      string  `json:"unit"`
	Quantity  float64 `json:"quantity"`
	UnitPrice float64 `json:"unit_price"`
	Total     float64 `json:"total"`
}

type TrackedOrderDetails struct {
	Status     string             `json:"status"`
	StatusName string             `json:"status_name"`
	Total      float64            `json:"total"`
	Discount   float64            `json:"discount"`
	Note       string             `json:"note"`
	Lines      []TrackedOrderLine `json:"lines"`
}

// LookupTrackedOrder asks Cloudify for the recorded order code and accepts
// a result only if ID, order code and customer code all match the delivered order.
func LookupTrackedOrder(ctx context.Context, database *gorm.DB, tracked models.CloudifyOrderConfirmation, encryptionKey string) (TrackedOrderDetails, error) {
	if tracked.OrderID <= 0 || tracked.OrderCode == "" || tracked.CustomerCode == "" {
		return TrackedOrderDetails{}, ErrTrackedOrderNotFound
	}
	fetcher, err := newCloudifyFetcher(database.WithContext(ctx), tracked.TenantID, encryptionKey)
	if err != nil {
		return TrackedOrderDetails{}, err
	}
	rows, err := fetcher.client.SearchWithFreshCloudifySession(ctx, fetcher.endpoint, map[string]any{
		"SO_DON_HANG": tracked.OrderCode,
	})
	if err != nil {
		return TrackedOrderDetails{}, err
	}
	return trackedOrderDetails(rows, tracked)
}

func trackedOrderDetails(rows []map[string]any, tracked models.CloudifyOrderConfirmation) (TrackedOrderDetails, error) {
	var match map[string]any
	var order Order
	for _, row := range rows {
		parsed, ok := ParseOrder(row)
		if !ok || parsed.ID != tracked.OrderID || !strings.EqualFold(parsed.Code, tracked.OrderCode) || !strings.EqualFold(parsed.CustomerCode, tracked.CustomerCode) {
			continue
		}
		if match != nil {
			return TrackedOrderDetails{}, errors.New("Cloudify returned multiple matches for the tracked order")
		}
		match, order = row, parsed
	}
	if match == nil {
		return TrackedOrderDetails{}, ErrTrackedOrderNotFound
	}

	receipt, ok := readReceiptDetails(match)
	if !ok || math.IsInf(order.Total, 0) || math.IsNaN(order.Total) {
		return TrackedOrderDetails{}, errors.New("malformed Cloudify order details")
	}
	status := NormalizeStatus(order.Status)
	details := TrackedOrderDetails{
		Status: status, StatusName: StatusName(status), Total: order.Total, Discount: receipt.Discount,
		Note: firstString(match, "GHI_CHU"), Lines: make([]TrackedOrderLine, 0, len(receipt.Lines)),
	}
	for _, item := range receipt.Lines {
		lineTotal := item.Quantity * item.UnitPrice
		if math.IsInf(lineTotal, 0) || math.IsNaN(lineTotal) {
			return TrackedOrderDetails{}, errors.New("invalid Cloudify order line total")
		}
		details.Lines = append(details.Lines, TrackedOrderLine{
			SKU: item.SKU, Name: item.Name, Unit: item.Unit, Quantity: item.Quantity,
			UnitPrice: item.UnitPrice, Total: lineTotal,
		})
	}
	return details, nil
}
