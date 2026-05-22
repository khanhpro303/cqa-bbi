package handlers

import (
	"context"
	"encoding/json"
	"log"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/vietbui/chat-quality-agent/channels"
	"github.com/vietbui/chat-quality-agent/config"
	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/db/models"
	"github.com/vietbui/chat-quality-agent/engine"
	"github.com/vietbui/chat-quality-agent/pkg"
)

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

// ZaloWebhookHandler processes incoming Zalo OA webhooks
func ZaloWebhookHandler(cfg *config.Config) gin.HandlerFunc {
	langflowClient := engine.NewLangflowClient(cfg)

	return func(c *gin.Context) {
		var payload ZaloWebhookPayload
		if err := c.ShouldBindJSON(&payload); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid payload"})
			return
		}

		// Always return 200 OK to Zalo quickly
		c.JSON(http.StatusOK, gin.H{"status": "ok"})

		if payload.EventName != "user_send_text" || payload.Message.Text == "" {
			return
		}

		// Process async to not block the webhook response
		go processZaloMessage(payload, cfg, langflowClient)
	}
}

func processZaloMessage(payload ZaloWebhookPayload, cfg *config.Config, langflowClient *engine.LangflowClient) {
	log.Printf("[webhook] received message from Zalo user %s to OA %s", payload.Sender.ID, payload.Recipient.ID)

	// 1. Find the channel in DB that matches this OA ID
	var allChannels []models.Channel
	if err := db.DB.Where("channel_type = ? AND is_active = true", "zalo_oa").Find(&allChannels).Error; err != nil {
		log.Printf("[webhook] error finding channels: %v", err)
		return
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

		// If OAId matches recipient ID, we found our channel
		if creds.OAId == payload.Recipient.ID || creds.AppID == payload.AppID {
			// Actually Zalo sends AppID, if we match AppID, it's good. But OAId is safer.
			// Let's match OAId or AppID (since a user might only have 1 OA per App)
			matchedChannel = &ch
			zaloCreds = creds
			break
		}
	}

	if matchedChannel == nil {
		log.Printf("[webhook] no active channel found for OA %s or App %s", payload.Recipient.ID, payload.AppID)
		return
	}

	// 2. Call Langflow API
	ctx := context.Background()
	replyText, err := langflowClient.RunFlow(ctx, payload.Sender.ID, payload.Message.Text)
	if err != nil {
		log.Printf("[webhook] langflow error: %v", err)
		return
	}

	if replyText == "" {
		log.Printf("[webhook] langflow returned empty response")
		return
	}

	// 3. Send message back to Zalo
	adapter := channels.NewZaloOAAdapter(zaloCreds)
	// Optionally set token refresh callback
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
		log.Printf("[webhook] failed to send reply to Zalo: %v", err)
		return
	}

	log.Printf("[webhook] successfully replied to user %s", payload.Sender.ID)
}
