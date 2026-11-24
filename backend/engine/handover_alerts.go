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
	// DedupeKey replaces the per-user dedupe (one alert per user per
	// window) when set: a complaint turn whose case alert failed is its own
	// alert, not a repeat of the user's last one.
	DedupeKey string
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
	if a.DedupeKey != "" {
		dedupKey = fmt.Sprintf("handover_alert:%s:%s", a.TenantID, a.DedupeKey)
	}
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

// SessionIssue is one problem the chat-memory summarizer found in a session.
type SessionIssue struct {
	Type     string
	Severity string
	Detail   string
	Evidence string
}

// SessionIssueAlert describes the issues of one summarized chatbot session.
type SessionIssueAlert struct {
	TenantID     string
	Channel      models.Channel
	SessionID    string
	ZaloUserID   string
	CustomerName string
	CustomerCode string
	ChatGroup    string
	Summary      string
	Issues       []SessionIssue
}

// BuildSessionIssueAlertMessage renders the staff alert for a session's
// issues. Pure for unit tests.
func BuildSessionIssueAlertMessage(a SessionIssueAlert, at time.Time) string {
	var b strings.Builder
	b.WriteString("⚠️ Phiên chatbot có vấn đề cần xử lý\n")
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
	if s := strings.TrimSpace(a.Summary); s != "" {
		fmt.Fprintf(&b, "Tóm tắt: %s\n", truncateRunes(s, 400))
	}
	for _, is := range a.Issues {
		fmt.Fprintf(&b, "• [%s] %s", issueTypeLabel(is.Type), truncateRunes(strings.TrimSpace(is.Detail), 300))
		if ev := strings.TrimSpace(is.Evidence); ev != "" {
			fmt.Fprintf(&b, " (khách: \"%s\")", truncateRunes(ev, 200))
		}
		b.WriteString("\n")
	}
	fmt.Fprintf(&b, "Thời điểm: %s", at.Format("15:04 02/01/2006"))
	return b.String()
}

// issueTypeLabel shows an issue type from the summarizer in Vietnamese.
func issueTypeLabel(t string) string {
	switch strings.ToLower(strings.TrimSpace(t)) {
	case "complaint":
		return "Khiếu nại"
	case "delivery":
		return "Giao hàng"
	case "product_defect":
		return "Hàng lỗi"
	case "price":
		return "Giá"
	case "payment":
		return "Thanh toán, công nợ"
	case "bot_failure":
		return "Bot trả lời sai"
	case "staff_request":
		return "Khách cần nhân viên"
	default:
		return "Khác"
	}
}

// NotifySessionIssues sends a session's serious issues to the channel's
// handover alert group (same destination as handover alerts). It reports
// whether the alert was sent; the caller records that so a session is
// alerted at most once.
func NotifySessionIssues(ctx context.Context, sender HandoverGroupSender, a SessionIssueAlert) bool {
	if len(a.Issues) == 0 {
		return false
	}
	groupID := ParseHandoverAlertGroupID(a.Channel.Metadata)
	if groupID == "" {
		log.Printf("[issue-alert] channel %s has no alert group configured; session %s not alerted", a.Channel.ID, a.SessionID)
		return false
	}
	logAlert := func(body, status, errMsg string) {
		recordHandoverAlertLog(HandoverAlert{TenantID: a.TenantID}, groupID, body, status, errMsg)
	}
	var group models.CRMGroup
	if err := db.DB.Where("id = ? AND tenant_id = ?", groupID, a.TenantID).First(&group).Error; err != nil || strings.TrimSpace(group.ZaloGroupID) == "" {
		log.Printf("[issue-alert] channel %s alert group %s misconfigured: %v", a.Channel.ID, groupID, err)
		logAlert("", "failed", "alert_group_misconfigured")
		return false
	}
	loc, err := time.LoadLocation(tenantTimezone(a.TenantID))
	if err != nil || loc == nil {
		loc = time.Local
	}
	msg := BuildSessionIssueAlertMessage(a, time.Now().In(loc))
	if err := sender.SendGroupMessage(ctx, group.ZaloGroupID, msg); err != nil {
		log.Printf("[issue-alert] send to group %s failed: %v", group.ZaloGroupID, err)
		logAlert(msg, "failed", err.Error())
		return false
	}
	logAlert(msg, "sent", "")
	return true
}

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// CaseAlert tells staff about a customer case: a new one, the customer
// asking again about an open one, or a resolved one the customer says is
// still not fixed.
type CaseAlert struct {
	TenantID      string
	Channel       models.Channel
	Kind          string // created | followup | reopened
	ZaloUserID    string
	ReporterName  string
	CustomerName  string
	CustomerCode  string
	ChatGroup     string
	TypeLabel     string
	Severity      string
	Title         string
	Detail        string
	Evidence      string
	StatusLabel   string
	FollowupCount int
	ImageCount    int
	// FromSummary: the problem was found when the session was summarized,
	// not recorded by the bot while chatting.
	FromSummary bool
	// StreamName is the complaint stream (bảo hành, marketing...) and
	// GroupID its alert group; an empty or unusable GroupID falls back to
	// the channel's handover alert group.
	StreamName string
	GroupID    string
	// MovedBy: staff who moved the case to this stream (Kind "moved").
	MovedBy string
}

// BuildCaseAlertMessage renders a case alert. Pure for unit tests. It never
// contains a case id: staff find the case by customer on the Insights khách
// page.
func BuildCaseAlertMessage(a CaseAlert, at time.Time) string {
	var b strings.Builder
	switch a.Kind {
	case "followup":
		fmt.Fprintf(&b, "🔁 Khách hỏi lại vấn đề đang chờ xử lý (lần %d)\n", a.FollowupCount)
	case "reopened":
		b.WriteString("♻️ Khách báo vấn đề đã xử lý vẫn chưa ổn\n")
	case "moved":
		if by := strings.TrimSpace(a.MovedBy); by != "" {
			fmt.Fprintf(&b, "➡️ %s chuyển khiếu nại sang luồng này\n", by)
		} else {
			b.WriteString("➡️ Khiếu nại được chuyển sang luồng này\n")
		}
	default:
		if a.Severity == "high" {
			b.WriteString("🚨 Khiếu nại mới cần xử lý gấp\n")
		} else {
			b.WriteString("📋 Khiếu nại mới cần xử lý\n")
		}
	}
	name := strings.TrimSpace(a.CustomerName)
	if name == "" {
		name = strings.TrimSpace(a.ReporterName)
	}
	if name == "" {
		name = "(chưa xác minh)"
	}
	fmt.Fprintf(&b, "Khách: %s\n", name)
	if code := strings.TrimSpace(a.CustomerCode); code != "" {
		fmt.Fprintf(&b, "Mã KH: %s\n", code)
	}
	if g := strings.TrimSpace(a.ChatGroup); g != "" {
		fmt.Fprintf(&b, "Nhóm chat: %s\n", g)
		if r := strings.TrimSpace(a.ReporterName); r != "" && r != name {
			fmt.Fprintf(&b, "Người báo: %s\n", r)
		}
	} else if a.ZaloUserID != "" {
		fmt.Fprintf(&b, "Zalo ID: %s\n", a.ZaloUserID)
	}
	label := strings.TrimSpace(a.TypeLabel)
	if label == "" {
		label = "Khác"
	}
	if s := strings.TrimSpace(a.StreamName); s != "" {
		fmt.Fprintf(&b, "Luồng: %s\n", s)
	}
	fmt.Fprintf(&b, "Vấn đề: [%s] %s\n", label, truncateRunes(strings.TrimSpace(a.Title), 200))
	if d := strings.TrimSpace(a.Detail); d != "" && d != strings.TrimSpace(a.Title) {
		fmt.Fprintf(&b, "Chi tiết: %s\n", truncateRunes(d, 500))
	}
	if ev := strings.TrimSpace(a.Evidence); ev != "" {
		fmt.Fprintf(&b, "Khách nói: \"%s\"\n", truncateRunes(ev, 300))
	}
	if a.ImageCount > 0 {
		fmt.Fprintf(&b, "Ảnh khách gửi: %d ảnh (xem ở trang Insights khách → Khiếu nại)\n", a.ImageCount)
	}
	if a.Kind != "created" && a.StatusLabel != "" {
		fmt.Fprintf(&b, "Trạng thái: %s\n", a.StatusLabel)
	}
	if a.FromSummary {
		b.WriteString("(Phát hiện khi tóm tắt phiên chat)\n")
	}
	fmt.Fprintf(&b, "Thời điểm: %s", at.Format("15:04 02/01/2006"))
	return b.String()
}

// NotifyCase sends a case alert to the case's stream group, or else the
// channel's handover alert group. It reports whether the alert was sent.
func NotifyCase(ctx context.Context, sender HandoverGroupSender, a CaseAlert) bool {
	handover := ParseHandoverAlertGroupID(a.Channel.Metadata)
	groupID := strings.TrimSpace(a.GroupID)
	if groupID == "" {
		groupID = handover
	}
	if groupID == "" {
		log.Printf("[case-alert] channel %s has no alert group configured; case not alerted", a.Channel.ID)
		return false
	}
	logAlert := func(body, status, errMsg string) {
		recordHandoverAlertLog(HandoverAlert{TenantID: a.TenantID}, groupID, body, status, errMsg)
	}
	var group models.CRMGroup
	err := db.DB.Where("id = ? AND tenant_id = ?", groupID, a.TenantID).First(&group).Error
	if (err != nil || strings.TrimSpace(group.ZaloGroupID) == "") && groupID != handover && handover != "" {
		// The stream's group is gone or unlinked: staff still hear about
		// the case in the handover group.
		log.Printf("[case-alert] channel %s stream group %s unusable (%v); using the handover group", a.Channel.ID, groupID, err)
		groupID = handover
		group = models.CRMGroup{}
		err = db.DB.Where("id = ? AND tenant_id = ?", groupID, a.TenantID).First(&group).Error
	}
	if err != nil || strings.TrimSpace(group.ZaloGroupID) == "" {
		log.Printf("[case-alert] channel %s alert group %s misconfigured: %v", a.Channel.ID, groupID, err)
		logAlert("", "failed", "alert_group_misconfigured")
		return false
	}
	loc, err := time.LoadLocation(tenantTimezone(a.TenantID))
	if err != nil || loc == nil {
		loc = time.Local
	}
	msg := BuildCaseAlertMessage(a, time.Now().In(loc))
	if err := sender.SendGroupMessage(ctx, group.ZaloGroupID, msg); err != nil {
		log.Printf("[case-alert] send to group %s failed: %v", group.ZaloGroupID, err)
		logAlert(msg, "failed", err.Error())
		return false
	}
	logAlert(msg, "sent", "")
	return true
}
