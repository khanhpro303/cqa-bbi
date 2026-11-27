package handlers

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/vietbui/chat-quality-agent/api/middleware"
	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/db/models"
	"gorm.io/gorm"
)

// The gateway records the peer as both IDs in a direct thread and stores the
// sender UID on incoming messages. Use that evidence before granting staff access.
func personalZaloStaffConversations(database *gorm.DB, channel models.Channel) *gorm.DB {
	return database.Model(&models.Conversation{}).
		Where("conversations.tenant_id = ? AND conversations.channel_id = ?", channel.TenantID, channel.ID).
		Where("JSON_UNQUOTE(JSON_EXTRACT(conversations.metadata, '$.account_external_id')) = ?", channel.ExternalID).
		Where("JSON_UNQUOTE(JSON_EXTRACT(conversations.metadata, '$.thread_type')) = ?", personalZaloThreadUser).
		Where("conversations.external_user_id REGEXP ? AND conversations.external_user_id <> ?",
			personalZaloUIDPattern.String(), channel.ExternalID).
		Where("conversations.external_conversation_id = CONCAT(?, conversations.external_user_id)", personalZaloThreadUser+":").
		Where(`EXISTS (SELECT 1 FROM messages AS incoming
			WHERE incoming.conversation_id = conversations.id AND incoming.tenant_id = conversations.tenant_id
			AND incoming.sender_type = 'customer'
			AND JSON_UNQUOTE(JSON_EXTRACT(incoming.raw_data, '$.sender_uid')) = conversations.external_user_id
			AND JSON_UNQUOTE(JSON_EXTRACT(incoming.raw_data, '$.thread_id')) = conversations.external_user_id)`)
}

func ListPersonalZaloStaffConversations(c *gin.Context) {
	channel, ok := personalZaloIdentityAdminChannel(c)
	if !ok {
		return
	}
	if !personalZaloUIDPattern.MatchString(channel.ExternalID) {
		c.JSON(http.StatusConflict, gin.H{"error": "personal_zalo_account_not_bound"})
		return
	}
	type result struct {
		ID       string `json:"id"`
		Name     string `json:"name"`
		SenderID string `json:"sender_id"`
	}
	conversations := make([]result, 0)
	query := personalZaloStaffConversations(db.DB.WithContext(c.Request.Context()), channel)
	if search := strings.TrimSpace(c.Query("q")); search != "" {
		if len(search) > 512 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request"})
			return
		}
		escaped := strings.NewReplacer("!", "!!", "%", "!%", "_", "!_").Replace(search)
		query = query.Where("conversations.customer_name LIKE ? ESCAPE '!' OR conversations.external_user_id = ?",
			"%"+escaped+"%", search)
	}
	err := query.Select("conversations.id, conversations.customer_name AS name, conversations.external_user_id AS sender_id").
		Order("conversations.last_message_at DESC, conversations.id").Limit(30).Scan(&conversations).Error
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "personal_zalo_identity_unavailable"})
		return
	}
	c.JSON(http.StatusOK, conversations)
}

func ApprovePersonalZaloStaffLink(c *gin.Context) {
	var req struct {
		ConversationID string `json:"conversation_id" binding:"required,max=36"`
		TargetID       string `json:"target_id" binding:"required,max=36"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request"})
		return
	}
	channel, ok := personalZaloIdentityAdminChannel(c)
	if !ok {
		return
	}
	if !personalZaloUIDPattern.MatchString(channel.ExternalID) {
		c.JSON(http.StatusConflict, gin.H{"error": "personal_zalo_account_not_bound"})
		return
	}
	var (
		link      models.PersonalZaloIdentityLink
		errorCode = "personal_zalo_identity_target_not_found"
	)
	now := time.Now().UTC()
	err := db.DB.WithContext(c.Request.Context()).Transaction(func(tx *gorm.DB) error {
		if _, err := loadPersonalZaloIdentitySource(tx, channel, personalZaloStaffIdentity, req.TargetID); err != nil {
			return err
		}
		var conversation models.Conversation
		err := personalZaloStaffConversations(tx, channel).Select("conversations.external_user_id").
			Where("conversations.id = ?", req.ConversationID).Take(&conversation).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			errorCode = "personal_zalo_identity_conversation_not_found"
			return err
		}
		if err != nil {
			return err
		}
		senderID := conversation.ExternalUserID
		if !personalZaloUIDPattern.MatchString(senderID) {
			errorCode = "personal_zalo_identity_conversation_not_found"
			return gorm.ErrRecordNotFound
		}
		var localStaff models.ZaloWhitelist
		err = tx.Select("id").Where("tenant_id = ? AND channel_id = ? AND zalo_user_id = ? AND status = ?",
			channel.TenantID, channel.ID, senderID, "active").Take(&localStaff).Error
		if err == nil && localStaff.ID != req.TargetID {
			return gorm.ErrDuplicatedKey
		}
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		var changed bool
		link, changed, err = savePersonalZaloStaffApproval(tx, channel, req.TargetID, senderID, now)
		if err != nil || !changed {
			return err
		}
		return tx.Create(&models.ActivityLog{
			ID: uuid.NewString(), TenantID: channel.TenantID, UserID: middleware.GetUserID(c), UserEmail: middleware.GetUserEmail(c),
			Action: "personal_zalo.staff_approved", ResourceType: "channel", ResourceID: channel.ID, Detail: link.ID,
			IPAddress: c.ClientIP(), CreatedAt: now,
		}).Error
	})
	switch {
	case errors.Is(err, gorm.ErrRecordNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": errorCode})
	case personalZaloDuplicateIdentity(err):
		c.JSON(http.StatusConflict, gin.H{"error": "personal_zalo_staff_approval_conflict"})
	case err != nil:
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "personal_zalo_identity_unavailable"})
	default:
		c.JSON(http.StatusOK, link)
	}
}

func savePersonalZaloStaffApproval(tx *gorm.DB, channel models.Channel, targetID, senderID string, now time.Time) (models.PersonalZaloIdentityLink, bool, error) {
	var link models.PersonalZaloIdentityLink
	err := personalZaloIdentityLinks(tx, channel).Where("kind = ? AND target_id = ?", personalZaloStaffIdentity, targetID).Take(&link).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		link = models.PersonalZaloIdentityLink{
			ID: uuid.NewString(), TenantID: channel.TenantID, ChannelID: channel.ID, AccountExternalID: channel.ExternalID,
			Kind: personalZaloStaffIdentity, TargetID: targetID, SenderID: &senderID, VerifiedAt: &now, ExpiresAt: now, CreatedAt: now,
		}
		err := tx.Create(&link).Error
		return link, err == nil, err
	}
	if err != nil {
		return link, false, err
	}
	if link.VerifiedAt != nil {
		if link.SenderID == nil || *link.SenderID != senderID {
			return link, false, gorm.ErrDuplicatedKey
		}
		return link, false, nil
	}
	// Consume any pending code for this staff profile when the admin approves the chat.
	updated := personalZaloIdentityLinks(tx, channel).Where("id = ? AND verified_at IS NULL", link.ID).Updates(map[string]any{
		"sender_id": senderID, "verified_at": now, "invite_digest": nil,
	})
	if updated.Error != nil {
		return link, false, updated.Error
	}
	if updated.RowsAffected != 1 {
		return link, false, gorm.ErrDuplicatedKey
	}
	link.SenderID, link.VerifiedAt, link.InviteDigest = &senderID, &now, nil
	return link, true, nil
}
