package handlers

import (
	"context"
	"errors"
	"log"
	"net/http"
	"regexp"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/gin-gonic/gin"
	"github.com/vietbui/chat-quality-agent/agents"
	"github.com/vietbui/chat-quality-agent/channels"
	"github.com/vietbui/chat-quality-agent/config"
	"github.com/vietbui/chat-quality-agent/customercases"
	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/db/models"
	"github.com/vietbui/chat-quality-agent/engine"
	"github.com/vietbui/chat-quality-agent/workers"
	"gorm.io/gorm"
)

const personalZaloTextLimit = 2000

var personalZaloUIDPattern = regexp.MustCompile(`^[0-9]{1,64}$`)

type personalZaloChatbotRequest struct {
	SchemaVersion     string    `json:"schema_version" binding:"required,oneof=personal-zalo-chatbot-v1 personal-zalo-chatbot-v2 personal-zalo-chatbot-v3 personal-zalo-internbot-v1"`
	RequestID         string    `json:"request_id" binding:"required,max=64"`
	TenantID          string    `json:"tenant_id" binding:"required,max=36"`
	ChannelID         string    `json:"channel_id" binding:"required,max=36"`
	AccountExternalID string    `json:"account_external_id" binding:"required"`
	ThreadID          string    `json:"thread_id" binding:"required"`
	ThreadType        string    `json:"thread_type"`
	SenderID          string    `json:"sender_id"`
	MessageID         string    `json:"message_id" binding:"required,max=128"`
	SentAt            time.Time `json:"sent_at" binding:"required"`
	Text              string    `json:"text"`
	BotMentioned      bool      `json:"bot_mentioned,omitempty"`
	GroupWakeText     *string   `json:"group_wake_text,omitempty"`
	MultipleImages    bool      `json:"multiple_images,omitempty"`
	SupportsInternbot bool      `json:"supports_internbot,omitempty"`
	SourceName        string    `json:"source_name,omitempty" binding:"max=128"`
	SenderName        string    `json:"sender_name,omitempty" binding:"max=128"`
	DeliveryReceipt   string    `json:"delivery_receipt" binding:"omitempty,uuid4"`
	DeliveryToken     string    `json:"delivery_token,omitempty" binding:"omitempty,max=4096"`
	DeliveryPart      string    `json:"delivery_part,omitempty" binding:"omitempty,oneof=text image"`
	DeliveryDigest    string    `json:"delivery_digest,omitempty" binding:"omitempty,len=64,hexadecimal"`
}

// NewPersonalZaloChatbotHandler receives live text turns from the
// authenticated gateway. The gateway owns the Zalo session and sends the reply.
func NewPersonalZaloChatbotHandler(cfg *config.Config) gin.HandlerFunc {
	ai := newPersonalZaloAI(cfg)
	activationKeys := append([]string{"chatbot_active"}, erpActivationKeys...)

	return func(c *gin.Context) {
		var req personalZaloChatbotRequest
		if err := c.ShouldBindJSON(&req); err != nil || (req.Text == "" && !req.BotMentioned) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request"})
			return
		}
		if c.GetString("internal_request_id") != req.RequestID {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "request_id_mismatch"})
			return
		}
		req.Text = strings.TrimSpace(req.Text)
		age := time.Since(req.SentAt)
		preflight := req.DeliveryToken != "" || req.DeliveryPart != "" || req.DeliveryDigest != ""
		internbotOnly := req.SchemaVersion == personalZaloInternbotProtocol
		if internbotOnly {
			if !req.SupportsInternbot || preflight || req.DeliveryReceipt != "" {
				c.Status(http.StatusNoContent)
				return
			}
			req.SchemaVersion = personalZaloDirectProtocol
			if req.ThreadType == personalZaloThreadGroup {
				req.SchemaVersion = "personal-zalo-chatbot-v2"
			}
		}
		if (req.Text == "" && !req.BotMentioned) || personalZaloTextBoundary(req.Text, personalZaloTextLimit) != len(req.Text) ||
			!personalZaloUIDPattern.MatchString(req.AccountExternalID) || !normalizePersonalZaloChatbotRequest(&req) ||
			((req.BotMentioned || req.GroupWakeText != nil) && req.ThreadType != personalZaloThreadGroup) ||
			(req.GroupWakeText != nil && personalZaloTextBoundary(*req.GroupWakeText, personalZaloTextLimit) != len(*req.GroupWakeText)) ||
			(req.ThreadType == personalZaloThreadUser && req.SchemaVersion != personalZaloDirectProtocol) ||
			(preflight && (req.ThreadType != personalZaloThreadUser || req.DeliveryToken == "" || req.DeliveryPart == "" || req.DeliveryDigest == "")) ||
			(!preflight && req.DeliveryReceipt == "" && (age > 2*time.Minute || age < -30*time.Second)) ||
			(req.DeliveryReceipt != "" && req.ThreadType != personalZaloThreadGroup) {
			c.Status(http.StatusNoContent)
			return
		}

		ctx, cancel := context.WithTimeout(c.Request.Context(), 45*time.Second)
		defer cancel()
		if req.SupportsInternbot && !preflight && req.DeliveryReceipt == "" {
			if ai.handleInternbotTurn(c, ctx, req) {
				return
			}
			if internbotOnly {
				c.Status(http.StatusNoContent)
				return
			}
		}
		channel, enabled, err := personalZaloChatbotChannel(ctx, req)
		if err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "chatbot_settings_unavailable"})
			return
		}
		if !enabled {
			c.Status(http.StatusNoContent)
			return
		}
		settings, err := loadPersonalZaloChatbotSettings(ctx, req.TenantID, ai.settingKeys...)
		if err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "chatbot_settings_unavailable"})
			return
		}
		if !personalZaloChatbotMasterEnabled(settings) {
			c.Status(http.StatusNoContent)
			return
		}

		if channel.ExternalID != req.AccountExternalID {
			c.Status(http.StatusNoContent)
			return
		}
		approved, err := personalZaloThreadApproved(ctx, req)
		if err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "chatbot_approval_unavailable"})
			return
		}
		if !approved {
			c.Status(http.StatusNoContent)
			return
		}

		if preflight {
			checkPersonalZaloDelivery(c, ctx, cfg.EncryptionKey, channel, req, settings)
			return
		}

		sessionConfig, err := parsePersonalZaloSessionConfig(channel, cfg)
		if err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "chatbot_session_unavailable"})
			return
		}
		if req.DeliveryReceipt != "" {
			confirmPersonalZaloGroupReply(ctx, sessionConfig, req)
			c.Status(http.StatusNoContent)
			return
		}
		verificationReply, handled, err := handlePersonalZaloIdentityVerification(ctx, channel, req)
		if err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "chatbot_verification_unavailable"})
			return
		}
		if handled {
			if verificationReply == "" {
				c.Status(http.StatusNoContent)
				return
			}
			writePersonalZaloDirectReply(c, cfg.EncryptionKey, req, gin.H{"reply": verificationReply}, personalZaloERPIdentity{}, personalZaloDirectGrant{})
			return
		}
		req.Text = redactPersonalZaloPairingCodes(req.Text)
		var identity personalZaloERPIdentity
		if req.ThreadType == personalZaloThreadUser {
			identity, err = resolvePersonalZaloERPIdentity(ctx, channel, req)
			if err != nil {
				c.JSON(http.StatusServiceUnavailable, gin.H{"error": "chatbot_verification_unavailable"})
				return
			}
			if sessionConfig.SessionWelcomeMessage == cfg.ChatbotSessionWelcomeMessage {
				sessionConfig.SessionWelcomeMessage = personalZaloDirectGreeting(identity.AgentType)
			}
		}
		guest := req.ThreadType == personalZaloThreadUser && identity.AgentType == ""
		// Specialist agents: the channel's team picks who answers. A team
		// with no agent for this sender leaves the chat to people.
		agentPlan := personalZaloAgentPlan(ctx, req, identity)
		if agentPlan.Mode == agents.ModeSilent {
			log.Printf("[agents] personal Zalo channel %s: no agent serves this sender here; staying silent (%s)", req.ChannelID, agentPlan.LogFields())
			c.Status(http.StatusNoContent)
			return
		}
		addressed := req.ThreadType == personalZaloThreadUser || personalZaloGroupMessageAddressed(req, sessionConfig)
		var inferredComplaint bool
		groupTurn := preparePersonalZaloGroupTurn(ctx, channel, sessionConfig, req, addressed, func(ctx context.Context, tenantID, input string) bool {
			reply, handover := workers.PersonalZaloGroupNeedsReply(ctx, tenantID, input)
			inferredComplaint = handover
			return reply
		})
		staffGroupComplaint := groupTurn.Staff && addressed
		complaintEligible := customercases.EnabledFor(channel.Metadata) && (!groupTurn.Staff || staffGroupComplaint) && req.Text != ""
		canRouteComplaint := complaintEligible && (agentPlan.Allows(agents.ToolComplaintRecord) || (addressed && agentPlan.NeedsRouting()))
		requestedHandover := personalZaloHandoverRequested(req.Text) && (addressed || (groupTurn.Inferred && !groupTurn.Staff))
		handover := !canRouteComplaint && !guest && requestedHandover
		directSessionRequired := req.ThreadType == personalZaloThreadUser &&
			strings.TrimSpace(strings.ReplaceAll(sessionConfig.SessionKeyword, ";", "")) != ""
		var turn personalZaloTurn
		if guest && !directSessionRequired {
			turn = personalZaloTurn{RunFlow: true, FlowScope: personalZaloLegacyFlowScope(req)}
		} else if directSessionRequired || !handover {
			turn, err = planPersonalZaloTurn(ctx, sessionConfig, req, groupTurn.Inferred)
			if err != nil {
				c.JSON(http.StatusServiceUnavailable, gin.H{"error": "chatbot_session_unavailable"})
				return
			}
		}
		if directSessionRequired && !turn.RunFlow {
			if turn.Reply == "" {
				c.Status(http.StatusNoContent)
				return
			}
			// A session keyword takes precedence if it also matches a handover phrase.
			handover = false
		}
		promoteComplaint := !guest && canRouteComplaint && addressed && !turn.RunFlow && turn.Reply == "" &&
			!personalZaloMatchesKeyword(req.Text, sessionConfig.SessionEndKeyword) &&
			(requestedHandover || workers.PersonalZaloComplaintNeedsHandover(ctx, req.TenantID, req.Text))
		if addressed && !handover && (turn.RunFlow || promoteComplaint) {
			agentPlan = routePersonalZaloAgent(ctx, cfg, req, agentPlan, sessionConfig.SessionTimeoutMinutes)
		}
		complaints := complaintEligible && agentPlan.Allows(agents.ToolComplaintRecord)
		if !complaints && !guest && requestedHandover && (!directSessionRequired || turn.RunFlow) {
			handover = true
		}
		if complaints && promoteComplaint {
			requestedHandover = true
			turn = personalZaloTurn{RunFlow: true, FlowScope: personalZaloLegacyFlowScope(req)}
		}
		generation := personalZaloAIResult{reply: turn.Reply, handover: handover, complaint: complaints && turn.RunFlow}
		if turn.RunFlow && !handover {
			flowCtx := ctx
			if generation.complaint {
				// Reserve time to recheck the channel and notify staff on timeout.
				deadline, _ := ctx.Deadline()
				var cancelFlow context.CancelFunc
				flowCtx, cancelFlow = context.WithDeadline(ctx, deadline.Add(-10*time.Second))
				defer cancelFlow()
			}
			generation, err = ai.generate(ctx, flowCtx, channel, req, settings, personalZaloAIOptions{
				scope: turn.FlowScope, identity: identity, guest: guest, complaint: generation.complaint,
				canHandover: addressed || !groupTurn.Staff, staffGroupComplaint: staffGroupComplaint, plan: agentPlan,
			})
			if generation.settingsError {
				c.JSON(http.StatusServiceUnavailable, gin.H{"error": "chatbot_settings_unavailable"})
				return
			}
			if err != nil && !generation.complaint {
				c.JSON(http.StatusBadGateway, gin.H{"error": "chatbot_reply_failed"})
				return
			}
			if !generation.complaint {
				generation.reply = personalZaloStripHandoverMarker(generation.reply)
			}
		}
		reply, imageSelection, directGrant := generation.reply, generation.images, generation.grant
		handover, complaintTurn := generation.handover, generation.complaint
		flowErr := err
		reply = strings.TrimSpace(reply)
		if !complaintTurn && !handover && reply == "" {
			c.Status(http.StatusNoContent)
			return
		}

		// A channel or tenant can be paused while Langflow is generating.
		latestChannel, enabled, err := personalZaloChatbotChannel(ctx, req)
		if err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "chatbot_settings_unavailable"})
			return
		}
		if !enabled {
			c.Status(http.StatusNoContent)
			return
		}
		approved, err = personalZaloThreadApproved(ctx, req)
		if err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "chatbot_approval_unavailable"})
			return
		}
		if !approved {
			c.Status(http.StatusNoContent)
			return
		}
		settings, err = loadPersonalZaloChatbotSettings(ctx, req.TenantID, activationKeys...)
		if err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "chatbot_settings_unavailable"})
			return
		}
		if !personalZaloChatbotMasterEnabled(settings) {
			c.Status(http.StatusNoContent)
			return
		}
		if identity.AgentType != "" {
			latestIdentity, err := resolvePersonalZaloERPIdentity(ctx, latestChannel, req)
			if err != nil || latestIdentity.AgentType != identity.AgentType || latestIdentity.ID != identity.ID ||
				!directGrant.stillAuthorized(ctx, req, latestIdentity, settings) {
				c.Status(http.StatusNoContent)
				return
			}
		}
		if groupTurn.Speaker != "" {
			latestSession, err := parsePersonalZaloSessionConfig(latestChannel, cfg)
			if err != nil || (groupTurn.Inferred && !latestSession.GroupAssistantMode) ||
				(groupTurn.Inferred && groupTurn.Staff && latestSession.GroupStaffCallOnly && !sessionConfig.GroupStaffCallOnly) {
				c.Status(http.StatusNoContent)
				return
			}
			sessionConfig = latestSession
		}
		if complaintTurn {
			if !customercases.EnabledFor(latestChannel.Metadata) {
				c.Status(http.StatusNoContent)
				return
			}
			var discardImages bool
			caseReq := req
			if generation.caseText != "" {
				caseReq.Text = generation.caseText
			}
			reply, discardImages = finishPersonalZaloComplaint(ctx, cfg, latestChannel, caseReq, generation.caseTurnID, reply,
				inferredComplaint || requestedHandover || handover || reply == personalZaloAIHandoverMarker, staffGroupComplaint)
			handover = false
			if discardImages {
				imageSelection = personalZaloImageSelection{}
			}
		}
		if handover {
			closePersonalZaloSession(ctx, req)
			clearPersonalZaloStaffFollowup(ctx, req)
			reply = sendPersonalZaloHandover(ctx, cfg, latestChannel, req)
		}
		if reply == "" {
			if flowErr != nil {
				c.JSON(http.StatusBadGateway, gin.H{"error": "chatbot_reply_failed"})
				return
			}
			c.Status(http.StatusNoContent)
			return
		}

		if personalZaloTextBoundary(reply, personalZaloTextLimit) < len(reply) {
			reply = strings.TrimSpace(reply[:personalZaloTextBoundary(reply, personalZaloTextLimit-1)]) + "…"
		}
		response := gin.H{"reply": reply}
		if !handover {
			images := personalZaloProductReplyImages(ctx, directGrant.advicePermissions, imageSelection.products)
			if len(images) == 0 {
				images = personalZaloKnowledgeReplyImages(ctx, req.TenantID, identity.AgentType, imageSelection.knowledgeIDs)
			}
			if len(images) > 0 {
				response["images"] = images
			}
		}
		if !handover && groupTurn.Speaker != "" && sessionConfig.GroupAssistantMode {
			if receipt := awaitPersonalZaloGroupDelivery(ctx, req, groupTurn, reply); receipt != "" {
				response["delivery_receipt"] = receipt
			}
		}
		if req.ThreadType == personalZaloThreadUser {
			writePersonalZaloDirectReply(c, cfg.EncryptionKey, req, response, identity, directGrant)
			return
		}
		c.JSON(http.StatusOK, response)
	}
}

type personalZaloKnowledgeImage struct {
	ID            string `json:"id"`
	DeliveryToken string `json:"delivery_token,omitempty"`
	channels.ZaloImage
	product bool
}

func personalZaloKnowledgeReplyImages(ctx context.Context, tenantID, audience string, imageIDs []string) []personalZaloKnowledgeImage {
	rows, err := engine.LoadPersonalZaloKnowledgeImages(ctx, tenantID, audience, imageIDs)
	if err != nil {
		log.Printf("[personal-zalo] failed to load knowledge image tenant=%s: %v", tenantID, err)
		return nil
	}
	byID := make(map[string]models.KnowledgeDocumentImage, len(rows))
	for _, row := range rows {
		byID[row.ID] = row
	}
	var images []personalZaloKnowledgeImage
	for _, id := range imageIDs {
		row, ok := byID[id]
		if !ok {
			continue
		}
		attachment, err := channels.PrepareZaloImage(row.Data)
		if err != nil {
			log.Printf("[personal-zalo] failed to prepare knowledge image %s: %v", row.ID, err)
			continue
		}
		current, err := engine.LoadDeliverableKnowledgeReplyImageMetadata(ctx, tenantID, audience, row.ID)
		if err != nil {
			log.Printf("[personal-zalo] failed to recheck knowledge image %s: %v", row.ID, err)
		}
		if len(current) == 0 {
			continue
		}
		images = append(images, personalZaloKnowledgeImage{ID: row.ID, ZaloImage: attachment})
	}
	return images
}

func personalZaloChatbotChannel(ctx context.Context, req personalZaloChatbotRequest) (models.Channel, bool, error) {
	var channel models.Channel
	err := db.DB.WithContext(ctx).Select("id", "tenant_id", "channel_type", "external_id", "metadata").
		Where("id = ? AND tenant_id = ? AND channel_type = ? AND is_active = ? AND personal_zalo_auto_reply_enabled = ?",
			req.ChannelID, req.TenantID, "personal_zalo_import", true, true).
		Take(&channel).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return channel, false, nil
	}
	if err != nil {
		return channel, false, err
	}
	if channel.ExternalID != "" && channel.ExternalID != req.AccountExternalID {
		return channel, false, nil
	}
	metadata, err := decodeMetadataObject(channel.Metadata)
	if err != nil {
		return channel, false, err
	}
	if mode, _ := metadata["chatbot_mode"].(string); mode == "internbot" {
		return channel, false, nil
	}
	scope, _ := metadata["sync_scope"].(string)
	if req.ThreadType == personalZaloThreadGroup {
		enabled, _ := metadata["personal_zalo_group_reply_enabled"].(bool)
		if !enabled || scope == "direct" {
			return channel, false, nil
		}

		blocked, err := personalZaloGroupBlocked(ctx, req.TenantID, req.ThreadID)
		return channel, err == nil && !blocked, err
	}
	return channel, scope != "group", nil
}

func loadPersonalZaloChatbotSettings(ctx context.Context, tenantID string, keys ...string) (map[string]models.AppSetting, error) {
	var rows []models.AppSetting
	err := db.DB.WithContext(ctx).Where("tenant_id = ? AND setting_key IN ?", tenantID, keys).Find(&rows).Error
	if err != nil {
		return nil, err
	}
	settings := make(map[string]models.AppSetting, len(rows))
	for _, row := range rows {
		settings[row.SettingKey] = row
	}
	return settings, nil
}

func personalZaloChatbotMasterEnabled(settings map[string]models.AppSetting) bool {
	setting, exists := settings["chatbot_active"]
	return !exists || setting.ValuePlain == "true"
}

func personalZaloTextBoundary(text string, limit int) int {
	for index, r := range text {
		limit -= utf16.RuneLen(r)
		if limit < 0 {
			return index
		}
	}
	return len(text)
}
