package handlers

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/vietbui/chat-quality-agent/config"
	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/db/models"
	"github.com/vietbui/chat-quality-agent/engine"
	"golang.org/x/text/unicode/norm"
)

const (
	personalZaloThreadUser       = "user"
	personalZaloThreadGroup      = "group"
	personalZaloAIHandoverMarker = engine.HumanReviewMarker
)

type personalZaloSessionConfig struct {
	SessionKeyword        string `json:"session_keyword"`
	SessionEndKeyword     string `json:"session_end_keyword"`
	SessionWelcomeMessage string `json:"session_welcome_message"`
	SessionGoodbyeMessage string `json:"session_goodbye_message"`
	SessionTimeoutMinutes int    `json:"session_timeout_minutes"`
	Managed               bool   `json:"-"`
	GroupBotNames         string `json:"group_bot_names"`
	GroupAssistantMode    bool   `json:"group_assistant_mode"`
	GroupStaffCallOnly    bool   `json:"group_staff_call_only"`
}

type personalZaloTurn struct {
	FlowScope string
	Reply     string
	RunFlow   bool
}

func normalizePersonalZaloChatbotRequest(req *personalZaloChatbotRequest) bool {
	if req.ThreadType == "" {
		req.ThreadType = personalZaloThreadUser
	}
	if req.SenderID == "" && req.ThreadType == personalZaloThreadUser {
		req.SenderID = req.ThreadID
	}
	if req.ThreadType != personalZaloThreadUser && req.ThreadType != personalZaloThreadGroup {
		return false
	}
	if req.ThreadType == personalZaloThreadGroup && req.SchemaVersion != "personal-zalo-chatbot-v2" {
		return false
	}
	if !personalZaloUIDPattern.MatchString(req.ThreadID) || !personalZaloUIDPattern.MatchString(req.SenderID) ||
		req.SenderID == req.AccountExternalID {
		return false
	}
	if req.ThreadType == personalZaloThreadUser {
		return req.SenderID == req.ThreadID
	}
	return req.SenderID != req.ThreadID && req.ThreadID != req.AccountExternalID
}

func parsePersonalZaloSessionConfig(channel models.Channel, cfg *config.Config) (personalZaloSessionConfig, error) {
	var session personalZaloSessionConfig
	if strings.TrimSpace(channel.Metadata) != "" {
		if err := json.Unmarshal([]byte(channel.Metadata), &session); err != nil {
			return session, err
		}
	}
	session.Managed = strings.Trim(session.SessionKeyword, "; \t\n\r") != "" ||
		strings.Trim(session.SessionEndKeyword, "; \t\n\r") != "" || session.SessionTimeoutMinutes > 0
	if strings.TrimSpace(session.SessionEndKeyword) == "" {
		session.SessionEndKeyword = cfg.ChatbotSessionEndKeyword
	}
	if strings.TrimSpace(session.SessionWelcomeMessage) == "" {
		session.SessionWelcomeMessage = cfg.ChatbotSessionWelcomeMessage
	}
	if strings.TrimSpace(session.SessionGoodbyeMessage) == "" {
		session.SessionGoodbyeMessage = cfg.ChatbotSessionGoodbyeMessage
	}
	if session.SessionTimeoutMinutes <= 0 {
		session.SessionTimeoutMinutes = cfg.ChatbotSessionTimeout
	}
	if session.SessionTimeoutMinutes <= 0 || session.SessionTimeoutMinutes > 1440 {
		session.SessionTimeoutMinutes = 30
	}
	return session, nil
}

func personalZaloMatchesKeyword(text, keywords string) bool {
	text = norm.NFC.String(strings.TrimSpace(text))
	for _, keyword := range strings.Split(keywords, ";") {
		if keyword = norm.NFC.String(strings.TrimSpace(keyword)); keyword != "" && strings.EqualFold(text, keyword) {
			return true
		}
	}
	return false
}

func personalZaloCallsBot(text, configuredNames string) bool {
	names := strings.Split(configuredNames, ";")
	if strings.Trim(configuredNames, "; \t\n\r") == "" {
		names = []string{"bot"}
	}
	lower := strings.ToLower(norm.NFC.String(strings.TrimSpace(text)))
	for _, name := range names {
		name = strings.ToLower(norm.NFC.String(strings.TrimSpace(name)))
		if name != "" && personalZaloContainsWholePhrase(lower, name) {
			return true
		}
	}
	return false
}

func personalZaloGroupMessageAddressed(req personalZaloChatbotRequest, session personalZaloSessionConfig) bool {
	if req.BotMentioned || personalZaloMatchesKeyword(req.Text, session.SessionKeyword) ||
		personalZaloMatchesKeyword(req.Text, session.SessionEndKeyword) {
		return true
	}
	text := req.Text
	if req.GroupWakeText != nil {
		text = *req.GroupWakeText
	}
	return personalZaloCallsBot(text, session.GroupBotNames)
}

func personalZaloHasHandoverDestination(channel models.Channel) bool {
	var metadata struct {
		AlertGroupID string `json:"handover_alert_group_id"`
		StaffID      string `json:"personal_zalo_handover_staff_id"`
	}
	return json.Unmarshal([]byte(channel.Metadata), &metadata) == nil &&
		(strings.TrimSpace(metadata.AlertGroupID) != "" || strings.TrimSpace(metadata.StaffID) != "")
}

// personalZaloHandoverPrompt is the tenant's handover rule (task prompt
// personal_zalo_handover), or "" when the channel has nowhere to hand over.
func personalZaloHandoverPrompt(channel models.Channel) string {
	if !personalZaloHasHandoverDestination(channel) {
		return ""
	}
	return engine.TaskPromptText(channel.TenantID, engine.PromptPZHandover)
}

func personalZaloStripHandoverMarker(text string) string {
	return strings.TrimSpace(strings.ReplaceAll(text, personalZaloAIHandoverMarker, ""))
}

func personalZaloContainsWholePhrase(text, phrase string) bool {
	for offset := 0; offset <= len(text)-len(phrase); {
		index := strings.Index(text[offset:], phrase)
		if index < 0 {
			return false
		}
		start := offset + index
		end := start + len(phrase)
		if !personalZaloWordBefore(text, start) && !personalZaloWordAfter(text, end) {
			return true
		}
		_, size := utf8.DecodeRuneInString(text[start:])
		offset = start + size
	}
	return false
}

func personalZaloWordBefore(text string, index int) bool {
	if index == 0 {
		return false
	}
	r, _ := utf8.DecodeLastRuneInString(text[:index])
	return personalZaloWordRune(r)
}

func personalZaloWordAfter(text string, index int) bool {
	if index >= len(text) {
		return false
	}
	for _, r := range text[index:] {
		return personalZaloWordRune(r)
	}
	return false
}

func personalZaloWordRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.Is(unicode.Mn, r)
}

func personalZaloLegacyFlowScope(req personalZaloChatbotRequest) string {
	digest := sha256.Sum256([]byte(strings.Join([]string{req.TenantID, req.ChannelID, req.AccountExternalID, req.ThreadID}, "\x00")))
	return "personal-zalo-" + hex.EncodeToString(digest[:])
}

func personalZaloSessionKey(req personalZaloChatbotRequest) string {
	digest := sha256.Sum256([]byte(strings.Join([]string{req.TenantID, req.ChannelID, req.AccountExternalID, req.ThreadType, req.ThreadID, req.SenderID}, "\x00")))
	return "personal_zalo_session:" + hex.EncodeToString(digest[:])
}

func personalZaloSessionFlowScope(req personalZaloChatbotRequest, sessionID string) string {
	digest := sha256.Sum256([]byte(strings.Join([]string{req.TenantID, req.ChannelID, req.AccountExternalID, req.ThreadType, req.ThreadID, req.SenderID, sessionID}, "\x00")))
	return "personal-zalo-" + hex.EncodeToString(digest[:])
}

func planPersonalZaloTurn(ctx context.Context, session personalZaloSessionConfig, req personalZaloChatbotRequest, groupAssistantReply bool) (personalZaloTurn, error) {
	group := req.ThreadType == personalZaloThreadGroup
	if group && !groupAssistantReply && !personalZaloGroupMessageAddressed(req, session) {
		return personalZaloTurn{}, nil
	}
	if !group && !session.Managed {
		return personalZaloTurn{FlowScope: personalZaloLegacyFlowScope(req), RunFlow: true}, nil
	}
	if db.RedisClient == nil {
		return personalZaloTurn{}, errors.New("Redis is required for configured personal Zalo sessions")
	}

	key := personalZaloSessionKey(req)
	ttl := time.Duration(session.SessionTimeoutMinutes) * time.Minute
	if personalZaloMatchesKeyword(req.Text, session.SessionEndKeyword) {
		_, err := db.RedisClient.GetDel(ctx, key).Result()
		if errors.Is(err, redis.Nil) {
			return personalZaloTurn{}, nil
		}
		if err != nil {
			return personalZaloTurn{}, err
		}
		return personalZaloTurn{Reply: session.SessionGoodbyeMessage}, nil
	}
	if personalZaloMatchesKeyword(req.Text, session.SessionKeyword) {
		sessionID, err := replacePersonalZaloSession(ctx, key, ttl)
		if err != nil {
			return personalZaloTurn{}, err
		}
		return personalZaloTurn{FlowScope: personalZaloSessionFlowScope(req, sessionID), Reply: session.SessionWelcomeMessage}, nil
	}
	active, err := db.RedisClient.GetEx(ctx, key, ttl).Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		return personalZaloTurn{}, err
	}
	if active == "" {
		if !group && strings.Trim(session.SessionKeyword, "; \t\n\r") != "" {
			return personalZaloTurn{}, nil
		}
		active, err = replacePersonalZaloSession(ctx, key, ttl)
		if err != nil {
			return personalZaloTurn{}, err
		}
	}
	flowScope := personalZaloSessionFlowScope(req, active)
	if group && req.BotMentioned && req.Text == "" {
		return personalZaloTurn{FlowScope: flowScope, Reply: session.SessionWelcomeMessage}, nil
	}
	return personalZaloTurn{FlowScope: flowScope, RunFlow: true}, nil
}

func closePersonalZaloSession(ctx context.Context, req personalZaloChatbotRequest) {
	if db.RedisClient != nil {
		_ = db.RedisClient.Del(ctx, personalZaloSessionKey(req)).Err()
	}
}

func replacePersonalZaloSession(ctx context.Context, key string, ttl time.Duration) (string, error) {
	sessionID := uuid.NewString()
	if err := db.RedisClient.Set(ctx, key, sessionID, ttl).Err(); err != nil {
		return "", fmt.Errorf("store personal Zalo session: %w", err)
	}
	return sessionID, nil
}
