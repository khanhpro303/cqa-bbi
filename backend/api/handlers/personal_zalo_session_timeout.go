package handlers

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/hibiken/asynq"
	"github.com/redis/go-redis/v9"
	"github.com/vietbui/chat-quality-agent/config"
	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/workers"
)

// The idle-timeout task fires at the timeout; the session key lives one
// minute longer. A key with more than this left was used again since.
const personalZaloTimeoutGrace = 65 * time.Second

// closeIdlePersonalZaloSession deletes the session key only when it still
// holds this session and nobody has used it since the task was scheduled.
// 1 = closed now, 0 = already gone or replaced, -1 = still in use.
var closeIdlePersonalZaloSession = redis.NewScript(`
if redis.call('GET', KEYS[1]) ~= ARGV[1] then
	return 0
end
if redis.call('PTTL', KEYS[1]) > tonumber(ARGV[2]) then
	return -1
end
redis.call('DEL', KEYS[1])
return 1
`)

// personalZaloTimeoutNoticeWanted reports whether this turn's session should
// announce its idle close. 1:1 chats follow the channel switch. In a group
// the session belongs to one sender, so only a customer's session announces
// it, never in group assistant mode (where the bot never announces sessions,
// as on Zalo OA) and never a staff member's session.
func personalZaloTimeoutNoticeWanted(session personalZaloSessionConfig, req personalZaloChatbotRequest, turn personalZaloTurn, staff bool) bool {
	if !session.timeoutNoticeOn() || turn.SessionKey == "" || turn.SessionID == "" {
		return false
	}
	if req.ThreadType == personalZaloThreadGroup {
		return !session.GroupAssistantMode && !staff
	}
	return req.ThreadType == personalZaloThreadUser
}

func schedulePersonalZaloSessionTimeout(client *asynq.Client, session personalZaloSessionConfig, req personalZaloChatbotRequest, turn personalZaloTurn) {
	if client == nil {
		return
	}
	task, err := workers.NewPersonalZaloSessionTimeoutTask(workers.PersonalZaloSessionTimeoutPayload{
		TenantID: req.TenantID, ChannelID: req.ChannelID, AccountExternalID: req.AccountExternalID,
		ThreadType: req.ThreadType, ThreadID: req.ThreadID, SenderID: req.SenderID,
		SessionKey: turn.SessionKey, SessionID: turn.SessionID, TimeoutMinutes: session.SessionTimeoutMinutes,
	})
	if err != nil {
		return
	}
	if _, err := client.Enqueue(task, asynq.ProcessIn(time.Duration(session.SessionTimeoutMinutes)*time.Minute)); err != nil {
		log.Printf("[personal-zalo] channel %s: cannot schedule session timeout: %v", req.ChannelID, err)
	}
}

// HandlePersonalZaloSessionTimeoutTask closes an idle personal Zalo session
// and, when the channel still wants it, sends the timeout message through the
// gateway. Sessions ended by keyword, handover or a newer session are left
// alone, like the Zalo OA timeout.
func HandlePersonalZaloSessionTimeoutTask(cfg *config.Config) asynq.HandlerFunc {
	return func(ctx context.Context, t *asynq.Task) error {
		var payload workers.PersonalZaloSessionTimeoutPayload
		if err := json.Unmarshal(t.Payload(), &payload); err != nil {
			return fmt.Errorf("json.Unmarshal failed: %v: %w", err, asynq.SkipRetry)
		}
		if db.RedisClient == nil || payload.SessionKey == "" || payload.SessionID == "" {
			return nil
		}
		closed, err := closeIdlePersonalZaloSession.Run(ctx, db.RedisClient, []string{payload.SessionKey},
			payload.SessionID, personalZaloTimeoutGrace.Milliseconds()).Int()
		if err != nil || closed != 1 {
			return nil
		}
		text, err := personalZaloTimeoutNoticeText(ctx, cfg, payload)
		if err != nil {
			log.Printf("[personal-zalo] channel %s: session closed after idle timeout, no message: %v", payload.ChannelID, err)
			return nil
		}
		if text == "" {
			return nil
		}
		if payload.ThreadType == personalZaloThreadGroup && !claimPersonalZaloGroupTimeoutNotice(ctx, payload) {
			// Another member's session already said goodbye to this group.
			return nil
		}
		status, err := sendPersonalZaloSessionNotice(ctx, cfg, payload, text)
		if err != nil {
			log.Printf("[personal-zalo] channel %s: session timeout message not sent: %v", payload.ChannelID, err)
			return nil
		}
		log.Printf("[personal-zalo] channel %s: closed idle session, timeout message %s", payload.ChannelID, status)
		return nil
	}
}

// personalZaloTimeoutNoticeText rechecks the channel as the reply path does
// and returns the message to send, or "" when the channel no longer wants it.
func personalZaloTimeoutNoticeText(ctx context.Context, cfg *config.Config, payload workers.PersonalZaloSessionTimeoutPayload) (string, error) {
	req := personalZaloChatbotRequest{
		TenantID: payload.TenantID, ChannelID: payload.ChannelID, AccountExternalID: payload.AccountExternalID,
		ThreadType: payload.ThreadType, ThreadID: payload.ThreadID, SenderID: payload.SenderID, SentAt: time.Now(),
	}
	channel, enabled, err := personalZaloChatbotChannel(ctx, req)
	if err != nil || !enabled || channel.ExternalID != req.AccountExternalID {
		return "", err
	}
	settings, err := loadPersonalZaloChatbotSettings(ctx, req.TenantID, "chatbot_active")
	if err != nil || !personalZaloChatbotMasterEnabled(settings) {
		return "", err
	}
	session, err := parsePersonalZaloSessionConfig(channel, cfg)
	if err != nil {
		return "", err
	}
	if !session.timeoutNoticeOn() || (req.ThreadType == personalZaloThreadGroup && session.GroupAssistantMode) {
		return "", nil
	}
	approved, err := personalZaloThreadApproved(ctx, req)
	if err != nil || !approved {
		return "", err
	}
	text := strings.TrimSpace(session.SessionTimeoutMessage)
	if personalZaloTextBoundary(text, personalZaloTextLimit) < len(text) {
		text = strings.TrimSpace(text[:personalZaloTextBoundary(text, personalZaloTextLimit-1)]) + "…"
	}
	return text, nil
}

// claimPersonalZaloGroupTimeoutNotice lets one timeout message reach a group
// per timeout window, however many members had a session with the bot.
func claimPersonalZaloGroupTimeoutNotice(ctx context.Context, payload workers.PersonalZaloSessionTimeoutPayload) bool {
	digest := sha256.Sum256([]byte(strings.Join([]string{payload.TenantID, payload.ChannelID, payload.AccountExternalID, payload.ThreadID}, "\x00")))
	window := time.Duration(payload.TimeoutMinutes) * time.Minute
	if window <= 0 {
		window = 30 * time.Minute
	}
	claimed, err := db.RedisClient.SetNX(ctx, "personal_zalo_timeout_notice:"+hex.EncodeToString(digest[:]), "1", window).Result()
	return err == nil && claimed
}

// sendPersonalZaloSessionNotice asks the gateway to post text in the chat.
// The gateway skips it when the chat is busy again or staff have answered by
// hand since the session went idle.
func sendPersonalZaloSessionNotice(ctx context.Context, cfg *config.Config, payload workers.PersonalZaloSessionTimeoutPayload, text string) (string, error) {
	if cfg.PersonalZaloGatewayBaseURL == "" {
		return "", errors.New("PERSONAL_ZALO_GATEWAY_BASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	accounts, err := listPersonalZaloGatewayAccountsContext(ctx, cfg.PersonalZaloGatewayBaseURL)
	if err != nil {
		return "", err
	}
	account := findPersonalZaloGatewayAccount(accounts, payload.TenantID, payload.ChannelID)
	if account == nil || account.AccountExternalID != payload.AccountExternalID {
		return "", errors.New("no gateway account for this channel")
	}
	idleSince := time.Now().Add(-time.Duration(payload.TimeoutMinutes) * time.Minute)
	var resp struct {
		Status string `json:"status"`
	}
	err = personalZaloGatewayJSONContext(ctx, cfg.PersonalZaloGatewayBaseURL, http.MethodPost,
		"/api/v1/accounts/"+url.PathEscape(account.ID)+"/chatbot/session-notice", map[string]string{
			"threadType": payload.ThreadType,
			"threadId":   payload.ThreadID,
			"text":       text,
			"idleSince":  idleSince.UTC().Format(time.RFC3339),
		}, &resp)
	if err != nil {
		return "", err
	}
	return resp.Status, nil
}

// GetChannelSessionDefaults returns the session texts a Zalo channel uses
// when its own field is empty, so the channel dialog can show them.
func GetChannelSessionDefaults(cfg *config.Config) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"session_keyword":         cfg.ChatbotSessionKeyword,
			"session_end_keyword":     cfg.ChatbotSessionEndKeyword,
			"session_welcome_message": cfg.ChatbotSessionWelcomeMessage,
			"session_goodbye_message": cfg.ChatbotSessionGoodbyeMessage,
			"session_timeout_minutes": cfg.ChatbotSessionTimeout,
			"session_timeout_message": cfg.ChatbotSessionTimeoutMessage,
		})
	}
}
