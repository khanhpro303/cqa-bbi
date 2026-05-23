package workers

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

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

// ChannelMetadata holds the session settings mapped from models.Channel.Metadata
type ChannelMetadata struct {
	SessionKeyword        string `json:"session_keyword"`
	SessionEndKeyword     string `json:"session_end_keyword"`
	SessionWelcomeMessage string `json:"session_welcome_message"`
	SessionTimeout        int    `json:"session_timeout_minutes"`

	LangflowAPIURL string `json:"langflow_api_url"`
	LangflowAPIKey string `json:"langflow_api_key"`
	LangflowFlowID string `json:"langflow_flow_id"`
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

		// Check if chatbot is active (defaults to true if setting is not found)
		var activeSetting models.AppSetting
		chatbotActive := true
		if err := db.DB.Where("tenant_id = ? AND setting_key = ?", matchedChannel.TenantID, "chatbot_active").First(&activeSetting).Error; err == nil {
			chatbotActive = (activeSetting.ValuePlain == "true")
		}

		if !chatbotActive {
			log.Printf("[worker] chatbot is disabled for tenant %s (ignoring message from Zalo user %s)", matchedChannel.TenantID, payload.Sender.ID)
			return nil
		}

		// Parse metadata for session configuration
		var meta ChannelMetadata
		if matchedChannel.Metadata != "" {
			_ = json.Unmarshal([]byte(matchedChannel.Metadata), &meta)
		}

		// Fallback to global config
		if meta.SessionKeyword == "" {
			meta.SessionKeyword = cfg.ChatbotSessionKeyword
		}
		if meta.SessionEndKeyword == "" {
			meta.SessionEndKeyword = cfg.ChatbotSessionEndKeyword
		}
		if meta.SessionWelcomeMessage == "" {
			meta.SessionWelcomeMessage = cfg.ChatbotSessionWelcomeMessage
		}
		if meta.SessionTimeout == 0 {
			meta.SessionTimeout = cfg.ChatbotSessionTimeout
		}
		if meta.LangflowAPIURL == "" {
			var setting models.AppSetting
			if err := db.DB.Where("tenant_id = ? AND setting_key = ?", matchedChannel.TenantID, "ai_engine_langflow_url").First(&setting).Error; err == nil && setting.ValuePlain != "" {
				meta.LangflowAPIURL = setting.ValuePlain
			} else {
				meta.LangflowAPIURL = cfg.LangflowAPIURL
			}
		}
		if meta.LangflowAPIKey == "" {
			var setting models.AppSetting
			if err := db.DB.Where("tenant_id = ? AND setting_key = ?", matchedChannel.TenantID, "ai_engine_langflow_token").First(&setting).Error; err == nil && len(setting.ValueEncrypted) > 0 {
				decrypted, _ := pkg.Decrypt(setting.ValueEncrypted, cfg.EncryptionKey)
				meta.LangflowAPIKey = string(decrypted)
			} else {
				meta.LangflowAPIKey = cfg.LangflowAPIKey
			}
		}
		if meta.LangflowFlowID == "" {
			var setting models.AppSetting
			if err := db.DB.Where("tenant_id = ? AND setting_key = ?", matchedChannel.TenantID, "ai_engine_langflow_flow_id").First(&setting).Error; err == nil && setting.ValuePlain != "" {
				meta.LangflowFlowID = setting.ValuePlain
			} else {
				meta.LangflowFlowID = cfg.LangflowFlowID
			}
		}

		// Setup Zalo adapter for replies
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

		sessionKey := fmt.Sprintf("zalo_session:%s:%s", matchedChannel.ID, payload.Sender.ID)
		
		// Check session
		var hasSession bool
		if db.RedisClient != nil {
			err := db.RedisClient.Get(ctx, sessionKey).Err()
			hasSession = (err == nil)
		}

		userText := strings.TrimSpace(payload.Message.Text)

		if !hasSession {
			// No active session. Check for trigger word.
			var isTriggered bool
			for _, kw := range strings.Split(meta.SessionKeyword, ";") {
				kw = strings.TrimSpace(kw)
				if kw != "" && strings.EqualFold(userText, kw) {
					isTriggered = true
					break
				}
			}
			if isTriggered {
				// Open session
				if db.RedisClient != nil {
					db.RedisClient.Set(ctx, sessionKey, "1", time.Duration(meta.SessionTimeout)*time.Minute)
				}
				// Send welcome message
				err := adapter.SendMessage(ctx, payload.Sender.ID, meta.SessionWelcomeMessage)
				if err != nil {
					log.Printf("[worker] failed to send welcome message: %v", err)
				}
				log.Printf("[worker] opened new session for user %s", payload.Sender.ID)
				return nil
			}
			// Ignore message
			log.Printf("[worker] ignoring message from %s (no session)", payload.Sender.ID)
			return nil
		}

		// Has session. Check for end word.
		var isEndTriggered bool
		for _, kw := range strings.Split(meta.SessionEndKeyword, ";") {
			kw = strings.TrimSpace(kw)
			if kw != "" && strings.EqualFold(userText, kw) {
				isEndTriggered = true
				break
			}
		}
		if isEndTriggered {
			if db.RedisClient != nil {
				db.RedisClient.Del(ctx, sessionKey)
			}
			err := adapter.SendMessage(ctx, payload.Sender.ID, "Phiên hỗ trợ đã kết thúc. Hẹn gặp lại bạn!")
			if err != nil {
				log.Printf("[worker] failed to send end session message: %v", err)
			}
			log.Printf("[worker] closed session for user %s", payload.Sender.ID)
			return nil
		}

		// Keep session alive
		if db.RedisClient != nil {
			db.RedisClient.Expire(ctx, sessionKey, time.Duration(meta.SessionTimeout)*time.Minute)
		}

		// 2. Call Langflow API
		replyText, err := langflowClient.RunFlowWithOverrides(ctx, payload.Sender.ID, payload.Message.Text, meta.LangflowAPIURL, meta.LangflowAPIKey, meta.LangflowFlowID)
		if err != nil {
			return fmt.Errorf("langflow error: %w", err)
		}

		if replyText == "" {
			log.Printf("[worker] langflow returned empty response")
			return nil
		}

		// 3. Send message back to Zalo
		err = adapter.SendMessage(ctx, payload.Sender.ID, replyText)
		if err != nil {
			return fmt.Errorf("failed to send reply to Zalo: %w", err)
		}

		log.Printf("[worker] successfully replied to user %s via Langflow", payload.Sender.ID)
		return nil
	}
}
