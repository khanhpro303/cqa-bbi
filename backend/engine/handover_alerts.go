package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/db/models"
)

// handoverAlertDedupWindow keeps one customer's complaint burst ("hàng lỗi",
// "sao chưa giao", "gọi lại giúp") from paging staff once per message.
const handoverAlertDedupWindow = 30 * time.Minute

const handoverAlertMaxQuote = 500

// HandoverGroupSender is the slice of the Zalo OA adapter the handover alert
// needs, so tests can capture the message without calling Zalo.
type HandoverGroupSender interface {
	SendGroupMessage(ctx context.Context, groupID, text string) error
}

// HandoverAlert describes one customer message the intent classifier marked
// HANDOVER (complaint or request for a human).
type HandoverAlert struct {
	TenantID     string
	Channel      models.Channel
	ZaloUserID   string
	CustomerName string
	CustomerCode string
	ChatGroup    string // CRM group name when the message came from a group chat
	Message      string
}

// ParseHandoverAlertGroupID returns the CRM group that receives handover
// alerts for a Zalo OA channel: `handover_alert_group_id` when set, otherwise
// the campaign alert group when campaign alerts are enabled, so channels that
// already route staff alerts somewhere get handover alerts there too.
func ParseHandoverAlertGroupID(metadata string) string {
	if strings.TrimSpace(metadata) == "" {
		return ""
	}
	var m map[string]interface{}
	if err := json.Unmarshal([]byte(metadata), &m); err != nil {
		return ""
	}
	if v, ok := m["handover_alert_group_id"].(string); ok && strings.TrimSpace(v) != "" {
		return strings.TrimSpace(v)
	}
	enabled, groupID := parseCampaignAlertConfig(metadata)
	if enabled {
		return groupID
	}
	return ""
}

// BuildHandoverAlertMessage renders the staff alert. Pure for unit tests.
func BuildHandoverAlertMessage(a HandoverAlert, at time.Time) string {
	var b strings.Builder
	b.WriteString("🔔 Khách cần nhân viên hỗ trợ\n")
	name := strings.TrimSpace(a.CustomerName)
	if name == "" {
		name = "(chưa xác minh)"
	}
	fmt.Fprintf(&b, "Khách: %s\n", name)
	if code := strings.TrimSpace(a.CustomerCode); code != "" {
		fmt.Fprintf(&b, "Mã KH: %s\n", code)
	}
	if g := strings.TrimSpace(a.ChatGroup); g != "" {
		fmt.Fprintf(&b, "Nhóm chat: %s\n", g)
	}
	fmt.Fprintf(&b, "Zalo ID: %s\n", a.ZaloUserID)
	quote := strings.TrimSpace(a.Message)
	if r := []rune(quote); len(r) > handoverAlertMaxQuote {
		quote = string(r[:handoverAlertMaxQuote]) + "…"
	}
	fmt.Fprintf(&b, "Tin nhắn: \"%s\"\n", quote)
	fmt.Fprintf(&b, "Thời điểm: %s", at.Format("15:04 02/01/2006"))
	return b.String()
}

// NotifyHandover sends the handover alert to the channel's staff alert group.
// It reports whether staff are covered: true when the alert was sent now or
// an alert for the same customer was already sent within the dedup window.
// It never returns an error: the customer reply must not depend on alerting.
// Every attempt is recorded in NotificationLog (source "handover").
func NotifyHandover(ctx context.Context, sender HandoverGroupSender, a HandoverAlert) bool {
	groupID := ParseHandoverAlertGroupID(a.Channel.Metadata)
	if groupID == "" {
		log.Printf("[handover-alert] channel %s has no alert group configured; staff not notified (user %s)", a.Channel.ID, a.ZaloUserID)
		return false
	}

	var group models.CRMGroup
	if err := db.DB.Where("id = ? AND tenant_id = ?", groupID, a.TenantID).First(&group).Error; err != nil || strings.TrimSpace(group.ZaloGroupID) == "" {
		log.Printf("[handover-alert] channel %s alert group %s misconfigured: %v", a.Channel.ID, groupID, err)
		recordHandoverAlertLog(a, groupID, "", "failed", "alert_group_misconfigured")
		return false
	}

	dedupKey := fmt.Sprintf("handover_alert:%s:%s:%s", a.TenantID, a.Channel.ID, a.ZaloUserID)
	if db.RedisClient != nil {
		fresh, err := db.RedisClient.SetNX(ctx, dedupKey, "1", handoverAlertDedupWindow).Result()
		if err == nil && !fresh {
			log.Printf("[handover-alert] alert for user %s already sent within %s; skipping", a.ZaloUserID, handoverAlertDedupWindow)
			return true
		}
	}

	loc, err := time.LoadLocation(tenantTimezone(a.TenantID))
	if err != nil || loc == nil {
		loc = time.Local
	}
	msg := BuildHandoverAlertMessage(a, time.Now().In(loc))

	if err := sender.SendGroupMessage(ctx, group.ZaloGroupID, msg); err != nil {
		log.Printf("[handover-alert] send to group %s failed: %v", group.ZaloGroupID, err)
		if db.RedisClient != nil {
			// Let the next complaint retry instead of being swallowed.
			db.RedisClient.Del(ctx, dedupKey)
		}
		recordHandoverAlertLog(a, groupID, msg, "failed", err.Error())
		return false
	}
	recordHandoverAlertLog(a, groupID, msg, "sent", "")
	return true
}

func recordHandoverAlertLog(a HandoverAlert, recipient, body, status, errMsg string) {
	if db.DB == nil {
		return
	}
	now := time.Now()
	if err := db.DB.Create(&models.NotificationLog{
		ID:           uuid.New().String(),
		TenantID:     a.TenantID,
		ChannelType:  "zalo",
		Source:       "handover",
		Recipient:    recipient,
		Subject:      "Khách cần nhân viên hỗ trợ",
		Body:         body,
		Status:       status,
		ErrorMessage: errMsg,
		SentAt:       now,
		CreatedAt:    now,
	}).Error; err != nil {
		log.Printf("[handover-alert] failed to write notification log: %v", err)
	}
}
