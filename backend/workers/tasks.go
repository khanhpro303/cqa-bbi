package workers

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"

	"github.com/vietbui/chat-quality-agent/ai"
	"github.com/vietbui/chat-quality-agent/channels"
	"github.com/vietbui/chat-quality-agent/config"
	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/db/models"
	"github.com/vietbui/chat-quality-agent/engine"
	"github.com/vietbui/chat-quality-agent/pkg"
)

// disambiguationReplyPattern matches the short replies the Langflow agent's
// DISAMBIGUATION FOLLOW-UP rule expects to resolve against a recent option
// list: a single digit 1–9, or an SP product code (with optional 1–4 char
// alpha/underscore suffix, e.g. SP458495_TL). These would otherwise be
// classified as CASUAL by the intent LLM — "1" looks exactly like the
// throwaway acknowledgements ("ok", "dạ") the prompt teaches it to drop —
// which closes the session and silences the bot mid-flow.
var disambiguationReplyPattern = regexp.MustCompile(`^\s*(?:[1-9]|SP\d{6}[a-zA-Z_]{0,4})\s*$`)

// isDisambiguationReply reports whether the given user text should bypass
// the CASUAL/HANDOVER intent classifier and be treated as IN_SCOPE because
// it is structurally a follow-up to an earlier product disambiguation list.
func isDisambiguationReply(text string) bool {
	return disambiguationReplyPattern.MatchString(text)
}

// pendingOptionsSuffix is appended to a session key to store the ordered list
// of postback strings most recently presented to the user as a numbered menu
// (e.g. the ten_dong_bo_web options in the inventory "dongsp" flow). A later
// bare-number reply ("1"/"2"/"3") is resolved against this list so the worker
// can replay the chosen postback without round-tripping through Langflow.
const pendingOptionsSuffix = ":pending_options"

// resolveNumericSelection resolves a bare-number reply against a previously
// presented numbered option list. It returns:
//   - payload: the stored postback at index n-1 (only when inRange is true)
//   - matched: true when text is a canonical positive integer and options is non-empty
//   - inRange: true when the parsed number falls within [1..len(options)]
//
// matched=true with inRange=false means the user typed a number but it is out
// of range — the caller should ask them to pick again while keeping the menu.
func resolveNumericSelection(text string, options []string) (payload string, matched bool, inRange bool) {
	if len(options) == 0 {
		return "", false, false
	}
	trimmed := strings.TrimSpace(text)
	n, err := strconv.Atoi(trimmed)
	// Reject non-canonical numeric forms ("01", "+1", "") so only clean menu
	// picks are treated as selections.
	if err != nil || strconv.Itoa(n) != trimmed {
		return "", false, false
	}
	if n >= 1 && n <= len(options) {
		return options[n-1], true, true
	}
	return "", true, false
}

// storePendingOptions persists the postbacks of a numbered menu under the
// session key so a later bare-number reply can replay the chosen option. TTL
// follows the session timeout. No-op when Redis is unavailable or the menu is
// empty.
func storePendingOptions(ctx context.Context, sessionKey string, buttons []channels.ZaloOAButton, timeoutMinutes int) {
	if db.RedisClient == nil || len(buttons) == 0 {
		return
	}
	postbacks := make([]string, len(buttons))
	for i, b := range buttons {
		postbacks[i] = b.Payload
	}
	raw, err := json.Marshal(postbacks)
	if err != nil {
		log.Printf("[worker] failed to marshal pending options for %s: %v", sessionKey, err)
		return
	}
	ttl := time.Duration(timeoutMinutes)*time.Minute + 1*time.Minute
	db.RedisClient.Set(ctx, sessionKey+pendingOptionsSuffix, raw, ttl)
}

const (
	TypeZaloWebhook    = "zalo:webhook"
	TypeSessionTimeout = "session:timeout"
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
	OAID string `json:"oa_id"`
}

// NewZaloWebhookTask creates a new task for processing a Zalo webhook
func NewZaloWebhookTask(payload ZaloWebhookPayload) (*asynq.Task, error) {
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	return asynq.NewTask(TypeZaloWebhook, payloadBytes), nil
}

type SessionTimeoutPayload struct {
	SessionKey     string `json:"session_key"`
	SessionID      string `json:"session_id"`
	TenantID       string `json:"tenant_id"`
	ChannelID      string `json:"channel_id"`
	ZaloUserID     string `json:"zalo_user_id"`
	ZaloGroupID    string `json:"zalo_group_id"`
	TimeoutMessage string `json:"timeout_message"`
}

// NewSessionTimeoutTask creates a new task to check session timeout
func NewSessionTimeoutTask(payload SessionTimeoutPayload) (*asynq.Task, error) {
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	return asynq.NewTask(TypeSessionTimeout, payloadBytes), nil
}

// ChannelMetadata holds the session settings mapped from models.Channel.Metadata
type ChannelMetadata struct {
	SessionKeyword        string `json:"session_keyword"`
	SessionEndKeyword     string `json:"session_end_keyword"`
	SessionWelcomeMessage string `json:"session_welcome_message"`
	SessionGoodbyeMessage string `json:"session_goodbye_message"`
	SessionTimeout        int    `json:"session_timeout_minutes"`
	SessionTimeoutMessage string `json:"session_timeout_message"`

	LangflowAPIURL       string `json:"langflow_api_url"`
	LangflowAPIKey       string `json:"langflow_api_key"`
	LangflowFlowID       string `json:"langflow_flow_id"`
	LangflowPublicFlowID string `json:"langflow_public_flow_id"`
	SystemPrompt         string `json:"system_prompt"`

	AstraDBAPIEndpoint string `json:"astradb_api_endpoint"`
	AstraDBToken       string `json:"astradb_token"`
	AstraDBKeyspace    string `json:"astradb_keyspace"`
	AstraDBCollection  string `json:"astradb_collection"`
}

// HandleZaloWebhookTask processes the webhook task
func HandleZaloWebhookTask(cfg *config.Config, langflowClient *engine.LangflowClient) asynq.HandlerFunc {
	var asynqClient *asynq.Client
	if cfg.RedisURL != "" {
		opt, err := asynq.ParseRedisURI(cfg.RedisURL)
		if err == nil {
			asynqClient = asynq.NewClient(opt)
		}
	}

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
		if meta.SessionTimeoutMessage == "" {
			meta.SessionTimeoutMessage = cfg.ChatbotSessionTimeoutMessage
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
		// System prompt is optional and per-tenant only — an empty value means
		// "use the SYSTEM_PROMPT global variable configured inside Langflow",
		// so there is no env/global fallback here.
		if meta.SystemPrompt == "" {
			var setting models.AppSetting
			if err := db.DB.Where("tenant_id = ? AND setting_key = ?", matchedChannel.TenantID, "ai_engine_system_prompt").First(&setting).Error; err == nil && setting.ValuePlain != "" {
				meta.SystemPrompt = setting.ValuePlain
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

		// Embedder for $vector field on AstraDB chat history rows. Reads the
		// tenant's `ai_api_key_openai` setting first (set via CQA Settings UI)
		// then falls back to env LANGFLOW_EMBEDDING_API_KEY / OPENAI_API_KEY.
		// Returns nil if no key is available — rows are then written without a
		// vector (degraded mode).
		astraEmbedder := engine.BuildTenantEmbedder(matchedChannel.TenantID, engine.EmbeddingConfig{
			FallbackAPIKey: cfg.LangflowEmbeddingAPIKey,
			Model:          cfg.LangflowEmbeddingModel,
			BaseURL:        cfg.LangflowEmbeddingBaseURL,
			EncryptionKey:  cfg.EncryptionKey,
		})

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
					db.RedisClient.Set(ctx, sessionKey, newSessionID, time.Duration(meta.SessionTimeout)*time.Minute+1*time.Minute)
				}
				if asynqClient != nil {
					timeoutTask, err := NewSessionTimeoutTask(SessionTimeoutPayload{
						SessionKey:     sessionKey,
						SessionID:      newSessionID,
						TenantID:       matchedChannel.TenantID,
						ChannelID:      matchedChannel.ID,
						ZaloUserID:     payload.Sender.ID,
						ZaloGroupID:    matchedGroup.ZaloGroupID,
						TimeoutMessage: meta.SessionTimeoutMessage,
					})
					if err == nil {
						_, _ = asynqClient.Enqueue(timeoutTask, asynq.ProcessIn(time.Duration(meta.SessionTimeout)*time.Minute))
					}
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
			db.RedisClient.Expire(ctx, sessionKey, time.Duration(meta.SessionTimeout)*time.Minute+1*time.Minute)
		}
		if asynqClient != nil {
			timeoutTask, err := NewSessionTimeoutTask(SessionTimeoutPayload{
				SessionKey:     sessionKey,
				SessionID:      activeSessionID,
				TenantID:       matchedChannel.TenantID,
				ChannelID:      matchedChannel.ID,
				ZaloUserID:     payload.Sender.ID,
				ZaloGroupID:    matchedGroup.ZaloGroupID,
				TimeoutMessage: meta.SessionTimeoutMessage,
			})
			if err == nil {
				_, _ = asynqClient.Enqueue(timeoutTask, asynq.ProcessIn(time.Duration(meta.SessionTimeout)*time.Minute))
			}
		}

		// Save user message to Astra DB asynchronously
		go func() {
			err := engine.SaveChatMessage(context.Background(), meta.AstraDBAPIEndpoint, meta.AstraDBToken, meta.AstraDBKeyspace, meta.AstraDBCollection, engine.ChatMessage{
				ZaloUserID: payload.Sender.ID,
				SessionID:  activeSessionID,
				Role:       "user",
				Content:    payload.Message.Text,
			}, astraEmbedder)
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

		// Resolve a bare-number reply ("1"/"2"/"3") against the most recently
		// presented numbered menu (inventory ten_dong_bo_web options). On a valid
		// pick, rewrite userText to the stored postback and fall through to the
		// button-intercept handlers below — no Langflow round-trip. An out-of-range
		// number re-prompts while keeping the menu so the user can pick again.
		if db.RedisClient != nil {
			pendingKey := sessionKey + pendingOptionsSuffix
			if raw, err := db.RedisClient.Get(ctx, pendingKey).Result(); err == nil && raw != "" {
				var pendingOptions []string
				if jsonErr := json.Unmarshal([]byte(raw), &pendingOptions); jsonErr == nil {
					if postback, matched, inRange := resolveNumericSelection(userText, pendingOptions); matched {
						if inRange {
							db.RedisClient.Del(ctx, pendingKey)
							userText = postback
						} else {
							msg := "Vui lòng chọn một số trong danh sách."
							if matchedGroup.ZaloGroupID != "" {
								_ = adapter.SendGroupMessage(ctx, matchedGroup.ZaloGroupID, msg)
							} else {
								_ = adapter.SendMessage(ctx, payload.Sender.ID, msg)
							}
							return nil
						}
					}
				}
			}
		}

		// Intercept Zalo OA interactive button clicks
		// A. Choose Flow Type: dongsp or skucuthe
		if strings.HasPrefix(userText, "#choose_flow_type:dongsp:") {
			keyword := strings.TrimPrefix(userText, "#choose_flow_type:dongsp:")
			matchedProducts, errSearch := searchProductsByWebNameFromCache(ctx, matchedChannel.TenantID, keyword)
			if errSearch != nil || len(matchedProducts) == 0 {
				_ = adapter.SendMessage(ctx, payload.Sender.ID, fmt.Sprintf("Không tìm thấy dòng sản phẩm nào khớp với từ khóa '%s'.", keyword))
				return nil
			}

			// Group the matched products by TEN_DONG_BO_WEB (synced web name);
			// fall back to MA_CHA when a product has no web name so legacy SKUs
			// still appear.
			webGroups := engine.RankProductWebGroups(matchedProducts)

			buttons := make([]channels.ZaloOAButton, 0, 3)
			for i, g := range webGroups {
				if i >= 3 {
					break
				}
				var btnPayload string
				if g.IsFallback && len(g.ParentCodes) > 0 {
					btnPayload = "#show_macha_options:" + g.ParentCodes[0]
				} else {
					btnPayload = "#show_macha_options_by_web:" + g.WebName
				}
				buttons = append(buttons, channels.ZaloOAButton{
					Title:   g.WebName,
					Payload: btnPayload,
				})
			}

			// Exactly one matching product line → don't ask. Rewrite userText to
			// that option's postback and fall through to #show_macha_options* below,
			// which sums inventory directly (sumInventoryByMaChaAndWebName).
			if len(buttons) == 1 {
				userText = buttons[0].Payload
			} else {
				prompt := fmt.Sprintf("Tôi tìm thấy các dòng sản phẩm khớp với '%s'. Bạn muốn kiểm tra tồn kho dòng nào?", keyword)

				// Zalo OA hiện không support template_type "list" trên /message/cs
				// (luôn trả -233). Fallback về plain text đánh số; lưu các postback
				// để khách gõ "1"/"2"/"3" là resolve được ở intercept phía trên.
				// Re-enable channels.BuildV3ListTemplatePayload nếu Zalo confirm hỗ trợ.
				text := channels.BuildButtonOptionsAsText(prompt, buttons)
				storePendingOptions(ctx, sessionKey, buttons, meta.SessionTimeout)
				var sendErr error
				if matchedGroup.ZaloGroupID != "" {
					sendErr = adapter.SendGroupMessage(ctx, matchedGroup.ZaloGroupID, text)
				} else {
					sendErr = adapter.SendMessage(ctx, payload.Sender.ID, text)
				}
				_ = sendErr
				return nil
			}
		}

		if strings.HasPrefix(userText, "#choose_flow_type:skucuthe:") {
			keyword := strings.TrimPrefix(userText, "#choose_flow_type:skucuthe:")
			reply := fmt.Sprintf("Bạn muốn xem tồn kho cụ thể của màu và size nào cho dòng sản phẩm %s?\nVui lòng nhập thông tin (Ví dụ: %s màu đỏ size L).", keyword, keyword)
			var sendErr error
			if matchedGroup.ZaloGroupID != "" {
				sendErr = adapter.SendGroupMessage(ctx, matchedGroup.ZaloGroupID, reply)
			} else {
				sendErr = adapter.SendMessage(ctx, payload.Sender.ID, reply)
			}
			_ = sendErr
			return nil
		}

		// 1. Parent Code Options Selection (#show_macha_options:<MA_CHA>)
		if strings.HasPrefix(userText, "#show_macha_options:") {
			maCha := strings.TrimPrefix(userText, "#show_macha_options:")
			// Fetch variants
			childProducts, err := getProductsByMaChaFromCache(ctx, matchedChannel.TenantID, maCha)
			if err != nil || len(childProducts) == 0 {
				_ = adapter.SendMessage(ctx, payload.Sender.ID, "Không tìm thấy thông tin chi tiết của dòng sản phẩm.")
				return nil
			}

			// Extract unique web sync names
			webNamesMap := make(map[string]bool)
			var webNames []string
			for _, p := range childProducts {
				wName := getMapString(p, "TEN_DONG_BO_WEB", "ten_dong_bo_web")
				if wName != "" && !webNamesMap[wName] {
					webNamesMap[wName] = true
					webNames = append(webNames, wName)
				}
			}

			if len(webNames) == 0 {
				// No web names found, perform direct realtime inventory lookup for this MA_CHA
				stock, details, err := sumInventoryByMaChaAndWebName(ctx, matchedChannel.TenantID, &permCtx, maCha, "")
				if err != nil {
					_ = adapter.SendMessage(ctx, payload.Sender.ID, "Có lỗi xảy ra khi truy vấn tồn kho dòng sản phẩm.")
					return nil
				}
				reply := fmt.Sprintf("Tổng tồn kho dòng %s: %.1f\n\nChi tiết:\n%s", maCha, stock, strings.Join(details, "\n"))
				if matchedGroup.ZaloGroupID != "" {
					_ = adapter.SendGroupMessage(ctx, matchedGroup.ZaloGroupID, reply)
				} else {
					_ = adapter.SendMessage(ctx, payload.Sender.ID, reply)
				}
				return nil
			}

			// Limit to top 3 web name options
			if len(webNames) > 3 {
				webNames = webNames[:3]
			}

			if len(webNames) == 1 {
				// Only 1 option, run stock lookup immediately
				stock, details, err := sumInventoryByMaChaAndWebName(ctx, matchedChannel.TenantID, &permCtx, maCha, webNames[0])
				if err != nil {
					_ = adapter.SendMessage(ctx, payload.Sender.ID, "Có lỗi xảy ra khi truy vấn tồn kho sản phẩm.")
					return nil
				}
				reply := fmt.Sprintf("Tổng tồn kho của %s: %.1f\n\nChi tiết:\n%s", webNames[0], stock, strings.Join(details, "\n"))
				if matchedGroup.ZaloGroupID != "" {
					_ = adapter.SendGroupMessage(ctx, matchedGroup.ZaloGroupID, reply)
				} else {
					_ = adapter.SendMessage(ctx, payload.Sender.ID, reply)
				}
				return nil
			}

			// Construct Zalo rich message with buttons (oa.query.hide)
			buttons := make([]channels.ZaloOAButton, 0, len(webNames))
			for _, wName := range webNames {
				btnPayload := map[string]string{
					"MA_CHA":   maCha,
					"WEB_NAME": wName,
				}
				btnPayloadJSON, _ := json.Marshal(btnPayload)
				buttons = append(buttons, channels.ZaloOAButton{
					Title:   wName,
					Payload: "#check_stock_webname:" + string(btnPayloadJSON),
				})
			}
			prompt := fmt.Sprintf("Dòng sản phẩm '%s' có các tùy chọn đồng bộ sau. Vui lòng chọn một tùy chọn để kiểm tra tồn kho:", maCha)

			// Zalo OA list template fallback to plain text — see flow_type prompt above.
			text := channels.BuildButtonOptionsAsText(prompt, buttons)
			var sendErr error
			if matchedGroup.ZaloGroupID != "" {
				sendErr = adapter.SendGroupMessage(ctx, matchedGroup.ZaloGroupID, text)
			} else {
				sendErr = adapter.SendMessage(ctx, payload.Sender.ID, text)
			}
			_ = sendErr
			return nil
		}

		// 1b. Inventory by Web Name (#show_macha_options_by_web:<TEN_DONG_BO_WEB>)
		// Triggered after the user selects a web-name option from the
		// dongsp flow. Aggregates inventory across every MA_CHA that shares
		// the same ten_dong_bo_web so a single product line answers with
		// totals across all parent SKUs.
		if strings.HasPrefix(userText, "#show_macha_options_by_web:") {
			webName := strings.TrimPrefix(userText, "#show_macha_options_by_web:")
			childProducts, err := getProductsByWebNameFromCache(ctx, matchedChannel.TenantID, webName)
			if err != nil || len(childProducts) == 0 {
				_ = adapter.SendMessage(ctx, payload.Sender.ID, fmt.Sprintf("Không tìm thấy sản phẩm nào trong dòng '%s'.", webName))
				return nil
			}

			maChaSeen := make(map[string]struct{})
			var maChas []string
			for _, p := range childProducts {
				if mc := getMapString(p, "MA_CHA", "ma_cha"); mc != "" {
					if _, dup := maChaSeen[mc]; !dup {
						maChaSeen[mc] = struct{}{}
						maChas = append(maChas, mc)
					}
				}
			}

			if len(maChas) == 0 {
				_ = adapter.SendMessage(ctx, payload.Sender.ID, fmt.Sprintf("Không xác định được mã cha cho dòng '%s'.", webName))
				return nil
			}

			var totalStock float64
			var details []string
			for _, mc := range maChas {
				stock, lines, sumErr := sumInventoryByMaChaAndWebName(ctx, matchedChannel.TenantID, &permCtx, mc, webName)
				if sumErr != nil {
					log.Printf("[zalo_webhook] inventory error ma_cha=%s web=%s: %v", mc, webName, sumErr)
					continue
				}
				totalStock += stock
				details = append(details, lines...)
			}

			reply := fmt.Sprintf("Tổng tồn kho của dòng %s: %.1f\n\nChi tiết:\n%s", webName, totalStock, strings.Join(details, "\n"))
			if matchedGroup.ZaloGroupID != "" {
				_ = adapter.SendGroupMessage(ctx, matchedGroup.ZaloGroupID, reply)
			} else {
				_ = adapter.SendMessage(ctx, payload.Sender.ID, reply)
			}
			return nil
		}

		// 2. Web Synchronized Name Selection (#check_stock_webname:<JSON>)
		if strings.HasPrefix(userText, "#check_stock_webname:") {
			payloadStr := strings.TrimPrefix(userText, "#check_stock_webname:")
			var parsed struct {
				MaCha   string `json:"MA_CHA"`
				WebName string `json:"WEB_NAME"`
			}
			if err := json.Unmarshal([]byte(payloadStr), &parsed); err != nil {
				_ = adapter.SendMessage(ctx, payload.Sender.ID, "Dữ liệu yêu cầu không hợp lệ.")
				return nil
			}

			stock, details, err := sumInventoryByMaChaAndWebName(ctx, matchedChannel.TenantID, &permCtx, parsed.MaCha, parsed.WebName)
			if err != nil {
				_ = adapter.SendMessage(ctx, payload.Sender.ID, "Có lỗi xảy ra khi tính toán tồn kho sản phẩm.")
				return nil
			}

			reply := fmt.Sprintf("Tổng tồn kho thực tế của '%s' (Mã cha: %s) là: %.1f\n\nChi tiết:\n%s", parsed.WebName, parsed.MaCha, stock, strings.Join(details, "\n"))
			if matchedGroup.ZaloGroupID != "" {
				_ = adapter.SendGroupMessage(ctx, matchedGroup.ZaloGroupID, reply)
			} else {
				_ = adapter.SendMessage(ctx, payload.Sender.ID, reply)
			}
			return nil
		}

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

		// 3. Show Product Variants Selection (#show_product_variants:<MA_CHA>)
		if strings.HasPrefix(userText, "#show_product_variants:") {
			maCha := strings.TrimPrefix(userText, "#show_product_variants:")
			childProducts, err := getProductsByMaChaFromCache(ctx, matchedChannel.TenantID, maCha)
			if err != nil || len(childProducts) == 0 {
				_ = adapter.SendMessage(ctx, payload.Sender.ID, "Không tìm thấy thông tin chi tiết của dòng sản phẩm.")
				return nil
			}

			if len(childProducts) == 1 {
				p := childProducts[0]
				sku := getMapString(p, "MA", "code", "ma")
				sendProductDetailsDirectly(ctx, adapter, matchedChannel.TenantID, sku, matchedGroup.ZaloGroupID, payload.Sender.ID)
				return nil
			}

			buttons := make([]channels.ZaloOAButton, 0, 4)
			for i, p := range childProducts {
				if i >= 4 { // Zalo template supports max 4 buttons
					break
				}
				sku := getMapString(p, "MA", "code", "ma")
				t1 := getMapString(p, "THUOC_TINH_1", "thuoc_tinh_1")
				t2 := getMapString(p, "THUOC_TINH_2", "thuoc_tinh_2")

				btnTitle := sku
				if t1 != "" && t2 != "" {
					btnTitle = fmt.Sprintf("%s (%s/%s)", sku, t1, t2)
				} else if t1 != "" {
					btnTitle = fmt.Sprintf("%s (%s)", sku, t1)
				} else if t2 != "" {
					btnTitle = fmt.Sprintf("%s (%s)", sku, t2)
				}

				buttons = append(buttons, channels.ZaloOAButton{
					Title:   btnTitle,
					Payload: "#show_product_detail:" + sku,
				})
			}
			prompt := fmt.Sprintf("Dòng sản phẩm '%s' có các tùy chọn màu/size sau. Vui lòng chọn một tùy chọn để xem chi tiết sản phẩm:", maCha)

			// Zalo OA list template fallback to plain text — see flow_type prompt above.
			text := channels.BuildButtonOptionsAsText(prompt, buttons)
			var sendErr error
			if matchedGroup.ZaloGroupID != "" {
				sendErr = adapter.SendGroupMessage(ctx, matchedGroup.ZaloGroupID, text)
			} else {
				sendErr = adapter.SendMessage(ctx, payload.Sender.ID, text)
			}
			_ = sendErr
			return nil
		}

		// 3b. Show Product Variants By Web Name (#show_web_variants:<TEN_DONG_BO_WEB>)
		// Triggered when the user taps a web-name option from the products
		// flow. Renders the variant SKUs (color/size) across every MA_CHA in
		// the line so the user sees the full catalog for that product line.
		if strings.HasPrefix(userText, "#show_web_variants:") {
			webName := strings.TrimPrefix(userText, "#show_web_variants:")
			childProducts, err := getProductsByWebNameFromCache(ctx, matchedChannel.TenantID, webName)
			if err != nil || len(childProducts) == 0 {
				_ = adapter.SendMessage(ctx, payload.Sender.ID, fmt.Sprintf("Không tìm thấy sản phẩm nào trong dòng '%s'.", webName))
				return nil
			}

			if len(childProducts) == 1 {
				p := childProducts[0]
				sku := getMapString(p, "MA", "code", "ma")
				sendProductDetailsDirectly(ctx, adapter, matchedChannel.TenantID, sku, matchedGroup.ZaloGroupID, payload.Sender.ID)
				return nil
			}

			buttons := make([]channels.ZaloOAButton, 0, 4)
			for i, p := range childProducts {
				if i >= 4 { // Zalo template supports max 4 buttons
					break
				}
				sku := getMapString(p, "MA", "code", "ma")
				t1 := getMapString(p, "THUOC_TINH_1", "thuoc_tinh_1")
				t2 := getMapString(p, "THUOC_TINH_2", "thuoc_tinh_2")

				btnTitle := sku
				if t1 != "" && t2 != "" {
					btnTitle = fmt.Sprintf("%s (%s/%s)", sku, t1, t2)
				} else if t1 != "" {
					btnTitle = fmt.Sprintf("%s (%s)", sku, t1)
				} else if t2 != "" {
					btnTitle = fmt.Sprintf("%s (%s)", sku, t2)
				}

				buttons = append(buttons, channels.ZaloOAButton{
					Title:   btnTitle,
					Payload: "#show_product_detail:" + sku,
				})
			}
			prompt := fmt.Sprintf("Dòng sản phẩm '%s' có các tùy chọn màu/size sau. Vui lòng chọn một tùy chọn để xem chi tiết sản phẩm:", webName)
			if len(childProducts) > 4 {
				prompt = fmt.Sprintf("Dòng sản phẩm '%s' có %d biến thể. Hiển thị 4 lựa chọn đầu — nhập từ khóa cụ thể hơn (vd. màu/size) để xem hết:", webName, len(childProducts))
			}

			// Zalo OA list template fallback to plain text — see flow_type prompt above.
			text := channels.BuildButtonOptionsAsText(prompt, buttons)
			var sendErr error
			if matchedGroup.ZaloGroupID != "" {
				sendErr = adapter.SendGroupMessage(ctx, matchedGroup.ZaloGroupID, text)
			} else {
				sendErr = adapter.SendMessage(ctx, payload.Sender.ID, text)
			}
			_ = sendErr
			return nil
		}

		// 4. Show Product Detail Selection (#show_product_detail:<SKU>)
		if strings.HasPrefix(userText, "#show_product_detail:") {
			sku := strings.TrimPrefix(userText, "#show_product_detail:")
			sendProductDetailsDirectly(ctx, adapter, matchedChannel.TenantID, sku, matchedGroup.ZaloGroupID, payload.Sender.ID)
			return nil
		}

		permissionToken, err := engine.SignPermissionToken(permCtx, cfg.EncryptionKey)
		if err != nil {
			log.Printf("[worker] failed to sign permission token: %v", err)
		}

		// 2. Call Langflow API (passing Zalo Sender ID as zaloUserID, customerCode, and permissionToken)
		replyText, err := langflowClient.RunFlowWithCustomer(ctx, activeSessionID, payload.Sender.ID, userText, meta.LangflowAPIURL, meta.LangflowAPIKey, flowIDToUse, customerCode, permissionToken, meta.SystemPrompt)
		if err != nil {
			return fmt.Errorf("langflow error: %w", err)
		}

		if replyText == "" {
			log.Printf("[worker] langflow returned empty response")
			return nil
		}

		// Save assistant reply to Astra DB asynchronously
		go func() {
			err := engine.SaveChatMessage(context.Background(), meta.AstraDBAPIEndpoint, meta.AstraDBToken, meta.AstraDBKeyspace, meta.AstraDBCollection, engine.ChatMessage{
				ZaloUserID: payload.Sender.ID,
				SessionID:  activeSessionID,
				Role:       "assistant",
				Content:    replyText,
			}, astraEmbedder)
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

// HandleSessionTimeoutTask processes the session timeout delayed task
func HandleSessionTimeoutTask(cfg *config.Config) asynq.HandlerFunc {
	return func(ctx context.Context, t *asynq.Task) error {
		var payload SessionTimeoutPayload
		if err := json.Unmarshal(t.Payload(), &payload); err != nil {
			return fmt.Errorf("json.Unmarshal failed: %v: %w", err, asynq.SkipRetry)
		}

		log.Printf("[worker] processing session timeout check for session %s (key: %s)", payload.SessionID, payload.SessionKey)

		if db.RedisClient == nil {
			log.Printf("[worker] RedisClient is nil, cannot process session timeout")
			return nil
		}

		val, err := db.RedisClient.Get(ctx, payload.SessionKey).Result()
		if err != nil || val == "" {
			// Session already expired or deleted, nothing to do
			log.Printf("[worker] session %s already expired or deleted from Redis", payload.SessionID)
			return nil
		}

		if val != payload.SessionID {
			// The session key now holds a different session ID (a new session was started)
			log.Printf("[worker] session ID mismatch (current: %s, task: %s), skipping timeout", val, payload.SessionID)
			return nil
		}

		// Check remaining TTL. If it has been extended, skip timeout.
		ttl, err := db.RedisClient.TTL(ctx, payload.SessionKey).Result()
		if err != nil {
			return fmt.Errorf("failed to get session TTL: %w", err)
		}

		if ttl > 65*time.Second {
			log.Printf("[worker] session %s was extended (remaining TTL: %v), skipping timeout", payload.SessionID, ttl)
			return nil
		}

		// Close the session: delete the key from Redis
		if err := db.RedisClient.Del(ctx, payload.SessionKey).Err(); err != nil {
			log.Printf("[worker] failed to delete session key %s: %v", payload.SessionKey, err)
		}

		// Find the channel to get credentials
		var ch models.Channel
		if err := db.DB.Where("id = ? AND is_active = true", payload.ChannelID).First(&ch).Error; err != nil {
			log.Printf("[worker] channel not found or inactive: %v", err)
			return nil
		}

		credBytes, err := pkg.Decrypt(ch.CredentialsEncrypted, cfg.EncryptionKey)
		if err != nil {
			log.Printf("[worker] failed to decrypt credentials for channel %s: %v", payload.ChannelID, err)
			return nil
		}

		var zaloCreds channels.ZaloOACredentials
		if err := json.Unmarshal(credBytes, &zaloCreds); err != nil {
			log.Printf("[worker] failed to unmarshal credentials: %v", err)
			return nil
		}

		adapter := channels.NewZaloOAAdapter(zaloCreds)
		adapter.SetTokenRefreshCallback(func(newAccess, newRefresh string) {
			var channel models.Channel
			if db.DB.First(&channel, "id = ?", ch.ID).Error == nil {
				credsMap := map[string]interface{}{
					"app_id":        zaloCreds.AppID,
					"app_secret":    zaloCreds.AppSecret,
					"access_token":  newAccess,
					"refresh_token": newRefresh,
					"oa_id":         zaloCreds.OAId,
				}
				newCredJSON, _ := json.Marshal(credsMap)
				encrypted, _ := pkg.Encrypt(newCredJSON, cfg.EncryptionKey)
				db.DB.Model(&channel).Update("credentials_encrypted", encrypted)
			}
		})

		timeoutMsg := payload.TimeoutMessage
		if timeoutMsg == "" {
			timeoutMsg = cfg.ChatbotSessionTimeoutMessage
		}

		var sendErr error
		if payload.ZaloGroupID != "" {
			sendErr = adapter.SendGroupMessage(ctx, payload.ZaloGroupID, timeoutMsg)
		} else {
			sendErr = adapter.SendMessage(ctx, payload.ZaloUserID, timeoutMsg)
		}

		if sendErr != nil {
			log.Printf("[worker] failed to send session timeout message: %v", sendErr)
			return sendErr
		}

		log.Printf("[worker] successfully sent timeout message and closed session %s", payload.SessionID)
		return nil
	}
}

func sumInventoryByMaCha(ctx context.Context, tenantID string, permCtx *engine.GroupPermissionContext, maCha string) (float64, error) {
	// 1. Fetch child products from Astra DB
	childProducts, err := getProductsByMaChaFromCache(ctx, tenantID, maCha)
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

func getProductsByMaChaFromCache(ctx context.Context, tenantID, maCha string) ([]map[string]interface{}, error) {
	var products []models.CachedProduct
	err := db.DB.WithContext(ctx).
		Where("tenant_id = ? AND ma_cha = ?", tenantID, maCha).
		Limit(100).
		Find(&products).Error
	if err != nil {
		return nil, fmt.Errorf("local MySQL cache query failed: %w", err)
	}

	var results []map[string]interface{}
	for _, p := range products {
		results = append(results, map[string]interface{}{
			"MA":                 p.MA,
			"ma":                 p.MA,
			"code":               p.MA,
			"ma_hang":            p.MA,
			"product_code":       p.MA,
			"TEN_DONG_BO_WEB":    p.TEN_DONG_BO_WEB,
			"TEN":                p.TEN,
			"ten":                p.TEN,
			"ten_hang":           p.TEN,
			"THUOC_TINH_1":       p.THUOC_TINH_1,
			"THUOC_TINH_2":       p.THUOC_TINH_2,
			"DON_GIA_BAN":        p.DON_GIA_BAN,
			"LINK_ANH":           p.LINK_ANH,
			"NHAN_HIEU_NAME":     p.NHAN_HIEU_NAME,
			"LIST_TEN_NHOM_VTHH": p.LIST_TEN_NHOM_VTHH,
			"KHO":                p.KHO,
			"MA_CHA":             p.MA_CHA,
			"ma_cha":             p.MA_CHA,
			"DVT":                p.DVT,
		})
	}
	return results, nil
}

func sendProductDetailsDirectly(ctx context.Context, adapter *channels.ZaloOAAdapter, tenantID, sku, zaloGroupID, senderID string) {
	var p models.CachedProduct
	if err := db.DB.WithContext(ctx).Where("tenant_id = ? AND ma = ?", tenantID, sku).First(&p).Error; err != nil {
		log.Printf("[worker] error finding cached product details: %v", err)
		_ = adapter.SendMessage(ctx, senderID, "Không tìm thấy thông tin chi tiết cho mã SKU: "+sku)
		return
	}

	// Format price nicely
	priceStr := "Liên hệ"
	if p.DON_GIA_BAN > 0 {
		priceStr = fmt.Sprintf("%.0fđ", p.DON_GIA_BAN)
	}

	// Format weight
	weightStr := "1.0 kg" // default fallback

	// Construct detail message
	name := p.TEN_DONG_BO_WEB
	if name == "" {
		name = p.TEN
	}

	var detailMsg strings.Builder
	detailMsg.WriteString("Thông tin chi tiết sản phẩm:\n")
	detailMsg.WriteString(fmt.Sprintf("- Mã sản phẩm/SKU: %s\n", p.MA))
	detailMsg.WriteString(fmt.Sprintf("- Tên sản phẩm: %s\n", name))
	detailMsg.WriteString(fmt.Sprintf("- Đơn giá bán: %s\n", priceStr))
	detailMsg.WriteString(fmt.Sprintf("- Đơn vị tính (DVT): %s\n", p.DVT))
	detailMsg.WriteString(fmt.Sprintf("- Trọng lượng: %s\n", weightStr))
	if p.THUOC_TINH_1 != "" {
		detailMsg.WriteString(fmt.Sprintf("- Thuộc tính 1: %s\n", p.THUOC_TINH_1))
	}
	if p.THUOC_TINH_2 != "" {
		detailMsg.WriteString(fmt.Sprintf("- Thuộc tính 2: %s\n", p.THUOC_TINH_2))
	}

	reply := detailMsg.String()
	if zaloGroupID != "" {
		_ = adapter.SendGroupMessage(ctx, zaloGroupID, reply)
	} else {
		_ = adapter.SendMessage(ctx, senderID, reply)
	}
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
	// 0. Fast-path: structural disambiguation reply (digit 1–9 or SPxxxxxx[_TL])
	//    must not be re-classified by the LLM. The classifier prompt teaches
	//    short tokens like "1"/"dạ"/"ok" as CASUAL, which would close the
	//    session and drop the customer's actual pick on the floor.
	if isDisambiguationReply(message) {
		return "IN_SCOPE", nil
	}

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

// getAIClient creates an AI provider client based on tenant settings.
func getAIClient(ctx context.Context, tenantID string) (ai.AIProvider, error) {
	provider := "claude"
	var providerSetting models.AppSetting
	if err := db.DB.Where("tenant_id = ? AND setting_key = 'ai_provider'", tenantID).First(&providerSetting).Error; err == nil && providerSetting.ValuePlain != "" {
		provider = providerSetting.ValuePlain
	}

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
				return nil, fmt.Errorf("failed to decrypt API key: %w", err)
			}
			apiKey = string(decrypted)
		} else {
			apiKey = setting.ValuePlain
		}
	}

	if apiKey == "" {
		if provider == "openai" {
			apiKey = cfg.LangflowAPIKey
		}
	}

	if apiKey == "" {
		return nil, fmt.Errorf("API key not configured for provider %s", provider)
	}

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

	var baseURL string
	var baseURLSetting models.AppSetting
	if err := db.DB.Where("tenant_id = ? AND setting_key = 'ai_base_url'", tenantID).First(&baseURLSetting).Error; err == nil {
		baseURL = baseURLSetting.ValuePlain
	}

	switch provider {
	case "claude":
		return ai.NewClaudeProvider(apiKey, model, cfg.AIMaxTokens, baseURL), nil
	case "gemini":
		return ai.NewGeminiProvider(apiKey, model, baseURL), nil
	case "openai":
		return ai.NewOpenAIProvider(apiKey, model, baseURL), nil
	default:
		return nil, fmt.Errorf("unsupported AI provider: %s", provider)
	}
}

// fuzzyMatchMaChaWithLLM uses the LLM to find the best matching ma_cha
// from the local cached_products table when SQL LIKE returns no results.
// The prompt includes a representative ten_dong_bo_web + ten per ma_cha so
// the LLM can bridge name-level variations (e.g. "Storm 3" ↔ "Storm III",
// "mu bao hiem" ↔ "mũ bảo hiểm") in addition to code-format variants.
func fuzzyMatchMaChaWithLLM(ctx context.Context, tenantID, keyword string) (string, error) {
	type maChaCandidate struct {
		MaCha        string `gorm:"column:ma_cha"`
		TenDongBoWeb string `gorm:"column:ten_dong_bo_web"`
		Ten          string `gorm:"column:ten"`
	}
	var candidates []maChaCandidate
	err := db.DB.WithContext(ctx).
		Table((&models.CachedProduct{}).TableName()).
		Select("ma_cha, MAX(ten_dong_bo_web) AS ten_dong_bo_web, MAX(ten) AS ten").
		Where("tenant_id = ? AND ma_cha != ''", tenantID).
		Group("ma_cha").
		Scan(&candidates).Error
	if err != nil || len(candidates) == 0 {
		return "", fmt.Errorf("no ma_cha candidates found: %w", err)
	}

	const fuzzyCandidateCap = 1500
	if len(candidates) > fuzzyCandidateCap {
		log.Printf("[fuzzy] tenant=%s has %d ma_cha, truncating to %d for LLM prompt",
			tenantID, len(candidates), fuzzyCandidateCap)
		candidates = candidates[:fuzzyCandidateCap]
	}

	aiClient, err := getAIClient(ctx, tenantID)
	if err != nil || aiClient == nil {
		return "", fmt.Errorf("AI client not available: %w", err)
	}

	lines := make([]string, 0, len(candidates))
	for _, c := range candidates {
		webName := strings.TrimSpace(c.TenDongBoWeb)
		ten := strings.TrimSpace(c.Ten)
		label := webName
		if label == "" {
			label = ten
		}
		if label == "" {
			lines = append(lines, c.MaCha)
			continue
		}
		if webName != "" && ten != "" && !strings.EqualFold(webName, ten) {
			label = webName + " — " + ten
		}
		lines = append(lines, fmt.Sprintf("%s | %s", c.MaCha, label))
	}

	systemPrompt := fmt.Sprintf(`Bạn là trợ lý mapping mã sản phẩm BBI.

Khách hàng tìm kiếm: %q

Hãy chọn 1 MÃ SẢN PHẨM (ma_cha) khớp nhất từ danh sách bên dưới.

Quy tắc khớp:
- Khớp cả theo MÃ và theo TÊN (web/ERP) sau dấu "|".
- Bỏ dấu gạch ngang, dấu chấm, số 0 đầu khi so mã (E05 = E-5, E.5 = E-5).
- Số La Mã ↔ số Ả Rập tương đương (Storm III = Storm 3, MK II = MK 2).
- Bỏ dấu tiếng Việt khi so tên ("mu bao hiem" = "mũ bảo hiểm").
- Viết tắt thương hiệu chấp nhận được (LS2 FF818 ≈ FF818).
- TRẢ LỜI CHỈ ma_cha duy nhất, không thêm chữ, dấu câu, hay tên.
- Nếu thực sự không có dòng nào hợp lý → trả "NONE".

Danh sách (ma_cha | tên):
%s`, keyword, strings.Join(lines, "\n"))

	llmCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	resp, err := aiClient.AnalyzeChat(llmCtx, systemPrompt, keyword)
	if err != nil {
		return "", err
	}

	matched := strings.TrimSpace(resp.Content)
	matched = strings.Trim(matched, "`'\"")
	if matched == "" || strings.EqualFold(matched, "NONE") {
		return "", nil
	}

	for _, c := range candidates {
		if strings.EqualFold(c.MaCha, matched) {
			return c.MaCha, nil
		}
	}

	return "", nil
}

// extractInventoryQueryKeyword uses LLM to identify if the message is asking for stock, and extracts the keyword.
func extractInventoryQueryKeyword(ctx context.Context, tenantID, message string) (bool, string, error) {
	aiClient, err := getAIClient(ctx, tenantID)
	if err != nil {
		log.Printf("[worker] Failed to get AI client for extractInventoryQueryKeyword: %v", err)
		return false, "", nil
	}

	systemPrompt := `Bạn là trợ lý trích xuất từ khóa sản phẩm thông minh của doanh nghiệp BBI.
Nhiệm vụ của bạn là phân tích tin nhắn của khách hàng và thực hiện:
1. Xác định xem khách hàng có đang hỏi về tồn kho, còn hàng, hết hàng của một dòng sản phẩm nào đó hay không.
2. Trích xuất từ khóa sản phẩm chính xác (ví dụ: "FF800", "FF600", "áo thun trơn",...). Chỉ trích xuất từ khóa ngắn gọn, đại diện cho mã/tên sản phẩm được nhắc đến trong câu hỏi tồn kho.

Định dạng trả về duy nhất là một đối tượng JSON:
{
  "is_inventory_query": true | false,
  "keyword": "từ khóa sản phẩm hoặc rỗng"
}
CHỈ trả về JSON, không thêm bất kỳ văn bản nào khác.`

	testCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	resp, err := aiClient.AnalyzeChat(testCtx, systemPrompt, message)
	if err != nil {
		return false, "", err
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
		IsInventoryQuery bool   `json:"is_inventory_query"`
		Keyword          string `json:"keyword"`
	}
	if err := json.Unmarshal([]byte(content), &parsed); err != nil {
		return false, "", nil
	}

	return parsed.IsInventoryQuery, parsed.Keyword, nil
}

// searchProductsByWebNameFromCache finds products in Astra DB with filter on TEN_DONG_BO_WEB column
func searchProductsByWebNameFromCache(ctx context.Context, tenantID, keyword string) ([]map[string]interface{}, error) {
	var products []models.CachedProduct
	likePattern := "%" + keyword + "%"
	err := db.DB.WithContext(ctx).
		Where("tenant_id = ? AND (ten_dong_bo_web LIKE ? OR ma LIKE ? OR ten LIKE ? OR ma_cha LIKE ?)", tenantID, likePattern, likePattern, likePattern, likePattern).
		Limit(100).
		Find(&products).Error
	if err != nil {
		return nil, fmt.Errorf("local MySQL cache query failed: %w", err)
	}

	// Fallback to LLM fuzzy matching if SQL LIKE returned no results
	if len(products) == 0 {
		matchedMaCha, llmErr := fuzzyMatchMaChaWithLLM(ctx, tenantID, keyword)
		if llmErr != nil {
			log.Printf("[worker] LLM fuzzy matching failed for keyword '%s': %v", keyword, llmErr)
		} else if matchedMaCha != "" {
			log.Printf("[worker] LLM fuzzy matched keyword '%s' to ma_cha '%s'", keyword, matchedMaCha)
			// Query again using the matched parent product code
			err = db.DB.WithContext(ctx).
				Where("tenant_id = ? AND ma_cha = ?", tenantID, matchedMaCha).
				Limit(100).
				Find(&products).Error
			if err != nil {
				return nil, fmt.Errorf("local MySQL cache query (LLM fallback) failed: %w", err)
			}
		}
	}

	var results []map[string]interface{}
	for _, p := range products {
		results = append(results, map[string]interface{}{
			"MA":                 p.MA,
			"ma":                 p.MA,
			"code":               p.MA,
			"ma_hang":            p.MA,
			"product_code":       p.MA,
			"TEN_DONG_BO_WEB":    p.TEN_DONG_BO_WEB,
			"TEN":                p.TEN,
			"ten":                p.TEN,
			"ten_hang":           p.TEN,
			"THUOC_TINH_1":       p.THUOC_TINH_1,
			"THUOC_TINH_2":       p.THUOC_TINH_2,
			"DON_GIA_BAN":        p.DON_GIA_BAN,
			"LINK_ANH":           p.LINK_ANH,
			"NHAN_HIEU_NAME":     p.NHAN_HIEU_NAME,
			"LIST_TEN_NHOM_VTHH": p.LIST_TEN_NHOM_VTHH,
			"KHO":                p.KHO,
			"MA_CHA":             p.MA_CHA,
			"ma_cha":             p.MA_CHA,
			"DVT":                p.DVT,
		})
	}
	return results, nil
}

// sumInventoryByMaChaAndWebName aggregates realtime inventory by MA_CHA and optionally WEB_NAME
func sumInventoryByMaChaAndWebName(ctx context.Context, tenantID string, permCtx *engine.GroupPermissionContext, maCha string, webName string) (float64, []string, error) {
	var products []models.CachedProduct
	q := db.DB.WithContext(ctx).Where("tenant_id = ? AND ma_cha = ?", tenantID, maCha)
	if webName != "" {
		q = q.Where("ten_dong_bo_web = ?", webName)
	}
	err := q.Limit(100).Find(&products).Error
	if err != nil {
		return 0, nil, fmt.Errorf("local MySQL cache query failed: %w", err)
	}

	childProducts := make([]map[string]interface{}, len(products))
	for i, p := range products {
		childProducts[i] = map[string]interface{}{
			"MA":                 p.MA,
			"ma":                 p.MA,
			"code":               p.MA,
			"ma_hang":            p.MA,
			"product_code":       p.MA,
			"TEN_DONG_BO_WEB":    p.TEN_DONG_BO_WEB,
			"TEN":                p.TEN,
			"ten":                p.TEN,
			"ten_hang":           p.TEN,
			"THUOC_TINH_1":       p.THUOC_TINH_1,
			"THUOC_TINH_2":       p.THUOC_TINH_2,
			"DON_GIA_BAN":        p.DON_GIA_BAN,
			"LINK_ANH":           p.LINK_ANH,
			"NHAN_HIEU_NAME":     p.NHAN_HIEU_NAME,
			"LIST_TEN_NHOM_VTHH": p.LIST_TEN_NHOM_VTHH,
			"KHO":                p.KHO,
			"MA_CHA":             p.MA_CHA,
			"ma_cha":             p.MA_CHA,
			"DVT":                p.DVT,
		}
	}

	// 2. Fetch allowed product groups for "inventory" resource
	_, _, allowedProductGroups := permCtx.IsResourceAllowed("inventory")

	childCodes := make(map[string]bool)
	skuToName := make(map[string]string)
	for _, p := range childProducts {
		sku := getMapString(p, "MA", "code", "ma")
		group := getMapString(p, "LIST_TEN_NHOM_VTHH", "list_ten_nhom_vthh", "group")
		name := getMapString(p, "TEN", "ten_hang", "ten")

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
			skuLower := strings.ToLower(sku)
			childCodes[skuLower] = true
			if name != "" {
				skuToName[skuLower] = name
			} else {
				skuToName[skuLower] = sku
			}
		}
	}

	if len(childCodes) == 0 && len(childProducts) > 0 {
		return 0, nil, nil
	}

	// 3. Load ERP credentials
	cfg, _ := config.Load()
	erpURL, erpDB, erpLogin, erpPassword, err := loadCloudifyCredentials(tenantID, cfg)
	if err != nil {
		return 0, nil, err
	}

	if erpURL == "" {
		return 45.0, []string{"[Mock Variant] FF800-Mock: 45.0"}, nil
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
		return 0, nil, err
	}

	// 5. Sum stock of matching items
	var totalStock float64
	var details []string
	for _, item := range inventoryList {
		code := getMapString(item, "code", "ma_hang", "ma", "product_code")
		codeLower := strings.ToLower(code)

		isMatch := false
		if len(childCodes) > 0 {
			isMatch = childCodes[codeLower]
		} else {
			isMatch = strings.HasPrefix(codeLower, strings.ToLower(maCha))
		}

		if isMatch {
			stock := getMapFloat(item, "stock", "ton", "ton_kho")
			totalStock += stock

			displayName := skuToName[codeLower]
			if displayName == "" {
				displayName = code
			}
			details = append(details, fmt.Sprintf("- %s: %.1f", displayName, stock))
		}
	}

	return totalStock, details, nil
}

// getProductsByWebNameFromCache returns every cached product whose
// ten_dong_bo_web equals webName for the given tenant. Used by the web-name
// option flow to expand a single "product line" selection into the underlying
// MA_CHA list / variant SKUs.
func getProductsByWebNameFromCache(ctx context.Context, tenantID, webName string) ([]map[string]interface{}, error) {
	var products []models.CachedProduct
	err := db.DB.WithContext(ctx).
		Where("tenant_id = ? AND ten_dong_bo_web = ?", tenantID, webName).
		Limit(100).
		Find(&products).Error
	if err != nil {
		return nil, fmt.Errorf("local MySQL cache query (by web name) failed: %w", err)
	}

	results := make([]map[string]interface{}, 0, len(products))
	for _, p := range products {
		results = append(results, map[string]interface{}{
			"MA":                 p.MA,
			"ma":                 p.MA,
			"code":               p.MA,
			"ma_hang":            p.MA,
			"product_code":       p.MA,
			"TEN_DONG_BO_WEB":    p.TEN_DONG_BO_WEB,
			"TEN":                p.TEN,
			"ten":                p.TEN,
			"ten_hang":           p.TEN,
			"THUOC_TINH_1":       p.THUOC_TINH_1,
			"THUOC_TINH_2":       p.THUOC_TINH_2,
			"DON_GIA_BAN":        p.DON_GIA_BAN,
			"LINK_ANH":           p.LINK_ANH,
			"NHAN_HIEU_NAME":     p.NHAN_HIEU_NAME,
			"LIST_TEN_NHOM_VTHH": p.LIST_TEN_NHOM_VTHH,
			"KHO":                p.KHO,
			"MA_CHA":             p.MA_CHA,
			"ma_cha":             p.MA_CHA,
			"DVT":                p.DVT,
		})
	}
	return results, nil
}
