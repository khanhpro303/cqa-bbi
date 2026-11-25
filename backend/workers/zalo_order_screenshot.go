package workers

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/hibiken/asynq"
	"github.com/vietbui/chat-quality-agent/cloudifyorders"
	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/db/models"
	"github.com/vietbui/chat-quality-agent/realtime"
	"gorm.io/gorm"
)

const typeZaloOrderScreenshot = "zalo:order_screenshot"

type zaloOrderScreenshotTask struct {
	TenantID, ChannelID, GroupID, MessageID string
}

func enqueueZaloOrderScreenshot(ctx context.Context, client *asynq.Client, channel *models.Channel, group *models.CRMGroup, messageID string) error {
	if os.Getenv("ZALO_ORDER_SCREENSHOT_PROFILE") == "" || client == nil || channel == nil || group == nil {
		return nil
	}
	profile, _, _, err := cloudifyorders.LoadZaloScreenshotProfile(channel.TenantID, channel.ID)
	if err != nil {
		return err
	}
	if profile.TenantID != channel.TenantID || profile.ChannelID != channel.ID || !profile.AllowsGroup(group.ZaloGroupID) {
		return nil
	}
	payload, err := json.Marshal(zaloOrderScreenshotTask{channel.TenantID, channel.ID, group.ID, messageID})
	if err != nil {
		return err
	}
	id := fmt.Sprintf("zalo-order-screenshot:%x", sha256.Sum256(payload))
	_, err = client.EnqueueContext(ctx, asynq.NewTask(typeZaloOrderScreenshot, payload),
		asynq.Queue("low"), asynq.TaskID(id), asynq.Retention(24*time.Hour),
		asynq.MaxRetry(2), asynq.Timeout(time.Minute))
	if errors.Is(err, asynq.ErrTaskIDConflict) {
		return nil
	}
	return err
}

func handleZaloOrderScreenshot(ctx context.Context, task *asynq.Task) error {
	var payload zaloOrderScreenshotTask
	if err := json.Unmarshal(task.Payload(), &payload); err != nil {
		return asynq.SkipRetry
	}
	if payload.TenantID == "" || payload.ChannelID == "" || payload.GroupID == "" || payload.MessageID == "" {
		return asynq.SkipRetry
	}
	profile, accountID, token, err := cloudifyorders.LoadZaloScreenshotProfile(payload.TenantID, payload.ChannelID)
	if err != nil || profile.TenantID != payload.TenantID || profile.ChannelID != payload.ChannelID {
		return err
	}
	database := db.DB.WithContext(ctx)
	var row models.CloudifyOrderConfirmation
	err = database.Select("id", "tenant_id", "channel_id", "zalo_group_id", "outbound_message_id", "message_id", "text").
		Where("tenant_id = ? AND channel_id = ? AND group_id = ? AND message_id = ?", payload.TenantID, payload.ChannelID, payload.GroupID, payload.MessageID).
		Where("confirmed_at IS NOT NULL AND evidence_content_type = '' AND outbound_message_id <> ''").
		Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if !profile.AllowsGroup(row.ZaloGroupID) {
		return nil
	}
	screenshot, err := cloudifyorders.CaptureZaloOrderScreenshot(ctx, profile, accountID, token, row)
	if err != nil {
		return err
	}
	result := database.Model(&models.CloudifyOrderConfirmation{}).
		Where("tenant_id = ? AND id = ? AND confirmed_at IS NOT NULL AND evidence_content_type = ''", row.TenantID, row.ID).
		Updates(map[string]any{
			"evidence_image": screenshot, "evidence_content_type": "image/png",
			"evidence_uploaded_at": time.Now().UTC(),
		})
	if result.RowsAffected == 1 {
		realtime.Publish(ctx, row.TenantID, realtime.EventCloudifyOrdersUpdated)
	}
	return result.Error
}
