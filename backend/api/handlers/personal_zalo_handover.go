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

func sendPersonalZaloHandover(ctx context.Context, cfg *config.Config, channel models.Channel, req personalZaloChatbotRequest) string {
	var metadata struct {
		GroupID string `json:"handover_alert_group_id"`
		StaffID string `json:"personal_zalo_handover_staff_id"`
	}
	if err := json.Unmarshal([]byte(channel.Metadata), &metadata); err != nil {
		return personalZaloHandoverUnavailable
	}
	groupID, staffID := strings.TrimSpace(metadata.GroupID), strings.TrimSpace(metadata.StaffID)
	if groupID == "" && staffID == "" {
		return personalZaloHandoverUnavailable
	}
	target, err := resolvePersonalZaloHandoverDestination(ctx, channel.TenantID, groupID, staffID)
	if err != nil {
		log.Printf("[personal-zalo-handover] channel %s cannot resolve recipient: %v", channel.ID, err)
		return personalZaloHandoverUnavailable
	}
	if (target.isGroup && req.ThreadType == "group" && target.id == req.ThreadID) ||
		(!target.isGroup && (target.id == req.SenderID || target.id == req.AccountExternalID)) {
		return personalZaloHandoverUnavailable
	}

	raw, err := pkg.Decrypt(target.channel.CredentialsEncrypted, cfg.EncryptionKey)
	if err != nil {
		log.Printf("[personal-zalo-handover] cannot decrypt OA channel %s", target.channel.ID)
		return personalZaloHandoverUnavailable
	}
	var credentials channels.ZaloOACredentials
	if err := json.Unmarshal(raw, &credentials); err != nil {
		log.Printf("[personal-zalo-handover] invalid credentials for OA channel %s", target.channel.ID)
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
	if engine.NotifyPersonalZaloHandover(ctx, adapter, alert, target.id, target.isGroup, hex.EncodeToString(turnHash[:])) {
		return "Dạ, em đã chuyển tin này cho nhân viên phụ trách hỗ trợ anh/chị ạ."
	}
	return personalZaloHandoverUnavailable
}

func validatePersonalZaloChatbotMetadata(ctx context.Context, tenantID string, metadata map[string]interface{}) error {
	textLimits := map[string]int{
		"session_keyword": 255, "session_end_keyword": 255, "group_bot_names": 255,
		"session_welcome_message": personalZaloTextLimit, "session_goodbye_message": personalZaloTextLimit,
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
	for _, key := range []string{"personal_zalo_group_reply_enabled", "group_assistant_mode", "group_staff_call_only"} {
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
		_, err := resolvePersonalZaloHandoverDestination(ctx, tenantID, groupID, staffID)
		return err
	}
	return nil
}
