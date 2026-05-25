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

		// First pass: try to match by exact OA ID (Recipient ID) using ExternalID field in DB
		for i, ch := range allChannels {
			if ch.ExternalID != "" && ch.ExternalID == payload.Recipient.ID {
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

		// Second pass: fallback to decrypted OA ID match (if ExternalID in DB was empty)
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
				if creds.OAId != "" && creds.OAId == payload.Recipient.ID {
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
		if err := db.DB.Where("tenant_id = ? AND zalo_group_id = ?", matchedChannel.TenantID, payload.Recipient.ID).First(&matchedGroup).Error; err == nil {
			// Verify membership
			isMember := false
			if isWhitelisted {
				// Whitelisted internal staff are always allowed
				isMember = true
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

		// Unverified public user block:
		if !isWhitelisted && !isCustomer {
			verifyInstructions := "Tài khoản của bạn chưa được xác thực trên hệ thống CRM. Vui lòng nhắn tin theo cú pháp `verify <mã_xác_thực>` được cung cấp bởi nhân viên để đăng ký sử dụng Bot."
			_ = adapter.SendMessage(ctx, payload.Sender.ID, verifyInstructions)
			log.Printf("[worker] blocking unverified Zalo user %s for tenant %s", payload.Sender.ID, matchedChannel.TenantID)
			return nil
		}

		// Determine which Flow ID to use
		flowIDToUse := meta.LangflowPublicFlowID
		if isWhitelisted {
			flowIDToUse = meta.LangflowFlowID
			log.Printf("[worker] Routing whitelisted internal staff %s to RAG Agent Flow (%s)", payload.Sender.ID, flowIDToUse)
		} else {
			if flowIDToUse == "" {
				flowIDToUse = meta.LangflowFlowID
			}
			log.Printf("[worker] Routing customer %s (code: %s) to Public Flow (%s)", payload.Sender.ID, customerCode, flowIDToUse)
		}

		// Resolve permission context and sign JWT token
		agentType := "public"
		if isWhitelisted {
			agentType = "private"
		}
		permCtx := engine.ResolvePermissions(matchedChannel.TenantID, payload.Sender.ID, customerCode, agentType)

		// Intercept Zalo OA interactive button clicks
		// A. "Xem theo màu" button click
		if strings.HasPrefix(userText, "#xem_mau_size:") {
			parts := strings.Split(strings.TrimPrefix(userText, "#xem_mau_size:"), ":")
			maChaName := parts[0]
			if len(parts) > 1 {
				maChaName = parts[1]
			}
			reply := fmt.Sprintf("Bạn muốn xem tồn kho cụ thể của màu và size nào cho dòng sản phẩm %s?\nVui lòng nhập thông tin (Ví dụ: %s màu đỏ size L).", maChaName, maChaName)
			_ = adapter.SendMessage(ctx, payload.Sender.ID, reply)
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
				_ = adapter.SendMessage(ctx, payload.Sender.ID, "Đã có lỗi xảy ra khi tính toán tồn kho dòng sản phẩm.")
				return nil
			}

			reply := fmt.Sprintf("Tổng tồn kho của dòng sản phẩm %s (Mã: %s) trên hệ thống hiện tại là: %.1f", displayName, clickPayload.MaCha, totalStock)
			_ = adapter.SendMessage(ctx, payload.Sender.ID, reply)
			return nil
		}

		permissionToken, err := engine.SignPermissionToken(permCtx, cfg.EncryptionKey)
		if err != nil {
			log.Printf("[worker] failed to sign permission token: %v", err)
		}

		// 2. Call Langflow API (passing Zalo Sender ID as zaloUserID, customerCode, and permissionToken)
		replyText, err := langflowClient.RunFlowWithCustomer(ctx, activeSessionID, payload.Sender.ID, payload.Message.Text, meta.LangflowAPIURL, meta.LangflowAPIKey, flowIDToUse, customerCode, permissionToken)
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
