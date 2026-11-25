package workers

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
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
	"github.com/vietbui/chat-quality-agent/channels/zalotoken"
	"github.com/vietbui/chat-quality-agent/chatmemory"
	"github.com/vietbui/chat-quality-agent/config"
	"github.com/vietbui/chat-quality-agent/customercases"
	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/db/models"
	"github.com/vietbui/chat-quality-agent/engine"
	"github.com/vietbui/chat-quality-agent/pkg"
	"github.com/vietbui/chat-quality-agent/realtime"
	"gorm.io/gorm"
)

// disambiguationReplyPattern matches the short replies the Langflow agent's
// DISAMBIGUATION FOLLOW-UP rule expects to resolve against a recent option
// list: a single digit 1–9, or an SP product code (with optional 1–4 char
// alpha/underscore suffix, e.g. SP458495_TL). These would otherwise be
// classified as CASUAL by the intent LLM — "1" looks exactly like the
// throwaway acknowledgements ("ok", "dạ") the prompt teaches it to skip.
var disambiguationReplyPattern = regexp.MustCompile(`^\s*(?:[1-9]|SP\d{6}[a-zA-Z_]{0,4})\s*$`)

// productCodePattern matches model/SKU-like tokens: letters followed by digits
// (FF805, SP000123, LS2), optionally with a trailing letter suffix.
var productCodePattern = regexp.MustCompile(`(?i)(^|[^\p{L}\d])\p{L}{1,5}\d{2,7}\p{L}{0,4}([^\p{L}\d]|$)`)

// businessKeywords are lowercase phrases that only appear in ERP lookups.
var businessKeywords = []string{
	"tồn", "còn hàng", "hết hàng", "giá", "bao nhiêu tiền", "đơn hàng", "công nợ",
	"mã sp", "mã sản phẩm", "sku", "size", "màu",
}

// isObviousBusinessQuery reports whether text is unmistakably an ERP question,
// so the intent classifier can skip the LLM.
func isObviousBusinessQuery(text string) bool {
	t := strings.ToLower(strings.TrimSpace(text))
	if t == "" {
		return false
	}
	if productCodePattern.MatchString(t) {
		return true
	}
	for _, kw := range businessKeywords {
		if strings.Contains(t, kw) {
			return true
		}
	}
	return false
}

// isDisambiguationReply reports whether the given user text should bypass
// the CASUAL/HANDOVER intent classifier and be treated as IN_SCOPE because
// it is structurally a follow-up to an earlier product disambiguation list.
func isDisambiguationReply(text string) bool {
	return disambiguationReplyPattern.MatchString(text)
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
		QuoteMsgID  string                  `json:"quote_msg_id,omitempty"`
		Attachments []ZaloWebhookAttachment `json:"attachments,omitempty"`
	} `json:"message"`
	OAID string `json:"oa_id"`
	// SignatureVerified: the webhook handler checked Zalo's signature on
	// this event. Always overwritten by the handler, never read from Zalo.
	SignatureVerified bool `json:"cqa_signature_verified,omitempty"`
}

// complaintTurnID is the turn id for a Zalo message, stable across retries
// of the same task: from the msg_id, or without one from the sender, chat,
// timestamp and content.
func complaintTurnID(channelID string, p ZaloWebhookPayload) string {
	key := channelID + ":" + strings.TrimSpace(p.Message.MsgID)
	if strings.TrimSpace(p.Message.MsgID) == "" {
		key = channelID + "|" + p.Sender.ID + "|" + p.Recipient.ID + "|" + p.Timestamp + "|" + p.EventName + "|" + p.Message.Text
		for _, a := range p.Message.Attachments {
			key += "|" + a.Payload.URL
		}
	}
	sum := sha256.Sum256([]byte(key))
	return "m" + hex.EncodeToString(sum[:])[:35]
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
	// Quiet closes the session without a message (group assistant mode,
	// where the bot never announces sessions).
	Quiet bool `json:"quiet,omitempty"`
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
	// GroupAssistant makes the bot a quiet member of customer groups: no
	// trigger word, it reads the whole group and answers only when needed.
	// See group_assistant.go.
	GroupAssistant bool `json:"group_assistant_mode"`

	HandoverAlertGroupID string `json:"handover_alert_group_id"`
	// Backend rollout gate for BBI OA; admin group routing uses the existing field.
	UnansweredHandoverEnabled bool `json:"unanswered_handover_enabled"`
	// GroupStaffCallOnly: in a customer group a staff message reaches the bot
	// only when it calls the bot or the staff member is already talking with
	// it. Unset means on. See group_staff.go.
	GroupStaffCallOnly *bool `json:"group_staff_call_only"`
	// GroupBotNames are the names staff call the bot by in a group,
	// separated by ";". Empty means "bot".
	GroupBotNames string `json:"group_bot_names"`

	LangflowAPIURL       string `json:"langflow_api_url"`
	LangflowAPIKey       string `json:"langflow_api_key"`
	LangflowFlowID       string `json:"langflow_flow_id"`
	LangflowPublicFlowID string `json:"langflow_public_flow_id"`
	SystemPrompt         string `json:"system_prompt"`
	SystemPromptInternal string `json:"system_prompt_internal"`

	AstraDBAPIEndpoint string `json:"astradb_api_endpoint"`
	AstraDBToken       string `json:"astradb_token"`
	AstraDBKeyspace    string `json:"astradb_keyspace"`
	AstraDBCollection  string `json:"astradb_collection"`
}

// selectSystemPrompt returns the flow-appropriate system prompt. The private
// staff flow gets the internal (ALL-scope) prompt; everything else gets the
// public (OWN-scope) prompt. An empty internal prompt intentionally returns ""
// so the private flow falls back to its own SYSTEM_PROMPT global — it must NOT
// fall back to the public prompt, whose "never ask the customer to identify
// themselves" rule is wrong for an internal bot that looks up other customers.
func selectSystemPrompt(agentType, publicPrompt, internalPrompt string) string {
	if agentType == "private" {
		return internalPrompt
	}
	return publicPrompt
}

const (
	botUnavailableMessage   = "Dạ hệ thống tra cứu đang bận nên em chưa trả lời được câu này. Anh/chị vui lòng nhắn lại sau ít phút giúp em ạ."
	botEmptyAnswerMessage   = "Dạ em chưa có đủ thông tin để xác nhận câu này. Anh/chị có thể hỏi em câu khác hoặc liên hệ nhân viên phụ trách để được hỗ trợ ạ."
	botOutOfScopeMessage    = "Dạ em chưa thể hỗ trợ câu hỏi này. Anh/chị có thể hỏi em câu khác nhé."
	stockUnconfirmedMessage = "Dạ em chưa xác nhận được tồn khả dụng của mẫu này trong dữ liệu em có thể tra cứu. Anh/chị có thể hỏi nhân viên phụ trách để được kiểm tra thêm ạ."
	// caseRecordedReply replaces a reply that was nothing but a ticket number.
	// It promises nothing: whether staff were told is not known here.
	caseRecordedReply = "Dạ em đã nhận được thông tin của anh/chị ạ."

	handoverStaffNotifiedMessage = "Dạ, em xin lỗi về trải nghiệm không tốt của mình ạ. Em đã báo các bạn nhân viên admin liên hệ trực tiếp hỗ trợ mình ngay nhé ạ!"
	handoverNoStaffMessage       = "Dạ, em xin lỗi về trải nghiệm không tốt của mình ạ. Anh/chị vui lòng nhắn trực tiếp cho nhân viên phụ trách để được hỗ trợ ngay giúp em nhé ạ!"
)

var errNoVisibleStock = errors.New("no eligible stock SKUs in scope")

func stockLookupFailureMessage(err error) string {
	if errors.Is(err, errNoVisibleStock) {
		return stockUnconfirmedMessage
	}
	return botUnavailableMessage
}

const langflowUnansweredInstruction = `QUY TẮC TRẢ LỜI KHI THIẾU DỮ LIỆU
Áp dụng các quy tắc dưới đây nếu hướng dẫn khác trong prompt có nội dung mâu thuẫn. Trả lời khách bằng ngôn ngữ họ đang dùng, dùng tiếng Việt cho hội thoại tiếng Việt.
- Tra giá, mã sản phẩm và tồn kho bằng ERP. Chỉ dùng tồn khả dụng tại Kho Tổng để trả lời về tồn hoặc còn/hết hàng. Với câu hỏi nối tiếp, giữ đúng sản phẩm, màu và size đã nêu. Tồn khả dụng lớn hơn 0 xác nhận còn hàng; 0 hoặc âm xác nhận hiện không có hàng khả dụng. Kết quả rỗng hoặc thiếu trường tồn chưa xác nhận được. Nếu khách hỏi riêng tồn vật lý thì nói rõ công cụ đang tra tồn khả dụng.
- Khi công cụ sản phẩm hoặc tồn kho của ERP trả lookup_outcome=no_visible_match, hoặc trả data rỗng với truy vấn có tên/mã và không có lựa chọn hay thông báo đã gửi qua Zalo, kết thúc tra cứu mục đó. Diễn đạt customer_message theo điều khách hỏi, hoặc nói ngắn gọn rằng em chưa xác nhận được từ dữ liệu hiện có và có thể hướng khách hỏi nhân viên phụ trách. Không kết luận sản phẩm không tồn tại, sai mã hay hết hàng. Không yêu cầu khách gửi lại mã, ảnh, màu hoặc size để giải quyết kết quả rỗng này. Không gọi search_knowledge, Astra, SQL hay nguồn khác để tìm lại thông tin ERP cho mục đó.
- Khi tra sản phẩm, chỉ hỏi bổ sung nếu còn thiếu thông tin nhận diện hoặc ERP trả lựa chọn hợp lệ cần khách chọn. Tiếp tục xử lý các câu hỏi khác trong cùng tin nhắn theo đúng nguồn của từng câu hỏi. Chỉ nói về giới hạn quyền khi công cụ trả lỗi từ chối quyền rõ ràng. Nếu công cụ báo lỗi tạm thời, nói chưa tra được và mời thử lại sau.`

// Some saved flow prompts still return this standalone fallback. Do not let
// older prompt wording reach Zalo as an English error in a Vietnamese bot.
func normalizeLangflowReply(reply string) string {
	const legacyNoSources = "No relevant supporting sources were found for that request"
	if strings.EqualFold(strings.TrimSuffix(strings.TrimSpace(reply), "."), legacyNoSources) {
		return botEmptyAnswerMessage
	}
	return reply
}

// sendChatReply sends text into the chat the message came from: the Zalo group
// when there is one, otherwise the 1:1 thread.
func sendChatReply(ctx context.Context, adapter *channels.ZaloOAAdapter, zaloGroupID, zaloUserID, text string) error {
	if zaloGroupID != "" {
		return adapter.SendGroupMessage(ctx, zaloGroupID, text)
	}
	return adapter.SendMessage(ctx, zaloUserID, text)
}

// isLastAttempt reports whether this asynq run is the final one (no retries
// left). Outside asynq (tests) it returns true so callers never stay silent.
func isLastAttempt(ctx context.Context) bool {
	retried, ok1 := asynq.GetRetryCount(ctx)
	maxRetry, ok2 := asynq.GetMaxRetry(ctx)
	if !ok1 || !ok2 {
		return true
	}
	return retried >= maxRetry
}

// quietGroupCasualTurn reports whether a turn is small talk inside a Zalo
// group, which the bot ignores instead of warning and ending the session.
func quietGroupCasualTurn(intent, zaloGroupID string) bool {
	return intent == "CASUAL" && zaloGroupID != ""
}

// loadTenantSettings fetches the requested app settings for a tenant in a
// single query, keyed by setting_key. Missing keys are simply absent.
func loadTenantSettings(tenantID string, keys ...string) map[string]models.AppSetting {
	out := make(map[string]models.AppSetting, len(keys))
	if db.DB == nil || len(keys) == 0 {
		return out
	}
	var rows []models.AppSetting
	if err := db.DB.Where("tenant_id = ? AND setting_key IN ?", tenantID, keys).Find(&rows).Error; err != nil {
		log.Printf("[worker] failed to load tenant settings for %s: %v", tenantID, err)
		return out
	}
	for _, r := range rows {
		out[r.SettingKey] = r
	}
	return out
}

// erpAgentTokenFor returns the tenant's ERP agent token for the given bot type
// ("public" or "private"). The private bot falls back to the legacy single
// token, which the ERP gateway also treats as private. Empty when unset.
func erpAgentTokenFor(tenantID, agentType string) string {
	key := "ai_agent_erp_token_" + agentType
	keys := []string{key}
	if agentType == "private" {
		keys = append(keys, "ai_agent_erp_token")
	}
	settings := loadTenantSettings(tenantID, keys...)
	for _, k := range keys {
		if v := strings.TrimSpace(settings[k].ValuePlain); v != "" {
			return v
		}
	}
	return ""
}

// HandleZaloWebhookTask processes the webhook task
func HandleZaloWebhookTask(cfg *config.Config, langflowClient *engine.LangflowClient, lookupOrderDelivery CurrentOrderDeliveryLookup) asynq.HandlerFunc {
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

		// 1b. An archived CRM group is out of service: the bot stays silent in it
		// until the group is restored (its Zalo group may still be alive).
		if resolvedGroup == nil && payload.Recipient.ID != "" && crmGroupArchived(payload.Recipient.ID) {
			log.Printf("[worker] Zalo group %s belongs to an archived CRM group; ignoring message from %s", payload.Recipient.ID, payload.Sender.ID)
			return nil
		}

		// Determine the OA ID from the payload (either Recipient.ID for direct messages, or OAID for group messages)
		oaID := payload.Recipient.ID
		if payload.OAID != "" {
			oaID = payload.OAID
		}

		// Single pass over the active channels, decrypting each credential blob
		// at most once (was up to three decrypt passes per message). Priority:
		// ExternalID == OA ID, then decrypted OAId == OA ID, then AppID.
		if matchedChannel == nil {
			const (
				byExternalID = iota + 1
				byOAID
				byAppID
			)
			bestRank := 0
			for i, ch := range allChannels {
				rank := 0
				if oaID != "" && ch.ExternalID != "" && ch.ExternalID == oaID {
					rank = byExternalID
				}
				credBytes, err := pkg.Decrypt(ch.CredentialsEncrypted, cfg.EncryptionKey)
				if err != nil {
					continue
				}
				var creds channels.ZaloOACredentials
				if err := json.Unmarshal(credBytes, &creds); err != nil {
					continue
				}
				if rank == 0 {
					switch {
					case oaID != "" && creds.OAId != "" && creds.OAId == oaID:
						rank = byOAID
					case creds.AppID != "" && creds.AppID == payload.AppID:
						rank = byAppID
					}
				}
				if rank != 0 && (bestRank == 0 || rank < bestRank) {
					bestRank = rank
					matchedChannel = &allChannels[i]
					zaloCreds = creds
					if rank == byExternalID {
						break
					}
				}
			}
		}

		if matchedChannel == nil {
			log.Printf("[worker] no active channel found for OA/Group %s (OAID: %s) or App %s", payload.Recipient.ID, payload.OAID, payload.AppID)
			return asynq.SkipRetry // Skip retry if channel not found
		}

		// Setup Zalo adapter for replies
		adapter := zalotoken.NewAdapter(matchedChannel.ID, zaloCreds, cfg.EncryptionKey)
		// Order confirmations are received even when conversational auto-reply
		// is disabled. Only signed messages from linked customers can write them.
		handled, err := captureOrderConfirmation(ctx, cfg, payload, matchedChannel, resolvedGroup, zaloCreds, adapter)
		if err != nil {
			log.Printf("[cloudify-orders] could not record confirmation for tenant %s: %v", matchedChannel.TenantID, err)
		}
		if handled {
			return enqueueZaloOrderScreenshot(ctx, asynqClient, matchedChannel, resolvedGroup, payload.Message.MsgID)
		}

		userText := strings.TrimSpace(payload.Message.Text)
		attachmentText := parseAttachmentText(payload.Message.Attachments)
		if attachmentText != "" {
			if userText == "" {
				userText = strings.TrimSpace(attachmentText)
			} else {
				userText = userText + "\n" + strings.TrimSpace(attachmentText)
			}
		}

		// Pairing ("verify <token>") must work whether or not the chatbot is switched on:
		// staff/customers link their Zalo account before any auto-reply is enabled, so
		// this runs before the auto-reply gate below.
		// 1. Check for verification token command: "verify <token>"
		if strings.HasPrefix(strings.ToLower(userText), "verify ") {
			token := strings.TrimSpace(userText[7:])
			if token != "" && verifyAttemptsExceeded(ctx, matchedChannel.TenantID, payload.Sender.ID) {
				log.Printf("[verify] too many wrong codes from Zalo user %s (tenant %s); ignoring this attempt", payload.Sender.ID, matchedChannel.TenantID)
				_ = adapter.SendMessage(ctx, payload.Sender.ID, verifyLockedMessage)
				return nil
			}
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
						// Let open admin screens flip "pending" to "linked" without a reload.
						realtime.Publish(ctx, matchedChannel.TenantID, realtime.EventZaloWhitelistUpdated)
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
						// A code links ONE Zalo account. Without this check anyone who
						// got hold of an approved customer's code could send it and take
						// over that customer's identity (orders, debt, profile).
						if customerRec.ZaloUserID != "" && customerRec.ZaloUserID != payload.Sender.ID {
							recordVerifyFailure(ctx, matchedChannel.TenantID, payload.Sender.ID)
							log.Printf("[verify] refused: customer %s is already linked to another Zalo account (attempt from %s)", customerRec.ID, payload.Sender.ID)
							_ = adapter.SendMessage(ctx, payload.Sender.ID, verifyAlreadyLinkedMessage)
							return nil
						}
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
						// Single use: the code that was just spent must not link again.
						customerRec.VerifyToken = pkg.NewVerifyToken()
						customerRec.UpdatedAt = time.Now()

						if err := db.DB.Save(&customerRec).Error; err == nil {
							if customerRec.Status == "approved" {
								successMsg := "Xác thực thành công! Tài khoản Zalo của bạn đã được liên kết với hệ thống CRM."
								_ = adapter.SendMessage(ctx, payload.Sender.ID, successMsg)

								// Auto-invite customer to Zalo GMF group chats they are already pre-added to locally
								var groupLinks []models.CRMGroupCustomer
								if err := db.DB.Where("zalo_customer_id = ? AND group_id IN (?)", customerRec.ID, db.ActiveCRMGroupIDs()).Find(&groupLinks).Error; err == nil {
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
							log.Printf("[worker] successfully processed customer verification (customer: %s, previous status: %s, current status: %s) for Zalo user %s", customerRec.ID, statusWas, customerRec.Status, payload.Sender.ID)
							return nil
						}
					}

					// Token invalid or expired
					recordVerifyFailure(ctx, matchedChannel.TenantID, payload.Sender.ID)
					errorMsg := "Mã xác thực không hợp lệ hoặc đã hết hạn. Vui lòng kiểm tra lại."
					_ = adapter.SendMessage(ctx, payload.Sender.ID, errorMsg)
					return nil
				}
			}
		}

		// Load every tenant setting this turn needs in ONE query instead of one
		// round-trip per key (was 7 sequential SELECTs on every message).
		tenantSettings := loadTenantSettings(matchedChannel.TenantID,
			"chatbot_active",
			"ai_engine_langflow_url",
			"ai_engine_langflow_token",
			"ai_engine_langflow_flow_id",
			"ai_engine_langflow_public_flow_id",
			"ai_engine_system_prompt",
			"ai_engine_system_prompt_internal",
		)

		// Check if chatbot is active (defaults to true if setting is not found)
		chatbotActive := true
		if activeSetting, ok := tenantSettings["chatbot_active"]; ok {
			chatbotActive = (activeSetting.ValuePlain == "true")
		}

		// Auto-reply requires BOTH the tenant master switch (chatbot_active) AND the
		// per-channel flag. AutoReplyEnabled comes from the already-loaded channel (no extra query).
		if !chatbotActive || !matchedChannel.AutoReplyEnabled {
			log.Printf("[zalo-drop] reason=auto_reply_off tenant=%s master=%v channel=%s auto_reply=%v sender=%s",
				matchedChannel.TenantID, chatbotActive, matchedChannel.ID, matchedChannel.AutoReplyEnabled, payload.Sender.ID)
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
		// Langflow connection: AI Engine settings → legacy channel metadata → .env,
		// resolved per field with its source (see engine.ResolveLangflowConfig).
		// lfTenantCfg is the override-free config used to self-heal when a stale
		// channel override is rejected by Langflow.
		lfCfg := engine.ResolveLangflowConfig(engine.LangflowOverrides{
			APIURL:       meta.LangflowAPIURL,
			APIKey:       meta.LangflowAPIKey,
			FlowID:       meta.LangflowFlowID,
			PublicFlowID: meta.LangflowPublicFlowID,
		}, tenantSettings, cfg)
		lfTenantCfg := engine.ResolveTenantLangflowConfig(tenantSettings, cfg)

		// System prompt is optional and per-tenant only — an empty value means
		// "use the SYSTEM_PROMPT global variable configured inside Langflow",
		// so there is no env/global fallback here.
		if meta.SystemPrompt == "" {
			if setting, ok := tenantSettings["ai_engine_system_prompt"]; ok && setting.ValuePlain != "" {
				meta.SystemPrompt = setting.ValuePlain
			}
		}
		// Internal/private flow system prompt. Same per-tenant, DB-only, no-fallback
		// semantics as the public prompt above. An empty value means "use the
		// private flow's own SYSTEM_PROMPT global" — it deliberately does NOT fall
		// back to the public prompt, whose OWN-scope "never ask for identity" rule
		// is wrong for an internal ALL-scope staff bot.
		if meta.SystemPromptInternal == "" {
			if setting, ok := tenantSettings["ai_engine_system_prompt_internal"]; ok && setting.ValuePlain != "" {
				meta.SystemPromptInternal = setting.ValuePlain
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

		// 2. Determine Whitelist / Staff / Customer Routing:
		// Check if the current sender is active in the Zalo whitelist for this tenant and channel.
		isWhitelisted := false
		var whitelistRec models.ZaloWhitelist
		if err := db.DB.Where("tenant_id = ? AND (channel_id = ? OR channel_id = '' OR channel_id IS NULL) AND zalo_user_id = ? AND status = ?", matchedChannel.TenantID, matchedChannel.ID, payload.Sender.ID, "active").First(&whitelistRec).Error; err == nil {
			isWhitelisted = true
		}

		isCustomer := false
		customerCode := ""
		// customerCodes carries the full set of mã KH for owners of multiple shops
		// (single-store customers resolve to exactly one). It is signed into the
		// permission token so scope="own" can aggregate debt/orders across shops.
		var customerCodes []string
		var customerRec models.ZaloCustomer
		if err := db.DB.Where("tenant_id = ? AND zalo_user_id = ? AND status = ?", matchedChannel.TenantID, payload.Sender.ID, "approved").First(&customerRec).Error; err == nil {
			isCustomer = true
			customerCode = customerRec.CustomerCode
			customerCodes = db.GetZaloCustomerCodes(matchedChannel.TenantID, customerRec.ID, customerRec.CustomerCode)
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

		if matchedGroup.ZaloGroupID == "" && (payload.EventName == "user_send_group_text" || payload.EventName == "user_send_group_image") {
			matchedGroup.ZaloGroupID = payload.Recipient.ID
		}

		// groupMember: the sender is a CRM member of this group (staff assigned
		// to it, or one of its customers).
		groupMember := false
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

			groupMember = isMember
			if isMember && matchedGroup.CustomerCode != "" {
				// Group context narrows to the group's shop(s): a group serving
				// multiple mã KH overrides the personal set with the group's full set.
				customerCode = matchedGroup.CustomerCode
				customerCodes = db.GetGroupCustomerCodes(matchedChannel.TenantID, []string{matchedGroup.ID})
				if len(customerCodes) == 0 {
					customerCodes = []string{matchedGroup.CustomerCode}
				}
				log.Printf("[worker] GMF group chat detected: %s (ID: %s). Overriding customerCode to %s (codes=%v) for sender %s", matchedGroup.Name, matchedGroup.ZaloGroupID, customerCode, customerCodes, payload.Sender.ID)
			} else if !isMember {
				log.Printf("[worker] Zalo user %s is not a member of GMF group %s (ID: %s). Skipping override.", payload.Sender.ID, matchedGroup.Name, matchedGroup.ZaloGroupID)
			}
		}

		// sessionKey is the ACTIVATION/base key: it stores the active session UUID
		// and drives the idle-timeout. It stays group-level for group chats so one
		// trigger word activates the bot for the whole group.
		sessionKey := fmt.Sprintf("zalo_session:%s:%s", matchedChannel.ID, payload.Sender.ID)
		if matchedGroup.ZaloGroupID != "" {
			sessionKey = fmt.Sprintf("zalo_session:%s:group:%s", matchedChannel.ID, matchedGroup.ZaloGroupID)
		}

		// workKey scopes a turn's FLOW STATE (the processing lock, pending_options,
		// and the awaiting_* sidecars) to the individual sender inside a group when
		// per-sender group sessions are enabled — so two employees never share a
		// pending menu/picker or block each other. It is built by the SAME function
		// the ERP handler uses (engine.BuildSessionKey), so the two ends can never
		// disagree on the exact key. For 1:1 chats, and for groups when the feature
		// is off, workKey == sessionKey (byte-identical to the previous behaviour).
		workKey := engine.BuildSessionKey(matchedChannel.ID, payload.Sender.ID, matchedGroup.ZaloGroupID)

		// Group assistant mode: the bot reads the whole customer group and
		// answers only when needed (group_assistant.go).
		groupAssistant := groupAssistantOn(meta, matchedGroup.ZaloGroupID, hasGroup, matchedGroup.CustomerCode)

		// Concurrency control: sequential processing per lock key using a safe,
		// owner-token Redis lock (engine.AcquireSessionLock / ReleaseSessionLock).
		// The lock TTL (engine.SessionLockTTL) comfortably exceeds the Langflow
		// timeout so it never lapses mid-flight, and release is compare-and-del so
		// a holder can only ever release its own lock — never one a later worker
		// acquired after a TTL lapse. Locking on workKey means different senders in
		// a group run in parallel (when the feature is on) instead of serialising.
		// lockKey/lockToken: this turn's session lock (empty without Redis).
		var lockKey, lockToken string
		if db.RedisClient != nil {
			lockKey = workKey + ":lock"
			acquired := false
			// Try to acquire the lock every 250ms for up to 30 seconds.
			for i := 0; i < 120; i++ {
				if token, ok := engine.AcquireSessionLock(ctx, lockKey); ok {
					lockToken = token
					acquired = true
					break
				}
				time.Sleep(250 * time.Millisecond)
			}
			if !acquired {
				return fmt.Errorf("timeout waiting for session lock for key %s", sessionKey)
			}
			defer engine.ReleaseSessionLock(ctx, lockKey, lockToken)
		}
		// turnStart is when this turn took the session lock; the small-talk
		// call is bounded from it so a main-bot fallback still fits.
		turnStart := time.Now()

		// Display name of the sender for chat memory.
		memSenderName := ""
		if isCustomer {
			memSenderName = customerRec.Name
		} else if isWhitelisted {
			memSenderName = whitelistRec.Name
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

				// Open session and generate unique session ID. Defensively wipe any
				// sidecar flow-state left over from a prior conversation: the base
				// session key is stable across open/close cycles, so an unclean
				// teardown (crash, Redis hiccup) could otherwise resume a stale
				// debt/order/menu state on this fresh session's first real turn.
				teardownSession(ctx, sessionKey, matchedGroup.ZaloGroupID)
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
						Quiet:          groupAssistant,
					})
					if err == nil {
						_, _ = asynqClient.Enqueue(timeoutTask, asynq.ProcessIn(time.Duration(meta.SessionTimeout)*time.Minute))
					}
				}
				// Record the trigger message; the welcome sent with this ctx
				// lands in the same chat-memory session.
				ctx = startChatMemoryTurn(ctx, chatMemoryTurn(matchedChannel.TenantID, matchedChannel.ID, newSessionID,
					payload.Sender.ID, matchedGroup.ZaloGroupID, matchedGroup.Name, memSenderName, customerCode,
					isWhitelisted && matchedGroup.ZaloGroupID == ""), payload, userText, memSenderName)
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
				if staffCallOnlyOn(meta, matchedGroup.ZaloGroupID, hasGroup, matchedGroup.CustomerCode, groupRoleFor(isWhitelisted, isCustomer)) {
					markStaffEngaged(ctx, sessionKey, payload.Sender.ID)
				}
				log.Printf("[worker] opened new session %s for key %s", newSessionID, sessionKey)
				return nil
			}
			if !groupAssistant {
				// Ignore message
				log.Printf("[zalo-drop] reason=no_session sender=%s key=%s (send the session keyword first)", payload.Sender.ID, sessionKey)
				return nil
			}
			// Group assistant mode: any message opens the session silently and
			// is handled in this same turn. The keep-alive below schedules the
			// (quiet) idle timeout.
			teardownSession(ctx, sessionKey, matchedGroup.ZaloGroupID)
			activeSessionID = uuid.New().String()
			if db.RedisClient != nil {
				db.RedisClient.Set(ctx, sessionKey, activeSessionID, time.Duration(meta.SessionTimeout)*time.Minute+time.Minute)
			}
			log.Printf("[group-assistant] opened session %s for group %s", activeSessionID, matchedGroup.ZaloGroupID)
		}

		sessionTTL := time.Duration(meta.SessionTimeout)*time.Minute + time.Minute
		groupName, groupRole := groupSpeaker(isWhitelisted, isCustomer, whitelistRec.Name, customerRec.Name)
		// Staff in a customer group mostly talk to the customer; their
		// messages reach the bot only when they call it (group_staff.go).
		staffGated := staffCallOnlyOn(meta, matchedGroup.ZaloGroupID, hasGroup, matchedGroup.CustomerCode, groupRole)

		// Verified-only interaction (design intent): the trigger-time gate above
		// only blocks unverified senders at session OPEN. Because a group session
		// is group-level (one member's trigger word activates the bot for the whole
		// group), without this check any UNVERIFIED participant whose message lands
		// while a groupmate's session is active would fall straight through to
		// Langflow — interacting with the bot, burning AI spend, and surfacing as a
		// raw Zalo ID in the per-employee cost chart. Enforce verify on every turn:
		// a sender who is neither whitelisted staff nor an approved customer is
		// ignored (no Langflow call, no cost, no usage log). Silent in groups to
		// avoid spamming verified members with repeated verify DMs.
		if !isWhitelisted && !isCustomer {
			if groupAssistant {
				// Not answered, but part of the group's conversation.
				rememberGroupLine(ctx, sessionKey, newGroupLine(groupName, groupRole, userText), true, sessionTTL)
			}
			log.Printf("[zalo-drop] reason=unverified_sender sender=%s tenant=%s (active session, not whitelisted/customer)", payload.Sender.ID, matchedChannel.TenantID)
			return nil
		}

		// Images: the tenant's AI model reads what the customer sent, and the
		// rest of the turn (classifier, bot, chat memory) sees it as text.
		var images []inboundImage
		if urls := imageURLs(payload.Message.Attachments); len(urls) > 0 {
			var archive func(context.Context, string, ai.ImageInput) (string, error)
			if payload.SignatureVerified && chatmemory.Enabled() {
				turnID := complaintTurnID(matchedChannel.ID, payload)
				archive = func(ctx context.Context, sourceURL string, img ai.ImageInput) (string, error) {
					return customercases.StageImage(ctx, matchedChannel.TenantID, matchedChannel.ID,
						payload.Sender.ID, turnID, sourceURL, img.MediaType, img.Data)
				}
			}
			images = readInboundImages(ctx, matchedChannel.TenantID, matchedChannel.ID, urls, strings.TrimSpace(payload.Message.Text), archive)
			// Reading images can take up to imageReadTimeout of the lock's
			// lease; renew it so the classifier and Langflow call that follow
			// still finish under the lock.
			if lockToken != "" {
				if held, err := engine.ExtendSessionLock(ctx, lockKey, lockToken); !held {
					// Nothing is recorded yet: retry the whole message later
					// rather than run without the lock.
					return fmt.Errorf("session lock not renewed after reading images (sender=%s): %v", payload.Sender.ID, err)
				}
			}
			if text := imagesText(images); text != "" {
				if userText == "" {
					userText = text
				} else {
					userText += "\n" + text
				}
			}
		}

		// Record this message in the session's chat memory (MySQL). Replies
		// sent with ctx from here on, including the goodbye, are recorded too.
		memTurn := chatMemoryTurn(matchedChannel.TenantID, matchedChannel.ID, activeSessionID,
			payload.Sender.ID, matchedGroup.ZaloGroupID, matchedGroup.Name, memSenderName, customerCode,
			isWhitelisted && matchedGroup.ZaloGroupID == "")
		ctx = startChatMemoryTurn(ctx, memTurn, payload, userText, memSenderName, imageAttachments(images)...)

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
			teardownSession(ctx, sessionKey, matchedGroup.ZaloGroupID)
			if staffGated {
				clearStaffEngaged(ctx, sessionKey, payload.Sender.ID)
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
			db.RedisClient.Expire(ctx, sessionKey, sessionTTL)
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
				Quiet:          groupAssistant,
			})
			if err == nil {
				_, _ = asynqClient.Enqueue(timeoutTask, asynq.ProcessIn(time.Duration(meta.SessionTimeout)*time.Minute))
			}
		}

		// Legacy raw history in Astra, read by the flow's history retriever.
		// Off by default: the flow's own history store already writes each
		// turn, and chat memory keeps the full transcript in MySQL.
		if cfg.AstraRawChatHistoryWrite {
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
		}

		// A pending order draft waits for this sender's "1"/"2". Settle it here,
		// before the intent classifier, which would read "ok" as small talk.
		if handled, err := handleOrderDraftReply(ctx, cfg, adapter, matchedChannel.TenantID, workKey, payload.Sender.ID, matchedGroup.ZaloGroupID, userText); handled {
			// Confirming or cancelling an order is business: end any
			// small-talk run (casual_budget.go).
			if casualBudgetActive(ctx) {
				engine.ResetCasualStreak(ctx, workKey)
			}
			return err
		}

		// Real-time intent classification for active sessions. Skip it when the
		// previous turn was a backend-pushed prompt awaiting this reply: the
		// answer ("tháng này", "quý 2", "3 ngày", a tapped option) is ordinary
		// short text the classifier could mislabel CASUAL and skip. The marker
		// is single-use, so only this continuation turn bypasses classification.
		// classified keeps the classifier's own label: the open-cart override
		// below routes small talk to the agent as IN_SCOPE, but small talk
		// must still not consume the variant-line lock further down.
		var intent, classified string
		// Read at most once and keep keyword-group history scoped to the
		// sender. Group assistant classification uses its own shared log.
		msgID, sessionID, input := payload.Message.MsgID, activeSessionID, userText
		history := onceTurns(func() []chatmemory.RecentTurn {
			if matchedGroup.ZaloGroupID != "" {
				if !meta.UnansweredHandoverEnabled {
					return nil
				}
				return chatmemory.RecentTurnsForSender(ctx, sessionID, msgID, input, payload.Sender.ID, 0)
			}
			return chatmemory.RecentTurns(ctx, sessionID, msgID, input, 0)
		})
		staffTurnKind := staffTurnUngated
		if staffGated {
			staffTurnKind = classifyStaffTurn(ctx, meta, matchedChannel.TenantID, sessionKey, workKey, payload.Sender.ID, userText)
		}
		publicChat := !isWhitelisted || matchedGroup.ZaloGroupID != ""
		orderPeriodKey := "current_order_period:" + matchedChannel.ID + ":" + activeSessionID + ":" + payload.Sender.ID
		orderDays := orderFollowupDays(input)
		orderPeriod := lookupOrderDelivery != nil && meta.UnansweredHandoverEnabled && payload.SignatureVerified &&
			publicChat && orderDays > 0 && hasOrderPeriodChoice(ctx, orderPeriodKey)
		awaiting := engine.TakeAwaitingFollowup(ctx, workKey)
		if (awaiting && !meta.UnansweredHandoverEnabled) ||
			(staffTurnKind != staffTurnNotForBot && (orderPeriod || (awaiting && isExpectedLookupFollowup(userText)))) {
			log.Printf("[worker] awaiting-followup marker set; treating reply as IN_SCOPE (bypassing intent classifier)")
			intent, classified = "IN_SCOPE", "IN_SCOPE"
		} else if staffTurnKind == staffTurnCalled {
			// A staff member called the bot by name: answer without asking
			// the classifier, which could read the call as group chatter.
			log.Printf("[group-staff] group %s staff %s called the bot", matchedGroup.ZaloGroupID, payload.Sender.ID)
			intent, classified = "IN_SCOPE", "IN_SCOPE"
		} else if staffTurnKind == staffTurnNotForBot {
			// Staff talking to the customer: not for the bot. No classifier
			// call, so no AI cost; the group log below still keeps the line.
			log.Printf("[group-staff] group %s staff %s did not call the bot; staying silent", matchedGroup.ZaloGroupID, payload.Sender.ID)
			intent, classified = "CASUAL", "CASUAL"
		} else if groupAssistant {
			intent = classifyGroupMessage(ctx, matchedChannel.TenantID, workKey, recentGroupLines(ctx, sessionKey), groupName, groupRole, userText, meta.UnansweredHandoverEnabled)
			log.Printf("[group-assistant] group %s sender %s: %s", matchedGroup.ZaloGroupID, payload.Sender.ID, intent)
			classified = intent
			if (!meta.UnansweredHandoverEnabled || intent != "OUT_OF_SCOPE") && keepOrderConversation(ctx, matchedChannel.TenantID, workKey, intent) {
				log.Printf("[worker] chat has an open order cart; treating %s reply as IN_SCOPE", intent)
				intent = "IN_SCOPE"
			}
		} else {
			var err error
			intent, err = classifyMessageIntent(ctx, matchedChannel.TenantID, userText, history, meta.UnansweredHandoverEnabled)
			if err != nil {
				log.Printf("[worker] error classifying message intent: %v. Proceeding as IN_SCOPE.", err)
				intent = "IN_SCOPE"
			}
			classified = intent
			if (!meta.UnansweredHandoverEnabled || intent != "OUT_OF_SCOPE") && keepOrderConversation(ctx, matchedChannel.TenantID, workKey, intent) {
				log.Printf("[worker] chat has an open order cart; treating %s reply as IN_SCOPE", intent)
				intent = "IN_SCOPE"
			}
		}

		// Complaint cases: a complaint goes to the bot, which records it as a
		// case (complaint_record) and answers the customer itself. The guide
		// rides on the AI Engine system prompt, so without one the legacy
		// handover path stays.
		caseAgentType := "public"
		if isWhitelisted && matchedGroup.ZaloGroupID == "" {
			caseAgentType = "private"
		}
		// Only a webhook with a verified Zalo signature may write cases:
		// otherwise anyone who knows the URL could file them as any customer.
		complaintCases := customercases.EnabledFor(matchedChannel.Metadata) && payload.SignatureVerified &&
			strings.TrimSpace(selectSystemPrompt(caseAgentType, meta.SystemPrompt, meta.SystemPromptInternal)) != ""
		// A sender in a group chat who is not a CRM member of it (a customer
		// in someone else's group) has their own cases, not the group's.
		groupGuest := matchedGroup.ZaloGroupID != "" && !groupMember && !isWhitelisted
		caseSubjectType, caseSubjectID := memTurn.Subject()
		if groupGuest {
			caseSubjectType, caseSubjectID = "user", payload.Sender.ID
		}
		// turnID ties the complaint reports the bot makes in this turn (via
		// the signed token) to this turn. It comes from the Zalo message id,
		// so a retried message is the same turn and does not report twice.
		turnID := complaintTurnID(matchedChannel.ID, payload)
		complaintHandover := intent == "HANDOVER"
		if complaintCases {
			handover := complaintHandover
			if handover {
				log.Printf("[cases] HANDOVER from %s goes to the bot to record a case", payload.Sender.ID)
				intent = "IN_SCOPE"
			}
			alert := engine.HandoverAlert{
				TenantID:   matchedChannel.TenantID,
				Channel:    *matchedChannel,
				ZaloUserID: payload.Sender.ID,
				ChatGroup:  matchedGroup.Name,
				Message:    userText,
			}
			if isCustomer {
				alert.CustomerName = customerRec.Name
				alert.CustomerCode = customerCode
			}
			// This turn's own alert, not deduplicated against the user's
			// earlier handover alerts.
			alert.DedupeKey = "turn:" + turnID
			// Staff must hear about this turn's complaint: if the bot did not
			// record one (on a HANDOVER turn), or recorded one whose alert did
			// not go out, the handover alert goes instead.
			defer func() {
				bg := context.WithoutCancel(ctx)
				recorded, notified := customercases.TurnCaseState(bg, matchedChannel.TenantID, turnID)
				if notified || (!recorded && !handover) {
					return
				}
				log.Printf("[cases] complaint turn from %s reached no staff (recorded=%t); sending the handover alert", payload.Sender.ID, recorded)
				engine.NotifyHandover(bg, adapter, alert)
			}()
		}

		unansweredEnabled := meta.UnansweredHandoverEnabled && payload.SignatureVerified && !complaintHandover && publicChat
		directOrder := lookupOrderDelivery != nil && unansweredEnabled && intent == "IN_SCOPE" &&
			(orderPeriod || isCurrentOrderDeliveryTurn(input, history))
		if groupAssistant {
			// Every message joins the group log the classifier reads. Messages
			// that will not reach Langflow as a question are also queued for
			// it, so the next answer knows what the group said meanwhile.
			rememberGroupLine(ctx, sessionKey, newGroupLine(groupName, groupRole, userText), intent != "IN_SCOPE" || directOrder, sessionTTL)
		}

		if intent == "HANDOVER" {
			log.Printf("[worker] message classified as HANDOVER. Closing session and sending handover message.")
			teardownSession(ctx, sessionKey, matchedGroup.ZaloGroupID)
			// Tell staff before promising the customer that staff will call.
			// Without a configured alert group nobody would be told, so the
			// customer gets an honest reply instead of an empty promise.
			alert := engine.HandoverAlert{
				TenantID:   matchedChannel.TenantID,
				Channel:    *matchedChannel,
				ZaloUserID: payload.Sender.ID,
				ChatGroup:  matchedGroup.Name,
				Message:    userText,
			}
			if isCustomer {
				alert.CustomerName = customerRec.Name
				alert.CustomerCode = customerCode
			}
			handoverMsg := handoverNoStaffMessage
			if engine.NotifyHandover(ctx, adapter, alert) {
				handoverMsg = handoverStaffNotifiedMessage
			}
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

		if quietGroupCasualTurn(intent, matchedGroup.ZaloGroupID) {
			// In a customer group the owner and staff also talk to each other
			// ("dạ", "ok anh", "chiều giao nhé"). Answering that chatter floods
			// the group, so the bot stays silent and leaves closing to the idle
			// timeout or end keyword.
			log.Printf("[worker] group %s: ignoring small talk from %s without closing the session", matchedGroup.ZaloGroupID, payload.Sender.ID)
			return nil
		}
		// In a 1:1 chat small talk goes to the agent like any other turn. The
		// agent sees the whole conversation, so it can answer "cảm ơn" in a
		// word and treat a short "đúng rồi" as the yes to its own question,
		// which a canned refusal from a classifier that reads one message
		// cannot do. How it answers small talk is up to the system prompt.
		// A long run of small talk is answered more cheaply (casual_budget.go).
		// Only a task's first attempt applies the budget: a retry answers as
		// before this feature, so it can never skew or wipe a newer run.
		if matchedGroup.ZaloGroupID == "" && casualBudgetActive(ctx) {
			switch intent {
			case "IN_SCOPE", "OUT_OF_SCOPE":
				engine.ResetCasualStreak(ctx, workKey)
			case "CASUAL":
				turnID := casualTurnID(ctx, payload.Message.MsgID)
				streak := engine.BumpCasualStreak(ctx, workKey, turnID, meta.SessionTimeout)
				budget := casualBudgetFrom(matchedChannel.Metadata)
				route := routeCasualTurn(streak, budget)
				if route == casualToAgent {
					break
				}
				reply, err := replyToCasualTurn(ctx, matchedChannel.TenantID, history(), userText, isWhitelisted,
					casualModeFor(route, streak, budget), casualCallDeadline(turnStart, time.Now()))
				if err == nil && reply.Result.HasUsage {
					casualAgentType := "public"
					if isWhitelisted {
						casualAgentType = "private"
					}
					go logChatbotAIUsage(matchedChannel.TenantID, matchedChannel.ID, payload.Sender.ID, whitelistRec.Name, casualAgentType, "", reply.Result, reply.Prompt, reply.Text)
				}
				switch {
				case err != nil:
					// No answer (no provider, timeout, no time left): the main
					// bot answers, in quiet mode too, so a business turn the
					// classifier mislabelled is never dropped unchecked.
					log.Printf("[casual-budget] sender %s: small-talk call failed on turn %d (%v); using the main bot", payload.Sender.ID, streak, err)
				case reply.Outcome == casualHandoff:
					// The second look says this needs the main bot after all
					// (a lookup, a reply to the bot's question): treat it as
					// business from here on, including the variant-line lock.
					log.Printf("[casual-budget] sender %s: small-talk model handed turn %d to the main bot", payload.Sender.ID, streak)
					engine.ResetCasualStreak(ctx, workKey)
					intent, classified = "IN_SCOPE", "IN_SCOPE"
				case route == casualToQuiet && reply.Outcome == casualQuietAck:
					log.Printf("[casual-budget] sender %s: small-talk turn %d in a row (quiet after %d); no reply", payload.Sender.ID, streak, budget.QuietAfter)
					return nil
				case route == casualToLite && reply.Outcome == casualSend:
					log.Printf("[casual-budget] sender %s: small-talk turn %d in a row answered with the short prompt", payload.Sender.ID, streak)
					// Always as plain text: never let model output become a
					// Zalo message template.
					if err := adapter.SendTextMessage(ctx, payload.Sender.ID, reply.Text); err != nil {
						return fmt.Errorf("failed to send small-talk reply to Zalo: %w", err)
					}
					return nil
				default:
					// Unusable or off-format answer: the main bot answers,
					// still as small talk (no variant-line lock, no reset).
					log.Printf("[casual-budget] sender %s: small-talk model answer unusable on turn %d (outcome %d); using the main bot", payload.Sender.ID, streak, reply.Outcome)
				}
			}
		}

		if intent == "OUT_OF_SCOPE" {
			if err := sendChatReply(ctx, adapter, matchedGroup.ZaloGroupID, payload.Sender.ID, botOutOfScopeMessage); err != nil {
				return fmt.Errorf("failed to send out-of-scope reply to Zalo: %w", err)
			}
			return nil
		}

		requestUnansweredHelp := func() string {
			// Keep internal alerts out of chatbot history. This adapter still
			// coordinates token rotation through the same channel store.
			staffAdapter := zalotoken.NewAdapter(matchedChannel.ID, zaloCreds, cfg.EncryptionKey)
			staffAdapter.SetSentObserver(nil)
			question := strings.TrimSpace(payload.Message.Text)
			if question == "" {
				question = "Tin nhắn có tệp đính kèm."
			}
			alert := engine.HandoverAlert{
				TenantID: matchedChannel.TenantID, Channel: *matchedChannel,
				ZaloUserID: payload.Sender.ID, ChatGroup: matchedGroup.Name, Message: question,
			}
			if isCustomer {
				alert.CustomerName = customerRec.Name
				alert.CustomerCode = customerCode
			}
			return unansweredCustomerReply(engine.NotifyUnansweredHandover(ctx, staffAdapter, *matchedChannel,
				meta.HandoverAlertGroupID, matchedGroup.ZaloGroupID, turnID, alert))
		}
		sendStockFailure := func(err error) error {
			reply := stockLookupFailureMessage(err)
			if unansweredEnabled && errors.Is(err, errNoVisibleStock) {
				reply = requestUnansweredHelp()
			}
			return sendChatReply(ctx, adapter, matchedGroup.ZaloGroupID, payload.Sender.ID, reply)
		}

		if staffGated && intent == "IN_SCOPE" {
			// The bot is answering this staff member: their next messages may
			// be replies to it, so the classifier reads them for a while.
			markStaffEngaged(ctx, sessionKey, payload.Sender.ID)
		}

		// Determine which flow to run: private (internal staff) only for a
		// whitelisted sender in a 1:1 chat; public/group flow otherwise.
		flowAgentType := "public"
		if isWhitelisted && matchedGroup.ZaloGroupID == "" {
			flowAgentType = "private"
		}
		flowIDToUse, flowSource := lfCfg.FlowFor(flowAgentType)
		log.Printf("[worker] routing sender=%s agent=%s code=%s flow=%s (flow_source=%s key_source=%s)",
			payload.Sender.ID, flowAgentType, customerCode, flowIDToUse, flowSource, lfCfg.APIKeySource)

		// Resolve permission context and sign JWT token
		agentType := "public"
		// Only set agentType to "private" if the user is whitelisted AND we are NOT in a GMF group chat context
		if isWhitelisted && matchedGroup.ZaloGroupID == "" {
			agentType = "private"
		}

		// Each flow gets its own system prompt: the internal/ALL-scope prompt for
		// the private staff flow, the public/OWN-scope prompt otherwise. Empty maps
		// to the flow's own SYSTEM_PROMPT global (see selectSystemPrompt).
		systemPromptToUse := selectSystemPrompt(agentType, meta.SystemPrompt, meta.SystemPromptInternal)
		systemPromptToUse = appendChatMemoryContext(ctx, cfg, matchedChannel.Metadata, systemPromptToUse, memTurn)
		if groupAssistant {
			systemPromptToUse = appendGroupAssistantContext(matchedChannel.TenantID, systemPromptToUse)
		}

		// A customer who sits in someone else's Zalo group without being a CRM
		// member of it keeps their own permissions. The group's permissions
		// would let the ERP fall back to the group's customer codes when the
		// sender has none, i.e. show them the group owner's orders and debt.
		permGroupID := matchedGroup.ID
		if hasGroup && !groupMember && isCustomer {
			permGroupID = ""
		}
		permCtx := engine.ResolvePermissionsWithGroup(matchedChannel.TenantID, payload.Sender.ID, customerCode, agentType, permGroupID)
		// Carry the full mã KH set (owners of multiple shops) into the signed token
		// so scope="own" aggregates across shops. CustomerCode remains the primary.
		permCtx.CustomerCodes = customerCodes
		// Pin the exact chat so ERP-side pushed prompts go out through this OA
		// and into this conversation (group or 1:1), and land under workKey.
		permCtx.ChannelID = matchedChannel.ID
		permCtx.ZaloGroupID = matchedGroup.ZaloGroupID
		permCtx.GroupGuest = groupGuest
		permCtx.TurnID = turnID
		permCtx.Complaints = complaintCases
		if unansweredEnabled && intent == "IN_SCOPE" && classified != "OUT_OF_SCOPE" {
			permCtx.UnansweredLookupID = uuid.NewString()
		}
		if n, ok := asynq.GetRetryCount(ctx); ok && n > 0 {
			permCtx.Retry = true
		}
		systemPromptToUse = appendOrderCartContext(ctx, matchedChannel.TenantID, workKey, &permCtx, systemPromptToUse)
		if complaintCases {
			// A guest's reply goes to the whole group: their own cases (and
			// staff updates on them) stay out of the prompt.
			systemPromptToUse = appendComplaintContext(ctx, matchedChannel.TenantID, matchedChannel.ID, caseSubjectType, caseSubjectID, customerCode, !groupGuest, systemPromptToUse)
		}
		if systemPromptToUse != "" {
			systemPromptToUse += "\n\n" + langflowUnansweredInstruction
			if meta.UnansweredHandoverEnabled && agentType == "public" {
				systemPromptToUse += "\n\n" + publicOrderLookupInstruction
			}
			if permCtx.UnansweredLookupID != "" {
				systemPromptToUse += "\n\n" + unansweredHandoverInstruction
			}
		}

		// Resolve a bare-number reply ("1"/"2"/"3") against the most recently
		// presented numbered menu (inventory ten_dong_bo_web options). On a valid
		// pick, rewrite userText to the stored postback and fall through to the
		// button-intercept handlers below — no Langflow round-trip. An out-of-range
		// number re-prompts while keeping the menu so the user can pick again.
		if db.RedisClient != nil {
			pendingKey := workKey + engine.PendingOptionsSuffix
			if raw, err := db.RedisClient.Get(ctx, pendingKey).Result(); err == nil && raw != "" {
				var pendingOptions []string
				if jsonErr := json.Unmarshal([]byte(raw), &pendingOptions); jsonErr == nil {
					if postback, matched, inRange := engine.ResolveNumericSelection(userText, pendingOptions); matched {
						if inRange {
							db.RedisClient.Del(ctx, pendingKey)
							log.Printf("[stock-pick] numeric reply %q resolved to postback %q (workKey=%s, %d pending options)", userText, postback, workKey, len(pendingOptions))
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
			allowed, _, groups := permCtx.IsResourceAllowed("inventory")
			if !allowed {
				return sendChatReply(ctx, adapter, matchedGroup.ZaloGroupID, payload.Sender.ID, "Tài khoản của anh/chị chưa có quyền xem tồn kho.")
			}
			keyword := strings.TrimPrefix(userText, "#choose_flow_type:dongsp:")
			matchedProducts, errSearch := searchProductsByWebNameFromCache(ctx, matchedChannel.TenantID, keyword)
			if errSearch != nil {
				return sendChatReply(ctx, adapter, matchedGroup.ZaloGroupID, payload.Sender.ID, botUnavailableMessage)
			}
			brands, allBrands := permCtx.ResolveBrandFilter("inventory")
			visible := matchedProducts[:0]
			for _, product := range matchedProducts {
				group := getMapString(product, "LIST_TEN_NHOM_VTHH", "list_ten_nhom_vthh")
				brand := getMapString(product, "NHAN_HIEU_NAME", "nhan_hieu_name")
				if engine.WarehouseProductAllowed(group, brand, groups, brands, allBrands) {
					visible = append(visible, product)
				}
			}
			if len(visible) == 0 {
				return sendStockFailure(errNoVisibleStock)
			}

			// Group the matched products by TEN_DONG_BO_WEB (synced web name);
			// fall back to MA_CHA when a product has no web name so legacy SKUs
			// still appear.
			webGroups := engine.RankProductWebGroups(visible)

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
				engine.StorePendingOptions(ctx, workKey, buttons, meta.SessionTimeout)
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
			// Lock the chosen line so the follow-up color/size free-text turn is
			// scoped to exactly this line. Without the lock the Agent re-derives a
			// bare model code from prose history ("FF901") that spans sibling lines
			// ("LS2 FF901" vs "LS2 FF901 Carbon") and answers for both. Consumed
			// (single-use) on the next free-text turn before Langflow (see below).
			engine.StoreAwaitingVariantLine(ctx, workKey, keyword, meta.SessionTimeout)
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

		// Stock disambiguation pick (deterministic, no Agent round-trip). When the
		// customer picks a product line from the `products` web-groups list by
		// number, the numeric-reply intercept above rewrote "1"/"2" to
		// #stockpick_web:<web_name>. Present the EXACT-web dòng-vs-SKU picker
		// (mirrors inventory Branch-0) so the follow-up sums by the exact web name
		// — it never re-LIKEs ("LS2 FF901" vs "LS2 FF901 Carbon") and never
		// re-enters Langflow. Fixes the prefix-collision loop + duplicate prose.
		if strings.HasPrefix(userText, engine.StockPickWebPrefix) {
			pickIntent, webName := engine.ParseStockPickWeb(userText)

			// Intent continuation: if the question that produced this list was a
			// PRICE ask, the customer wants a price for the line they just picked —
			// not the dòng-vs-SKU stock picker. Answer the exact-web price range
			// directly (deterministic, no Langflow round-trip) and stop. Any other
			// or ambiguous intent falls through to the stock picker, matching the
			// system prompt's "ambiguous → stock" default. The intent travels
			// inside the postback (baked at list-build time from the Agent's
			// classification), so the pick replays it verbatim — no keyword guess.
			if pickIntent == engine.StockPickIntentPrice {
				if priceReply, priceErr := priceRangeReplyByWebName(ctx, matchedChannel.TenantID, &permCtx, webName); priceErr == nil && priceReply != "" {
					var sendErr error
					if matchedGroup.ZaloGroupID != "" {
						sendErr = adapter.SendGroupMessage(ctx, matchedGroup.ZaloGroupID, priceReply)
					} else {
						sendErr = adapter.SendMessage(ctx, payload.Sender.ID, priceReply)
					}
					if sendErr != nil {
						log.Printf("[worker] failed to send stockpick_web price reply: %v", sendErr)
					}
					return nil
				}
				// Price lookup failed or returned nothing → fall through to the stock
				// picker so the customer still gets a usable next step, not silence.
				log.Printf("[worker] stockpick_web PRICE intent but no price for %q; falling back to stock picker", webName)
			}

			prompt, buttons := engine.BuildExactWebStockPicker(webName)
			text := channels.BuildButtonOptionsAsText(prompt, buttons)
			engine.StorePendingOptions(ctx, workKey, buttons, meta.SessionTimeout)
			var sendErr error
			if matchedGroup.ZaloGroupID != "" {
				sendErr = adapter.SendGroupMessage(ctx, matchedGroup.ZaloGroupID, text)
			} else {
				sendErr = adapter.SendMessage(ctx, payload.Sender.ID, text)
			}
			if sendErr != nil {
				log.Printf("[worker] failed to send stockpick_web picker: %v", sendErr)
			}
			return nil
		}

		// 1. Parent Code Options Selection (#show_macha_options:<MA_CHA>)
		if strings.HasPrefix(userText, "#show_macha_options:") {
			maCha := strings.TrimPrefix(userText, "#show_macha_options:")
			allowed, _, groups := permCtx.IsResourceAllowed("inventory")
			if !allowed {
				return sendChatReply(ctx, adapter, matchedGroup.ZaloGroupID, payload.Sender.ID, "Tài khoản của anh/chị chưa có quyền xem tồn kho.")
			}
			brands, allBrands := permCtx.ResolveBrandFilter("inventory")
			// Fetch variants
			childProducts, err := getProductsByMaChaFromCache(ctx, matchedChannel.TenantID, maCha)
			if err != nil {
				return sendStockFailure(err)
			}

			// Extract unique web sync names
			webNamesMap := make(map[string]bool)
			var webNames []string
			for _, p := range childProducts {
				group := getMapString(p, "LIST_TEN_NHOM_VTHH", "list_ten_nhom_vthh", "group")
				brand := getMapString(p, "NHAN_HIEU_NAME", "nhan_hieu_name")
				if !engine.WarehouseProductAllowed(group, brand, groups, brands, allBrands) {
					continue
				}
				wName := getMapString(p, "TEN_DONG_BO_WEB", "ten_dong_bo_web")
				if wName != "" && !webNamesMap[wName] {
					webNamesMap[wName] = true
					webNames = append(webNames, wName)
				}
			}

			if len(webNames) == 0 {
				// No web names found, perform direct realtime inventory lookup for this MA_CHA
				stock, details, err := sumInventoryByParentWithDKCopy(ctx, matchedChannel.TenantID, &permCtx, maCha)
				if err != nil {
					return sendStockFailure(err)
				}
				reply := fmt.Sprintf("Tồn khả dụng dòng %s tại Kho Tổng: %g\n\nChi tiết:\n%s", maCha, stock, strings.Join(details, "\n"))
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
					return sendStockFailure(err)
				}
				reply := fmt.Sprintf("Tồn khả dụng của %s tại Kho Tổng: %g\n\nChi tiết:\n%s", webNames[0], stock, strings.Join(details, "\n"))
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
		// Resolve the selected web name to its parent codes, then fetch
		// inventory for all eligible SKUs belonging to those parents.
		if strings.HasPrefix(userText, "#show_macha_options_by_web:") {
			if allowed, _, _ := permCtx.IsResourceAllowed("inventory"); !allowed {
				reply := "Tài khoản của anh/chị chưa có quyền xem tồn kho."
				if matchedGroup.ZaloGroupID != "" {
					_ = adapter.SendGroupMessage(ctx, matchedGroup.ZaloGroupID, reply)
				} else {
					_ = adapter.SendMessage(ctx, payload.Sender.ID, reply)
				}
				return nil
			}
			webName := strings.TrimPrefix(userText, "#show_macha_options_by_web:")
			log.Printf("[stock-pick] entering inventory-by-web for web=%q (tenant=%s)", webName, matchedChannel.TenantID)
			var maChas []string
			err := db.DB.WithContext(ctx).Model(&models.CachedProduct{}).
				Where("tenant_id = ? AND ten_dong_bo_web = ? AND ma_cha <> ''", matchedChannel.TenantID, webName).
				Distinct().Pluck("ma_cha", &maChas).Error
			if err != nil {
				log.Printf("[stock-pick] failed to resolve parent codes for web=%q: %v", webName, err)
				return sendStockFailure(err)
			}

			stockCtx, cancel := context.WithTimeout(ctx, engine.WarehouseAvailabilityTimeout)
			defer cancel()
			var totalStock float64
			var details []string
			var okCount, errCount int
			for _, mc := range maChas {
				stock, lines, sumErr := sumInventoryByMaChaAndWebName(stockCtx, matchedChannel.TenantID, &permCtx, mc, "")
				if errors.Is(sumErr, errNoVisibleStock) {
					continue
				}
				if sumErr != nil {
					errCount++
					log.Printf("[zalo_webhook] inventory error ma_cha=%s web=%s: %v", mc, webName, sumErr)
					continue
				}
				okCount++
				totalStock += stock
				details = append(details, lines...)
			}

			// A read error can hide eligible stock. Parents with no eligible
			// SKUs do not contribute to the scoped total.
			if errCount > 0 {
				return sendChatReply(ctx, adapter, matchedGroup.ZaloGroupID, payload.Sender.ID, botUnavailableMessage)
			}
			if okCount == 0 {
				return sendStockFailure(errNoVisibleStock)
			}

			log.Printf("[stock-pick] inventory-by-web web=%q done: %d ma_cha ok, %d failed, total=%.1f, %d detail lines", webName, okCount, errCount, totalStock, len(details))
			// Concise customer reply: chỉ tổng tồn cả dòng + gợi ý hỏi tiếp theo
			// màu/size. Chi tiết từng biến thể (details) chỉ dùng cho log nội bộ —
			// không đẩy lên Zalo để tránh tin nhắn dài bị từ chối.
			reply := fmt.Sprintf("Tồn khả dụng của dòng %s tại Kho Tổng là %g. Anh/chị cần tồn theo màu/size cụ thể nào thì nhắn em nhé.", webName, totalStock)
			var sendErr error
			if matchedGroup.ZaloGroupID != "" {
				sendErr = adapter.SendGroupMessage(ctx, matchedGroup.ZaloGroupID, reply)
			} else {
				sendErr = adapter.SendMessage(ctx, payload.Sender.ID, reply)
			}
			if sendErr != nil {
				log.Printf("[stock-pick] FAILED to deliver inventory-by-web reply web=%q (%d chars): %v", webName, len(reply), sendErr)
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

			stock, _, err := sumInventoryByMaChaAndWebName(ctx, matchedChannel.TenantID, &permCtx, parsed.MaCha, parsed.WebName)
			if err != nil {
				return sendStockFailure(err)
			}

			// Concise reply: chỉ tổng tồn cả dòng, bỏ liệt kê biến thể (tránh tin dài).
			reply := fmt.Sprintf("Tồn khả dụng của dòng %s tại Kho Tổng là %g. Anh/chị cần tồn theo màu/size cụ thể nào thì nhắn em nhé.", parsed.WebName, stock)
			var sendErr error
			if matchedGroup.ZaloGroupID != "" {
				sendErr = adapter.SendGroupMessage(ctx, matchedGroup.ZaloGroupID, reply)
			} else {
				sendErr = adapter.SendMessage(ctx, payload.Sender.ID, reply)
			}
			if sendErr != nil {
				log.Printf("[stock-pick] FAILED to deliver check_stock_webname reply web=%q ma_cha=%q (%d chars): %v", parsed.WebName, parsed.MaCha, len(reply), sendErr)
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
				if !errors.Is(err, errNoVisibleStock) {
					log.Printf("[worker] error summing inventory: %v", err)
				}
				return sendStockFailure(err)
			}

			reply := fmt.Sprintf("Tồn khả dụng của dòng %s (mã %s) tại Kho Tổng là %g.", displayName, clickPayload.MaCha, totalStock)
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
			maCha := engine.RealParentOf(strings.TrimPrefix(userText, "#show_product_variants:"))
			childProducts, err := getProductsByMaChaFromCache(ctx, matchedChannel.TenantID, maCha, engine.ExcludeDKCopies)
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

		// STOCK-pick continuation lock. The customer chose "🔍 xem theo mã SKU
		// cụ thể" for a specific line at the skucuthe step, then typed color/size
		// as free text. That reply matches no #postback above and reaches Langflow.
		// Inject the locked line as a hard input constraint so the Agent scopes
		// resolution to the EXACT line the customer picked (exact_web_name=true)
		// instead of re-deriving a bare model code that spans sibling lines.
		// Single-use: consumed here so it never leaks into unrelated later turns.
		// Small talk ("cảm ơn" before the color and size) leaves it for the
		// actual answer.
		if !strings.HasPrefix(userText, "#") && classified != "CASUAL" {
			if lockedLine := engine.TakeAwaitingVariantLine(ctx, workKey); lockedLine != "" {
				log.Printf("[stock-pick] variant-line lock active: scoping %q to chosen line %q (workKey=%s)", userText, lockedLine, workKey)
				userText = fmt.Sprintf("%s [DÒNG ĐÃ CHỌN: %s — chỉ tra tồn kho đúng dòng này bằng exact_web_name=true; KHÔNG mở rộng sang dòng khác]", userText, lockedLine)
			}
		}

		permissionToken, err := engine.SignPermissionToken(permCtx, cfg.EncryptionKey)
		if err != nil {
			log.Printf("[worker] failed to sign permission token: %v", err)
		}

		// Per-sender Langflow memory: in a group, scope the session_id to the sender
		// so each employee's short-term chat memory stays separate (the history
		// retriever already filters by zalo_user_id; this isolates Langflow's
		// built-in chat memory too). With the feature off, this is the unchanged
		// group/1:1 session UUID.
		langflowSessionID := activeSessionID
		if matchedGroup.ZaloGroupID != "" && engine.PerSenderGroupSessions() && !groupAssistant {
			langflowSessionID = activeSessionID + ":" + payload.Sender.ID
		}

		// Group assistant mode: one Langflow conversation per group. The input
		// carries the group messages Langflow has not seen and who is asking.
		langflowInput := userText
		var unseenGroup []groupLine
		var unseenGroupRaw []string
		if groupAssistant {
			unseenGroup, unseenGroupRaw = unseenGroupLines(ctx, sessionKey)
			langflowInput = groupAssistantInput(unseenGroup, groupName, groupRole, userText)
		}

		// Drop any stale prompt-delivery receipt so this turn's [RICH_MESSAGE_SENT]
		// is judged only on what the ERP handler did during THIS Langflow call.
		engine.ClearPromptDelivery(ctx, workKey)

		// The ERP MCP server authenticates the bot with the agent token that
		// matches this flow (public vs private) and scopes data with the signed
		// permission token; both reach Langflow as request-scoped global variables.
		erpAgentToken := erpAgentTokenFor(matchedChannel.TenantID, agentType)

		// A clear customer delivery question goes through the same signed ERP
		// lookup as Langflow before any date picker or model-generated answer.
		if directOrder {
			reply := "Dạ, anh/chị nhắn riêng cho OA để em kiểm tra đơn hàng của mình nhé."
			if matchedGroup.ZaloGroupID == "" || (groupMember && matchedGroup.CustomerCode != "") {
				if !orderPeriod {
					orderDays = 0
				}
				var unresolved bool
				var lookupErr error
				reply, unresolved, lookupErr = lookupOrderDelivery(ctx, matchedChannel.TenantID, erpAgentToken, permissionToken, orderDays)
				if lookupErr != nil {
					log.Printf("[worker] scoped order delivery lookup failed for channel %s: %v", matchedChannel.ID, lookupErr)
					reply = botUnavailableMessage
				} else {
					if orderDays == 0 {
						rememberOrderPeriodChoice(ctx, orderPeriodKey, sessionTTL)
					}
					if unresolved {
						reply += "\n" + requestUnansweredHelp()
					}
				}
			}
			if err := sendChatReply(ctx, adapter, matchedGroup.ZaloGroupID, payload.Sender.ID, reply); err != nil {
				return fmt.Errorf("failed to send order delivery reply to Zalo: %w", err)
			}
			if groupAssistant {
				rememberGroupLine(ctx, sessionKey, newGroupLine("Bot", groupRoleBot, reply), true, sessionTTL)
			}
			return nil
		}

		// 2. Call Langflow API (passing Zalo Sender ID as zaloUserID, customerCode, and permissionToken)
		lfRes, err := langflowClient.RunFlowWithCustomerUsage(ctx, langflowSessionID, payload.Sender.ID, langflowInput, lfCfg.APIURL, lfCfg.APIKey, flowIDToUse, customerCode, permissionToken, erpAgentToken, systemPromptToUse)
		// Self-heal: a per-channel Langflow override that Langflow rejects (bad
		// key / deleted flow) must not take the whole bot down while the AI
		// Engine settings are fine. Retry once with the tenant config.
		if err != nil && engine.IsLangflowConfigError(err) && lfCfg.UsesChannelOverride() && !lfCfg.Equivalent(lfTenantCfg, flowAgentType) {
			fallbackFlow, fallbackSource := lfTenantCfg.FlowFor(flowAgentType)
			log.Printf("[worker] WARNING channel %s Langflow override rejected (%v); falling back to AI Engine config flow=%s (source=%s). Clear the Langflow fields on this channel to silence this.",
				matchedChannel.ID, err, fallbackFlow, fallbackSource)
			if fallbackFlow != "" && lfTenantCfg.APIURL != "" {
				lfRes, err = langflowClient.RunFlowWithCustomerUsage(ctx, langflowSessionID, payload.Sender.ID, langflowInput, lfTenantCfg.APIURL, lfTenantCfg.APIKey, fallbackFlow, customerCode, permissionToken, erpAgentToken, systemPromptToUse)
				flowIDToUse, flowSource = fallbackFlow, fallbackSource
				lfCfg = lfTenantCfg
			}
		}
		if err != nil {
			// Retry transient failures, but never leave the customer in silence:
			// on the last attempt tell them the bot could not answer.
			// Permanent failures get the fallback immediately.
			if isLastAttempt(ctx) || engine.IsPermanentLangflowError(err) {
				log.Printf("[zalo-drop] reason=langflow_error sender=%s flow=%s flow_source=%s key_source=%s err=%v", payload.Sender.ID, flowIDToUse, flowSource, lfCfg.APIKeySource, err)
				if sendErr := sendChatReply(ctx, adapter, matchedGroup.ZaloGroupID, payload.Sender.ID, botUnavailableMessage); sendErr != nil {
					log.Printf("[worker] failed to send langflow-error fallback: %v", sendErr)
				}
				return fmt.Errorf("langflow error: %w: %w", err, asynq.SkipRetry)
			}
			log.Printf("[worker] langflow error (will retry) sender=%s flow=%s: %v", payload.Sender.ID, flowIDToUse, err)
			return fmt.Errorf("langflow error: %w", err)
		}
		replyText := normalizeLangflowReply(lfRes.Text)
		if groupAssistant {
			markGroupLinesSeen(ctx, sessionKey, unseenGroupRaw)
		}

		// Record AI spend for this chatbot turn (non-blocking). Tokens come from
		// the Langflow response when present, else are estimated from text length.
		// matchedGroup.ID is "" for 1:1 chats; whitelistRec.Name is "" for customers.
		go logChatbotAIUsage(matchedChannel.TenantID, matchedChannel.ID, payload.Sender.ID, whitelistRec.Name, agentType, matchedGroup.ID, lfRes, systemPromptToUse+langflowInput, lfRes.Text)

		if strings.TrimSpace(replyText) == "" {
			log.Printf("[zalo-drop] reason=langflow_empty sender=%s flow=%s", payload.Sender.ID, flowIDToUse)
			if sendErr := sendChatReply(ctx, adapter, matchedGroup.ZaloGroupID, payload.Sender.ID, botEmptyAnswerMessage); sendErr != nil {
				log.Printf("[worker] failed to send empty-answer fallback: %v", sendErr)
			}
			return nil
		}

		// The Agent returns the [RICH_MESSAGE_SENT] sentinel when the backend
		// already pushed a Zalo rich/disambiguation message during a tool call
		// (orders/debt date-range prompts, inventory dòng-vs-SKU). Suppress the
		// Agent's prose so the customer doesn't receive a duplicate — often
		// hallucinated — answer alongside the backend-pushed prompt.
		action := isUnansweredHandoverAction(lfRes.Text)
		rich := strings.Contains(replyText, "[RICH_MESSAGE_SENT]")
		if rich || action {
			// The Agent says "sent" whether or not the ERP handler's push to Zalo
			// actually succeeded. Check the handler's receipt: if the push failed,
			// deliver the prompt ourselves through the channel we already hold.
			delivered, undelivered, known := engine.TakePromptDelivery(ctx, workKey)
			if rich || known {
				switch {
				case delivered:
					log.Printf("[worker] suppressing Langflow reply: backend already sent rich message (sentinel, receipt ok)")
				case undelivered != "":
					log.Printf("[worker] sentinel but backend push FAILED; resending prompt via worker channel (sender=%s)", payload.Sender.ID)
					if sendErr := sendChatReply(ctx, adapter, matchedGroup.ZaloGroupID, payload.Sender.ID, undelivered); sendErr != nil {
						return fmt.Errorf("failed to resend undelivered prompt to Zalo: %w", sendErr)
					}
				case !known:
					log.Printf("[worker] suppressing Langflow reply on sentinel without delivery receipt (legacy path?) sender=%s", payload.Sender.ID)
				}
				// The backend just pushed a prompt expecting the customer's next
				// message as the answer (debt period, orders date range, inventory
				// dòng-vs-SKU). Mark the session so that reply — which arrives as
				// ordinary short text — bypasses the CASUAL classifier once instead
				// of being dropped as small talk.
				engine.StoreAwaitingFollowup(ctx, workKey, meta.SessionTimeout)
				return nil
			}
		}

		if !action && strings.Contains(replyText, unansweredHandoverAction) {
			replyText = botEmptyAnswerMessage
		}
		if action {
			replyText = botEmptyAnswerMessage
			if engine.HasUnansweredLookup(ctx, &permCtx) {
				recorded := false
				if complaintCases {
					recorded, _ = customercases.TurnCaseState(ctx, matchedChannel.TenantID, turnID)
				}
				if recorded {
					replyText = caseRecordedReply
				} else {
					replyText = requestUnansweredHelp()
				}
			}
		}

		// Customers are never given a ticket or case number, whatever the
		// model wrote (see customercases.StripRefs).
		if complaintCases {
			if cleaned := customercases.StripRefs(replyText); cleaned != replyText {
				log.Printf("[cases] removed a case reference from the reply to %s", payload.Sender.ID)
				replyText = cleaned
				if strings.TrimSpace(replyText) == "" {
					replyText = caseRecordedReply
				}
			}
		}

		// Legacy raw history in Astra (see the user message above). The reply
		// is recorded in chat memory by the adapter when it is sent.
		if cfg.AstraRawChatHistoryWrite {
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
		}

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

		if groupAssistant {
			rememberGroupLine(ctx, sessionKey, newGroupLine("Bot", groupRoleBot, replyText), false, sessionTTL)
		}
		log.Printf("[worker] successfully replied to user %s via Langflow", payload.Sender.ID)
		return nil
	}
}

// teardownSession clears a finished session's Redis state. When per-sender group
// sessions are enabled, a group's flow-state sidecars live under
// "...:group:<gid>:user:<sender>", so a single ClearSessionState(base) would
// miss every member's sidecars; wipe the whole group instead (the base
// activation key is group-level, so ending it ends the session for everyone).
// For 1:1 chats — and whenever the feature is off — this falls back to the exact
// ClearSessionState(sessionKey), i.e. today's behaviour byte-for-byte.
func teardownSession(ctx context.Context, sessionKey, zaloGroupID string) error {
	if zaloGroupID != "" && engine.PerSenderGroupSessions() {
		_, err := engine.ClearGroupSessionState(ctx, zaloGroupID)
		if err != nil {
			log.Printf("[worker] failed to clear group session state for group %s: %v", zaloGroupID, err)
		}
		return err
	}
	err := engine.ClearSessionState(ctx, sessionKey)
	if err != nil {
		log.Printf("[worker] failed to clear session state for key %s: %v", sessionKey, err)
	}
	return err
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

		// Close the session: delete the session key AND every sidecar flow-state
		// key so an idle timeout leaves nothing for the next conversation to
		// resume (matches the end-keyword / HANDOVER / CASUAL teardown). For a
		// per-sender group session the per-user sidecars live off the per-user
		// work key, so wipe the whole group rather than just the base key.
		teardownSession(ctx, payload.SessionKey, payload.ZaloGroupID)
		if payload.Quiet {
			log.Printf("[worker] closed session %s quietly after idle timeout", payload.SessionID)
			return nil
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

		adapter := zalotoken.NewAdapter(ch.ID, zaloCreds, cfg.EncryptionKey)

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

// allowedBrandSet returns the set of permitted brand (nhãn hiệu) names, lowercased,
// for a resource — or nil when there is no brand restriction (all brands allowed).
// A nil set makes brandAllowedBySet a no-op so legacy inventory sums are unchanged.
func allowedBrandSet(permCtx *engine.GroupPermissionContext, resource string) map[string]struct{} {
	if permCtx == nil {
		return nil
	}
	brands, allBrands := permCtx.ResolveBrandFilter(resource)
	if allBrands || len(brands) == 0 {
		return nil
	}
	set := make(map[string]struct{}, len(brands))
	for _, b := range brands {
		if v := strings.ToLower(strings.TrimSpace(b)); v != "" {
			set[v] = struct{}{}
		}
	}
	if len(set) == 0 {
		return nil
	}
	return set
}

// brandAllowedBySet reports whether a product's brand passes the boundary.
// nil/empty set = no restriction → always allowed (exact, case-insensitive match).
func brandAllowedBySet(set map[string]struct{}, brand string) bool {
	if len(set) == 0 {
		return true
	}
	_, ok := set[strings.ToLower(strings.TrimSpace(brand))]
	return ok
}

func sumInventoryByMaCha(ctx context.Context, tenantID string, permCtx *engine.GroupPermissionContext, maCha string) (float64, error) {
	// 1. Fetch child products from Astra DB
	childProducts, err := getProductsByMaChaFromCache(ctx, tenantID, maCha)
	if err != nil {
		return 0, err
	}

	// 2. Fetch allowed product groups + brand boundary for "inventory" resource
	canView, _, allowedGroups := permCtx.IsResourceAllowed("inventory")
	if !canView {
		return 0, fmt.Errorf("inventory access denied")
	}
	allowedBrands, allBrands := permCtx.ResolveBrandFilter("inventory")

	// Collect original-case SKUs (ERP MA_HANG is case-sensitive). Dedup, preserve
	// first-seen order.
	var allowedSKUs []string
	seen := make(map[string]struct{})
	for _, p := range childProducts {
		sku := strings.TrimSpace(getMapString(p, "MA", "code", "ma"))
		group := getMapString(p, "LIST_TEN_NHOM_VTHH", "list_ten_nhom_vthh", "group")

		brand := getMapString(p, "NHAN_HIEU_NAME", "nhan_hieu_name")
		if engine.WarehouseProductAllowed(group, brand, allowedGroups, allowedBrands, allBrands) && sku != "" {
			if _, dup := seen[sku]; dup {
				continue
			}
			seen[sku] = struct{}{}
			allowedSKUs = append(allowedSKUs, sku)
		}
	}

	if len(allowedSKUs) == 0 {
		return 0, errNoVisibleStock
	}

	// 3. Load ERP credentials
	cfg, _ := config.Load()
	erpURL, erpDB, erpLogin, erpPassword, err := loadCloudifyCredentials(tenantID, cfg)
	if err != nil {
		return 0, err
	}

	if erpURL == "" {
		return 0, fmt.Errorf("Cloudify inventory URL is not configured")
	}

	client := &pkg.CloudifyClient{
		BaseURL:  erpURL,
		DB:       erpDB,
		Login:    erpLogin,
		Password: erpPassword,
	}

	totals, err := engine.WarehouseAvailableStock(ctx, client, allowedSKUs)
	if err != nil {
		return 0, err
	}
	var totalStock float64
	for _, sku := range allowedSKUs {
		totalStock += totals[sku]
	}

	return totalStock, nil
}

func getProductsByMaChaFromCache(ctx context.Context, tenantID, maCha string, scopes ...func(*gorm.DB) *gorm.DB) ([]map[string]interface{}, error) {
	var products []models.CachedProduct
	err := db.DB.WithContext(ctx).Scopes(scopes...).
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
	if err := db.DB.WithContext(ctx).Scopes(engine.ExcludeDKCopies).Where("tenant_id = ? AND ma = ?", tenantID, sku).First(&p).Error; err != nil {
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

// priceRangeReplyByWebName builds a price-range reply for ONE exact product-line
// web name, reading the local product cache (the same source the `products`
// resource uses) and the canonical engine price formatter. Variants the caller
// is not permitted to see (product-group scope) are excluded. Returns "" when no
// priced variant is visible, so the caller can fall back to the stock picker
// instead of sending an empty or misleading reply.
func priceRangeReplyByWebName(ctx context.Context, tenantID string, permCtx *engine.GroupPermissionContext, webName string) (string, error) {
	var products []models.CachedProduct
	err := db.DB.WithContext(ctx).Scopes(engine.ExcludeDKCopies).
		Where("tenant_id = ? AND ten_dong_bo_web = ?", tenantID, webName).
		Limit(200).Find(&products).Error
	if err != nil {
		return "", fmt.Errorf("price cache query failed: %w", err)
	}
	if len(products) == 0 {
		return "", nil
	}

	_, _, allowedGroups := permCtx.IsResourceAllowed("products")
	brandSet := allowedBrandSet(permCtx, "products")
	prices := make([]float64, 0, len(products))
	for _, p := range products {
		if !isProductGroupAllowed(p.LIST_TEN_NHOM_VTHH, allowedGroups) {
			continue
		}
		if !brandAllowedBySet(brandSet, p.NHAN_HIEU_NAME) {
			continue
		}
		if p.DON_GIA_BAN > 0 {
			prices = append(prices, p.DON_GIA_BAN)
		}
	}
	if len(prices) == 0 {
		return "", nil
	}

	minP, maxP := engine.PriceRangeOfPrices(prices)
	label := engine.FormatPriceRange(minP, maxP)
	return fmt.Sprintf("Giá %s: %s. Nếu anh/chị cần giá đúng theo màu + size cụ thể thì cho mình biết nhé.", webName, label), nil
}

// isProductGroupAllowed reports whether a product's group falls within the
// permitted product groups. An empty allow-list means all groups are allowed
// (no group restriction configured). Match is case-insensitive substring, the
// same rule sumInventoryByMaChaAndWebName applies.
func isProductGroupAllowed(group string, allowedGroups []string) bool {
	if len(allowedGroups) == 0 {
		return true
	}
	gLower := strings.ToLower(group)
	for _, allowed := range allowedGroups {
		if strings.Contains(gLower, strings.ToLower(allowed)) {
			return true
		}
	}
	return false
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

// classifyMessageIntent labels a message outside group assistant mode.
// history, when set, loads the last turns of the session so the model can tell
// a reply to the bot's own question from small talk. It is only called when
// the model is, so the fast paths below cost no database read.
func classifyMessageIntent(ctx context.Context, tenantID, message string, history func() []chatmemory.RecentTurn, businessRouting bool) (string, error) {
	// 0. Fast-path: structural disambiguation reply (digit 1–9 or SPxxxxxx[_TL])
	//    must not be re-classified by the LLM. The classifier prompt teaches
	//    short tokens like "1"/"dạ"/"ok" as CASUAL and skip the customer's pick.
	if isDisambiguationReply(message) {
		return "IN_SCOPE", nil
	}
	// Ambiguous lookup words ("giá", "size") can also appear in unrelated
	// questions, so only relatively specific ERP cues bypass classification.
	if (businessRouting && isExplicitERPLookup(message)) || (!businessRouting && isObviousBusinessQuery(message)) {
		return "IN_SCOPE", nil
	}

	var recent []chatmemory.RecentTurn
	if history != nil {
		recent = history()
	}
	if businessRouting && isCurrentOrderFollowup(message, recent) {
		return "IN_SCOPE", nil
	}
	content, err := classifyWithTaskPrompt(ctx, tenantID, engine.PromptIntentClassifier, intentClassifierInput(recent, message), businessRouting)
	if err != nil || content == "" {
		return "IN_SCOPE", err
	}
	return parseMessageIntent(content, message), nil
}

// classifyWithTaskPrompt asks the tenant's AI provider to label input with
// the given task prompt and returns the raw answer. It returns "" without an
// error when no provider is configured, so each caller picks its own fallback.
func classifyWithTaskPrompt(ctx context.Context, tenantID, promptKey, input string, businessRouting bool) (string, error) {
	// Resolve the tenant's AI provider (settings → key → model → base URL).
	cfg, _ := config.Load()
	resolved, err := engine.ResolveTenantAIProvider(tenantID, cfg)
	if err != nil {
		return "", err
	}
	if resolved.Client == nil {
		return "", nil
	}
	systemPrompt := engine.BuildTaskPrompt(tenantID, promptKey, nil)
	if businessRouting {
		systemPrompt += "\n\n" + businessClassifierInstruction
	}

	testCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	resp, err := resolved.Client.AnalyzeChat(testCtx, systemPrompt, input)
	if err != nil {
		return "", err
	}
	return resp.Content, nil
}

// classifyGroupMessage decides whether the bot answers a message in group
// assistant mode. The classifier sees the recent group log, so members
// talking to each other stay CASUAL (silent). Without a working classifier
// the bot answers only obvious business questions: answering every message
// in a busy group would be worse than missing one.
func classifyGroupMessage(ctx context.Context, tenantID, workKey string, recent []groupLine, name, role, message string, businessRouting bool) string {
	if isDisambiguationReply(message) && hasPendingOptions(ctx, workKey) {
		return "IN_SCOPE"
	}
	content, err := classifyWithTaskPrompt(ctx, tenantID, engine.PromptGroupClassifier, groupClassifierInput(recent, name, role, message), businessRouting)
	if err != nil {
		log.Printf("[group-assistant] classifier error: %v; using keyword fallback", err)
	}
	if label := parseGroupIntent(content); label != "" {
		return label
	}
	if isObviousBusinessQuery(message) {
		return "IN_SCOPE"
	}
	return "CASUAL"
}

// parseGroupIntent reads the group classifier's label as given. Unlike
// parseMessageIntent it does not turn questions into IN_SCOPE: in a group
// most questions are for other members. "" means no usable answer.
func parseGroupIntent(content string) string {
	content = strings.TrimSpace(content)
	if content == "" {
		return ""
	}
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
		parsed.Label = content
	}
	switch label := strings.ToUpper(strings.TrimSpace(parsed.Label)); label {
	case "IN_SCOPE", "HANDOVER", "CASUAL", "OUT_OF_SCOPE":
		return label
	}
	return ""
}

func parseMessageIntent(content, message string) string {
	content = strings.TrimSpace(content)
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
		parsed.Label = content // Accept bare labels as well as the requested JSON.
	}

	switch label := strings.ToUpper(strings.TrimSpace(parsed.Label)); label {
	case "HANDOVER":
		return label
	case "CASUAL", "OUT_OF_SCOPE":
		// These are product categories, even when the classifier assumes
		// the requested category is outside the bot's current permissions.
		text := strings.ToLower(message)
		if strings.Contains(text, "mũ nguyên đầu") || strings.Contains(text, "mũ nửa đầu") {
			return "IN_SCOPE"
		}
		if label == "OUT_OF_SCOPE" || !strings.ContainsAny(message, "?？") {
			return label
		}
	}
	return "IN_SCOPE"
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
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}
	resolved, err := engine.ResolveTenantAIProvider(tenantID, cfg)
	if err != nil {
		return nil, err
	}
	if resolved.Client == nil {
		return nil, fmt.Errorf("API key not configured for provider %s", resolved.Provider)
	}
	return resolved.Client, nil
}

// resolveChatbotPricing returns the provider/model used to price chatbot LLM
// spend when the Langflow response carried no real usage numbers. It prefers
// dedicated chatbot_* settings, then the tenant's general ai_* settings, then a
// sane default.
func resolveChatbotPricing(tenantID string) (provider, model string) {
	get := func(key string) string {
		var s models.AppSetting
		if err := db.DB.Where("tenant_id = ? AND setting_key = ?", tenantID, key).First(&s).Error; err == nil {
			return s.ValuePlain
		}
		return ""
	}
	provider = get("chatbot_provider")
	if provider == "" {
		provider = get("ai_provider")
	}
	if provider == "" {
		provider = "openai"
	}
	model = get("chatbot_model")
	if model == "" {
		model = get("ai_model")
	}
	if model == "" {
		switch provider {
		case "claude":
			model = "claude-haiku-4-5"
		case "gemini":
			model = "gemini-2.0-flash"
		default:
			model = "gpt-5.4-mini"
		}
	}
	return provider, model
}

// logChatbotAIUsage records the estimated (or real) AI spend for a single Zalo
// OA chatbot turn. It never blocks the reply path: failures are only logged.
// When the Langflow response carried real token usage it is used verbatim;
// otherwise tokens are estimated from text length (a lower bound, since history
// and RAG context injected inside Langflow are invisible to the backend).
func logChatbotAIUsage(tenantID, channelID, senderID, senderName, agentType, groupID string, res engine.LangflowResult, promptText, replyText string) {
	provider, model := res.Provider, res.Model
	inTok, outTok := res.InputTokens, res.OutputTokens

	if !res.HasUsage {
		provider, model = resolveChatbotPricing(tenantID)
		inTok = ai.EstimateTokens(promptText)
		outTok = ai.EstimateTokens(replyText)
	} else if provider == "" {
		// Real token counts but no provider/model label — price with tenant config.
		provider, model = resolveChatbotPricing(tenantID)
	}

	// GMF (public) customers aren't in the whitelist, so senderName arrives empty.
	// Bake their display name in now (zalo_customers, else whitelist) so the
	// per-employee cost chart shows a name — and keeps showing it even if the
	// customer is later deleted, since the log row already carries the name.
	if senderName == "" && senderID != "" {
		senderName = resolveSenderDisplayName(tenantID, senderID)
	}

	usage := models.AIUsageLog{
		ID:               pkg.NewUUID(),
		TenantID:         tenantID,
		Source:           "chatbot",
		ChannelID:        channelID,
		SenderExternalID: senderID,
		SenderName:       senderName,
		AgentType:        agentType,
		GroupID:          groupID,
		Provider:         provider,
		Model:            model,
		InputTokens:      inTok,
		OutputTokens:     outTok,
		CostUSD:          ai.CalculateCostUSD(provider, model, inTok, outTok),
		CreatedAt:        time.Now(),
	}
	if err := db.DB.Create(&usage).Error; err != nil {
		log.Printf("[worker] failed to log chatbot AI usage for tenant %s: %v", tenantID, err)
	}
}

// resolveSenderDisplayName looks up a friendly name for a Zalo user ID,
// preferring the whitelist (NV employee) over zalo_customers (GMF). Returns ""
// when neither has a name, leaving the caller to keep the raw ID.
func resolveSenderDisplayName(tenantID, zaloUserID string) string {
	var wl models.ZaloWhitelist
	if err := db.DB.Select("name").
		Where("tenant_id = ? AND zalo_user_id = ? AND name <> ''", tenantID, zaloUserID).
		First(&wl).Error; err == nil && wl.Name != "" {
		return wl.Name
	}

	var cust models.ZaloCustomer
	if err := db.DB.Select("name").
		Where("tenant_id = ? AND zalo_user_id = ? AND name <> ''", tenantID, zaloUserID).
		First(&cust).Error; err == nil && cust.Name != "" {
		return cust.Name
	}

	return ""
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

	systemPrompt := engine.BuildTaskPrompt(tenantID, engine.PromptProductCodeMatch, map[string]string{
		"keyword":    strconv.Quote(keyword),
		"candidates": strings.Join(lines, "\n"),
	})

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

	systemPrompt := engine.BuildTaskPrompt(tenantID, engine.PromptInventoryKeyword, nil)

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
	// No _DK filter here: this lookup only feeds the stock picker, and _DK
	// SKUs hold real stock.
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

// sumInventoryByParentWithDKCopy sums a line picked by parent code (no web
// name) together with its "_DK" copy parent. The picker hides the copy as a
// line of its own, but its SKUs hold real stock that belongs to this line.
// Only this call site adds the copy: the by-web-name sum already reads every
// parent of the web name, copy included.
func sumInventoryByParentWithDKCopy(ctx context.Context, tenantID string, permCtx *engine.GroupPermissionContext, maCha string) (float64, []string, error) {
	ctx, cancel := context.WithTimeout(ctx, engine.WarehouseAvailabilityTimeout)
	defer cancel()
	stock, details, err := sumInventoryByMaChaAndWebName(ctx, tenantID, permCtx, maCha, "")
	if err != nil && !errors.Is(err, errNoVisibleStock) {
		return 0, nil, err
	}
	dkParent := engine.DKCopyParentOf(maCha)
	if dkParent == strings.TrimSpace(maCha) {
		return stock, details, err
	}
	copyStock, copyDetails, copyErr := sumInventoryByMaChaAndWebName(ctx, tenantID, permCtx, dkParent, "")
	if copyErr != nil && !errors.Is(copyErr, errNoVisibleStock) {
		return 0, nil, copyErr
	}
	if err != nil && copyErr != nil {
		return 0, nil, errNoVisibleStock
	}
	return stock + copyStock, append(details, copyDetails...), nil
}

// sumInventoryByMaChaAndWebName aggregates realtime inventory by MA_CHA and optionally WEB_NAME
func sumInventoryByMaChaAndWebName(ctx context.Context, tenantID string, permCtx *engine.GroupPermissionContext, maCha string, webName string) (float64, []string, error) {
	var products []models.CachedProduct
	q := db.DB.WithContext(ctx).Where("tenant_id = ? AND ma_cha = ?", tenantID, maCha)
	if webName != "" {
		q = q.Where("ten_dong_bo_web = ?", webName).Limit(100)
	}
	err := q.Find(&products).Error
	if err != nil {
		return 0, nil, fmt.Errorf("local MySQL cache query failed: %w", err)
	}
	// A parent picked within one web line also owns that line's SKUs under
	// its "_DK" copy parent, which pickers no longer list on their own. The
	// no-web-name case is left to its callers: the by-web postback already
	// sums every parent of the line, and sumInventoryByParentWithDKCopy adds
	// the copy for a bare parent pick.
	if dkParent := engine.DKCopyParentOf(maCha); webName != "" && dkParent != strings.TrimSpace(maCha) {
		var copies []models.CachedProduct
		if err := db.DB.WithContext(ctx).Where("tenant_id = ? AND ma_cha = ?", tenantID, dkParent).
			Where("ten_dong_bo_web = ?", webName).Limit(100).Find(&copies).Error; err != nil {
			return 0, nil, fmt.Errorf("local MySQL cache query failed: %w", err)
		}
		seen := make(map[string]struct{}, len(products))
		for _, p := range products {
			seen[p.MA] = struct{}{}
		}
		for _, p := range copies {
			if _, dup := seen[p.MA]; !dup {
				seen[p.MA] = struct{}{}
				products = append(products, p)
			}
		}
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

	// 2. Fetch allowed product groups + brand boundary for "inventory" resource
	canView, _, allowedGroups := permCtx.IsResourceAllowed("inventory")
	if !canView {
		return 0, nil, fmt.Errorf("inventory access denied")
	}
	allowedBrands, allBrands := permCtx.ResolveBrandFilter("inventory")

	// allowedSKU pairs an original-case SKU with its display name. Original case
	// matters: the ERP MA_HANG lookup is case-sensitive, so the code we send must
	// not be lowercased. Dedup on the SKU; preserve first-seen order so replies
	// are stable.
	type allowedSKU struct{ Code, Name string }
	var allowedSKUs []allowedSKU
	seen := make(map[string]struct{})
	for _, p := range childProducts {
		sku := strings.TrimSpace(getMapString(p, "MA", "code", "ma"))
		group := getMapString(p, "LIST_TEN_NHOM_VTHH", "list_ten_nhom_vthh", "group")
		name := getMapString(p, "TEN", "ten_hang", "ten")

		brand := getMapString(p, "NHAN_HIEU_NAME", "nhan_hieu_name")
		if engine.WarehouseProductAllowed(group, brand, allowedGroups, allowedBrands, allBrands) && sku != "" {
			if _, dup := seen[sku]; dup {
				continue
			}
			seen[sku] = struct{}{}
			if name == "" {
				name = sku
			}
			allowedSKUs = append(allowedSKUs, allowedSKU{Code: sku, Name: name})
		}
	}

	if len(allowedSKUs) == 0 {
		return 0, nil, errNoVisibleStock
	}

	// 3. Load ERP credentials
	cfg, _ := config.Load()
	erpURL, erpDB, erpLogin, erpPassword, err := loadCloudifyCredentials(tenantID, cfg)
	if err != nil {
		return 0, nil, err
	}

	if erpURL == "" {
		return 0, nil, fmt.Errorf("Cloudify inventory URL is not configured")
	}

	client := &pkg.CloudifyClient{
		BaseURL:  erpURL,
		DB:       erpDB,
		Login:    erpLogin,
		Password: erpPassword,
	}

	skus := make([]string, 0, len(allowedSKUs))
	for _, sku := range allowedSKUs {
		skus = append(skus, sku.Code)
	}
	totals, err := engine.WarehouseAvailableStock(ctx, client, skus)
	if err != nil {
		return 0, nil, err
	}

	var totalStock float64
	var details []string
	for _, sku := range allowedSKUs {
		stock := totals[sku.Code]
		totalStock += stock
		details = append(details, fmt.Sprintf("- %s: %g", sku.Name, stock))
		log.Printf("[stock-pick] warehouse=%s ma_cha=%s sku=%s stock=%.1f", engine.MainWarehouseCode, maCha, sku.Code, stock)
	}

	return totalStock, details, nil
}

// getProductsByWebNameFromCache returns every cached product whose
// ten_dong_bo_web equals webName for the given tenant. Used by the web-name
// option flow to expand a single "product line" selection into the underlying
// MA_CHA list / variant SKUs.
func getProductsByWebNameFromCache(ctx context.Context, tenantID, webName string) ([]map[string]interface{}, error) {
	var products []models.CachedProduct
	err := db.DB.WithContext(ctx).Scopes(engine.ExcludeDKCopies).
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
