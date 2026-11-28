package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/db/models"
	"gorm.io/gorm"
)

const (
	personalZaloComplaintCustomersKey = "complaint_group_customers"
	personalZaloComplaintCustomersMax = 100
)

type personalZaloGroupComplaintCustomer struct {
	Code      string
	Name      string
	GroupName string
}

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
	if len(entries) > personalZaloComplaintCustomersMax {
		return fmt.Errorf("Mỗi kênh chỉ được cấu hình tối đa %d nhóm khiếu nại dự phòng", personalZaloComplaintCustomersMax)
	}

	mappings := make(map[string]string, len(entries))
	for id, value := range entries {
		code, _ := value.(string)
		id, code = strings.TrimSpace(id), strings.ToUpper(strings.TrimSpace(code))
		if id == "" || len(id) > 36 || code == "" || utf8.RuneCountInString(code) > 50 {
			return errors.New("Mỗi nhóm Zalo phải có một mã khách hàng hợp lệ")
		}
		if _, duplicate := mappings[id]; duplicate {
			return errors.New("Mỗi nhóm Zalo chỉ được cấu hình một mã khách hàng dự phòng")
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
		err := db.DB.WithContext(ctx).Select("id").
			Where("id = ? AND tenant_id = ? AND channel_id = ? AND external_conversation_id LIKE ?",
				conversationID, channel.TenantID, channel.ID, "group:%").Take(&conversation).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return errors.New("Chỉ có thể gắn mã khách với nhóm đã đồng bộ của kênh này")
		}
		if err != nil {
			return err
		}
		if _, reply := personalZaloComplaintCustomer(ctx, channel.TenantID, code); reply != "" {
			return errors.New(reply)
		}
	}
	merged[personalZaloComplaintCustomersKey] = mappings
	return nil
}

func personalZaloComplaintCustomerFallback(ctx context.Context, channel models.Channel, zaloGroupID string) (personalZaloGroupComplaintCustomer, error) {
	var settings struct {
		Customers map[string]string `json:"complaint_group_customers"`
	}
	if err := json.Unmarshal([]byte(channel.Metadata), &settings); err != nil || len(settings.Customers) == 0 {
		return personalZaloGroupComplaintCustomer{}, nil
	}

	var conversation models.Conversation
	err := db.DB.WithContext(ctx).Select("id", "customer_name").
		Where("tenant_id = ? AND channel_id = ? AND external_conversation_id = ?", channel.TenantID, channel.ID,
			composeImportConversationID(personalZaloThreadGroup, zaloGroupID)).Take(&conversation).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return personalZaloGroupComplaintCustomer{}, nil
	}
	if err != nil {
		return personalZaloGroupComplaintCustomer{}, err
	}
	code := strings.ToUpper(strings.TrimSpace(settings.Customers[conversation.ID]))
	if code == "" {
		return personalZaloGroupComplaintCustomer{}, nil
	}
	name, reply := personalZaloComplaintCustomer(ctx, channel.TenantID, code)
	if reply != "" {
		return personalZaloGroupComplaintCustomer{}, errors.New(reply)
	}
	return personalZaloGroupComplaintCustomer{Code: code, Name: name, GroupName: conversation.CustomerName}, nil
}
