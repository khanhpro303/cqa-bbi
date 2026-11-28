package handlers

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"regexp"
	"strings"

	"github.com/vietbui/chat-quality-agent/channels"
	"github.com/vietbui/chat-quality-agent/channels/zalotoken"
	"github.com/vietbui/chat-quality-agent/config"
	"github.com/vietbui/chat-quality-agent/customercases"
	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/db/models"
	"github.com/vietbui/chat-quality-agent/engine"
	"github.com/vietbui/chat-quality-agent/pkg"
	"golang.org/x/text/unicode/norm"
)

const personalZaloHandoverUnavailable = "Dạ, em chưa chuyển được tin này. Anh/chị vui lòng nhắn trực tiếp cho nhân viên phụ trách giúp em ạ."

var personalZaloHandoverPhrase = regexp.MustCompile(`(?:^|[^\pL\pN])(?:gặp|nói chuyện với|kết nối với|chuyển(?: tiếp)?(?: cho| sang| tới)?|nhờ|gọi) (?:em |mình |tôi |anh |chị )?(?:đến |cho |với )?(?:nhân viên|người hỗ trợ|tư vấn viên)(?:$|[^\pL\pN])`)

func personalZaloHandoverRequested(text string) bool {
	normalized := strings.Join(strings.Fields(strings.ToLower(norm.NFC.String(text))), " ")
	switch strings.Trim(normalized, " .!?") {
	case "nhân viên", "chuyển nhân viên", "gặp nhân viên", "nói chuyện với nhân viên":
		return true
	}
	return personalZaloHandoverPhrase.MatchString(normalized)
}

type personalZaloHandoverDestination struct {
	channel models.Channel
	id      string
	isGroup bool
}

func resolvePersonalZaloHandoverDestination(ctx context.Context, tenantID, groupID, staffID string) (personalZaloHandoverDestination, error) {
	var target personalZaloHandoverDestination
	if groupID != "" && staffID != "" {
		return target, errors.New("choose one handover recipient")
	}
	if groupID == "" && staffID == "" {
		return target, errors.New("handover recipient missing")
	}

	var oaID string
	if groupID != "" {
		var group models.CRMGroup
		err := db.DB.WithContext(ctx).
			Where("id = ? AND tenant_id = ? AND COALESCE(TRIM(channel_id), '') <> ''", groupID, tenantID).
			Where("COALESCE(TRIM(zalo_group_id), '') <> '' AND COALESCE(TRIM(customer_code), '') = ''").
			Where("NOT EXISTS (SELECT 1 FROM crm_group_customer_codes WHERE group_id = crm_groups.id)").
			Where("NOT EXISTS (SELECT 1 FROM crm_group_customers WHERE group_id = crm_groups.id)").
			Take(&group).Error
		if err != nil {
			return target, errors.New("choose an active internal CRM group linked to a Zalo OA")
		}
		oaID, target.id, target.isGroup = group.ChannelID, strings.TrimSpace(group.ZaloGroupID), true
	} else {
		var staff models.ZaloWhitelist
		err := db.DB.WithContext(ctx).
			Where("id = ? AND tenant_id = ? AND status = ?", staffID, tenantID, "active").
			Where("COALESCE(TRIM(zalo_user_id), '') <> ''").
			Take(&staff).Error
		if err != nil {
			return target, errors.New("choose an active Zalo whitelist employee")
		}
		oaID, target.id = staff.ChannelID, strings.TrimSpace(staff.ZaloUserID)
	}

	query := db.DB.WithContext(ctx).Where("tenant_id = ? AND channel_type = ? AND is_active = ?", tenantID, "zalo_oa", true)
	if oaID != "" {
		query = query.Where("id = ?", oaID)
	}
	var oaChannels []models.Channel
	if err := query.Limit(2).Find(&oaChannels).Error; err != nil || len(oaChannels) != 1 {
		return target, errors.New("handover recipient must resolve to one active Zalo OA")
	}
	target.channel = oaChannels[0]
	return target, nil
}

func sendPersonalZaloHandover(ctx context.Context, cfg *config.Config, channel models.Channel, req personalZaloChatbotRequest, observers ...engine.HandoverDeliveryObserver) string {
	var metadata struct {
		GroupID string `json:"handover_alert_group_id"`
		StaffID string `json:"personal_zalo_handover_staff_id"`
	}
	groupID, kind, zaloGroupID := "", "fallback", ""
	trace := func(status, errorCode string) {
		for _, observe := range observers {
			if observe != nil {
				observe(groupID, zaloGroupID, kind, status, errorCode)
			}
		}
	}
	if err := json.Unmarshal([]byte(channel.Metadata), &metadata); err != nil {
		trace("failed", "destination_unavailable")
		return personalZaloHandoverUnavailable
	}
	groupID, staffID := strings.TrimSpace(metadata.GroupID), strings.TrimSpace(metadata.StaffID)
	if staffID != "" && groupID == "" {
		kind = "fallback_staff"
	}
	if groupID == "" && staffID == "" {
		trace("failed", "alert_group_not_configured")
		return personalZaloHandoverUnavailable
	}
	target, err := resolvePersonalZaloHandoverDestination(ctx, channel.TenantID, groupID, staffID)
	if err != nil {
		log.Printf("[personal-zalo-handover] channel %s cannot resolve recipient: %v", channel.ID, err)
		trace("failed", "destination_unavailable")
		return personalZaloHandoverUnavailable
	}
	if target.isGroup {
		zaloGroupID = target.id
	}
	if (target.isGroup && req.ThreadType == "group" && target.id == req.ThreadID) ||
		(!target.isGroup && (target.id == req.SenderID || target.id == req.AccountExternalID)) {
		trace("failed", "alert_group_is_source_chat")
		return personalZaloHandoverUnavailable
	}

	raw, err := pkg.Decrypt(target.channel.CredentialsEncrypted, cfg.EncryptionKey)
	if err != nil {
		log.Printf("[personal-zalo-handover] cannot decrypt OA channel %s", target.channel.ID)
		trace("failed", "sender_unavailable")
		return personalZaloHandoverUnavailable
	}
	var credentials channels.ZaloOACredentials
	if err := json.Unmarshal(raw, &credentials); err != nil {
		log.Printf("[personal-zalo-handover] invalid credentials for OA channel %s", target.channel.ID)
		trace("failed", "sender_unavailable")
		return personalZaloHandoverUnavailable
	}
	adapter := zalotoken.NewAdapter(target.channel.ID, credentials, cfg.EncryptionKey)
	adapter.SetSentObserver(nil)

	alert := engine.HandoverAlert{
		TenantID: channel.TenantID, Channel: channel, ZaloUserID: req.SenderID, Message: req.Text,
	}
	if req.ThreadType == "group" {
		alert.ChatGroup = req.ThreadID
	}
	turnHash := sha256.Sum256([]byte(strings.Join([]string{req.AccountExternalID, req.ThreadType, req.ThreadID, req.MessageID}, "\x00")))
	onAttempt := func(err error) {
		if err != nil {
			trace("failed", "delivery_unconfirmed")
		} else {
			trace("sent", "")
		}
	}
	if engine.NotifyPersonalZaloHandover(ctx, adapter, alert, target.id, target.isGroup, hex.EncodeToString(turnHash[:]), onAttempt) {
		return "Dạ, em đã chuyển tin này cho nhân viên phụ trách hỗ trợ anh/chị ạ."
	}
	return personalZaloHandoverUnavailable
}

func validatePersonalZaloChatbotMetadata(ctx context.Context, tenantID string, metadata map[string]interface{}) error {
	textLimits := map[string]int{
		"session_keyword": 255, "session_end_keyword": 255, "group_bot_names": 255,
		"session_welcome_message": personalZaloTextLimit, "session_goodbye_message": personalZaloTextLimit,
		"session_timeout_message": personalZaloTextLimit,
		"handover_alert_group_id": 36, "personal_zalo_handover_staff_id": 36,
	}
	for key, limit := range textLimits {
		if value, exists := metadata[key]; exists {
			text, ok := value.(string)
			if !ok || personalZaloTextBoundary(text, limit) != len(text) {
				return fmt.Errorf("%s must be text up to %d characters", key, limit)
			}
		}
	}
	if value, exists := metadata["session_timeout_minutes"]; exists {
		minutes, ok := value.(float64)
		if !ok || minutes < 0 || minutes > 1440 || minutes != float64(int(minutes)) {
			return errors.New("session_timeout_minutes must be a whole number from 0 to 1440")
		}
	}
	for _, key := range []string{"personal_zalo_group_reply_enabled", "group_assistant_mode", "group_staff_call_only", "session_timeout_notice", customercases.MetaEnabled} {
		if value, exists := metadata[key]; exists {
			if _, ok := value.(bool); !ok {
				return fmt.Errorf("%s must be true or false", key)
			}
		}
	}
	open, _ := metadata["session_keyword"].(string)
	close, _ := metadata["session_end_keyword"].(string)
	for _, a := range strings.Split(open, ";") {
		a = strings.TrimSpace(norm.NFC.String(a))
		for _, b := range strings.Split(close, ";") {
			if a != "" && strings.EqualFold(a, strings.TrimSpace(norm.NFC.String(b))) {
				return errors.New("opening and closing session keywords must differ")
			}
		}
	}
	groupID, _ := metadata["handover_alert_group_id"].(string)
	staffID, _ := metadata["personal_zalo_handover_staff_id"].(string)
	groupID, staffID = strings.TrimSpace(groupID), strings.TrimSpace(staffID)
	if groupID != "" || staffID != "" {
		if _, err := resolvePersonalZaloHandoverDestination(ctx, tenantID, groupID, staffID); err != nil {
			return err
		}
	}
	if errKey, err := normalizeComplaintSettings(tenantID, metadata); err != nil {
		return fmt.Errorf("%s: %w", errKey, err)
	}
	complaints, _ := metadata[customercases.MetaEnabled].(bool)
	if !complaints {
		return nil
	}
	// Every complaint needs a reliable group fallback. The decision model may
	// fail to select a stream, and case alerts are group notifications even
	// when ordinary personal-Zalo handovers are configured for one employee.
	if groupID == "" {
		return errors.New("complaint cases require an internal handover alert group")
	}
	streams, _ := customercases.NormalizeStreams(metadata[customercases.MetaStreams])
	for _, stream := range streams {
		if stream.AlertGroupID == "" {
			continue
		}
		if _, err := resolvePersonalZaloHandoverDestination(ctx, tenantID, stream.AlertGroupID, ""); err != nil {
			return fmt.Errorf("complaint stream %s: %w", stream.Name, err)
		}
	}
	return nil
}
