package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/vietbui/chat-quality-agent/api/middleware"
	"github.com/vietbui/chat-quality-agent/channels"
	"github.com/vietbui/chat-quality-agent/config"
	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/db/models"
	"github.com/vietbui/chat-quality-agent/messengerlabels"
	"github.com/vietbui/chat-quality-agent/pkg"
	"gorm.io/gorm"
)

func GetMessengerLabels(c *gin.Context) {
	report, err := messengerlabels.BuildReport(c.Request.Context(), db.DB, middleware.GetTenantID(c), c.Query("channel_id"), time.Now())
	if errors.Is(err, messengerlabels.ErrTooLarge) && report != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": err.Error(), "pages": report.Pages, "report": report})
		return
	}
	if err != nil {
		messengerLabelError(c, err)
		return
	}
	c.JSON(http.StatusOK, report)
}

func messengerLabelChannel(c *gin.Context) (models.Channel, bool) {
	var channel models.Channel
	err := db.DB.WithContext(c.Request.Context()).Where("id = ? AND tenant_id = ? AND channel_type = ?", c.Param("channelId"), middleware.GetTenantID(c), "facebook").First(&channel).Error
	if err != nil {
		messengerLabelError(c, err)
		return channel, false
	}
	return channel, true
}

func messengerLabelReader(channel models.Channel) (*channels.FacebookAdapter, string, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, "", errors.New("configuration unavailable")
	}
	data, err := pkg.Decrypt(channel.CredentialsEncrypted, cfg.EncryptionKey)
	if err != nil {
		return nil, "", &channels.LabelAPIError{Kind: "auth"}
	}
	var creds channels.FacebookCredentials
	if json.Unmarshal(data, &creds) != nil || creds.AccessToken == "" || creds.PageID == "" {
		return nil, "", &channels.LabelAPIError{Kind: "auth"}
	}
	return channels.NewFacebookAdapter(creds), creds.PageID, nil
}

func RefreshMessengerLabelCatalog(c *gin.Context) {
	channel, ok := messengerLabelChannel(c)
	if !ok {
		return
	}
	if !channel.IsActive {
		c.JSON(http.StatusConflict, gin.H{"error": "Fanpage đang tạm dừng."})
		return
	}
	reader, _, err := messengerLabelReader(channel)
	if err != nil {
		messengerLabelError(c, err)
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 45*time.Second)
	defer cancel()
	catalog, err := reader.FetchPageLabels(ctx)
	if err != nil {
		messengerLabelError(c, err)
		return
	}
	now := time.Now()
	if err = messengerlabels.SaveCatalog(db.DB.WithContext(ctx), channel, catalog, now); err != nil {
		messengerLabelError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"catalog": catalog, "catalog_synced_at": now})
}

func SaveMessengerLabelPolicy(c *gin.Context) {
	var policy messengerlabels.Policy
	if c.ShouldBindJSON(&policy) != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "cấu hình nhãn không hợp lệ"})
		return
	}
	// Validate shape before database access; catalogue membership is checked below.
	shapePolicy := policy
	shapePolicy.Enabled = false
	if shapePolicy.Validate(nil) != nil || policy.Enabled && len(policy.Rules) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": messengerlabels.ErrConfiguration.Error()})
		return
	}
	channel, ok := messengerLabelChannel(c)
	if !ok {
		return
	}
	if policy.Enabled && !channel.IsActive {
		c.JSON(http.StatusConflict, gin.H{"error": "Fanpage đang tạm dừng."})
		return
	}
	if err := messengerlabels.SavePolicy(db.DB.WithContext(c.Request.Context()), channel, policy); err != nil {
		messengerLabelError(c, err)
		return
	}
	detail, _ := json.Marshal(policy)
	db.LogActivity(channel.TenantID, middleware.GetUserID(c), middleware.GetUserEmail(c), "messenger_labels.policy_updated", "channel", channel.ID, string(detail), "", c.ClientIP())
	c.JSON(http.StatusOK, policy)
}

func SyncMessengerLabels(c *gin.Context) {
	channel, ok := messengerLabelChannel(c)
	if !ok {
		return
	}
	if !channel.IsActive {
		c.JSON(http.StatusConflict, gin.H{"error": "Fanpage đang tạm dừng."})
		return
	}
	reader, pageID, err := messengerLabelReader(channel)
	if err != nil {
		messengerLabelError(c, err)
		return
	}
	state, err := messengerlabels.LoadState(db.DB.WithContext(c.Request.Context()), channel)
	if err != nil {
		messengerLabelError(c, err)
		return
	}
	if !state.Enabled {
		db.LogActivity(channel.TenantID, middleware.GetUserID(c), middleware.GetUserEmail(c), "messenger_labels.intake_sync_started", "channel", channel.ID, "Lưu nhãn mặc định cho các hội thoại chưa backfill", "", c.ClientIP())
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Hour)
			defer cancel()
			succeeded, failed, captureErr := messengerlabels.CaptureMissingIntakeLabels(ctx, db.DB, channel, pageID, reader)
			if captureErr != nil {
				log.Printf("[messenger-labels] manual intake capture failed for channel %s after %d successes and %d failures: %s", channel.ID, succeeded, failed, messengerlabels.ErrorMessage(messengerlabels.ErrorKind(captureErr)))
				return
			}
			log.Printf("[messenger-labels] manual intake capture completed for channel %s: %d captured, %d failed", channel.ID, succeeded, failed)
		}()
		c.JSON(http.StatusAccepted, gin.H{"status": "capturing_intake"})
		return
	}
	token, err := messengerlabels.ClaimSync(db.DB.WithContext(c.Request.Context()), channel, time.Now())
	if err != nil {
		messengerLabelError(c, err)
		return
	}
	db.LogActivity(channel.TenantID, middleware.GetUserID(c), middleware.GetUserEmail(c), "messenger_labels.sync_started", "channel", channel.ID, "Đọc lại nhãn từ Meta Inbox", "", c.ClientIP())
	go func() {
		if err := messengerlabels.RunClaimedSync(context.Background(), db.DB, channel, pageID, token, reader); err != nil {
			log.Printf("[messenger-labels] sync failed for channel %s: %s", channel.ID, messengerlabels.ErrorMessage(messengerlabels.ErrorKind(err)))
		}
	}()
	c.JSON(http.StatusAccepted, gin.H{"status": "syncing"})
}

func messengerLabelError(c *gin.Context, err error) {
	status := http.StatusInternalServerError
	message := messengerlabels.ErrorMessage(messengerlabels.ErrorKind(err))
	switch {
	case errors.Is(err, gorm.ErrRecordNotFound):
		status = http.StatusNotFound
		message = "Không tìm thấy Fanpage."
	case errors.Is(err, messengerlabels.ErrBusy), errors.Is(err, messengerlabels.ErrDisabled):
		status = http.StatusConflict
		message = err.Error()
	case errors.Is(err, messengerlabels.ErrConfiguration), errors.Is(err, messengerlabels.ErrIntakeLabel):
		status = http.StatusBadRequest
		message = err.Error()
	case errors.Is(err, messengerlabels.ErrTooLarge):
		status = http.StatusUnprocessableEntity
		message = err.Error()
	default:
		var apiError *channels.LabelAPIError
		if errors.As(err, &apiError) {
			status = http.StatusBadGateway
			if apiError.Kind == "rate_limit" {
				status = http.StatusTooManyRequests
			}
		}
	}
	c.JSON(status, gin.H{"error": message})
}
