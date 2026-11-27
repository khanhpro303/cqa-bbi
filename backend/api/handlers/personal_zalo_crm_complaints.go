package handlers

import (
	"context"
	"strings"
	"time"

	"github.com/vietbui/chat-quality-agent/customercases"
	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/db/models"
)

func personalZaloGroupBlocked(ctx context.Context, tenantID, zaloGroupID string) (bool, error) {
	// OA owns its GMF even when paused. An archived CRM group is also
	// excluded so the personal bot cannot keep answering after archive.
	var ids []string
	err := db.DB.WithContext(ctx).Model(&models.CRMGroup{}).Unscoped().
		Joins("LEFT JOIN channels AS oa ON oa.id = crm_groups.channel_id AND oa.tenant_id = crm_groups.tenant_id").
		Where("crm_groups.tenant_id = ? AND crm_groups.zalo_group_id = ?", tenantID, zaloGroupID).
		Where("(oa.channel_type = ? OR crm_groups.archived_at IS NOT NULL)", "zalo_oa").
		Limit(1).Pluck("crm_groups.id", &ids).Error
	return len(ids) > 0, err
}

func applyPersonalZaloStaffComplaintSource(ctx context.Context, channel models.Channel, report *customercases.Report) string {
	blocked, err := personalZaloGroupBlocked(ctx, channel.TenantID, report.ZaloGroupID)
	if err != nil || blocked {
		return "Nhóm này hiện không do chatbot Zalo cá nhân xử lý."
	}
	approved, err := personalZaloThreadApproved(ctx, personalZaloChatbotRequest{
		ChannelID: channel.ID, AccountExternalID: channel.ExternalID, ThreadType: personalZaloThreadGroup,
		ThreadID: report.ZaloGroupID, SentAt: time.Now(),
	})
	if err != nil || !approved {
		return "Nhóm này hiện chưa được duyệt để chatbot Zalo cá nhân trả lời."
	}

	const unidentified = "Chưa xác định được nhân viên đã liên kết với kênh này."
	owners, err := decodePersonalZaloAccountOwners(channel.Metadata)
	if err != nil {
		return unidentified
	}
	var reporter models.User
	for _, owner := range owners {
		if owner.AccountExternalID != report.ZaloUserID || owner.UserID == "" {
			continue
		}
		err := db.DB.WithContext(ctx).Model(&models.User{}).Select("users.name").
			Joins("JOIN user_tenants AS membership ON membership.user_id = users.id AND membership.tenant_id = ?", channel.TenantID).
			Where("users.id = ?", owner.UserID).Take(&reporter).Error
		if err != nil {
			return unidentified
		}
		break
	}
	if strings.TrimSpace(reporter.Name) == "" {
		return unidentified
	}

	var groups []models.CRMGroup
	err = db.DB.WithContext(ctx).Select("name", "customer_code").
		Where("tenant_id = ? AND zalo_group_id = ?", channel.TenantID, report.ZaloGroupID).Limit(2).Find(&groups).Error
	if err != nil || len(groups) != 1 || strings.TrimSpace(groups[0].CustomerCode) == "" {
		return "Chưa xác định được mã khách của nhóm trong CRM → Nhóm Zalo. Chưa ghi nhận được khiếu nại thay khách."
	}
	group := groups[0]
	code := strings.TrimSpace(group.CustomerCode)
	report.SubjectType, report.SubjectID = personalZaloThreadGroup, report.ZaloGroupID
	report.MatchCustomerOnly = true
	report.ReporterName, report.ChatGroup = strings.TrimSpace(reporter.Name), group.Name
	report.CustomerCode, report.CustomerName = code, firstNonEmpty(cachedCustomerName(channel.TenantID, code), group.Name, code)
	return ""
}
