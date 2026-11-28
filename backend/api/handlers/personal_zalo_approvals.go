package handlers

import (
	"context"
	"errors"
	"log"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/vietbui/chat-quality-agent/api/middleware"
	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/db/models"
	"github.com/vietbui/chat-quality-agent/realtime"
	"gorm.io/gorm"
)

func personalZaloThreadApproved(ctx context.Context, req personalZaloChatbotRequest) (bool, error) {
	var approval models.PersonalZaloThreadApproval
	err := db.DB.WithContext(ctx).Select("first_seen_at", "decided_at").
		Where("channel_id = ? AND account_external_id = ? AND thread_type = ? AND thread_id = ? AND status = ? AND present = ?",
			req.ChannelID, req.AccountExternalID, req.ThreadType, req.ThreadID, personalZaloApproved, true).
		Take(&approval).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	// A decision must not release messages that arrived while the thread was pending.
	return !req.SentAt.Before(approval.FirstSeenAt) &&
		(approval.DecidedAt == nil || !req.SentAt.Before(*approval.DecidedAt)), nil
}

func GetPersonalZaloApprovals(c *gin.Context) {
	channel, ok := getPersonalZaloImportChannel(c)
	if !ok {
		return
	}
	query := struct {
		Status string `form:"status" binding:"oneof=pending approved rejected"`
		Limit  int    `form:"limit" binding:"min=1,max=100"`
		Offset int    `form:"offset" binding:"min=0"`
	}{Status: personalZaloPending, Limit: 50}
	if c.ShouldBindQuery(&query) != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_approval_filter"})
		return
	}
	items := make([]models.PersonalZaloThreadApproval, 0)
	rows := db.DB.WithContext(c.Request.Context()).Model(&models.PersonalZaloThreadApproval{}).
		Where("channel_id = ? AND account_external_id = ? AND present = ? AND status = ?",
			channel.ID, channel.ExternalID, true, query.Status)
	var total int64
	if err := rows.Count(&total).Error; err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "personal_zalo_approvals_unavailable"})
		return
	}
	if err := rows.Order("first_seen_at DESC, thread_type, thread_id").Limit(query.Limit).Offset(query.Offset).Find(&items).Error; err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "personal_zalo_approvals_unavailable"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": items, "total": total})
}

func GetPendingPersonalZaloApprovalChannels(c *gin.Context) {
	type pendingChannel struct {
		ChannelID string `json:"channel_id"`
		Name      string `json:"name"`
		Count     int64  `json:"count"`
	}
	channels := make([]pendingChannel, 0)
	err := db.DB.WithContext(c.Request.Context()).Table("channels AS channel").
		Select("channel.id AS channel_id, channel.name, COUNT(*) AS count").
		Joins("JOIN personal_zalo_thread_approvals AS approval ON approval.channel_id = channel.id AND approval.account_external_id = channel.external_id").
		Where("channel.tenant_id = ? AND channel.channel_type = ? AND channel.is_active = ? AND channel.personal_zalo_auto_reply_enabled = ?",
			middleware.GetTenantID(c), "personal_zalo_import", true, true).
		Where("COALESCE(NULLIF(JSON_UNQUOTE(JSON_EXTRACT(channel.metadata, '$.chatbot_mode')), 'null'), ?) = ?",
			personalZaloChatbotMode, personalZaloChatbotMode).
		Where("approval.status = ? AND approval.present = ?", personalZaloPending, true).
		Group("channel.id, channel.name").Order("channel.name, channel.id").
		Find(&channels).Error
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "personal_zalo_approvals_unavailable"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"channels": channels})
}

func DecidePersonalZaloApproval(c *gin.Context) {
	tenantID := middleware.GetTenantID(c)
	channelID, threadType, threadID := c.Param("channelId"), c.Param("threadType"), c.Param("threadId")
	var req struct {
		Status         string `json:"status" binding:"required,oneof=approved rejected"`
		ExpectedStatus string `json:"expected_status" binding:"required,oneof=pending approved rejected"`
	}
	if !validPersonalZaloApprovalThread(threadType, threadID) || c.ShouldBindJSON(&req) != nil || req.Status == req.ExpectedStatus {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_approval_decision"})
		return
	}

	ctx := c.Request.Context()
	var applied bool
	err := db.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		channel, err := lockPersonalZaloRosterChannel(tx, tenantID, channelID)
		if err != nil {
			return err
		}
		result := tx.Model(&models.PersonalZaloThreadApproval{}).
			Where("channel_id = ? AND account_external_id = ? AND thread_type = ? AND thread_id = ? AND status = ? AND present = ?",
				channel.ID, channel.ExternalID, threadType, threadID, req.ExpectedStatus, true).
			Updates(map[string]any{
				"status": req.Status, "decided_at": time.Now().UTC().Truncate(time.Millisecond), "decided_by": middleware.GetUserID(c),
			})
		applied = result.RowsAffected == 1
		return result.Error
	})
	switch {
	case errors.Is(err, gorm.ErrRecordNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": "channel_not_found"})
	case err != nil:
		log.Printf("[personal-zalo] failed to decide approval for channel %s: %v", channelID, err)
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "personal_zalo_approvals_unavailable"})
	case !applied:
		c.JSON(http.StatusConflict, gin.H{"error": "personal_zalo_approval_changed"})
	default:
		realtime.Publish(ctx, tenantID, realtime.EventPersonalZaloApprovalsUpdated)
		c.Status(http.StatusNoContent)
	}
}

func CheckPersonalZaloGroupThreadAccess(c *gin.Context) {
	var body struct {
		RequestID         string    `json:"request_id" binding:"required,max=64"`
		TenantID          string    `json:"tenant_id" binding:"required,max=36"`
		ChannelID         string    `json:"channel_id" binding:"required,max=36"`
		AccountExternalID string    `json:"account_external_id" binding:"required"`
		ThreadType        string    `json:"thread_type" binding:"required,oneof=group"`
		ThreadID          string    `json:"thread_id" binding:"required"`
		SentAt            time.Time `json:"sent_at" binding:"required"`
	}
	if c.ShouldBindJSON(&body) != nil || !personalZaloUIDPattern.MatchString(body.AccountExternalID) ||
		!validPersonalZaloApprovalThread(body.ThreadType, body.ThreadID) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request"})
		return
	}
	if c.GetString("internal_request_id") != body.RequestID {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "request_id_mismatch"})
		return
	}
	req := personalZaloChatbotRequest{
		TenantID: body.TenantID, ChannelID: body.ChannelID, AccountExternalID: body.AccountExternalID,
		ThreadType: body.ThreadType, ThreadID: body.ThreadID, SentAt: body.SentAt,
	}
	ctx := c.Request.Context()
	channel, enabled, err := personalZaloChatbotChannel(ctx, req)
	if err != nil {
		c.Status(http.StatusServiceUnavailable)
		return
	}
	if !enabled || channel.ExternalID != req.AccountExternalID {
		c.Status(http.StatusNoContent)
		return
	}
	settings, err := loadPersonalZaloChatbotSettings(ctx, req.TenantID, "chatbot_active")
	if err != nil {
		c.Status(http.StatusServiceUnavailable)
		return
	}
	if !personalZaloChatbotMasterEnabled(settings) {
		c.Status(http.StatusNoContent)
		return
	}
	approved, err := personalZaloThreadApproved(ctx, req)
	switch {
	case err != nil:
		c.Status(http.StatusServiceUnavailable)
	case !approved:
		c.Status(http.StatusNoContent)
	default:
		c.Status(http.StatusOK)
	}
}
