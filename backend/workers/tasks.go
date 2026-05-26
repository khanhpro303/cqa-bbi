package workers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
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
	"github.com/vietbui/chat-quality-agent/ai"
)

const (
	TypeZaloWebhook = "zalo:webhook"
)

type ZaloWebhookAttachment struct {
	Type    string `json:"type"`
	Payload struct {
		Thumbnail   string `json:"thumbnail,omitempty"`
		Description string `json:"description,omitempty"`
		URL         string `json:"url,omitempty"`
		Size        string `json:"size,omitempty"`
		Name        string `json:"name,omitempty"`
		Checksum    string `json:"checksum,omitempty"`
		Type        string `json:"type,omitempty"`
		Coordinates struct {
			Latitude  string `json:"latitude,omitempty"`
			Longitude string `json:"longitude,omitempty"`
		} `json:"coordinates,omitempty"`
	} `json:"payload"`
}

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
		Text        string                  `json:"text"`
		MsgID       string                  `json:"msg_id"`
		Attachments []ZaloWebhookAttachment `json:"attachments,omitempty"`
	} `json:"message"`
	OAID      string `json:"oa_id"`
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

	LangflowAPIURL       string `json:"langflow_api_url"`
	LangflowAPIKey       string `json:"langflow_api_key"`
	LangflowFlowID       string `json:"langflow_flow_id"`
	LangflowPublicFlowID string `json:"langflow_public_flow_id"`

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
		var resolvedGroup *models.CRMGroup

		// 1a. Try to match by group ID if payload.Recipient.ID matches a Zalo group ID in our CRM groups
		if payload.Recipient.ID != "" {
			var group models.CRMGroup
			if err := db.DB.Where("zalo_group_id = ?", payload.Recipient.ID).First(&group).Error; err == nil {
				for i, ch := range allChannels {
					if ch.ID == group.ChannelID {
						credBytes, err := pkg.Decrypt(ch.CredentialsEncrypted, cfg.EncryptionKey)
						if err == nil {
							var creds channels.ZaloOACredentials
							if err := json.Unmarshal(credBytes, &creds); err == nil {
								matchedChannel = &allChannels[i]
								zaloCreds = creds
								resolvedGroup = &group
								break
							}
						}
					}
				}
			}
		}

		// Determine the OA ID from the payload (either Recipient.ID for direct messages, or OAID for group messages)
		oaID := payload.Recipient.ID
		if payload.OAID != "" {
			oaID = payload.OAID
		}

		// First pass: try to match by exact OA ID using ExternalID field in DB
		if matchedChannel == nil && oaID != "" {
			for i, ch := range allChannels {
				if ch.ExternalID != "" && ch.ExternalID == oaID {
					credBytes, err := pkg.Decrypt(ch.CredentialsEncrypted, cfg.EncryptionKey)
					if err != nil {
						continue
					}
					var creds channels.ZaloOACredentials
					if err := json.Unmarshal(credBytes, &creds); err != nil {
						continue
					}
					matchedChannel = &allChannels[i]
					zaloCreds = creds
					break
				}
			}
		}

		// Second pass: fallback to decrypted OA ID match (if ExternalID in DB was empty)
		if matchedChannel == nil && oaID != "" {
			for i, ch := range allChannels {
				credBytes, err := pkg.Decrypt(ch.CredentialsEncrypted, cfg.EncryptionKey)
				if err != nil {
					continue
				}
				var creds channels.ZaloOACredentials
				if err := json.Unmarshal(credBytes, &creds); err != nil {
					continue
				}
				if creds.OAId != "" && creds.OAId == oaID {
					matchedChannel = &allChannels[i]
					zaloCreds = creds
					break
				}
			}
		}

		// Third pass: last-resort fallback to AppID if no exact OA ID matches
		if matchedChannel == nil {
			for i, ch := range allChannels {
				credBytes, err := pkg.Decrypt(ch.CredentialsEncrypted, cfg.EncryptionKey)
				if err != nil {
					continue
				}
				var creds channels.ZaloOACredentials
				if err := json.Unmarshal(credBytes, &creds); err != nil {
					continue
				}
				if creds.AppID != "" && creds.AppID == payload.AppID {
					matchedChannel = &allChannels[i]
					zaloCreds = creds
					break
				}
			}
		}

		if matchedChannel == nil {
			log.Printf("[worker] no active channel found for OA/Group %s (OAID: %s) or App %s", payload.Recipient.ID, payload.OAID, payload.AppID)
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
		if meta.LangflowPublicFlowID == "" {
			var setting models.AppSetting
			if err := db.DB.Where("tenant_id = ? AND setting_key = ?", matchedChannel.TenantID, "ai_engine_langflow_public_flow_id").First(&setting).Error; err == nil && setting.ValuePlain != "" {
				meta.LangflowPublicFlowID = setting.ValuePlain
			} else {
				meta.LangflowPublicFlowID = cfg.LangflowPublicFlowID
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
		attachmentText := parseAttachmentText(payload.Message.Attachments)
		if attachmentText != "" {
			if userText == "" {
				userText = strings.TrimSpace(attachmentText)
			} else {
				userText = userText + "\n" + strings.TrimSpace(attachmentText)
			}
		}

		// 1. Check for verification token command: "verify <token>"
		if strings.HasPrefix(strings.ToLower(userText), "verify ") {
			token := strings.TrimSpace(userText[7:])
			if token != "" {
				// Try to find a pending whitelist record for this tenant and channel with this token
				var whitelistRec models.ZaloWhitelist
				if err := db.DB.Where("tenant_id = ? AND (channel_id = ? OR channel_id = '' OR channel_id IS NULL) AND verify_token = ? AND status = ?", matchedChannel.TenantID, matchedChannel.ID, token, "pending").First(&whitelistRec).Error; err == nil {
					// Found! Fetch their Zalo profile to get their avatar
					avatarURL := ""
					if profile, err := adapter.FetchUserProfile(ctx, payload.Sender.ID); err == nil {
						avatarURL = profile.Avatar
					}

					// Update whitelist record (Keep the original Name entered by the admin)
					whitelistRec.ZaloUserID = payload.Sender.ID
					if whitelistRec.ChannelID == "" {
						whitelistRec.ChannelID = matchedChannel.ID
					}
					if avatarURL != "" {
						whitelistRec.Avatar = avatarURL
					}
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
					// Not found in whitelist staff. Try ZaloCustomer!
					var customerRec models.ZaloCustomer
					if err := db.DB.Where("tenant_id = ? AND verify_token = ?", matchedChannel.TenantID, token).First(&customerRec).Error; err == nil {
						avatarURL := ""
						if profile, err := adapter.FetchUserProfile(ctx, payload.Sender.ID); err == nil {
							avatarURL = profile.Avatar
							if profile.DisplayName != "" {
								customerRec.Name = profile.DisplayName
							}
						}

						customerRec.ZaloUserID = payload.Sender.ID
						if avatarURL != "" {
							customerRec.Avatar = avatarURL
						}
						
						statusWas := customerRec.Status
						if customerRec.Status == "pending_verify" {
							customerRec.Status = "pending_approval"
						}
						customerRec.UpdatedAt = time.Now()

						if err := db.DB.Save(&customerRec).Error; err == nil {
							if customerRec.Status == "approved" {
								successMsg := "Xác thực thành công! Tài khoản Zalo của bạn đã được liên kết với hệ thống CRM."
								_ = adapter.SendMessage(ctx, payload.Sender.ID, successMsg)

								// Auto-invite customer to Zalo GMF group chats they are already pre-added to locally
								var groupLinks []models.CRMGroupCustomer
								if err := db.DB.Where("zalo_customer_id = ?", customerRec.ID).Find(&groupLinks).Error; err == nil {
									for _, link := range groupLinks {
										var grp models.CRMGroup
										if err := db.DB.First(&grp, "id = ?", link.GroupID).Error; err == nil && grp.ZaloGroupID != "" {
											if inviteErr := adapter.InviteGMFGroupMembers(ctx, grp.ZaloGroupID, []string{customerRec.ZaloUserID}); inviteErr != nil {
												log.Printf("[worker] failed to auto-invite customer %s to Zalo GMF group %s: %v", customerRec.ID, grp.Name, inviteErr)
											} else {
												log.Printf("[worker] successfully auto-invited customer %s to Zalo GMF group %s", customerRec.ID, grp.Name)
											}
										}
									}
								}
							} else {
								successMsg := "Xác thực thành công! Yêu cầu liên kết tài khoản của bạn đã được gửi tới Ban quản trị để phê duyệt."
								_ = adapter.SendMessage(ctx, payload.Sender.ID, successMsg)
							}
							log.Printf("[worker] successfully processed customer verification (token: %s, previous status: %s, current status: %s) for Zalo user %s", token, statusWas, customerRec.Status, payload.Sender.ID)
							return nil
						}
					}

					// Token invalid or expired
					errorMsg := "Mã xác thực không hợp lệ hoặc đã hết hạn. Vui lòng kiểm tra lại."
					_ = adapter.SendMessage(ctx, payload.Sender.ID, errorMsg)
					return nil
				}
			}
		}

		// 2. Determine Whitelist / Staff / Customer Routing:
		// Check if the current sender is active in the Zalo whitelist for this tenant and channel.
		isWhitelisted := false
		var whitelistRec models.ZaloWhitelist
		if err := db.DB.Where("tenant_id = ? AND (channel_id = ? OR channel_id = '' OR channel_id IS NULL) AND zalo_user_id = ? AND status = ?", matchedChannel.TenantID, matchedChannel.ID, payload.Sender.ID, "active").First(&whitelistRec).Error; err == nil {
			isWhitelisted = true
		}

		isCustomer := false
		customerCode := ""
		var customerRec models.ZaloCustomer
		if err := db.DB.Where("tenant_id = ? AND zalo_user_id = ? AND status = ?", matchedChannel.TenantID, payload.Sender.ID, "approved").First(&customerRec).Error; err == nil {
			isCustomer = true
			customerCode = customerRec.CustomerCode
		}

		// GMF Group Chat Context Detection & CustomerCode Override
		var matchedGroup models.CRMGroup
		var hasGroup bool
		if resolvedGroup != nil {
			matchedGroup = *resolvedGroup
			hasGroup = true
		} else if matchedChannel != nil {
			if err := db.DB.Where("tenant_id = ? AND zalo_group_id = ?", matchedChannel.TenantID, payload.Recipient.ID).First(&matchedGroup).Error; err == nil {
				hasGroup = true
			}
		}

		if matchedGroup.ZaloGroupID == "" && payload.EventName == "user_send_group_text" {
			matchedGroup.ZaloGroupID = payload.Recipient.ID
		}

		if hasGroup {
			// Verify membership
			isMember := false
			if isWhitelisted {
				// Verify if the whitelisted staff is assigned to this group in crm_group_employees
				var count int64
				db.DB.Model(&models.CRMGroupEmployee{}).Where("group_id = ? AND zalo_whitelist_id = ?", matchedGroup.ID, whitelistRec.ID).Count(&count)
				if count > 0 {
					isMember = true
				}
			} else if isCustomer {
				var count int64
				db.DB.Model(&models.CRMGroupCustomer{}).Where("group_id = ? AND zalo_customer_id = ?", matchedGroup.ID, customerRec.ID).Count(&count)
				if count > 0 {
					isMember = true
				}
			}

			if isMember && matchedGroup.CustomerCode != "" {
				customerCode = matchedGroup.CustomerCode
				log.Printf("[worker] GMF group chat detected: %s (ID: %s). Overriding customerCode to %s for sender %s", matchedGroup.Name, matchedGroup.ZaloGroupID, customerCode, payload.Sender.ID)
			} else if !isMember {
				log.Printf("[worker] Zalo user %s is not a member of GMF group %s (ID: %s). Skipping override.", payload.Sender.ID, matchedGroup.Name, matchedGroup.ZaloGroupID)
			}
		}

		sessionKey := fmt.Sprintf("zalo_session:%s:%s", matchedChannel.ID, payload.Sender.ID)
		if matchedGroup.ZaloGroupID != "" {
			sessionKey = fmt.Sprintf("zalo_session:%s:group:%s", matchedChannel.ID, matchedGroup.ZaloGroupID)
		}

		// Concurrency control: Sequential processing per sessionKey using Redis lock
		if db.RedisClient != nil {
			lockKey := sessionKey + ":lock"
			acquired := false
			// Try to acquire lock every 250ms for up to 30 seconds
			for i := 0; i < 120; i++ {
				locked, err := db.RedisClient.SetNX(ctx, lockKey, "1", 45*time.Second).Result()
				if err == nil && locked {
					acquired = true
					break
				}
				time.Sleep(250 * time.Millisecond)
			}
			if !acquired {
				return fmt.Errorf("timeout waiting for session lock for key %s", sessionKey)
			}
			defer db.RedisClient.Del(ctx, lockKey)
		}

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
				// If the user triggers it, but is unverified, send them the verification DM!
				if !isWhitelisted && !isCustomer {
					verifyInstructions := "Tài khoản của bạn chưa được xác thực trên hệ thống CRM. Vui lòng nhắn tin theo cú pháp `verify <mã_xác_thực>` được cung cấp bởi nhân viên để đăng ký sử dụng Bot."
					_ = adapter.SendMessage(ctx, payload.Sender.ID, verifyInstructions)
					log.Printf("[worker] blocking unverified Zalo user %s for tenant %s (triggered bot)", payload.Sender.ID, matchedChannel.TenantID)
					return nil
				}

				// Open session and generate unique session ID
				newSessionID := uuid.New().String()
				if db.RedisClient != nil {
					db.RedisClient.Set(ctx, sessionKey, newSessionID, time.Duration(meta.SessionTimeout)*time.Minute)
				}
				// Send welcome message
				var err error
				if matchedGroup.ZaloGroupID != "" {
					err = adapter.SendGroupMessage(ctx, matchedGroup.ZaloGroupID, meta.SessionWelcomeMessage)
				} else {
					err = adapter.SendMessage(ctx, payload.Sender.ID, meta.SessionWelcomeMessage)
				}
				if err != nil {
					log.Printf("[worker] failed to send welcome message: %v", err)
				}
				log.Printf("[worker] opened new session %s for key %s", newSessionID, sessionKey)
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
			var err error
			if matchedGroup.ZaloGroupID != "" {
				err = adapter.SendGroupMessage(ctx, matchedGroup.ZaloGroupID, meta.SessionGoodbyeMessage)
			} else {
				err = adapter.SendMessage(ctx, payload.Sender.ID, meta.SessionGoodbyeMessage)
			}
			if err != nil {
				log.Printf("[worker] failed to send end session message: %v", err)
			}
			log.Printf("[worker] closed session for key %s", sessionKey)
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

		// Unverified public user block:
		// If session is already active, auto-ignore unverified users instead of sending DM instructions
		if !isWhitelisted && !isCustomer {
			log.Printf("[worker] auto-ignoring unverified Zalo user %s in active session %s for tenant %s", payload.Sender.ID, activeSessionID, matchedChannel.TenantID)
			return nil
		}

		// Real-time intent classification for active sessions
		intent, err := classifyMessageIntent(ctx, matchedChannel.TenantID, payload.Message.Text)
		if err != nil {
			log.Printf("[worker] error classifying message intent: %v. Proceeding as IN_SCOPE.", err)
			intent = "IN_SCOPE"
		}

		if intent == "HANDOVER" {
			log.Printf("[worker] message classified as HANDOVER. Closing session and sending handover message.")
			if db.RedisClient != nil {
				db.RedisClient.Del(ctx, sessionKey)
			}
			handoverMsg := "Dạ, em xin lỗi về trải nghiệm không tốt của mình ạ. Em sẽ báo các bạn nhân viên admin liên hệ trực tiếp hỗ trợ mình ngay nhé ạ!"
			var sendErr error
			if matchedGroup.ZaloGroupID != "" {
				sendErr = adapter.SendGroupMessage(ctx, matchedGroup.ZaloGroupID, handoverMsg)
			} else {
				sendErr = adapter.SendMessage(ctx, payload.Sender.ID, handoverMsg)
			}
			if sendErr != nil {
				log.Printf("[worker] failed to send handover message: %v", sendErr)
			}
			return nil
		}

		if intent == "CASUAL" {
			log.Printf("[worker] message classified as CASUAL. Closing session and passing through (auto-ignoring).")
			if db.RedisClient != nil {
				db.RedisClient.Del(ctx, sessionKey)
			}
			return nil
		}

		// Determine which Flow ID to use
		flowIDToUse := meta.LangflowPublicFlowID
		// Only route to the private flow if the user is whitelisted AND we are NOT in a GMF group chat context
		if isWhitelisted && matchedGroup.ZaloGroupID == "" {
			flowIDToUse = meta.LangflowFlowID
			log.Printf("[worker] Routing whitelisted internal staff %s to RAG Agent Flow (%s)", payload.Sender.ID, flowIDToUse)
		} else {
			if flowIDToUse == "" {
				flowIDToUse = meta.LangflowFlowID
			}
			log.Printf("[worker] Routing user/customer %s (code: %s) to Public/Group Flow (%s)", payload.Sender.ID, customerCode, flowIDToUse)
		}

		// Resolve permission context and sign JWT token
		agentType := "public"
		// Only set agentType to "private" if the user is whitelisted AND we are NOT in a GMF group chat context
		if isWhitelisted && matchedGroup.ZaloGroupID == "" {
			agentType = "private"
		}
		permCtx := engine.ResolvePermissionsWithGroup(matchedChannel.TenantID, payload.Sender.ID, customerCode, agentType, matchedGroup.ID)

		// Intercept Zalo OA interactive button clicks
		// A. "Xem theo màu" button click
		if strings.HasPrefix(userText, "#xem_mau_size:") {
			parts := strings.Split(strings.TrimPrefix(userText, "#xem_mau_size:"), ":")
			maChaName := parts[0]
			if len(parts) > 1 {
				maChaName = parts[1]
			}
			reply := fmt.Sprintf("Bạn muốn xem tồn kho cụ thể của màu và size nào cho dòng sản phẩm %s?\nVui lòng nhập thông tin (Ví dụ: %s màu đỏ size L).", maChaName, maChaName)
			var err error
			if matchedGroup.ZaloGroupID != "" {
				err = adapter.SendGroupMessage(ctx, matchedGroup.ZaloGroupID, reply)
			} else {
				err = adapter.SendMessage(ctx, payload.Sender.ID, reply)
			}
			_ = err
			return nil
		}

		// B. "Xem theo toàn dòng" button click
		var clickPayload struct {
			MaCha     string `json:"MA_CHA"`
			MaChaName string `json:"MA_CHA_NAME"`
		}
		if strings.HasPrefix(userText, "{") && json.Unmarshal([]byte(userText), &clickPayload) == nil && clickPayload.MaCha != "" {
			displayName := clickPayload.MaCha
			if clickPayload.MaChaName != "" {
				displayName = clickPayload.MaChaName
			}

			totalStock, err := sumInventoryByMaCha(ctx, matchedChannel.TenantID, &permCtx, clickPayload.MaCha)
			if err != nil {
				log.Printf("[worker] error summing inventory: %v", err)
				var sendErr error
				if matchedGroup.ZaloGroupID != "" {
					sendErr = adapter.SendGroupMessage(ctx, matchedGroup.ZaloGroupID, "Đã có lỗi xảy ra khi tính toán tồn kho dòng sản phẩm.")
				} else {
					sendErr = adapter.SendMessage(ctx, payload.Sender.ID, "Đã có lỗi xảy ra khi tính toán tồn kho dòng sản phẩm.")
				}
				_ = sendErr
				return nil
			}

			reply := fmt.Sprintf("Tổng tồn kho của dòng sản phẩm %s (Mã: %s) trên hệ thống hiện tại là: %.1f", displayName, clickPayload.MaCha, totalStock)
			var sendErr error
			if matchedGroup.ZaloGroupID != "" {
				sendErr = adapter.SendGroupMessage(ctx, matchedGroup.ZaloGroupID, reply)
			} else {
				sendErr = adapter.SendMessage(ctx, payload.Sender.ID, reply)
			}
			_ = sendErr
			return nil
		}

		permissionToken, err := engine.SignPermissionToken(permCtx, cfg.EncryptionKey)
		if err != nil {
			log.Printf("[worker] failed to sign permission token: %v", err)
		}

		// 2. Call Langflow API (passing Zalo Sender ID as zaloUserID, customerCode, and permissionToken)
		replyText, err := langflowClient.RunFlowWithCustomer(ctx, activeSessionID, payload.Sender.ID, userText, meta.LangflowAPIURL, meta.LangflowAPIKey, flowIDToUse, customerCode, permissionToken)
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
		var sendErr error
		if matchedGroup.ZaloGroupID != "" {
			sendErr = adapter.SendGroupMessage(ctx, matchedGroup.ZaloGroupID, replyText)
		} else {
			sendErr = adapter.SendMessage(ctx, payload.Sender.ID, replyText)
		}
		if sendErr != nil {
			return fmt.Errorf("failed to send reply to Zalo: %w", sendErr)
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

func sumInventoryByMaCha(ctx context.Context, tenantID string, permCtx *engine.GroupPermissionContext, maCha string) (float64, error) {
	// 1. Fetch child products from Astra DB
	childProducts, err := getProductsByMaChaFromAstraDB(ctx, tenantID, maCha)
	if err != nil {
		return 0, err
	}

	// 2. Fetch allowed product groups for "inventory" resource
	_, _, allowedProductGroups := permCtx.IsResourceAllowed("inventory")

	childCodes := make(map[string]bool)
	for _, p := range childProducts {
		sku := getMapString(p, "MA", "code", "ma")
		group := getMapString(p, "LIST_TEN_NHOM_VTHH", "list_ten_nhom_vthh", "group")

		isAllowed := len(allowedProductGroups) == 0
		if !isAllowed {
			gLower := strings.ToLower(group)
			for _, allowed := range allowedProductGroups {
				if strings.Contains(gLower, strings.ToLower(allowed)) {
					isAllowed = true
					break
				}
			}
		}

		if isAllowed && sku != "" {
			childCodes[strings.ToLower(sku)] = true
		}
	}

	// If the user has permission to see none of the variants, return 0 stock
	if len(childCodes) == 0 && len(childProducts) > 0 {
		return 0, nil
	}

	// 3. Load ERP credentials
	cfg, _ := config.Load()
	erpURL, erpDB, erpLogin, erpPassword, err := loadCloudifyCredentials(tenantID, cfg)
	if err != nil {
		return 0, err
	}

	// If ERP URL is empty, fallback to mock sum
	if erpURL == "" {
		return 45.0, nil
	}

	client := &pkg.CloudifyClient{
		BaseURL:  erpURL,
		DB:       erpDB,
		Login:    erpLogin,
		Password: erpPassword,
	}

	// 4. Search live inventory on ERP using the maCha code as keyword
	inventoryList, err := client.SearchInventory(maCha, 100)
	if err != nil {
		return 0, err
	}

	// 5. Sum stock of matching items
	var totalStock float64
	for _, item := range inventoryList {
		code := getMapString(item, "code", "ma_hang", "ma", "product_code")
		codeLower := strings.ToLower(code)

		isMatch := false
		if len(childCodes) > 0 {
			isMatch = childCodes[codeLower]
		} else {
			// fallback to prefix matching if child list was empty
			isMatch = strings.HasPrefix(codeLower, strings.ToLower(maCha))
		}

		if isMatch {
			stock := getMapFloat(item, "stock", "ton", "ton_kho")
			totalStock += stock
		}
	}

	return totalStock, nil
}

func getProductsByMaChaFromAstraDB(ctx context.Context, tenantID, maCha string) ([]map[string]interface{}, error) {
	cfg, _ := config.Load()
	apiEndpoint := cfg.AstraDBAPIEndpoint
	token := cfg.AstraDBToken
	keyspace := "cache_product"
	if cfg.AstraDBKeyspace != "" {
		keyspace = cfg.AstraDBKeyspace
	}
	collection := "erp_product_bbi"
	if cfg.AstraDBProductCollection != "" {
		collection = cfg.AstraDBProductCollection
	}

	var endpointSetting models.AppSetting
	if err := db.DB.Where("tenant_id = ? AND setting_key = ?", tenantID, "astradb_api_endpoint").First(&endpointSetting).Error; err == nil && endpointSetting.ValuePlain != "" {
		apiEndpoint = endpointSetting.ValuePlain
	}
	var tokenSetting models.AppSetting
	if err := db.DB.Where("tenant_id = ? AND setting_key = ?", tenantID, "astradb_token").First(&tokenSetting).Error; err == nil {
		if len(tokenSetting.ValueEncrypted) > 0 {
			if decrypted, err := pkg.Decrypt(tokenSetting.ValueEncrypted, cfg.EncryptionKey); err == nil {
				token = string(decrypted)
			}
		} else if tokenSetting.ValuePlain != "" {
			token = tokenSetting.ValuePlain
		}
	}
	var keyspaceSetting models.AppSetting
	if err := db.DB.Where("tenant_id = ? AND setting_key = ?", tenantID, "astradb_keyspace").First(&keyspaceSetting).Error; err == nil && keyspaceSetting.ValuePlain != "" {
		keyspace = keyspaceSetting.ValuePlain
	}
	var collectionSetting models.AppSetting
	if err := db.DB.Where("tenant_id = ? AND setting_key = ?", tenantID, "astradb_product_collection").First(&collectionSetting).Error; err == nil && collectionSetting.ValuePlain != "" {
		collection = collectionSetting.ValuePlain
	}

	if apiEndpoint == "" || token == "" {
		return nil, fmt.Errorf("Astra DB is not configured")
	}

	url := fmt.Sprintf("%s/api/json/v1/%s/%s", apiEndpoint, keyspace, collection)
	payload := map[string]interface{}{
		"find": map[string]interface{}{
			"filter": map[string]interface{}{
				"MA_CHA": strings.ToUpper(maCha),
			},
			"options": map[string]interface{}{
				"limit": 100,
			},
		},
	}

	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(bodyBytes))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Token", token)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return nil, fmt.Errorf("Astra DB returned status code %d", resp.StatusCode)
	}

	var astraResp struct {
		Data struct {
			Documents []map[string]interface{} `json:"documents"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&astraResp); err != nil {
		return nil, err
	}

	if len(astraResp.Errors) > 0 {
		return nil, fmt.Errorf("Astra DB error: %s", astraResp.Errors[0].Message)
	}

	return astraResp.Data.Documents, nil
}

func getMapString(m map[string]interface{}, keys ...string) string {
	for _, k := range keys {
		if val, ok := m[k]; ok && val != nil {
			if s, ok := val.(string); ok {
				return s
			}
			return fmt.Sprintf("%v", val)
		}
	}
	return ""
}

func getMapFloat(m map[string]interface{}, keys ...string) float64 {
	for _, k := range keys {
		if val, ok := m[k]; ok && val != nil {
			switch v := val.(type) {
			case float64:
				return v
			case float32:
				return float64(v)
			case int:
				return float64(v)
			case int64:
				return float64(v)
			case string:
				if f, err := strconv.ParseFloat(v, 64); err == nil {
					return f
				}
			}
		}
	}
	return 0
}

func loadCloudifyCredentials(tenantID string, cfg *config.Config) (erpURL, erpDB, erpLogin, erpPassword string, err error) {
	settings := map[string]*string{
		"erp_api_url":      &erpURL,
		"erp_api_db":       &erpDB,
		"erp_api_username": &erpLogin,
	}
	for key, dst := range settings {
		var s models.AppSetting
		if e := db.DB.Where("tenant_id = ? AND setting_key = ?", tenantID, key).First(&s).Error; e == nil {
			*dst = strings.TrimSpace(s.ValuePlain)
		}
	}

	var pwSetting models.AppSetting
	if e := db.DB.Where("tenant_id = ? AND setting_key = 'erp_api_password'", tenantID).First(&pwSetting).Error; e == nil {
		if len(pwSetting.ValueEncrypted) > 0 {
			decrypted, decErr := pkg.Decrypt(pwSetting.ValueEncrypted, cfg.EncryptionKey)
			if decErr != nil {
				err = fmt.Errorf("decrypt ERP password: %w", decErr)
				return
			}
			erpPassword = string(decrypted)
		} else {
			erpPassword = pwSetting.ValuePlain
		}
	}

	return
}

func classifyMessageIntent(ctx context.Context, tenantID, message string) (string, error) {
	// 1. Get AI provider from tenant settings
	provider := "claude"
	var providerSetting models.AppSetting
	if err := db.DB.Where("tenant_id = ? AND setting_key = 'ai_provider'", tenantID).First(&providerSetting).Error; err == nil && providerSetting.ValuePlain != "" {
		provider = providerSetting.ValuePlain
	}

	// 2. Get API key from tenant settings (per provider)
	var setting models.AppSetting
	keyFound := false
	for _, key := range ai.ProviderAPIKeySettingKeys(provider) {
		if err := db.DB.Where("tenant_id = ? AND setting_key = ?", tenantID, key).First(&setting).Error; err == nil {
			keyFound = true
			break
		}
	}

	cfg, _ := config.Load()
	apiKey := ""
	if keyFound {
		if setting.ValueEncrypted != nil && len(setting.ValueEncrypted) > 0 {
			decrypted, err := pkg.Decrypt(setting.ValueEncrypted, cfg.EncryptionKey)
			if err != nil {
				return "IN_SCOPE", fmt.Errorf("failed to decrypt API key: %w", err)
			}
			apiKey = string(decrypted)
		} else {
			apiKey = setting.ValuePlain
		}
	}

	// If no API key found, fallback to global configs (if any) or skip
	if apiKey == "" {
		if provider == "openai" {
			apiKey = cfg.LangflowAPIKey
		}
	}

	if apiKey == "" {
		return "IN_SCOPE", nil // Fallback if no API key configured
	}

	// 3. Get model from settings (fallback to provider defaults)
	model := "claude-haiku-4-5"
	if provider == "gemini" {
		model = "gemini-2.0-flash"
	} else if provider == "openai" {
		model = "gpt-5-mini"
	}
	var modelSetting models.AppSetting
	if err := db.DB.Where("tenant_id = ? AND setting_key = 'ai_model'", tenantID).First(&modelSetting).Error; err == nil && modelSetting.ValuePlain != "" {
		model = modelSetting.ValuePlain
	}

	// 4. Get base URL
	var baseURL string
	var baseURLSetting models.AppSetting
	if err := db.DB.Where("tenant_id = ? AND setting_key = 'ai_base_url'", tenantID).First(&baseURLSetting).Error; err == nil {
		baseURL = baseURLSetting.ValuePlain
	}

	// 5. Initialize provider client
	var aiClient ai.AIProvider
	switch provider {
	case "claude":
		aiClient = ai.NewClaudeProvider(apiKey, model, cfg.AIMaxTokens, baseURL)
	case "gemini":
		aiClient = ai.NewGeminiProvider(apiKey, model, baseURL)
	case "openai":
		aiClient = ai.NewOpenAIProvider(apiKey, model, baseURL)
	default:
		return "IN_SCOPE", fmt.Errorf("unsupported AI provider: %s", provider)
	}

	// Define system prompt
	systemPrompt := `Bạn là trợ lý lọc tin nhắn thông minh cho Chatbot doanh nghiệp BBI.
Nhiệm vụ của bạn là phân loại tin nhắn của khách hàng thành một trong ba nhãn sau:
1. "IN_SCOPE": Tin nhắn hỏi về thông tin sản phẩm, tồn kho, giá cả, đơn hàng, công nợ (financial debt), hoặc các vấn đề liên quan trực tiếp đến hoạt động mua bán của doanh nghiệp BBI mà RAG Bot có thể tự trả lời dựa trên tài liệu sản phẩm hoặc dữ liệu tồn kho/công nợ.
2. "HANDOVER": Tin nhắn phàn nàn về chất lượng dịch vụ, sản phẩm lỗi, chậm giao hàng, thái độ phục vụ, yêu cầu gặp nhân viên hoặc các vấn đề nghiêm trọng cần con người giải quyết.
3. "CASUAL": Tin nhắn chào hỏi xã giao, cảm ơn, đồng ý (ví dụ: "ok", "dạ", "cảm ơn", "hello", "hi") hoặc các câu nói bâng quơ không yêu cầu bot xử lý nghiệp vụ.

Định dạng trả về duy nhất là một đối tượng JSON:
{
  "label": "IN_SCOPE" | "HANDOVER" | "CASUAL",
  "reason": "Giải thích ngắn gọn lý do phân loại"
}
CHỈ trả về JSON, không thêm bất kỳ văn bản nào khác.`

	testCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	resp, err := aiClient.AnalyzeChat(testCtx, systemPrompt, message)
	if err != nil {
		return "IN_SCOPE", err
	}

	content := strings.TrimSpace(resp.Content)
	if strings.HasPrefix(content, "```") {
		if idx := strings.Index(content, "\n"); idx != -1 {
			content = content[idx+1:]
		}
		if idx := strings.LastIndex(content, "```"); idx != -1 {
			content = content[:idx]
		}
		content = strings.TrimSpace(content)
	}

	var parsed struct {
		Label string `json:"label"`
	}
	if err := json.Unmarshal([]byte(content), &parsed); err != nil {
		contentUpper := strings.ToUpper(content)
		if strings.Contains(contentUpper, "HANDOVER") {
			return "HANDOVER", nil
		}
		if strings.Contains(contentUpper, "CASUAL") {
			return "CASUAL", nil
		}
		return "IN_SCOPE", nil
	}

	label := strings.ToUpper(strings.TrimSpace(parsed.Label))
	if label == "HANDOVER" || label == "CASUAL" {
		return label, nil
	}
	return "IN_SCOPE", nil
}

func parseAttachmentText(atts []ZaloWebhookAttachment) string {
	var sb strings.Builder
	for _, att := range atts {
		switch att.Type {
		case "location":
			sb.WriteString(fmt.Sprintf("\n[Vị trí: vĩ độ %s, kinh độ %s]", att.Payload.Coordinates.Latitude, att.Payload.Coordinates.Longitude))
		case "file":
			sb.WriteString(fmt.Sprintf("\n[File: %s, Url: %s, dung lượng: %s byte]", att.Payload.Name, att.Payload.URL, att.Payload.Size))
		case "link":
			// Check if description contains business card JSON
			if strings.Contains(att.Payload.Description, "phone") && strings.Contains(att.Payload.Description, "qrCodeUrl") {
				var bizCard struct {
					Phone     string `json:"phone"`
					QRCodeURL string `json:"qrCodeUrl"`
				}
				if err := json.Unmarshal([]byte(att.Payload.Description), &bizCard); err == nil {
					sb.WriteString(fmt.Sprintf("\n[Danh thiếp: SĐT %s, QR: %s]", bizCard.Phone, bizCard.QRCodeURL))
					continue
				}
			}
			sb.WriteString(fmt.Sprintf("\n[Liên kết: %s - %s]", att.Payload.Description, att.Payload.URL))
		}
	}
	return sb.String()
}

