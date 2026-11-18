package cloudifyorders

import (
	"context"
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
		status, note := deliverOne(ctx, tenantID, event, settings, sender, now)
		updates := map[string]any{"delivery_status": status, "delivery_note": truncate(note, 250), "attempts": event.Attempts + 1}
		if status == DeliverySent {
			updates["delivered_at"] = now
		}
		if status == DeliveryPending {
			updates["next_attempt_at"] = now.Add(retryDelay(event.Attempts + 1))
		} else {
			done++
		}
		database.Model(&models.CloudifyOrderEvent{}).Where("id = ? AND delivery_status = ?", event.ID, DeliveryPending).Updates(updates)
	}
	return done, nil
}

func deliverOne(ctx context.Context, tenantID string, event models.CloudifyOrderEvent, settings Settings, sender Sender, now time.Time) (string, string) {
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
	text := Render(settings.Templates[event.Kind], event)
	if text == "" {
		return DeliverySkipped, "empty_template"
	}
	sent, failures := 0, []string{}
	for _, to := range recipients {
		if err := sender.Send(ctx, tenantID, to, text); err != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", to.Name, err))
			continue
		}
		sent++
	}
	switch {
	case sent > 0 && len(failures) == 0:
		return DeliverySent, fmt.Sprintf("sent to %d group(s)", sent)
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
