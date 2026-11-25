package engine

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/db/models"
)

const (
	unansweredHandoverSubject = "Câu hỏi cần quản lý kiểm tra"
	unansweredHandoverTTL     = 24 * time.Hour
)

type unansweredHandoverSender interface {
	SendTrackedGroupMessage(ctx context.Context, groupID, content string) (string, error)
	SendTextMessage(ctx context.Context, userID, content string) error
}

// findUnansweredHandoverGroup resolves a linked, internal CRM group for this OA.
// A new customer assignment or a group archive invalidates the destination.
func findUnansweredHandoverGroup(ctx context.Context, channel models.Channel, groupID string) (models.CRMGroup, error) {
	var group models.CRMGroup
	if db.DB == nil || channel.ChannelType != "zalo_oa" || channel.TenantID == "" || channel.ID == "" {
		return group, errors.New("a Zalo OA channel is required")
	}
	err := db.DB.WithContext(ctx).
		Where("id = ? AND tenant_id = ?", strings.TrimSpace(groupID), channel.TenantID).
		Where("(channel_id = ? OR channel_id = '' OR channel_id IS NULL)", channel.ID).
		Where("COALESCE(TRIM(zalo_group_id), '') <> '' AND COALESCE(TRIM(customer_code), '') = ''").
		Where("NOT EXISTS (SELECT 1 FROM crm_group_customer_codes WHERE group_id = crm_groups.id)").
		Where("NOT EXISTS (SELECT 1 FROM crm_group_customers WHERE group_id = crm_groups.id)").
		Take(&group).Error
	if err != nil {
		return models.CRMGroup{}, fmt.Errorf("management group must be active, internal, and linked to this Zalo OA: %w", err)
	}
	return group, nil
}

func unansweredHandoverStaff(ctx context.Context, channel models.Channel) ([]string, error) {
	var userIDs []string
	err := db.DB.WithContext(ctx).Model(&models.ZaloWhitelist{}).
		Where("tenant_id = ? AND status = ?", channel.TenantID, "active").
		Where("(channel_id = ? OR channel_id = '' OR channel_id IS NULL)", channel.ID).
		Where("COALESCE(TRIM(zalo_user_id), '') <> ''").
		Distinct().Pluck("zalo_user_id", &userIDs).Error
	return userIDs, err
}

type unansweredHandoverDestination struct {
	id      string
	isGroup bool
}

// NotifyUnansweredHandover reports confirmed delivery and whether a manager
// group or eligible staff destination was found. The caller supplies a Zalo adapter
// without the customer chat transcript observer.
func NotifyUnansweredHandover(ctx context.Context, sender unansweredHandoverSender, channel models.Channel, groupID, originatingZaloGroupID, turnID string, alert HandoverAlert) (confirmed, hasDestination bool) {
	if db.DB == nil || channel.ChannelType != "zalo_oa" || channel.TenantID == "" || channel.ID == "" || turnID == "" {
		return false, false
	}
	var destinations []unansweredHandoverDestination
	if groupID = strings.TrimSpace(groupID); groupID != "" {
		group, err := findUnansweredHandoverGroup(ctx, channel, groupID)
		if err != nil || strings.TrimSpace(group.ZaloGroupID) == strings.TrimSpace(originatingZaloGroupID) {
			log.Printf("[unanswered-handover] channel %s management group %s invalid: %v", channel.ID, groupID, err)
			recordUnansweredHandoverLog(ctx, channel.TenantID, groupID, "", errors.New("invalid_management_group"))
			return false, false
		}
		destinations = append(destinations, unansweredHandoverDestination{id: strings.TrimSpace(group.ZaloGroupID), isGroup: true})
	} else {
		userIDs, err := unansweredHandoverStaff(ctx, channel)
		if err != nil {
			log.Printf("[unanswered-handover] channel %s cannot list active staff: %v", channel.ID, err)
			return false, false
		}
		for _, userID := range userIDs {
			destinations = append(destinations, unansweredHandoverDestination{id: strings.TrimSpace(userID)})
		}
	}
	if len(destinations) == 0 {
		log.Printf("[unanswered-handover] channel %s has no active staff to notify", channel.ID)
		return false, false
	}

	location, err := time.LoadLocation(tenantTimezone(channel.TenantID))
	if err != nil {
		location = time.Local
	}
	body := BuildHandoverAlertMessage(alert, time.Now().In(location)) +
		"\nLý do: Bot đã tra cứu và chưa xác nhận được câu trả lời."
	return notifyUnansweredHandoverRecipients(ctx, db.RedisClient, sender, channel, turnID, body, destinations), true
}

func notifyUnansweredHandoverRecipients(ctx context.Context, client *redis.Client, sender unansweredHandoverSender, channel models.Channel, turnID, body string, destinations []unansweredHandoverDestination) bool {
	confirmed := false
	for _, destination := range destinations {
		kind := "dm"
		if destination.isGroup {
			kind = "group"
		}
		key := fmt.Sprintf("unanswered_handover:%s:%s:%s:%s:%s", channel.TenantID, channel.ID, turnID, kind, destination.id)
		if deliverUnansweredHandover(ctx, client, key, func() error {
			err := sendUnansweredHandover(ctx, sender, destination, body)
			recordUnansweredHandoverLog(ctx, channel.TenantID, destination.id, body, err)
			return err
		}) {
			confirmed = true
		}
	}
	return confirmed
}

func sendUnansweredHandover(ctx context.Context, sender unansweredHandoverSender, destination unansweredHandoverDestination, body string) error {
	if !destination.isGroup {
		return sender.SendTextMessage(ctx, destination.id, body)
	}
	receipt, err := sender.SendTrackedGroupMessage(ctx, destination.id, body)
	if err != nil {
		return err
	}
	if strings.TrimSpace(receipt) == "" {
		return errors.New("Zalo did not confirm management-group delivery")
	}
	return nil
}

var completeUnansweredHandover = redis.NewScript(`
if redis.call("get", KEYS[1]) == ARGV[1] then
	return redis.call("set", KEYS[1], ARGV[2], "KEEPTTL")
end
return false
`)

func deliverUnansweredHandover(ctx context.Context, client *redis.Client, key string, send func() error) bool {
	if client == nil {
		return false
	}
	owner := "pending:" + uuid.NewString()
	acquired, err := client.SetNX(ctx, key, owner, unansweredHandoverTTL).Result()
	if err != nil {
		log.Printf("[unanswered-handover] cannot reserve delivery: %v", err)
		return false
	}
	if !acquired {
		return client.Get(ctx, key).Val() == "confirmed"
	}
	// A transport error or missing receipt can follow a successful provider
	// send. Keep this turn reserved so a retry cannot duplicate that alert.
	state := "unconfirmed"
	if err := send(); err == nil {
		state = "confirmed"
	}
	receiptCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer cancel()
	if err := completeUnansweredHandover.Run(receiptCtx, client, []string{key}, owner, state).Err(); err != nil {
		log.Printf("[unanswered-handover] cannot store delivery status: %v", err)
	}
	return state == "confirmed"
}

func recordUnansweredHandoverLog(ctx context.Context, tenantID, recipient, body string, sendErr error) {
	if db.DB == nil {
		return
	}
	entry := models.NotificationLog{
		ID:          uuid.NewString(),
		TenantID:    tenantID,
		ChannelType: "zalo",
		Source:      "handover",
		Recipient:   recipient,
		Subject:     unansweredHandoverSubject,
		Body:        body,
		Status:      "sent",
		SentAt:      time.Now(),
		CreatedAt:   time.Now(),
	}
	if sendErr != nil {
		entry.Status = "failed"
		entry.ErrorMessage = sendErr.Error()
	}
	logCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer cancel()
	if err := db.DB.WithContext(logCtx).Create(&entry).Error; err != nil {
		log.Printf("[unanswered-handover] failed to write notification log: %v", err)
	}
}
