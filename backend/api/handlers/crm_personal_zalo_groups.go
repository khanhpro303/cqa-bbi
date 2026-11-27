package handlers

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/vietbui/chat-quality-agent/api/middleware"
	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/db/models"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const personalZaloGroupConversationPrefix = "group:"

type personalZaloCRMConversation struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	ChannelID   string `json:"channel_id"`
	ChannelName string `json:"channel_name"`
	ZaloGroupID string `json:"-"`
}

func personalZaloCRMConversations(database *gorm.DB, tenantID string) *gorm.DB {
	return database.Table("conversations AS conversation").
		Select("conversation.id, conversation.customer_name AS name, channel.id AS channel_id, channel.name AS channel_name, SUBSTRING(conversation.external_conversation_id, ?) AS zalo_group_id", len(personalZaloGroupConversationPrefix)+1).
		Joins("JOIN channels AS channel ON channel.id = conversation.channel_id AND channel.tenant_id = conversation.tenant_id").
		Where("conversation.tenant_id = ? AND channel.channel_type = ? AND channel.is_active = ?", tenantID, "personal_zalo_import", true).
		Where("REGEXP_LIKE(conversation.external_conversation_id, ?, 'c')", "^"+personalZaloGroupConversationPrefix+"[0-9]{1,64}$")
}

// ListUnlinkedPersonalZaloCRMConversations lists synced groups that do not
// already have a CRM identity, including groups whose identities are archived.
func ListUnlinkedPersonalZaloCRMConversations(c *gin.Context) {
	search := strings.TrimSpace(c.Query("search"))
	if utf8.RuneCountInString(search) > 128 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_search"})
		return
	}
	query := personalZaloCRMConversations(db.DB.WithContext(c.Request.Context()), middleware.GetTenantID(c)).
		Where(`NOT EXISTS (
			SELECT 1 FROM crm_groups AS crm_group
			WHERE crm_group.tenant_id = conversation.tenant_id
			AND crm_group.zalo_group_id = SUBSTRING(conversation.external_conversation_id, ?)
		)`, len(personalZaloGroupConversationPrefix)+1)
	if search != "" {
		pattern := "%" + strings.NewReplacer("!", "!!", "%", "!%", "_", "!_").Replace(search) + "%"
		query = query.Where("conversation.customer_name LIKE ? ESCAPE '!' OR conversation.external_conversation_id = ?", pattern, personalZaloGroupConversationPrefix+search)
	}
	conversations := make([]personalZaloCRMConversation, 0)
	if err := query.Order("conversation.last_message_at DESC, conversation.id").Limit(50).Find(&conversations).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed_to_list_personal_zalo_conversations"})
		return
	}
	for i := range conversations {
		if strings.TrimSpace(conversations[i].Name) == "" {
			conversations[i].Name = conversations[i].ZaloGroupID
		}
	}
	c.JSON(http.StatusOK, conversations)
}

func linkPersonalZaloCRMGroup(c *gin.Context, tenantID, conversationID, name, description string, codes []string) {
	name = strings.TrimSpace(name)
	if name == "" || utf8.RuneCountInString(name) > 255 || len(conversationID) > 36 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request"})
		return
	}

	var group models.CRMGroup
	var existing models.CRMGroup
	var conversation personalZaloCRMConversation
	err := db.DB.WithContext(c.Request.Context()).Transaction(func(tx *gorm.DB) error {
		// Multiple personal accounts can sync the same Zalo group into
		// different conversations. Serialize creates on the tenant first,
		// so the second request sees the CRM identity made by the first.
		var tenant models.Tenant
		if err := tx.Select("id").Clauses(clause.Locking{Strength: "UPDATE"}).Take(&tenant, "id = ?", tenantID).Error; err != nil {
			return err
		}
		if err := personalZaloCRMConversations(tx, tenantID).Where("conversation.id = ?", conversationID).Take(&conversation).Error; err != nil {
			return err
		}
		err := tx.Unscoped().Select("id", "name", "archived_at").
			Where("tenant_id = ? AND zalo_group_id = ?", tenantID, conversation.ZaloGroupID).
			Take(&existing).Error
		if err == nil {
			return nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		group = models.CRMGroup{
			ID: uuid.NewString(), TenantID: tenantID, Name: name, Description: description,
			ZaloGroupID: conversation.ZaloGroupID, ChannelID: conversation.ChannelID, CustomerCode: codes[0],
		}
		if err := tx.Create(&group).Error; err != nil {
			return err
		}
		_, err = db.SetGroupCustomerCodes(tx, tenantID, group.ID, codes, codes[0])
		return err
	})
	if errors.Is(err, gorm.ErrRecordNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": "personal_zalo_group_conversation_not_found", "details": "Không tìm thấy hội thoại nhóm Zalo cá nhân đã đồng bộ."})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed_to_link_personal_zalo_group"})
		return
	}
	if existing.ID != "" {
		details := fmt.Sprintf("Nhóm Zalo này đã thuộc nhóm CRM %q.", existing.Name)
		if existing.ArchivedAt.Valid {
			details += " Hãy khôi phục nhóm CRM đã lưu trữ để tiếp tục sử dụng."
		}
		c.JSON(http.StatusConflict, gin.H{"error": "crm_group_already_linked", "details": details})
		return
	}

	group.CustomerCodes = codes
	group.Channel = &models.Channel{ID: conversation.ChannelID, TenantID: tenantID, ChannelType: "personal_zalo_import", Name: conversation.ChannelName, IsActive: true}
	db.LogActivity(tenantID, middleware.GetUserID(c), middleware.GetUserEmail(c), "crm_group.create", "crm_group", group.ID,
		fmt.Sprintf("Liên kết nhóm Zalo cá nhân %q (zalo_group_id=%s, khách=%s)", group.Name, group.ZaloGroupID, group.CustomerCode), "", c.ClientIP())
	c.JSON(http.StatusCreated, group)
}
