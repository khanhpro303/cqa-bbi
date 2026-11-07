package handlers

import (
	"context"
	"errors"
	"log"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/vietbui/chat-quality-agent/api/middleware"
	"github.com/vietbui/chat-quality-agent/config"
	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/db/models"
	"github.com/vietbui/chat-quality-agent/engine"
	"github.com/vietbui/chat-quality-agent/messengerlabels"
	"github.com/vietbui/chat-quality-agent/pancake"
	"github.com/vietbui/chat-quality-agent/pancakelabels"
	"gorm.io/gorm"
)

// Pancake tags are the alternative label source while Meta withholds custom
// labels. Tokens are write-only: responses only say whether one is stored.

func GetPancakeLabels(c *gin.Context) {
	cfg, err := config.Load()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "configuration unavailable"})
		return
	}
	report, err := pancakelabels.BuildReport(c.Request.Context(), db.DB, middleware.GetTenantID(c), c.Query("channel_id"), cfg.PancakeSocketEnabled, time.Now())
	if errors.Is(err, messengerlabels.ErrTooLarge) && report != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": err.Error(), "pages": report.Pages, "report": report})
		return
	}
	if err != nil {
		pancakeLabelError(c, err)
		return
	}
	c.JSON(http.StatusOK, report)
}

type pancakeTokenRequest struct {
	Token string `json:"token"`
}

func SavePancakeConnection(c *gin.Context) {
	var body pancakeTokenRequest
	if c.ShouldBindJSON(&body) != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "dữ liệu không hợp lệ"})
		return
	}
	cfg, err := config.Load()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "configuration unavailable"})
		return
	}
	tenantID := middleware.GetTenantID(c)
	if err := pancakelabels.SaveUserToken(db.DB.WithContext(c.Request.Context()), tenantID, body.Token, cfg.EncryptionKey); err != nil {
		pancakeLabelError(c, err)
		return
	}
	action := "pancake_labels.user_token_saved"
	if body.Token == "" {
		action = "pancake_labels.user_token_cleared"
	}
	db.LogActivity(tenantID, middleware.GetUserID(c), middleware.GetUserEmail(c), action, "tenant", tenantID, "", "", c.ClientIP())
	c.JSON(http.StatusOK, gin.H{"has_user_token": body.Token != ""})
}

func SavePancakePageToken(c *gin.Context) {
	var body pancakeTokenRequest
	if c.ShouldBindJSON(&body) != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "dữ liệu không hợp lệ"})
		return
	}
	channel, ok := messengerLabelChannel(c)
	if !ok {
		return
	}
	cfg, err := config.Load()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "configuration unavailable"})
		return
	}
	if err := pancakelabels.SavePageToken(db.DB.WithContext(c.Request.Context()), channel, body.Token, cfg.EncryptionKey); err != nil {
		pancakeLabelError(c, err)
		return
	}
	db.LogActivity(channel.TenantID, middleware.GetUserID(c), middleware.GetUserEmail(c), "pancake_labels.page_token_saved", "channel", channel.ID, "", "", c.ClientIP())
	c.JSON(http.StatusOK, gin.H{"has_page_token": body.Token != ""})
}

func pancakeClient(c *gin.Context, channel models.Channel) (*pancake.Client, bool) {
	cfg, err := config.Load()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "configuration unavailable"})
		return nil, false
	}
	state, err := pancakelabels.LoadState(db.DB.WithContext(c.Request.Context()), channel)
	if err != nil {
		pancakeLabelError(c, err)
		return nil, false
	}
	token := pancakelabels.PageToken(state, cfg.EncryptionKey)
	if token == "" {
		pancakeLabelError(c, pancakelabels.ErrNoPageToken)
		return nil, false
	}
	return pancake.NewClient(state.PancakePageID, token), true
}

func RefreshPancakeLabelCatalog(c *gin.Context) {
	channel, ok := messengerLabelChannel(c)
	if !ok {
		return
	}
	client, ok := pancakeClient(c, channel)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 45*time.Second)
	defer cancel()
	tags, err := client.FetchTags(ctx)
	if err != nil {
		pancakeLabelError(c, err)
		return
	}
	catalog := pancakelabels.CatalogFromTags(tags)
	now := time.Now()
	if err := pancakelabels.SaveCatalog(db.DB.WithContext(ctx), channel, catalog, now); err != nil {
		pancakeLabelError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"catalog": catalog, "catalog_synced_at": now})
}

func SavePancakeLabelPolicy(c *gin.Context) {
	var policy messengerlabels.Policy
	if c.ShouldBindJSON(&policy) != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "cấu hình nhãn không hợp lệ"})
		return
	}
	shape := policy
	shape.Enabled = false
	if shape.Validate(nil) != nil || policy.Enabled && len(policy.Rules) == 0 {
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
	if err := pancakelabels.SavePolicy(db.DB.WithContext(c.Request.Context()), channel, policy); err != nil {
		pancakeLabelError(c, err)
		return
	}
	db.LogActivity(channel.TenantID, middleware.GetUserID(c), middleware.GetUserEmail(c), "pancake_labels.policy_updated", "channel", channel.ID, "", "", c.ClientIP())
	c.JSON(http.StatusOK, policy)
}

func SyncPancakeLabels(c *gin.Context) {
	channel, ok := messengerLabelChannel(c)
	if !ok {
		return
	}
	if !channel.IsActive {
		c.JSON(http.StatusConflict, gin.H{"error": "Fanpage đang tạm dừng."})
		return
	}
	client, ok := pancakeClient(c, channel)
	if !ok {
		return
	}
	token, err := pancakelabels.ClaimSync(db.DB.WithContext(c.Request.Context()), channel, time.Now())
	if err != nil {
		pancakeLabelError(c, err)
		return
	}
	db.LogActivity(channel.TenantID, middleware.GetUserID(c), middleware.GetUserEmail(c), "pancake_labels.sync_started", "channel", channel.ID, "Đối soát nhãn Pancake", "", c.ClientIP())
	go func() {
		if err := pancakelabels.RunClaimedSync(context.Background(), db.DB, channel, token, client, engine.PancakePSIDIndex()); err != nil {
			log.Printf("[pancake-labels] manual reconcile failed for channel %s: %s", channel.ID, pancakelabels.ErrorMessage(pancake.ErrorKind(err)))
		}
	}()
	c.JSON(http.StatusAccepted, gin.H{"status": "syncing"})
}

func pancakeLabelError(c *gin.Context, err error) {
	status := http.StatusInternalServerError
	message := pancakelabels.ErrorMessage(pancake.ErrorKind(err))
	var apiErr *pancake.APIError
	switch {
	case errors.Is(err, gorm.ErrRecordNotFound):
		status, message = http.StatusNotFound, "Không tìm thấy Fanpage."
	case errors.Is(err, pancakelabels.ErrBusy), errors.Is(err, pancakelabels.ErrMetaEnabled):
		status, message = http.StatusConflict, err.Error()
	case errors.Is(err, pancakelabels.ErrNoPageToken), errors.Is(err, pancakelabels.ErrNoUserToken), errors.Is(err, pancakelabels.ErrTokenTooLong), errors.Is(err, messengerlabels.ErrConfiguration):
		status, message = http.StatusBadRequest, err.Error()
	case errors.Is(err, messengerlabels.ErrTooLarge):
		status, message = http.StatusUnprocessableEntity, err.Error()
	case errors.As(err, &apiErr):
		status = http.StatusBadGateway
		if apiErr.Kind == "rate_limit" {
			status = http.StatusTooManyRequests
		}
	}
	c.JSON(status, gin.H{"error": message})
}
