package cloudifyorders

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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

type sendReceiptImage func(context.Context, []byte, string) (string, error)
type recordReceiptPage func(layoutKey, groupID, messageID string) error

func (s ZaloGroupSender) openTrackedReceipt(ctx context.Context, tenantID string, to Recipient) (string, sendReceiptImage, error) {
	notifier, err := s.trackedNotifier(ctx, tenantID, to)
	if err != nil {
		return "", nil, err
	}
	return notifier.ZaloGroupID(), notifier.SendTrackedImage, nil
}

func sendReceiptConfirmation(ctx context.Context, database *gorm.DB, event models.CloudifyOrderEvent, to Recipient, sender trackedOrderSender, pages []receiptPage, account PaymentAccount) error {
	layoutKey := receiptLayoutKey(pages, account)
	return sendOrderConfirmation(ctx, database, event, to, func(current models.CloudifyOrderConfirmation, recordPage recordReceiptPage) (string, string, error) {
		groupID, sendImage, err := sender.openTrackedReceipt(ctx, event.TenantID, to)
		if err != nil {
			return "", "", err
		}
		sent := 0
		if progress := current.ReceiptDelivery; progress != nil {
			sent = len(progress.MessageIDs)
			if sent > len(pages) || progress.LayoutKey != layoutKey || current.ZaloGroupID != groupID || current.ChannelID != to.ChannelID {
				return "", "", errors.New("receipt layout, payment account or recipient changed during delivery")
			}
		}
		lastMessageID := current.OutboundMessageID
		for _, page := range pages[sent:] {
			messageID, err := sendImage(ctx, page.png, page.filename)
			if err != nil {
				return "", "", fmt.Errorf("%s: %w", page.filename, err)
			}
			if err := recordPage(layoutKey, groupID, messageID); err != nil {
				return "", "", fmt.Errorf("record %s: %w", page.filename, err)
			}
			if messageID != "" {
				lastMessageID = messageID
			}
		}
		return groupID, lastMessageID, nil
	})
}

func receiptLayoutKey(pages []receiptPage, account PaymentAccount) string {
	hash := sha256.New()
	fmt.Fprintf(hash, "%q %q %q\n", account.BankBIN, account.AccountNo, account.AccountName)
	for _, page := range pages {
		fmt.Fprintf(hash, "%d\n", page.itemEnd)
	}
	return hex.EncodeToString(hash.Sum(nil))
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
func sendOrderConfirmation(ctx context.Context, database *gorm.DB, event models.CloudifyOrderEvent, to Recipient, send func(models.CloudifyOrderConfirmation, recordReceiptPage) (string, string, error)) error {
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
	if err := dbRow.WithContext(ctx).Select("id", "delivered_at", "channel_id", "zalo_group_id", "outbound_message_id", "receipt_delivery").Take(&current).Error; err != nil {
		return fmt.Errorf("load order tracking: %w", err)
	}
	if current.DeliveredAt != nil {
		return nil
	}

	save := func(updates map[string]any) error {
		// A send may have used the caller's remaining time. Persist its result
		// with a fresh deadline and retry the write without resending.
		saveCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		defer cancel()
		var err error
		for attempt := 0; attempt < 3; attempt++ {
			err = dbRow.WithContext(saveCtx).Where("delivered_at IS NULL").Updates(updates).Error
			if err == nil || saveCtx.Err() != nil {
				return err
			}
		}
		return err
	}
	progress := current.ReceiptDelivery
	recordPage := func(layoutKey, groupID, messageID string) error {
		if progress == nil {
			progress = &models.CloudifyReceiptDelivery{LayoutKey: layoutKey}
		}
		progress.MessageIDs = append(progress.MessageIDs, messageID)
		encoded, err := json.Marshal(progress)
		if err != nil {
			return err
		}
		updates := map[string]any{
			"zalo_group_id": groupID, "channel_id": to.ChannelID, "receipt_delivery": string(encoded),
		}
		if messageID != "" {
			updates["outbound_message_id"] = messageID
		}
		return save(updates)
	}

	at := time.Now().UTC()
	groupID, messageID, err := send(current, recordPage)
	if err != nil {
		return err
	}
	updates := map[string]any{
		"zalo_group_id": groupID, "outbound_message_id": messageID, "delivered_at": at,
		"channel_id": to.ChannelID, "group_name": to.Name,
	}
	if err := save(updates); err != nil {
		if progress != nil {
			return fmt.Errorf("finish receipt tracking: %w", err)
		}
		log.Printf("[cloudify-orders] sent notification but could not finish tracking tenant=%s event=%s group=%s: %v", event.TenantID, event.ID, to.GroupID, err)
	}
	return nil
}
