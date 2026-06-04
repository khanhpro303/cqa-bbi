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

// campaignAlertDedupeWindow caps how often the scheduler path sends a failure
// alert for the same campaign. Each segment fires as an independent job, so a
// burst of segments sharing one cron tick would otherwise produce one alert per
// segment; this window collapses them into roughly one message.
const campaignAlertDedupeWindow = 5 * time.Minute

// campaignAlertChannelType is the NotificationLog.ChannelType value used for
// campaign failure alerts (distinct from the job dispatcher's telegram|email).
const campaignAlertChannelType = "zalo_oa"

// alertFail is one failed segment as shown in the alert message: a group name
// and a human-friendly reason. Kept tiny so the message builder stays pure.
type alertFail struct {
	GroupName string
	Reason    string
}

// friendlyRunError maps a CampaignRun.ErrorMessage (sentinel code or raw error)
// to a Vietnamese reason for the alert message.
func friendlyRunError(code string) string {
	code = strings.TrimSpace(code)
	switch code {
	case "group_not_found":
		return "Nhóm không tồn tại"
	case "group_has_no_zalo_group":
		return "Nhóm chưa liên kết nhóm Zalo"
	case "channel_inactive":
		return "Kênh Zalo OA đang tắt"
	}
	// "text sent but N/M image(s) failed: ..." -> "Gửi chữ OK nhưng N/M ảnh lỗi"
	if strings.HasPrefix(code, "text sent but ") {
		var n, m int
		if _, err := fmt.Sscanf(code, "text sent but %d/%d image(s) failed", &n, &m); err == nil {
			return fmt.Sprintf("Gửi chữ OK nhưng %d/%d ảnh lỗi", n, m)
		}
	}
	if code == "" {
		return "Lỗi không xác định"
	}
	return code
}

// buildCampaignAlertMessage renders the aggregated failure alert. Pure (no I/O)
// so it is unit-testable; the caller passes an already tenant-localized time.
func buildCampaignAlertMessage(campaignName string, fails []alertFail, at time.Time) string {
	var b strings.Builder
	name := strings.TrimSpace(campaignName)
	if name == "" {
		name = "(không tên)"
	}
	fmt.Fprintf(&b, "⚠️ Chiến dịch \"%s\" có %d nhóm gửi lỗi\n", name, len(fails))
	for _, f := range fails {
		g := strings.TrimSpace(f.GroupName)
		if g == "" {
			g = "(không rõ nhóm)"
		}
		fmt.Fprintf(&b, "• %s: %s\n", g, f.Reason)
	}
	fmt.Fprintf(&b, "Thời điểm: %s", at.Format("15:04 02/01/2006"))
	return b.String()
}

// parseCampaignAlertConfig reads the alert config from a channel's metadata JSON.
// Returns (false, "") for any missing/malformed value so callers no-op safely.
func parseCampaignAlertConfig(metadata string) (enabled bool, groupID string) {
	if strings.TrimSpace(metadata) == "" {
		return false, ""
	}
	var m map[string]interface{}
	if err := json.Unmarshal([]byte(metadata), &m); err != nil {
		return false, ""
	}
	if v, ok := m["campaign_alerts_enabled"].(bool); ok {
		enabled = v
	}
	if v, ok := m["campaign_alert_group_id"].(string); ok {
		groupID = strings.TrimSpace(v)
	}
	return enabled, groupID
}

// shouldSendCampaignAlert reports whether the scheduler path may send a failure
// alert for this campaign now, i.e. no alert was successfully sent within
// campaignAlertDedupeWindow. Only "sent" rows suppress; a failed attempt does
// not block a retry. The manual ("Gửi ngay") path skips this — it already
// aggregates one alert per broadcast.
func shouldSendCampaignAlert(tenantID, campaignID string) bool {
	var n int64
	cutoff := time.Now().Add(-campaignAlertDedupeWindow)
	db.DB.Model(&models.NotificationLog{}).
		Where("tenant_id = ? AND channel_type = ? AND job_id = ? AND status = ? AND sent_at > ?",
			tenantID, campaignAlertChannelType, campaignID, "sent", cutoff).
		Limit(1).Count(&n)
	return n == 0
}

// recordCampaignAlertLog writes a NotificationLog row for a campaign alert. The
// campaign id is stored in JobID (reused as the dedupe key); the alert group id
// in Recipient. Best-effort: a logging failure never affects the send.
func recordCampaignAlertLog(tenantID, campaignID, recipient, subject, body, status, errMsg string) {
	now := time.Now()
	if err := db.DB.Create(&models.NotificationLog{
		ID:           uuid.New().String(),
		TenantID:     tenantID,
		JobID:        campaignID,
		ChannelType:  campaignAlertChannelType,
		Recipient:    recipient,
		Subject:      subject,
		Body:         body,
		Status:       status,
		ErrorMessage: errMsg,
		SentAt:       now,
		CreatedAt:    now,
	}).Error; err != nil {
		log.Printf("[campaign-alert] failed to write notification log (campaign %s): %v", campaignID, err)
	}
}

// notifyFailures sends one aggregated Zalo alert for the given failed runs to the
// channel's configured alert group. No-op when the campaign's channel has alerts
// disabled or no alert group. Never returns an error — alerting must never fail a
// broadcast; outcomes are logged and recorded in NotificationLog.
func (b *campaignBroadcast) notifyFailures(ctx context.Context, failedRuns []models.CampaignRun) {
	if len(failedRuns) == 0 {
		return
	}
	enabled, groupID := parseCampaignAlertConfig(b.channel.Metadata)
	if !enabled || groupID == "" {
		return
	}

	var alertGroup models.CRMGroup
	gErr := db.DB.Where("id = ? AND tenant_id = ?", groupID, b.tenantID).First(&alertGroup).Error
	if gErr != nil || strings.TrimSpace(alertGroup.ZaloGroupID) == "" {
		log.Printf("[campaign-alert] campaign %s alert group %s misconfigured: %v", b.campaign.ID, groupID, gErr)
		recordCampaignAlertLog(b.tenantID, b.campaign.ID, groupID, b.campaign.Name, "", "failed", "alert_group_misconfigured")
		return
	}

	names := b.resolveSegmentGroupNames(failedRuns)
	fails := make([]alertFail, 0, len(failedRuns))
	for _, r := range failedRuns {
		fails = append(fails, alertFail{GroupName: names[r.SegmentID], Reason: friendlyRunError(r.ErrorMessage)})
	}

	loc, err := time.LoadLocation(tenantTimezone(b.tenantID))
	if err != nil || loc == nil {
		loc = time.Local
	}
	msg := buildCampaignAlertMessage(b.campaign.Name, fails, time.Now().In(loc))

	status, errMsg := "sent", ""
	if sErr := b.adapter.SendGroupMessage(ctx, alertGroup.ZaloGroupID, msg); sErr != nil {
		status, errMsg = "failed", sErr.Error()
		log.Printf("[campaign-alert] campaign %s alert send failed: %v", b.campaign.ID, sErr)
	}
	recordCampaignAlertLog(b.tenantID, b.campaign.ID, groupID, b.campaign.Name, msg, status, errMsg)
}

// resolveSegmentGroupNames maps each failed run's SegmentID to its group's name,
// using the broadcast's preloaded segments and one CRMGroup lookup.
func (b *campaignBroadcast) resolveSegmentGroupNames(runs []models.CampaignRun) map[string]string {
	segToGroup := make(map[string]string, len(b.campaign.Segments))
	for _, s := range b.campaign.Segments {
		segToGroup[s.ID] = s.GroupID
	}

	groupIDs := make([]string, 0, len(runs))
	seen := map[string]bool{}
	for _, r := range runs {
		if gid := segToGroup[r.SegmentID]; gid != "" && !seen[gid] {
			seen[gid] = true
			groupIDs = append(groupIDs, gid)
		}
	}

	nameByGroup := make(map[string]string, len(groupIDs))
	if len(groupIDs) > 0 {
		var groups []models.CRMGroup
		db.DB.Where("id IN ? AND tenant_id = ?", groupIDs, b.tenantID).Find(&groups)
		for _, g := range groups {
			nameByGroup[g.ID] = g.Name
		}
	}

	out := make(map[string]string, len(runs))
	for _, r := range runs {
		out[r.SegmentID] = nameByGroup[segToGroup[r.SegmentID]]
	}
	return out
}
