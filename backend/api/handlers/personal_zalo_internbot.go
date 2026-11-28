package handlers

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"github.com/vietbui/chat-quality-agent/customercases"
	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/db/models"
	"github.com/vietbui/chat-quality-agent/engine"
	"github.com/vietbui/chat-quality-agent/workers"
	"gorm.io/gorm"
)

const personalZaloInternbotProtocol = "personal-zalo-internbot-v1"

var personalZaloInternbotSecret = regexp.MustCompile(`(?i)(?:\b(?:otp|mã xác thực|mã đăng nhập)\s*[:=]?\s*[0-9]{4,10}\b|\b(?:password|mật khẩu|token|api[_ -]?key)\s*[:=]\s*[^\s,;]+|\bbearer\s+[A-Za-z0-9._~+/=-]{12,})`)

func redactPersonalZaloInternbotSecrets(text string) string {
	return personalZaloInternbotSecret.ReplaceAllString(redactPersonalZaloPairingCodes(text), "[Đã ẩn thông tin xác thực]")
}

func personalZaloInternbotChannel(ctx context.Context, req personalZaloChatbotRequest) (models.Channel, personalZaloInternbotConfig, error) {
	var channel models.Channel
	err := db.DB.WithContext(ctx).Select("id", "tenant_id", "channel_type", "external_id", "metadata", "is_active", "personal_zalo_auto_reply_enabled").
		Where("id = ? AND tenant_id = ? AND channel_type = ?", req.ChannelID, req.TenantID, "personal_zalo_import").
		Take(&channel).Error
	if err != nil {
		return channel, personalZaloInternbotConfig{}, err
	}
	mode, err := decodePersonalZaloInternbotConfig(channel.Metadata)
	return channel, mode, err
}

func (ai personalZaloAI) handleInternbotTurn(c *gin.Context, ctx context.Context, req personalZaloChatbotRequest) bool {
	channel, settings, err := personalZaloInternbotChannel(ctx, req)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		c.Status(http.StatusNoContent)
		return true
	}
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "internbot_settings_unavailable"})
		return true
	}
	if settings.Mode != "internbot" {
		return false
	}
	if status := ai.generateInternbotDelivery(c, ctx, req, channel, settings); status != http.StatusOK {
		c.Status(status)
	}
	return true
}

func (ai personalZaloAI) generateInternbotDelivery(c *gin.Context, ctx context.Context, req personalZaloChatbotRequest,
	channel models.Channel, mode personalZaloInternbotConfig) int {
	eligible, err := personalZaloInternbotEligible(ctx, channel, req, mode)
	if err != nil {
		return http.StatusServiceUnavailable
	}
	if !eligible {
		return http.StatusNoContent
	}
	if _, verification := personalZaloVerificationCommand(req.Text); verification {
		return http.StatusNoContent
	}
	req.Text = strings.TrimSpace(redactPersonalZaloInternbotSecrets(req.Text))
	if req.Text == "" {
		return http.StatusNoContent
	}

	settings, err := loadPersonalZaloChatbotSettings(ctx, req.TenantID, ai.settingKeys...)
	if err != nil {
		return http.StatusServiceUnavailable
	}
	if !personalZaloChatbotMasterEnabled(settings) {
		return http.StatusNoContent
	}
	identity, staff, err := personalZaloInternbotIdentity(ctx, channel, req)
	if err != nil {
		return http.StatusServiceUnavailable
	}
	if staff {
		return http.StatusNoContent
	}

	handover := personalZaloHandoverRequested(req.Text)
	if !handover && customercases.EnabledFor(channel.Metadata) {
		handover = workers.PersonalZaloComplaintNeedsHandover(ctx, req.TenantID, req.Text)
	}
	var result personalZaloAIResult
	if !handover {
		scope, err := ai.internbotScope(ctx, channel, req, mode)
		if err != nil {
			return http.StatusServiceUnavailable
		}
		result, err = ai.generate(ctx, ctx, channel, req, settings, personalZaloAIOptions{
			scope: scope, identity: identity, guest: identity.AgentType == "", draft: true,
		})
		if result.settingsError {
			return http.StatusServiceUnavailable
		}
		if err != nil {
			return http.StatusBadGateway
		}
		handover = result.handover || strings.TrimSpace(result.reply) == personalZaloAIHandoverMarker
	}
	if handover {
		result.reply = engine.TaskPromptText(req.TenantID, engine.PromptPZInternbotHandover)
		result.images = personalZaloImageSelection{}
	}
	result.reply = strings.TrimSpace(redactPersonalZaloInternbotSecrets(
		customercases.StripRefs(personalZaloStripHandoverMarker(result.reply))))
	if result.reply == "" {
		return http.StatusNoContent
	}

	// Delivery authorization repeats these checks immediately before each send.
	latest, latestMode, err := personalZaloInternbotChannel(ctx, req)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return http.StatusNoContent
	}
	if err != nil {
		return http.StatusServiceUnavailable
	}
	if latestMode.Version != mode.Version {
		return http.StatusNoContent
	}
	eligible, err = personalZaloInternbotEligible(ctx, latest, req, latestMode)
	if err != nil {
		return http.StatusServiceUnavailable
	}
	if !eligible {
		return http.StatusNoContent
	}

	images := personalZaloProductReplyImages(ctx, result.grant.advicePermissions, result.images.products)
	if len(images) == 0 {
		images = personalZaloKnowledgeReplyImages(ctx, req.TenantID, identity.AgentType, result.images.knowledgeIDs)
	}
	delivery, err := buildPersonalZaloInternbotDelivery(ai.cfg.EncryptionKey, req, mode, identity, result.grant,
		formatPersonalZaloInternbotDraft(req, result.reply, handover), images)
	if err != nil {
		return http.StatusServiceUnavailable
	}
	c.JSON(http.StatusOK, gin.H{"internbot_delivery": delivery})
	return http.StatusOK
}

func personalZaloInternbotEligible(ctx context.Context, channel models.Channel, req personalZaloChatbotRequest,
	mode personalZaloInternbotConfig) (bool, error) {
	if mode.Mode != "internbot" || !mode.Enabled || !channel.IsActive || channel.PersonalZaloAutoReplyEnabled ||
		channel.ExternalID != req.AccountExternalID || mode.SupervisorAccountID != req.AccountExternalID ||
		mode.Version == "" || mode.ActivatedAt.IsZero() || req.SentAt.Before(mode.ActivatedAt) ||
		!personalZaloUIDPattern.MatchString(mode.SupervisorGroupID) || mode.SupervisorGroupID == req.AccountExternalID ||
		(req.ThreadType == personalZaloThreadGroup && mode.SupervisorGroupID == req.ThreadID) {
		return false, nil
	}
	return personalZaloInternbotGroupPresent(ctx, channel, mode.SupervisorGroupID)
}

func personalZaloInternbotIdentity(ctx context.Context, channel models.Channel, req personalZaloChatbotRequest) (personalZaloERPIdentity, bool, error) {
	if req.ThreadType == personalZaloThreadGroup {
		staff, err := isPersonalZaloGroupStaff(ctx, channel, req.SenderID)
		if staff || err != nil {
			return personalZaloERPIdentity{}, staff, err
		}
		// The same Personal Zalo sender ID can have a staff link in a 1:1
		// conversation. A customer link alone cannot authorize a group query.
		req.ThreadType, req.ThreadID = personalZaloThreadUser, req.SenderID
		identity, err := resolvePersonalZaloERPIdentity(ctx, channel, req)
		return personalZaloERPIdentity{}, identity.AgentType == "private", err
	}
	identity, err := resolvePersonalZaloERPIdentity(ctx, channel, req)
	return identity, identity.AgentType == "private", err
}

func (ai personalZaloAI) internbotScope(ctx context.Context, channel models.Channel, req personalZaloChatbotRequest,
	mode personalZaloInternbotConfig) (string, error) {
	base := "internbot:" + mode.Version + ":" + personalZaloSessionKey(req)
	session, err := parsePersonalZaloSessionConfig(channel, ai.cfg)
	if err != nil {
		return "", err
	}
	if db.RedisClient == nil {
		if session.Managed || req.ThreadType == personalZaloThreadGroup {
			return "", errors.New("Redis is required for Internbot session expiration")
		}
		return personalZaloSessionFlowScope(req, base), nil
	}
	ttl := time.Duration(session.SessionTimeoutMinutes) * time.Minute
	id, err := db.RedisClient.GetEx(ctx, base, ttl).Result()
	if errors.Is(err, redis.Nil) {
		id, err = replacePersonalZaloSession(ctx, base, ttl)
	}
	if err != nil {
		return "", err
	}
	return personalZaloSessionFlowScope(req, base+":"+id), nil
}

// formatPersonalZaloInternbotDraft fills the tenant's frame (edited in AI
// Engine) for the supervisor group.
func formatPersonalZaloInternbotDraft(req personalZaloChatbotRequest, reply string, handover bool) string {
	return fillPersonalZaloInternbotDraft(engine.TaskPromptText(req.TenantID, engine.PromptPZInternbotFrame), req, reply, handover)
}

func fillPersonalZaloInternbotDraft(frame string, req personalZaloChatbotRequest, reply string, handover bool) string {
	source := strings.Join(strings.Fields(redactPersonalZaloInternbotSecrets(req.SourceName)), " ")
	if source == "" {
		source = "Chat 1:1"
		if req.ThreadType == personalZaloThreadGroup {
			source = "Nhóm " + req.ThreadID
		}
	}
	sender := strings.Join(strings.Fields(redactPersonalZaloInternbotSecrets(req.SenderName)), " ")
	if sender == "" {
		sender = req.SenderID
	}
	location := time.FixedZone("ICT", 7*60*60)
	// The reply gets the room the rest of the frame leaves under the Zalo
	// limit, shared when the frame repeats it.
	vars := map[string]string{
		"nhom": source, "khach": sender, "gio": req.SentAt.In(location).Format("15:04"),
		"tin_nhan": clipPersonalZaloInternbotText(req.Text, 400),
	}
	show := map[string]bool{"neu_can_nhan_vien": handover}
	vars["phan_hoi"] = ""
	empty := engine.FillMessageTemplate(frame, vars, show)
	vars["phan_hoi"] = "x"
	slots := len(engine.FillMessageTemplate(frame, vars, show)) - len(empty)
	room := personalZaloTextLimit - personalZaloUTF16Len(empty)
	if slots > 1 {
		room /= slots
	}
	vars["phan_hoi"] = clipPersonalZaloInternbotText(reply, room)
	return clipPersonalZaloInternbotText(engine.FillMessageTemplate(frame, vars, show), personalZaloTextLimit)
}

func personalZaloUTF16Len(value string) int {
	n := 0
	for _, r := range value {
		n += utf16.RuneLen(r)
	}
	return n
}

func clipPersonalZaloInternbotText(value string, limit int) string {
	if personalZaloTextBoundary(value, limit) == len(value) {
		return value
	}
	return strings.TrimSpace(value[:personalZaloTextBoundary(value, limit-1)]) + "…"
}
