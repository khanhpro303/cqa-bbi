package cloudifyorders

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/vietbui/chat-quality-agent/db/models"
	"github.com/vietbui/chat-quality-agent/notifications"
	"gorm.io/gorm"
)

// Delivery statuses.
const (
	DeliveryPending = "pending"
	DeliverySent    = "sent"
	DeliverySkipped = "skipped"
	DeliveryFailed  = "failed"
)

const (
	maxAttempts = 5
	// staleAfter: an event that could not be delivered for this long (for
	// example during an outage) is skipped rather than sent late.
	staleAfter    = 2 * time.Hour
	deliveryBatch = 20
)

// Recipient is one Zalo group (GMF) that serves the order's customer code.
type Recipient struct {
	GroupID   string
	ChannelID string
	Name      string
}

// Sender finds who to tell about an order and sends the message.
type Sender interface {
	Recipients(tenantID, customerCode string) ([]Recipient, error)
	Send(ctx context.Context, tenantID string, to Recipient, text string) error
}

type receiptSender interface {
	SendReceiptPages(ctx context.Context, tenantID string, to Recipient, pages []receiptPage) error
}

type trackedOrderSender interface {
	sendTrackedText(ctx context.Context, tenantID string, to Recipient, text string) (zaloGroupID, messageID string, err error)
	openTrackedReceipt(ctx context.Context, tenantID string, to Recipient) (string, sendReceiptImage, error)
}

// ZaloGroupSender messages the customer's linked Zalo group through the OA
// channel the group belongs to (the same path CRM campaign alerts use).
type ZaloGroupSender struct{ DB *gorm.DB }

func (s ZaloGroupSender) Recipients(tenantID, customerCode string) ([]Recipient, error) {
	code := strings.TrimSpace(customerCode)
	if code == "" {
		return nil, nil
	}
	var groups []models.CRMGroup
	err := s.DB.Where("tenant_id = ? AND zalo_group_id <> '' AND channel_id <> ''", tenantID).
		Where("UPPER(customer_code) = UPPER(?) OR id IN (?)", code,
			s.DB.Model(&models.CRMGroupCustomerCode{}).Select("group_id").
				Where("tenant_id = ? AND UPPER(customer_code) = UPPER(?)", tenantID, code)).
		Find(&groups).Error
	if err != nil {
		return nil, err
	}
	out := make([]Recipient, 0, len(groups))
	for _, g := range groups {
		out = append(out, Recipient{GroupID: g.ID, ChannelID: g.ChannelID, Name: g.Name})
	}
	return out, nil
}

func (s ZaloGroupSender) Send(ctx context.Context, tenantID string, to Recipient, text string) error {
	notifier, err := notifications.BuildZaloGroupNotifier(tenantID, to.ChannelID, to.GroupID)
	if err != nil {
		return err
	}
	return notifier.Send(ctx, "", text)
}

type receiptPage struct {
	png      []byte
	filename string
	itemEnd  int
}

func (s ZaloGroupSender) SendReceiptPages(ctx context.Context, tenantID string, to Recipient, pages []receiptPage) error {
	notifier, err := notifications.BuildZaloGroupNotifier(tenantID, to.ChannelID, to.GroupID)
	if err != nil {
		return err
	}
	for _, page := range pages {
		if err := notifier.SendImage(ctx, page.png, page.filename); err != nil {
			return err
		}
	}
	return nil
}

// DeliverPending sends (or skips) the tenant's pending events, oldest first.
// It returns how many events it finished.
func DeliverPending(ctx context.Context, database *gorm.DB, tenantID string, settings Settings, sender Sender, now time.Time) (int, error) {
	var events []models.CloudifyOrderEvent
	if err := database.WithContext(ctx).Where("tenant_id = ? AND delivery_status = ?", tenantID, DeliveryPending).
		Where("next_attempt_at IS NULL OR next_attempt_at <= ?", now).
		Order("created_at ASC").Limit(deliveryBatch).Find(&events).Error; err != nil {
		return 0, err
	}
	done := 0
	for _, event := range events {
		if ctx.Err() != nil {
			break
		}
		var status, note string
		if event.Kind == KindCreated && settings.NotifyEnabled && settings.NotifyKinds[event.Kind] && createdByChatbot(ctx, database, tenantID, event.OrderCode) {
			// The customer ordered in the chat and already got the bot's
			// closing message; a second "order created" notice is noise.
			status, note = DeliverySkipped, "chatbot_order"
		} else {
			status, note = deliverOne(ctx, database, tenantID, event, settings, sender, now)
		}
		updates := map[string]any{"delivery_status": status, "delivery_note": truncate(note, 250), "attempts": event.Attempts + 1}
		if status == DeliverySent {
			updates["delivered_at"] = now
		}
		if status == DeliveryPending {
			updates["next_attempt_at"] = now.Add(retryDelay(event.Attempts + 1))
		}
		updateCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		err := database.WithContext(updateCtx).Model(&models.CloudifyOrderEvent{}).
			Where("tenant_id = ? AND id = ? AND delivery_status = ?", tenantID, event.ID, DeliveryPending).
			Updates(updates).Error
		cancel()
		if err != nil {
			return done, err
		}
		if status != DeliveryPending {
			done++
		}
	}
	return done, nil
}

func deliverOne(ctx context.Context, database *gorm.DB, tenantID string, event models.CloudifyOrderEvent, settings Settings, sender Sender, now time.Time) (string, string) {
	if !settings.NotifyEnabled {
		return DeliverySkipped, "notify_off"
	}
	if !settings.NotifyKinds[event.Kind] {
		return DeliverySkipped, "kind_off"
	}
	if now.Sub(event.CreatedAt) > staleAfter {
		return DeliverySkipped, "stale"
	}
	recipients, err := sender.Recipients(tenantID, event.CustomerCode)
	if err != nil {
		return retryOrFail(event, "recipients: "+err.Error())
	}
	if len(recipients) == 0 {
		return DeliverySkipped, "no_zalo_group"
	}
	var send func(Recipient) error
	tracked, track := sender.(trackedOrderSender)
	track = track && event.Kind == KindCreated && event.OrderID > 0 && event.OrderCode != "" && event.CustomerCode != ""
	isReceipt := event.Kind == KindCreated && event.ReceiptData != "" && !settings.PaymentAccount.IsEmpty()
	if isReceipt {
		imageSender, ok := sender.(receiptSender)
		if !ok {
			return retryOrFail(event, "image sender unavailable")
		}
		pages, err := prepareReceipt(ctx, database, tenantID, event, settings.PaymentAccount)
		if err != nil {
			return retryOrFail(event, "receipt image: "+err.Error())
		}
		skip, err := checkReceiptDelivery(ctx, database, tenantID, event, settings.PaymentAccount)
		if err != nil {
			return retryOrFail(event, "receipt image: "+err.Error())
		}
		if skip != "" {
			return DeliverySkipped, skip
		}
		send = func(to Recipient) error { return imageSender.SendReceiptPages(ctx, tenantID, to, pages) }
		if track {
			send = func(to Recipient) error {
				return sendReceiptConfirmation(ctx, database, event, to, tracked, pages, settings.PaymentAccount)
			}
		}
	} else {
		text := Render(settings.Templates[event.Kind], event)
		if text == "" {
			return DeliverySkipped, "empty_template"
		}
		send = func(to Recipient) error { return sender.Send(ctx, tenantID, to, text) }
		if track {
			send = func(to Recipient) error {
				return sendOrderConfirmation(ctx, database, event, to, func(current models.CloudifyOrderConfirmation, _ recordReceiptPage) (string, string, error) {
					if current.ReceiptDelivery != nil {
						return "", "", errors.New("payment settings changed during receipt delivery")
					}
					return tracked.sendTrackedText(ctx, tenantID, to, text)
				})
			}
		}
	}
	sent, failures := 0, []string{}
	for _, to := range recipients {
		if err := send(to); err != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", to.Name, err))
			continue
		}
		sent++
	}
	switch {
	case sent > 0 && len(failures) == 0:
		return DeliverySent, fmt.Sprintf("sent to %d group(s)", sent)
	case sent > 0 && track && isReceipt:
		return retryOrFail(event, strings.Join(failures, "; "))
	case sent > 0:
		// Some groups got it; retrying would message them twice.
		return DeliverySent, fmt.Sprintf("sent to %d, failed %d: %s", sent, len(failures), strings.Join(failures, "; "))
	default:
		return retryOrFail(event, strings.Join(failures, "; "))
	}
}

// retryDelay backs off 30s, 1m, 2m, 4m between attempts.
func retryDelay(attempts int) time.Duration {
	if attempts < 1 {
		attempts = 1
	}
	return (30 * time.Second) << (attempts - 1)
}

func retryOrFail(event models.CloudifyOrderEvent, note string) (string, string) {
	if event.Attempts+1 >= maxAttempts {
		return DeliveryFailed, note
	}
	return DeliveryPending, note
}

// createdByChatbot reports whether the order was written by a confirmed
// chatbot order draft (orderactions: kind "create", status "done").
func createdByChatbot(ctx context.Context, database *gorm.DB, tenantID, code string) bool {
	if strings.TrimSpace(code) == "" {
		return false
	}
	var count int64
	if err := database.WithContext(ctx).Model(&models.OrderAction{}).
		Where("tenant_id = ? AND kind = ? AND status = ? AND order_code = ?", tenantID, "create", "done", code).
		Count(&count).Error; err != nil {
		return false
	}
	return count > 0
}
