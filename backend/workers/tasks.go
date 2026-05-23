package workers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
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
	SessionGoodbyeMessage string `json:"session_goodbye_message"`
	SessionTimeout        int    `json:"session_timeout_minutes"`

	LangflowAPIURL string `json:"langflow_api_url"`
	LangflowAPIKey string `json:"langflow_api_key"`
	LangflowFlowID string `json:"langflow_flow_id"`

	AstraDBAPIEndpoint string `json:"astradb_api_endpoint"`
	AstraDBToken       string `json:"astradb_token"`
	AstraDBKeyspace    string `json:"astradb_keyspace"`
	AstraDBCollection  string `json:"astradb_collection"`
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
		if meta.SessionGoodbyeMessage == "" {
			meta.SessionGoodbyeMessage = cfg.ChatbotSessionGoodbyeMessage
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

		// Astra DB Configuration Fallbacks
		if meta.AstraDBAPIEndpoint == "" {
			meta.AstraDBAPIEndpoint = cfg.AstraDBAPIEndpoint
		}
		if meta.AstraDBToken == "" {
			meta.AstraDBToken = cfg.AstraDBToken
		}
		if meta.AstraDBKeyspace == "" {
			meta.AstraDBKeyspace = cfg.AstraDBKeyspace
		}
		if meta.AstraDBCollection == "" {
			meta.AstraDBCollection = cfg.AstraDBCollection
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

		userText := strings.TrimSpace(payload.Message.Text)

		// 1. Check for verification token command: "verify <token>"
		if strings.HasPrefix(strings.ToLower(userText), "verify ") {
			token := strings.TrimSpace(userText[7:])
			if token != "" {
				// Try to find a pending whitelist record for this tenant with this token
				var whitelistRec models.ZaloWhitelist
				if err := db.DB.Where("tenant_id = ? AND verify_token = ? AND status = ?", matchedChannel.TenantID, token, "pending").First(&whitelistRec).Error; err == nil {
					// Found! Fetch their Zalo profile to get their name and avatar
					displayName := payload.Sender.ID
					avatarURL := ""
					if profile, err := adapter.FetchUserProfile(ctx, payload.Sender.ID); err == nil {
						if profile.DisplayName != "" {
							displayName = profile.DisplayName
						}
						avatarURL = profile.Avatar
					}

					// Update whitelist record
					whitelistRec.ZaloUserID = payload.Sender.ID
					whitelistRec.Name = displayName
					whitelistRec.Avatar = avatarURL
					whitelistRec.Status = "active"
					whitelistRec.UpdatedAt = time.Now()

					if err := db.DB.Save(&whitelistRec).Error; err == nil {
						// Send success message back to Zalo
						successMsg := "Xác thực thành công! Tài khoản Zalo của bạn đã được thêm vào danh sách nhân viên nội bộ được phép sử dụng Chatbot."
						_ = adapter.SendMessage(ctx, payload.Sender.ID, successMsg)
						log.Printf("[worker] successfully whitelisted Zalo user %s for tenant %s", payload.Sender.ID, matchedChannel.TenantID)
						return nil
					}
				} else {
					// Token invalid or expired
					errorMsg := "Mã xác thực không hợp lệ hoặc đã hết hạn. Vui lòng kiểm tra lại."
					_ = adapter.SendMessage(ctx, payload.Sender.ID, errorMsg)
					return nil
				}
			}
		}

		// 2. Apply Whitelist Access Control:
		// Check if there are any active whitelisted records for this tenant.
		// If yes, restrict access to only whitelisted users.
		var activeCount int64
		if err := db.DB.Model(&models.ZaloWhitelist{}).Where("tenant_id = ? AND status = ?", matchedChannel.TenantID, "active").Count(&activeCount).Error; err == nil && activeCount > 0 {
			// Whitelist is active! Check if the current sender's ZaloUserID is whitelisted
			var whitelistRec models.ZaloWhitelist
			if err := db.DB.Where("tenant_id = ? AND zalo_user_id = ? AND status = ?", matchedChannel.TenantID, payload.Sender.ID, "active").First(&whitelistRec).Error; err != nil {
				// Not whitelisted! Log access denied and ignore the message
				log.Printf("[worker] access denied for Zalo user %s (not in whitelist for tenant %s)", payload.Sender.ID, matchedChannel.TenantID)
				return nil
			}
		}

		sessionKey := fmt.Sprintf("zalo_session:%s:%s", matchedChannel.ID, payload.Sender.ID)
		
		// Check session
		var activeSessionID string
		if db.RedisClient != nil {
			val, err := db.RedisClient.Get(ctx, sessionKey).Result()
			if err == nil && val != "" {
				activeSessionID = val
			}
		}

		if activeSessionID == "" {
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
				// Open session and generate unique session ID
				newSessionID := uuid.New().String()
				if db.RedisClient != nil {
					db.RedisClient.Set(ctx, sessionKey, newSessionID, time.Duration(meta.SessionTimeout)*time.Minute)
				}
				// Send welcome message
				err := adapter.SendMessage(ctx, payload.Sender.ID, meta.SessionWelcomeMessage)
				if err != nil {
					log.Printf("[worker] failed to send welcome message: %v", err)
				}
				log.Printf("[worker] opened new session %s for user %s", newSessionID, payload.Sender.ID)
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
			err := adapter.SendMessage(ctx, payload.Sender.ID, meta.SessionGoodbyeMessage)
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

		// Save user message to Astra DB asynchronously
		go func() {
			err := saveMessageToAstraDB(context.Background(), meta.AstraDBAPIEndpoint, meta.AstraDBToken, meta.AstraDBKeyspace, meta.AstraDBCollection, payload.Sender.ID, activeSessionID, "user", payload.Message.Text)
			if err != nil {
				log.Printf("[worker] failed to save user message to Astra DB: %v", err)
			}
		}()

		// 2. Call Langflow API (passing Zalo Sender ID as zaloUserID)
		replyText, err := langflowClient.RunFlowWithOverrides(ctx, activeSessionID, payload.Sender.ID, payload.Message.Text, meta.LangflowAPIURL, meta.LangflowAPIKey, meta.LangflowFlowID)
		if err != nil {
			return fmt.Errorf("langflow error: %w", err)
		}

		if replyText == "" {
			log.Printf("[worker] langflow returned empty response")
			return nil
		}

		// Save assistant reply to Astra DB asynchronously
		go func() {
			err := saveMessageToAstraDB(context.Background(), meta.AstraDBAPIEndpoint, meta.AstraDBToken, meta.AstraDBKeyspace, meta.AstraDBCollection, payload.Sender.ID, activeSessionID, "assistant", replyText)
			if err != nil {
				log.Printf("[worker] failed to save assistant message to Astra DB: %v", err)
			}
		}()

		// 3. Send message back to Zalo
		err = adapter.SendMessage(ctx, payload.Sender.ID, replyText)
		if err != nil {
			return fmt.Errorf("failed to send reply to Zalo: %w", err)
		}

		log.Printf("[worker] successfully replied to user %s via Langflow", payload.Sender.ID)
		return nil
	}
}

// saveMessageToAstraDB saves a chat message to DataStax Astra DB asynchronously.
func saveMessageToAstraDB(ctx context.Context, apiEndpoint, token, keyspace, collection, zaloUserID, sessionID, role, content string) error {
	if apiEndpoint == "" || token == "" || collection == "" {
		// Skip if not configured
		return nil
	}

	// Format endpoint: https://<ASTRA_DB_ID>-<REGION>.apps.astra.datastax.com/api/json/v1/<KEYSPACE>/<COLLECTION>
	if keyspace == "" {
		keyspace = "default_keyspace"
	}
	url := fmt.Sprintf("%s/api/json/v1/%s/%s", apiEndpoint, keyspace, collection)

	// Build the document representing the chat log
	document := map[string]interface{}{
		"zalo_user_id": zaloUserID,
		"session_id":   sessionID,
		"role":         role,
		"content":      content,
		"created_at":   time.Now().Unix(),
	}

	// Payload for inserting one document into Astra DB
	payload := map[string]interface{}{
		"insertOne": map[string]interface{}{
			"document": document,
		},
	}

	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal astra db payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(bodyBytes))
	if err != nil {
		return fmt.Errorf("create astra db request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Token", token)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("astra db request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return fmt.Errorf("astra db api error (status %d)", resp.StatusCode)
	}

	return nil
}
