package chatmemory

import (
	"context"
	"log"
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

// recentRow is the projection RecentTurns reads: no raw webhook payloads.
type recentRow struct {
	ExternalMessageID string
	SenderType        string
	Content           string
	Source            string
}

// RecentTurns returns the last messages of the session before the message
// being handled now, oldest first. Only messages recorded live under this
// session id are read, so a previous session of the same chat never leaks in.
//
// "Before" is decided by created_at, the server clock at which each row was
// written, not by sent_at: the customer's sent_at is Zalo's clock and the
// bot's is ours, so comparing them could hide the bot question a quick
// "đúng rồi" answers. The current message (stored by RecordInbound before
// classification) is the cursor when its Zalo id is known, so a retried task
// does not see what was written after it; without an id the cursor is now and
// the current message is dropped by its text. It returns nil when chat memory
// is off or nothing is found; callers then classify without context.
func RecentTurns(ctx context.Context, sessionID, currentMsgID, currentText string, limit int) []RecentTurn {
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
		Where("id = ?", sessionID).Take(&sess).Error; err != nil || sess.ConversationID == "" {
		// A session is recorded on its first message, so a miss is rare.
		if err != nil {
			log.Printf("[chat-memory] recent turns: session %s: %v", sessionID, err)
		}
		return nil
	}
	q := db.DB.WithContext(ctx).Model(&models.Message{}).
		Where("tenant_id = ? AND conversation_id = ?", sess.TenantID, sess.ConversationID).
		Where("JSON_UNQUOTE(JSON_EXTRACT(raw_data, '$.cqa_session_id')) = ?", sessionID)
	var current models.Message
	if currentMsgID != "" && db.DB.WithContext(ctx).Select("id, created_at").
		Where("tenant_id = ? AND conversation_id = ? AND external_message_id = ?", sess.TenantID, sess.ConversationID, currentMsgID).
		Take(&current).Error == nil {
		q = q.Where("created_at < ?", current.CreatedAt)
	} else {
		q = q.Where("created_at <= ?", time.Now())
	}
	var rows []recentRow
	err := q.Select("external_message_id, sender_type, content, " +
		"COALESCE(JSON_UNQUOTE(JSON_EXTRACT(raw_data, '$.cqa_source')), '') AS source").
		Order("created_at DESC, id DESC").Limit(limit + 1).Scan(&rows).Error
	if err != nil {
		log.Printf("[chat-memory] recent turns: session %s: %v", sessionID, err)
		return nil
	}
	return recentTurnsFrom(rows, currentMsgID, currentText, limit)
}

// recentTurnsFrom turns newest-first rows into an oldest-first excerpt,
// without the message being classified and within the size limits.
func recentTurnsFrom(newestFirst []recentRow, currentMsgID, currentText string, limit int) []RecentTurn {
	rows := make([]recentRow, 0, len(newestFirst))
	droppedCurrent := false
	for _, r := range newestFirst {
		if currentMsgID != "" && r.ExternalMessageID == currentMsgID {
			continue
		}
		// Without a Zalo id, drop the newest customer message with the same
		// text: that is the message RecordInbound just stored.
		if currentMsgID == "" && !droppedCurrent && r.SenderType == "customer" &&
			strings.TrimSpace(r.Content) == strings.TrimSpace(currentText) {
			droppedCurrent = true
			continue
		}
		rows = append(rows, r)
	}
	if len(rows) > limit {
		rows = rows[:limit]
	}
	turns := make([]RecentTurn, 0, len(rows))
	total := 0
	for _, r := range rows {
		text := strings.Join(strings.Fields(r.Content), " ")
		if text == "" {
			continue
		}
		text = truncate(text, recentTurnChars)
		if total+len(text) > recentTurnsMaxChars {
			break
		}
		total += len(text)
		speaker := "Khách"
		if r.SenderType != "customer" {
			speaker = "Nhân viên"
			if r.Source == "chatbot" {
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
