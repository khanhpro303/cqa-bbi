package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/vietbui/chat-quality-agent/customercases"
	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/db/models"
	"gorm.io/gorm"
)

const personalZaloComplaintCustomersKey = "complaint_group_customers"

func validatePersonalZaloComplaintCustomers(ctx context.Context, channel models.Channel, incoming string, merged map[string]interface{}) error {
	patch, err := decodeMetadataObject(incoming)
	if err != nil {
		return err
	}
	raw, changed := patch[personalZaloComplaintCustomersKey]
	if !changed {
		return nil
	}
	entries, ok := raw.(map[string]interface{})
	if !ok {
		return errors.New("Cấu hình nhóm khiếu nại phải chứa nhóm Zalo và mã khách hàng")
	}
	mappings := make(map[string]string, len(entries))
	for id, value := range entries {
		code, _ := value.(string)
		code = strings.TrimSpace(code)
		if id == "" || len(id) > 36 || code == "" || utf8.RuneCountInString(code) > 50 {
			return errors.New("Mỗi nhóm Zalo phải có một mã khách hàng hợp lệ")
		}
		mappings[id] = code
	}
	var previous struct {
		Customers map[string]string `json:"complaint_group_customers"`
	}
	_ = json.Unmarshal([]byte(channel.Metadata), &previous)
	for conversationID, code := range mappings {
		if code == previous.Customers[conversationID] {
			continue
		}
		var conversation models.Conversation
		err := db.DB.WithContext(ctx).Select("id", "external_conversation_id").
			Where("id = ? AND tenant_id = ? AND channel_id = ? AND external_conversation_id LIKE ?",
				conversationID, channel.TenantID, channel.ID, "group:%").Take(&conversation).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return errors.New("Chỉ có thể gắn mã khách với nhóm đã đồng bộ của kênh này")
		}
		if err != nil {
			return err
		}
		if _, ok := cachedCustomer(channel.TenantID, code); !ok {
			return fmt.Errorf("Không tìm thấy mã khách hàng %s", code)
		}
	}
	merged[personalZaloComplaintCustomersKey] = mappings
	return nil
}

func applyPersonalZaloStaffComplaintSource(ctx context.Context, channel models.Channel, report *customercases.Report) string {
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
		if err != nil || strings.TrimSpace(reporter.Name) == "" {
			return unidentified
		}
		break
	}
	if reporter.Name == "" {
		return unidentified
	}

	const missingGroupCustomer = "Nhóm này chưa được gắn mã khách hàng trong phần Khiếu nại của kênh. Chưa ghi nhận được khiếu nại thay khách."
	var settings struct {
		Customers map[string]string `json:"complaint_group_customers"`
	}
	if json.Unmarshal([]byte(channel.Metadata), &settings) != nil || len(settings.Customers) == 0 {
		return missingGroupCustomer
	}
	var conversation models.Conversation
	err = db.DB.WithContext(ctx).Select("id", "customer_name").
		Where("tenant_id = ? AND channel_id = ? AND external_conversation_id = ?", channel.TenantID, channel.ID,
			composeImportConversationID(personalZaloThreadGroup, report.ZaloGroupID)).Take(&conversation).Error
	if err != nil {
		return missingGroupCustomer
	}
	code := strings.TrimSpace(settings.Customers[conversation.ID])
	if code == "" {
		return missingGroupCustomer
	}
	customerName, ok := cachedCustomer(channel.TenantID, code)
	if !ok {
		return "Mã khách hàng đã gắn với nhóm này không còn tra được. Chưa ghi nhận được khiếu nại thay khách."
	}
	report.SubjectType, report.SubjectID = personalZaloThreadGroup, report.ZaloGroupID
	report.MatchCustomerOnly = true
	report.ReporterName, report.ChatGroup = strings.TrimSpace(reporter.Name), conversation.CustomerName
	report.CustomerCode, report.CustomerName = code, firstNonEmpty(customerName, conversation.CustomerName, code)
	return ""
}
