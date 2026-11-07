package messengerjourney

import (
	"context"
	"encoding/json"
	"time"

	"github.com/vietbui/chat-quality-agent/db/models"
	"gorm.io/gorm"
)

// MessageCursor identifies exactly which stored messages an analysis covered.
// Coverage is complete only while this cursor still equals the conversation's
// current messages; any new, late or deleted message makes it stale.
type MessageCursor struct {
	ConversationID      string     `gorm:"column:conversation_id"`
	SourceLastMessageAt *time.Time `gorm:"column:source_last_message_at"`
	SourceLastMessageID string     `gorm:"column:source_last_message_id"`
	SourceMessageCount  int64      `gorm:"column:source_message_count"`
	// LastStoredAt is when CQA stored the newest of these messages. It is not
	// part of coverage identity; it proves an earlier analysis could read them.
	LastStoredAt *time.Time `gorm:"column:last_stored_at"`
}

// LoadMessageCursors returns the current cursor for every requested
// conversation. A conversation without messages gets a zero-count cursor.
func LoadMessageCursors(ctx context.Context, database *gorm.DB, tenantID string, conversationIDs []string) (map[string]MessageCursor, error) {
	result := make(map[string]MessageCursor, len(conversationIDs))
	for _, conversationID := range conversationIDs {
		result[conversationID] = MessageCursor{ConversationID: conversationID}
	}
	if len(conversationIDs) == 0 {
		return result, nil
	}
	for start := 0; start < len(conversationIDs); start += 500 {
		end := start + 500
		if end > len(conversationIDs) {
			end = len(conversationIDs)
		}
		var cursors []MessageCursor
		err := database.WithContext(ctx).Raw(`
		SELECT m.conversation_id,
		       COUNT(*) AS source_message_count,
		       MAX(m.sent_at) AS source_last_message_at,
		       MAX(m.created_at) AS last_stored_at,
		       (SELECT m2.id FROM messages m2
		        WHERE m2.tenant_id = ? AND m2.conversation_id = m.conversation_id
		        ORDER BY m2.sent_at DESC, m2.id DESC LIMIT 1) AS source_last_message_id
		FROM messages m
		WHERE m.tenant_id = ? AND m.conversation_id IN ?
		GROUP BY m.conversation_id`, tenantID, tenantID, conversationIDs[start:end]).Scan(&cursors).Error
		if err != nil {
			return nil, err
		}
		for _, cursor := range cursors {
			result[cursor.ConversationID] = cursor
		}
	}
	return result, nil
}

// CoverageCursor reads the message cursor stored in a coverage projection row.
func CoverageCursor(item models.MessengerJourneyCoverage) (MessageCursor, bool) {
	var payload struct {
		SourceLastMessageAt *time.Time `json:"source_last_message_at"`
		SourceLastMessageID string     `json:"source_last_message_id"`
		SourceMessageCount  *int64     `json:"source_message_count"`
	}
	if json.Unmarshal([]byte(item.Payload), &payload) != nil || payload.SourceMessageCount == nil || *payload.SourceMessageCount < 0 {
		return MessageCursor{}, false
	}
	if *payload.SourceMessageCount > 0 && (payload.SourceLastMessageAt == nil || payload.SourceLastMessageID == "") {
		return MessageCursor{}, false
	}
	if payload.SourceLastMessageAt != nil {
		value := payload.SourceLastMessageAt.UTC()
		payload.SourceLastMessageAt = &value
	}
	return MessageCursor{
		ConversationID: item.ConversationID, SourceLastMessageAt: payload.SourceLastMessageAt,
		SourceLastMessageID: payload.SourceLastMessageID, SourceMessageCount: *payload.SourceMessageCount,
	}, true
}

// CursorsMatch compares the coverage identity of two cursors.
func CursorsMatch(coverage, current MessageCursor) bool {
	if coverage.SourceMessageCount != current.SourceMessageCount || coverage.SourceLastMessageID != current.SourceLastMessageID {
		return false
	}
	if coverage.SourceLastMessageAt == nil || current.SourceLastMessageAt == nil {
		return coverage.SourceLastMessageAt == nil && current.SourceLastMessageAt == nil
	}
	return coverage.SourceLastMessageAt.Equal(*current.SourceLastMessageAt)
}

// CoverageComplete reports whether a projection row proves analysis of the
// conversation's current messages.
func CoverageComplete(item models.MessengerJourneyCoverage, exists bool, current MessageCursor) bool {
	if !exists || item.Status != "complete" {
		return false
	}
	covered, valid := CoverageCursor(item)
	return valid && CursorsMatch(covered, current)
}
