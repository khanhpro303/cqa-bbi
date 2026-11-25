package cloudifyorders

import (
	"bytes"
	"context"
	"encoding/json"
	"image/png"
	"testing"
	"time"

	"github.com/vietbui/chat-quality-agent/db/models"
)

func TestCaptureCloudifyReceipt(t *testing.T) {
	row := sampleRow(16, "ĐH000016", "1")
	row["DON_DAT_HANG_CHI_TIET"] = []any{map[string]any{
		"MA_HANG":  []any{834.0, "AQ12-7.5 - Bình ắc quy"},
		"TEN_HANG": "Bình ắc quy 12V-7.5Ah", "DVT": []any{39.0, "Bình"},
		"SO_LUONG": 6.0, "DON_GIA": "485000",
	}}
	row["GIAM_GIA_HOA_DON"] = 10000.0
	var receipt receiptDetails
	if err := json.Unmarshal([]byte(captureReceipt(row)), &receipt); err != nil {
		t.Fatal(err)
	}
	order, ok := ParseOrder(row)
	if !ok || order.Total != 2900000 || receipt.Discount != 10000 || len(receipt.Lines) != 1 {
		t.Fatalf("order = %+v, receipt = %+v", order, receipt)
	}
	item := receipt.Lines[0]
	if item.SKU != "AQ12-7.5" || item.Name != "Bình ắc quy 12V-7.5Ah" || item.Unit != "Bình" || item.Quantity != 6 || item.UnitPrice != 485000 {
		t.Fatalf("item = %+v", item)
	}
	row["GIAM_GIA_HOA_DON"] = "invalid"
	if got := captureReceipt(row); got != "{}" {
		t.Fatalf("malformed discount created receipt data: %s", got)
	}
}

func TestCloudifyLegacyConfirmationKeepsText(t *testing.T) {
	now := time.Now()
	sender := &fakeSender{recipients: map[string][]Recipient{"KH125": {{GroupID: "group"}}}}
	settings := ParseSettings(map[string]string{
		KeyNotifyEnabled: "true", KeyPaymentBankBIN: "970415", KeyPaymentAccountNo: "0012345678", KeyPaymentAccountName: "Công ty mẫu",
	})
	legacy := models.CloudifyOrderEvent{Kind: KindCreated, OrderCode: "ĐH16", CustomerCode: "KH125", CreatedAt: now}
	status, note := deliverOne(context.Background(), nil, "tenant", legacy, settings, sender, now)
	if status != DeliverySent || len(sender.sent) != 1 {
		t.Fatalf("status = %s (%s), messages = %+v", status, note, sender.sent)
	}
}

type confirmationImageSender struct {
	fakeSender
	images [][]byte
}

func (s *confirmationImageSender) SendReceipt(_ context.Context, _ string, _ Recipient, img []byte) error {
	s.images = append(s.images, img)
	return nil
}

func TestCloudifyReceiptOutbox(t *testing.T) {
	database := testDatabase(t)
	if err := database.AutoMigrate(&models.CachedProduct{}); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	tenant := "receipts"
	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	values := map[string]string{
		KeyWatchEnabled: "true", KeyNotifyEnabled: "true", KeyPaymentBankBIN: "970415",
		KeyPaymentAccountNo: "0012345678", KeyPaymentAccountName: "Công ty mẫu",
	}
	for key, value := range values {
		setSetting(t, database, tenant, key, value)
	}
	settings, err := LoadSettings(database, tenant)
	if err != nil {
		t.Fatal(err)
	}

	free := sampleRow(16, "ĐH16", "1")
	free["DON_DAT_HANG_CHI_TIET"] = []any{map[string]any{"MA_HANG": "SAMPLE", "SO_LUONG": 1.0, "DON_GIA": 0.0}}
	free["GIAM_GIA_HOA_DON"] = 0.0
	payable := sampleRow(17, "ĐH17", "1")
	res, err := Ingest(ctx, database, tenant, []map[string]any{free, payable}, SourcePoll, DiffOptions{}, now)
	if err != nil || len(res.Events) != 2 {
		t.Fatalf("ingest: %+v, %v", res, err)
	}
	for _, event := range res.Events {
		if event.ReceiptData == "" || event.ReceiptData == "{}" {
			t.Fatalf("missing order details: %+v", event)
		}
		if event.OrderID == 17 {
			skip, err := checkReceiptDelivery(ctx, database, tenant, event, settings.PaymentAccount)
			if err != nil || skip != "" {
				t.Fatalf("payable verification: skip %q, err %v", skip, err)
			}
			if err := database.Model(&models.CloudifyOrderSnapshot{}).Where("tenant_id = ? AND order_id = ?", tenant, 17).
				Update("total", event.Total+1000).Error; err != nil {
				t.Fatal(err)
			}
			skip, err = checkReceiptDelivery(ctx, database, tenant, event, settings.PaymentAccount)
			if err != nil || skip != "order_total_changed" {
				t.Fatalf("stale amount verification: skip %q, err %v", skip, err)
			}
			if err := database.Model(&models.CloudifyOrderSnapshot{}).Where("tenant_id = ? AND order_id = ?", tenant, 17).
				Update("status", "3").Error; err != nil {
				t.Fatal(err)
			}
			skip, err = checkReceiptDelivery(ctx, database, tenant, event, settings.PaymentAccount)
			if err != nil || skip != "order_cancelled" {
				t.Fatalf("cancelled order verification: skip %q, err %v", skip, err)
			}
			if err := database.Model(&models.CloudifyOrderSnapshot{}).Where("tenant_id = ? AND order_id = ?", tenant, 17).
				Update("customer_code", "KH999").Error; err != nil {
				t.Fatal(err)
			}
			skip, err = checkReceiptDelivery(ctx, database, tenant, event, settings.PaymentAccount)
			if err != nil || skip != "order_customer_changed" {
				t.Fatalf("stale customer verification: skip %q, err %v", skip, err)
			}
			if err := database.Delete(&event).Error; err != nil {
				t.Fatal(err)
			}
		}
	}
	sender := &confirmationImageSender{fakeSender: fakeSender{recipients: map[string][]Recipient{"KH125": {{GroupID: "g1"}, {GroupID: "g2"}}}}}
	if done, err := DeliverPending(ctx, database, tenant, settings, sender, now); done != 1 || err != nil {
		t.Fatalf("deliver: %d, %v", done, err)
	}
	if len(sender.sent) != 0 || len(sender.images) != 2 || !bytes.Equal(sender.images[0], sender.images[1]) {
		t.Fatalf("expected the same image once per group, got %d images and %d texts", len(sender.images), len(sender.sent))
	}
	if _, err := png.DecodeConfig(bytes.NewReader(sender.images[0])); err != nil {
		t.Fatal(err)
	}
}
