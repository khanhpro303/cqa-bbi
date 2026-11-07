package messengerlabels

import (
	"time"

	"github.com/vietbui/chat-quality-agent/db/models"
	"gorm.io/gorm"
)

// FailedReadRetryAfter is how long a person-level label read that Meta refused
// (unsupported object or unusable response) is not repeated, unless the
// conversation gets a new message. Production data on 2026-11-07 showed 2,796
// of 2,799 reads refused with Graph code #100 under Standard Access; repeating
// them on every 15-minute message sync spent hundreds of thousands of Graph
// calls a day on the same Page token that message sync needs.
const FailedReadRetryAfter = 24 * time.Hour

// recentlyRefusedConversationIDs returns conversations whose last label read
// was refused recently and that have no message after that attempt.
func recentlyRefusedConversationIDs(database *gorm.DB, channel models.Channel, now time.Time) (map[string]bool, error) {
	var ids []string
	err := database.Table("messenger_label_snapshots AS s").
		Joins("JOIN conversations AS c ON c.id = s.conversation_id").
		Where("s.tenant_id = ? AND s.channel_id = ? AND s.status = ? AND s.error_kind IN ? AND s.attempted_at > ?",
			channel.TenantID, channel.ID, "error", []string{"unsupported", "invalid_response"}, now.Add(-FailedReadRetryAfter)).
		Where("c.last_message_at IS NULL OR c.last_message_at <= s.attempted_at").
		Pluck("s.conversation_id", &ids).Error
	if err != nil {
		return nil, err
	}
	result := make(map[string]bool, len(ids))
	for _, id := range ids {
		result[id] = true
	}
	return result, nil
}

func withoutRefused(convs []models.Conversation, refused map[string]bool) []models.Conversation {
	if len(refused) == 0 {
		return convs
	}
	kept := make([]models.Conversation, 0, len(convs))
	for _, conv := range convs {
		if !refused[conv.ID] {
			kept = append(kept, conv)
		}
	}
	return kept
}
