package chatmemory

import (
	"context"
	"strings"
	"time"

	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/db/models"
)

// Limits for the recent-turns excerpt handed to the intent classifier. A few
// turns are enough to tell "đúng rồi" answering the bot's question from small
// talk; more only adds cost and latency to every message.
const (
	recentTurnsDefault  = 6
	recentTurnChars     = 400
	recentTurnsMaxChars = 2000
	recentTurnsTimeout  = 2 * time.Second
)

// RecentTurn is one message of the running session, labelled by speaker.
type RecentTurn struct {
	Speaker string // "Khách", "Bot" or "Nhân viên"
	Text    string
}

// RecentTurns returns the last messages of the session before the message
// being handled now, oldest first. currentText is that message: RecordInbound
// stores it before the worker classifies it, so it is dropped here when it is
// the newest customer message. It returns nil when chat memory is off, the
// session is unknown or the lookup fails: callers then classify without
// context, as before.
func RecentTurns(ctx context.Context, sessionID, currentText string, limit int) []RecentTurn {
	if !Enabled() || db.DB == nil || strings.TrimSpace(sessionID) == "" {
		return nil
	}
	if limit <= 0 {
		limit = recentTurnsDefault
	}
	ctx, cancel := context.WithTimeout(ctx, recentTurnsTimeout)
	defer cancel()

	var sess models.ChatSession
	if err := db.DB.WithContext(ctx).
		Select("id, tenant_id, conversation_id, started_at").
		Where("id = ?", sessionID).First(&sess).Error; err != nil || sess.ConversationID == "" {
		return nil
	}
	var msgs []models.Message
	if err := db.DB.WithContext(ctx).
		Select("id, sender_type, sender_name, content, content_type, sent_at, raw_data").
		Where("tenant_id = ? AND conversation_id = ? AND sent_at >= ?",
			sess.TenantID, sess.ConversationID, sess.StartedAt.Add(-time.Minute)).
		Order("sent_at DESC, id DESC").Limit(limit + 1).Find(&msgs).Error; err != nil {
		return nil
	}
	return recentTurnsFrom(msgs, currentText, limit)
}

// recentTurnsFrom turns newest-first messages into an oldest-first excerpt,
// without the message being classified and within the size limits.
func recentTurnsFrom(newestFirst []models.Message, currentText string, limit int) []RecentTurn {
	msgs := newestFirst
	if len(msgs) > 0 && msgs[0].SenderType == "customer" &&
		strings.TrimSpace(msgs[0].Content) == strings.TrimSpace(currentText) {
		msgs = msgs[1:]
	}
	if len(msgs) > limit {
		msgs = msgs[:limit]
	}
	turns := make([]RecentTurn, 0, len(msgs))
	total := 0
	for _, m := range msgs {
		text := strings.Join(strings.Fields(m.Content), " ")
		if text == "" {
			continue
		}
		text = truncate(text, recentTurnChars)
		if total+len(text) > recentTurnsMaxChars {
			break
		}
		total += len(text)
		speaker := "Khách"
		if m.SenderType != "customer" {
			speaker = "Nhân viên"
			if IsBotMessage(m) {
				speaker = "Bot"
			}
		}
		turns = append(turns, RecentTurn{Speaker: speaker, Text: text})
	}
	for i, j := 0, len(turns)-1; i < j; i, j = i+1, j-1 {
		turns[i], turns[j] = turns[j], turns[i]
	}
	return turns
}
