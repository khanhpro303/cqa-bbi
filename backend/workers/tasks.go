package workers

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	"github.com/hibiken/asynq"
	"github.com/vietbui/chat-quality-agent/channels"
	"github.com/vietbui/chat-quality-agent/config"
	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/db/models"
	"github.com/vietbui/chat-quality-agent/engine"
	"github.com/vietbui/chat-quality-agent/pkg"
)

const (
	TypeZaloWebhook = "zalo:webhook"
)

// ZaloWebhookPayload must match the one in handlers/webhooks.go
type ZaloWebhookPayload struct {
	AppID     string `json:"app_id"`
	EventName string `json:"event_name"`
	Timestamp string `json:"timestamp"`
	Sender    struct {
		ID string `json:"id"`
	} `json:"sender"`
	Recipient struct {
		ID string `json:"id"`
	} `json:"recipient"`
	Message struct {
		Text  string `json:"text"`
		MsgID string `json:"msg_id"`
	} `json:"message"`
}

// NewZaloWebhookTask creates a new task for processing a Zalo webhook
func NewZaloWebhookTask(payload ZaloWebhookPayload) (*asynq.Task, error) {
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	return asynq.NewTask(TypeZaloWebhook, payloadBytes), nil
}

// HandleZaloWebhookTask processes the webhook task
func HandleZaloWebhookTask(cfg *config.Config, langflowClient *engine.LangflowClient) asynq.HandlerFunc {
	return func(ctx context.Context, t *asynq.Task) error {
		var payload ZaloWebhookPayload
		if err := json.Unmarshal(t.Payload(), &payload); err != nil {
			return fmt.Errorf("json.Unmarshal failed: %v: %w", err, asynq.SkipRetry)
		}

		log.Printf("[worker] processing webhook for Zalo user %s to OA %s", payload.Sender.ID, payload.Recipient.ID)

		// 1. Find the channel in DB that matches this OA ID
		var allChannels []models.Channel
		if err := db.DB.Where("channel_type = ? AND is_active = true", "zalo_oa").Find(&allChannels).Error; err != nil {
			return fmt.Errorf("error finding channels: %w", err)
		}

		var matchedChannel *models.Channel
		var zaloCreds channels.ZaloOACredentials

		for _, ch := range allChannels {
			credBytes, err := pkg.Decrypt(ch.CredentialsEncrypted, cfg.EncryptionKey)
			if err != nil {
				continue
			}
			var creds channels.ZaloOACredentials
			if err := json.Unmarshal(credBytes, &creds); err != nil {
				continue
			}

			if creds.OAId == payload.Recipient.ID || creds.AppID == payload.AppID {
				matchedChannel = &ch
				zaloCreds = creds
				break
			}
		}

		if matchedChannel == nil {
			log.Printf("[worker] no active channel found for OA %s or App %s", payload.Recipient.ID, payload.AppID)
			return asynq.SkipRetry // Skip retry if channel not found
		}

		// 2. Call Langflow API
		replyText, err := langflowClient.RunFlow(ctx, payload.Sender.ID, payload.Message.Text)
		if err != nil {
			return fmt.Errorf("langflow error: %w", err)
		}

		if replyText == "" {
			log.Printf("[worker] langflow returned empty response")
			return nil
		}

		// 3. Send message back to Zalo
		adapter := channels.NewZaloOAAdapter(zaloCreds)
		adapter.SetTokenRefreshCallback(func(newAccess, newRefresh string) {
			var ch models.Channel
			if db.DB.First(&ch, "id = ?", matchedChannel.ID).Error == nil {
				credsMap := map[string]interface{}{
					"app_id":        zaloCreds.AppID,
					"app_secret":    zaloCreds.AppSecret,
					"access_token":  newAccess,
					"refresh_token": newRefresh,
					"oa_id":         zaloCreds.OAId,
				}
				newCredJSON, _ := json.Marshal(credsMap)
				encrypted, _ := pkg.Encrypt(newCredJSON, cfg.EncryptionKey)
				db.DB.Model(&ch).Update("credentials_encrypted", encrypted)
			}
		})

		err = adapter.SendMessage(ctx, payload.Sender.ID, replyText)
		if err != nil {
			return fmt.Errorf("failed to send reply to Zalo: %w", err)
		}

		log.Printf("[worker] successfully replied to user %s via Langflow", payload.Sender.ID)
		return nil
	}
}
