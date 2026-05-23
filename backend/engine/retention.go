package engine

import (
	"log"
	"time"

	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/db/models"
)

// CleanupOldSyncLogs deletes sync-related activity logs older than the retention period.
// successDays: keep successful sync logs for this many days (e.g. 90)
// errorDays: keep error sync logs for this many days (e.g. 180)
func CleanupOldSyncLogs(successDays, errorDays int) {
	now := time.Now()

	// Delete successful sync logs older than successDays
	successCutoff := now.AddDate(0, 0, -successDays)
	result := db.DB.Where(
		"(action = 'sync.completed' OR action = 'import.personal_zalo') AND created_at < ?",
		successCutoff,
	).Delete(&models.ActivityLog{})
	successDeleted := result.RowsAffected

	// Delete error sync logs older than errorDays
	errorCutoff := now.AddDate(0, 0, -errorDays)
	result = db.DB.Where(
		"(action = 'sync.error' OR action = 'import.personal_zalo.error') AND created_at < ?",
		errorCutoff,
	).Delete(&models.ActivityLog{})
	errorDeleted := result.RowsAffected

	if successDeleted > 0 || errorDeleted > 0 {
		log.Printf("[retention] cleaned up sync logs: %d success (>%dd), %d error (>%dd)",
			successDeleted, successDays, errorDeleted, errorDays)
	}
}
