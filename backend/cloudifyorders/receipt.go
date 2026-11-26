package cloudifyorders

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"unicode"

	"github.com/vietbui/chat-quality-agent/db/models"
	"gorm.io/gorm"
)

type receiptDetails struct {
	Lines    []ReceiptLine
	Discount float64
}

// captureReceipt stores just the fields needed for the original confirmation.
// An empty object represents a malformed Cloudify detail payload. An empty
// string identifies outbox rows created before receipt details were captured.
func captureReceipt(raw map[string]any) string {
	details, ok := readReceiptDetails(raw)
	if !ok {
		return "{}"
	}
	encoded, err := json.Marshal(details)
	if err != nil {
		return "{}"
	}
	return string(encoded)
}

func readReceiptDetails(raw map[string]any) (receiptDetails, bool) {
	rawLines, ok := raw["DON_DAT_HANG_CHI_TIET"].([]any)
	if !ok {
		return receiptDetails{}, false
	}
	var discount float64
	if value := raw["GIAM_GIA_HOA_DON"]; value != nil && value != false {
		discount, ok = asFloat(value)
		if !ok || math.IsNaN(discount) || math.IsInf(discount, 0) {
			return receiptDetails{}, false
		}
	}
	details := receiptDetails{Lines: make([]ReceiptLine, 0, len(rawLines)), Discount: discount}
	for _, entry := range rawLines {
		line, ok := entry.(map[string]any)
		if !ok {
			return receiptDetails{}, false
		}
		quantity, haveQuantity := asFloat(line["SO_LUONG"])
		price, havePrice := asFloat(line["DON_GIA"])
		if !haveQuantity || !havePrice || math.IsNaN(quantity) || math.IsNaN(price) || math.IsInf(quantity, 0) || math.IsInf(price, 0) {
			return receiptDetails{}, false
		}
		sku, _, _ := strings.Cut(receiptRelationLabel(line["MA_HANG"]), " - ")
		details.Lines = append(details.Lines, ReceiptLine{
			SKU: strings.TrimSpace(sku), Name: firstString(line, "TEN_HANG"),
			Unit: receiptRelationLabel(line["DVT"]), Quantity: quantity, UnitPrice: price,
		})
	}
	return details, true
}

func receiptRelationLabel(value any) string {
	if pair, ok := value.([]any); ok && len(pair) >= 2 {
		value = pair[1]
	}
	label, _ := value.(string)
	return strings.TrimSpace(label)
}

func prepareReceipt(ctx context.Context, database *gorm.DB, tenantID string, event models.CloudifyOrderEvent, account PaymentAccount) ([]receiptPage, error) {
	var details receiptDetails
	if err := json.Unmarshal([]byte(event.ReceiptData), &details); err != nil || details.Lines == nil {
		return nil, errors.New("Cloudify order details are unavailable")
	}
	order := Order{
		ID: event.OrderID, Code: event.OrderCode, Date: event.OrderDate, Status: event.Status,
		CustomerCode: event.CustomerCode, CustomerName: event.CustomerName, Total: event.Total,
	}
	if math.IsNaN(order.Total) || math.IsInf(order.Total, 0) {
		return nil, errors.New("invalid Cloudify order total")
	}
	var payment ReceiptPayment
	if receiptIsPayable(event) {
		if order.Total > vietQRMaxAmount || order.Total != math.Trunc(order.Total) {
			return nil, errors.New("Cloudify order total exceeds the VietQR amount format")
		}
		qr, memo, err := VietQRImage(ctx, account, int64(order.Total), order.CustomerCode)
		if err != nil {
			return nil, err
		}
		payment = ReceiptPayment{QR: qr, AccountNo: account.AccountNo, AccountName: account.AccountName, TransferMemo: memo}
	}
	images, err := receiptProductImages(ctx, database, tenantID, details.Lines)
	if err != nil {
		return nil, errors.New("cannot load the product image cache")
	}
	for i := range details.Lines {
		details.Lines[i].Image = images[details.Lines[i].SKU]
	}
	pages, err := renderReceiptPages(order, details.Lines, details.Discount, payment)
	if err != nil {
		return nil, err
	}
	orderCode := receiptFilenameCode(order.Code)
	if orderCode == "" {
		orderCode = strconv.FormatInt(order.ID, 10)
	}
	customerCode := receiptFilenameCode(order.CustomerCode)
	for i := range pages {
		pages[i].filename = fmt.Sprintf("XNDH-%s-%s-%d.png", orderCode, customerCode, i+1)
	}
	return pages, nil
}

func receiptFilenameCode(code string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || strings.ContainsRune(`<>:"/\|?*`, r) {
			return '_'
		}
		return r
	}, strings.TrimSpace(code))
}

func receiptIsPayable(event models.CloudifyOrderEvent) bool {
	return event.Total > 0 && NormalizeStatus(event.Status) != "3"
}

// Verify that a receipt prepared from the creation snapshot is still payable
// to the configured account before dispatching it.
func checkReceiptDelivery(ctx context.Context, database *gorm.DB, tenantID string, event models.CloudifyOrderEvent, account PaymentAccount) (string, error) {
	database = database.WithContext(ctx)
	settings, err := LoadSettings(database, tenantID)
	if err != nil {
		return "", errors.New("cannot recheck payment settings")
	}
	if !settings.WatchEnabled || !settings.NotifyEnabled || !settings.NotifyKinds[KindCreated] {
		return "notify_off", nil
	}
	if settings.PaymentAccount != account {
		return "", errors.New("payment account changed during preparation")
	}
	var current models.CloudifyOrderSnapshot
	if err := database.Select("order_code", "customer_code", "status", "total").
		Where("tenant_id = ? AND order_id = ?", tenantID, event.OrderID).Take(&current).Error; err != nil {
		return "", errors.New("cannot recheck the latest Cloudify order snapshot")
	}
	if !strings.EqualFold(current.CustomerCode, event.CustomerCode) {
		return "order_customer_changed", nil
	}
	if current.OrderCode != event.OrderCode {
		return "order_code_changed", nil
	}
	if NormalizeStatus(current.Status) == "3" && NormalizeStatus(event.Status) != "3" {
		return "order_cancelled", nil
	}
	if current.Total != event.Total {
		return "order_total_changed", nil
	}
	return "", nil
}
