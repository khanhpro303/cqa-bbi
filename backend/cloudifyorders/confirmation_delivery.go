package cloudifyorders

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/google/uuid"
	"github.com/vietbui/chat-quality-agent/db/models"
	"github.com/vietbui/chat-quality-agent/notifications"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func (s ZaloGroupSender) sendTrackedText(ctx context.Context, tenantID string, to Recipient, text string) (string, string, error) {
	notifier, err := s.trackedNotifier(ctx, tenantID, to)
	if err != nil {
		return "", "", err
	}
	messageID, err := notifier.SendTrackedText(ctx, text)
	return notifier.ZaloGroupID(), messageID, err
}

func (s ZaloGroupSender) sendTrackedReceipt(ctx context.Context, tenantID string, to Recipient, image []byte) (string, string, error) {
	notifier, err := s.trackedNotifier(ctx, tenantID, to)
	if err != nil {
		return "", "", err
	}
	messageID, err := notifier.SendTrackedImage(ctx, image, "xac-nhan-don-hang.png")
	return notifier.ZaloGroupID(), messageID, err
}

func (s ZaloGroupSender) trackedNotifier(ctx context.Context, tenantID string, to Recipient) (*notifications.ZaloNotifier, error) {
	var group models.CRMGroup
	if err := s.DB.WithContext(ctx).Where("tenant_id = ? AND id = ? AND channel_id = ?", tenantID, to.GroupID, to.ChannelID).First(&group).Error; err != nil {
		return nil, err
	}
	notifier, err := notifications.BuildZaloGroupNotifier(tenantID, to.ChannelID, to.GroupID)
	if err != nil {
		return nil, err
	}
	if group.ZaloGroupID != notifier.ZaloGroupID() {
		return nil, errors.New("CRM group changed during order delivery")
	}
	return notifier, nil
}

// Store the recipient before sending so a database failure cannot send a
// notification without first reserving its tracking row. An already completed
// recipient is skipped if the outbox is retried after a partial DB failure.
func sendOrderConfirmation(ctx context.Context, database *gorm.DB, event models.CloudifyOrderEvent, to Recipient, send func() (string, string, error)) error {
	tracking := models.CloudifyOrderConfirmation{
		ID: uuid.NewString(), TenantID: event.TenantID, EventID: event.ID, OrderID: event.OrderID,
		OrderCode: event.OrderCode, CustomerCode: event.CustomerCode, CustomerName: event.CustomerName,
		GroupID: to.GroupID, ChannelID: to.ChannelID, GroupName: to.Name,
	}
	if err := database.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&tracking).Error; err != nil {
		return fmt.Errorf("reserve order tracking: %w", err)
	}
	dbRow := database.Model(&models.CloudifyOrderConfirmation{}).
		Where("tenant_id = ? AND event_id = ? AND group_id = ?", event.TenantID, event.ID, to.GroupID)
	var current models.CloudifyOrderConfirmation
	if err := dbRow.WithContext(ctx).Select("id", "delivered_at").Take(&current).Error; err != nil {
		return fmt.Errorf("load order tracking: %w", err)
	}
	if current.DeliveredAt != nil {
		return nil
	}

	at := time.Now().UTC()
	groupID, messageID, err := send()
	if err != nil {
		return err
	}
	updates := map[string]any{
		"zalo_group_id": groupID, "outbound_message_id": messageID, "delivered_at": at,
		"channel_id": to.ChannelID, "group_name": to.Name,
	}
	// The send has succeeded. Give the write a fresh deadline, even if the
	// outbox caller is expiring, and retry briefly without resending to Zalo.
	saveCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer cancel()
	for attempt := 0; attempt < 3 && saveCtx.Err() == nil; attempt++ {
		err = dbRow.WithContext(saveCtx).Where("delivered_at IS NULL").Updates(updates).Error
		if err == nil {
			return nil
		}
	}
	log.Printf("[cloudify-orders] sent notification but could not finish tracking tenant=%s event=%s group=%s: %v", event.TenantID, event.ID, to.GroupID, err)
	return nil
}
