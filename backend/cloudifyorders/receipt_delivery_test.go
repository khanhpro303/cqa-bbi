package cloudifyorders

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image/png"
	"slices"
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
	pages [][]receiptPage
}

func (s *confirmationImageSender) SendReceiptPages(_ context.Context, _ string, _ Recipient, pages []receiptPage) error {
	s.pages = append(s.pages, pages)
	return nil
}

type trackedConfirmationImageSender struct {
	confirmationImageSender
	sentPages map[string][]string
}

func (s *trackedConfirmationImageSender) openTrackedReceipt(_ context.Context, _ string, to Recipient) (string, sendReceiptImage, error) {
	send := func(_ context.Context, image []byte, filename string) (string, error) {
		if _, err := png.DecodeConfig(bytes.NewReader(image)); err != nil {
			return "", err
		}
		history := s.sentPages[to.GroupID]
		s.sentPages[to.GroupID] = append(history, filename)
		if to.GroupID == "g2" && len(history) == 1 {
			return "", errors.New("simulated page two failure")
		}
		return to.GroupID + "/" + filename, nil
	}
	return to.GroupID, send, nil
}

func (s *trackedConfirmationImageSender) sendTrackedText(context.Context, string, Recipient, string) (string, string, error) {
	return "", "", errors.New("expected receipt images")
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
	if len(sender.sent) != 0 || len(sender.pages) != 2 {
		t.Fatalf("expected one receipt per group, got %d receipts and %d texts", len(sender.pages), len(sender.sent))
	}
	for _, pages := range sender.pages {
		if len(pages) != 1 || pages[0].filename != "XNDH-ĐH16-KH125-1.png" || !bytes.Equal(pages[0].png, sender.pages[0][0].png) {
			t.Fatal("expected the same named single image in each group")
		}
	}
	if _, err := png.DecodeConfig(bytes.NewReader(sender.pages[0][0].png)); err != nil {
		t.Fatal(err)
	}
}

func TestCloudifyReceiptOutboxResumesFailedPage(t *testing.T) {
	database := testDatabase(t)
	if err := database.AutoMigrate(&models.CachedProduct{}, &models.CloudifyOrderConfirmation{}); err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	const tenant = "receipts"
	now := time.Now().UTC().Truncate(time.Second)
	for key, value := range map[string]string{
		KeyWatchEnabled: "true", KeyNotifyEnabled: "true", KeyPaymentBankBIN: "970415",
		KeyPaymentAccountNo: "0012345678", KeyPaymentAccountName: "Công ty mẫu",
	} {
		setSetting(t, database, tenant, key, value)
	}
	settings, err := LoadSettings(database, tenant)
	if err != nil {
		t.Fatal(err)
	}
	row := sampleRow(16, "ĐH000016", "1")
	row["MA_KHACH_HANG"] = "S084"
	lines := make([]any, 40)
	for i := range lines {
		lines[i] = map[string]any{"MA_HANG": fmt.Sprintf("SKU%d", i+1), "SO_LUONG": 1.0, "DON_GIA": 0.0}
	}
	row["DON_DAT_HANG_CHI_TIET"] = lines
	if result, err := Ingest(ctx, database, tenant, []map[string]any{row}, SourcePoll, DiffOptions{}, now); err != nil || len(result.Events) != 1 {
		t.Fatalf("ingest: %+v, %v", result, err)
	}
	sender := &trackedConfirmationImageSender{sentPages: make(map[string][]string)}
	sender.recipients = map[string][]Recipient{"S084": {{GroupID: "g1"}, {GroupID: "g2"}}}
	if done, err := DeliverPending(ctx, database, tenant, settings, sender, now); done != 0 || err != nil {
		t.Fatalf("expected a retry after the partial delivery, finished %d: %v", done, err)
	}
	setSetting(t, database, tenant, KeyPaymentAccountNo, "0012345679")
	changedSettings, err := LoadSettings(database, tenant)
	if err != nil {
		t.Fatal(err)
	}
	if done, err := DeliverPending(ctx, database, tenant, changedSettings, sender, now.Add(time.Minute)); done != 0 || err != nil || len(sender.sentPages["g2"]) != 2 {
		t.Fatalf("receipt continued after the payment account changed, finished %d: %v", done, err)
	}
	setSetting(t, database, tenant, KeyPaymentAccountNo, settings.PaymentAccount.AccountNo)
	if done, err := DeliverPending(ctx, database, tenant, settings, sender, now.Add(2*time.Minute)); done != 1 || err != nil {
		t.Fatalf("expected the remaining pages to be delivered, finished %d: %v", done, err)
	}
	successful := sender.sentPages["g1"]
	if len(successful) < 2 {
		t.Fatalf("expected multiple images, got %v", successful)
	}
	for i, filename := range successful {
		if want := fmt.Sprintf("XNDH-ĐH000016-S084-%d.png", i+1); filename != want {
			t.Fatalf("filename %q, want %q", filename, want)
		}
	}
	wantAttempts := append([]string{successful[0], successful[1]}, successful[1:]...)
	if attempts := sender.sentPages["g2"]; !slices.Equal(attempts, wantAttempts) {
		t.Fatalf("retry sent %v, want %v", attempts, wantAttempts)
	}
	var tracking models.CloudifyOrderConfirmation
	firstMessageID := "g2/" + successful[0]
	err = database.Where("group_id = ? AND JSON_CONTAINS(receipt_delivery, JSON_QUOTE(?), '$.message_ids')", "g2", firstMessageID).Take(&tracking).Error
	if err != nil || tracking.DeliveredAt == nil || tracking.ReceiptDelivery == nil || len(tracking.ReceiptDelivery.MessageIDs) != len(successful) ||
		tracking.ReceiptDelivery.MessageIDs[0] != firstMessageID || tracking.OutboundMessageID != "g2/"+successful[len(successful)-1] {
		t.Fatalf("unexpected delivery tracking %+v: %v", tracking, err)
	}
}
