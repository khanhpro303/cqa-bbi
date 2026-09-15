package messengerlabels

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/vietbui/chat-quality-agent/channels"
	"github.com/vietbui/chat-quality-agent/db/models"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const syncLeaseDuration = 5 * time.Minute
const syncBatchSize = 100

// Bound full sweeps across Pages in this process. The database lease additionally
// prevents duplicate work on the same Page across server replicas.
var pageSyncSlots = make(chan struct{}, 2)
var errLeaseLost = errors.New("messenger label sync lease lost")

func completedSync(status string) bool {
	return status == "success" || status == "partial"
}

func ownsLease(state models.MessengerLabelState, token string, now time.Time) bool {
	return token != "" && state.Enabled && state.SyncToken == token && state.SyncStatus == "syncing" && state.LeaseUntil != nil && state.LeaseUntil.After(now)
}

func renewLease(database *gorm.DB, channel models.Channel, token string) error {
	now := time.Now()
	result := database.Model(&models.MessengerLabelState{}).
		Where("channel_id = ? AND tenant_id = ? AND sync_token = ? AND enabled = ? AND sync_status = ? AND lease_until > ?", channel.ID, channel.TenantID, token, true, "syncing", now).
		Update("lease_until", now.Add(syncLeaseDuration))
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return errLeaseLost
	}
	return nil
}

// The state row lock fences an old worker out of both snapshot writes and the
// checkpoint. Either the entire batch commits, or the next run reads it again.
func saveBatch(database *gorm.DB, channel models.Channel, token, cursor string, snapshots []models.MessengerLabelSnapshot, failed int) error {
	return database.Transaction(func(tx *gorm.DB) error {
		var state models.MessengerLabelState
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("channel_id = ? AND tenant_id = ?", channel.ID, channel.TenantID).First(&state).Error; err != nil {
			return err
		}
		if !ownsLease(state, token, time.Now()) {
			return errLeaseLost
		}
		for _, snapshot := range snapshots {
			if snapshot.ChannelID != channel.ID || snapshot.TenantID != channel.TenantID {
				return errors.New("messenger label snapshot scope mismatch")
			}
		}
		if len(snapshots) > 0 {
			ids := make([]string, 0, len(snapshots))
			for _, snapshot := range snapshots {
				ids = append(ids, snapshot.ConversationID)
			}
			var existing []models.MessengerLabelSnapshot
			if err := tx.Select("conversation_id,intake_labels,intake_labels_captured_at").Where("conversation_id IN ?", ids).Find(&existing).Error; err != nil {
				return err
			}
			byConversation := make(map[string]models.MessengerLabelSnapshot, len(existing))
			for _, snapshot := range existing {
				byConversation[snapshot.ConversationID] = snapshot
			}
			if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "conversation_id"}}, DoUpdates: clause.AssignmentColumns([]string{"psid", "labels", "status", "error_kind", "checked_at", "attempted_at"})}).CreateInBatches(snapshots, syncBatchSize).Error; err != nil {
				return err
			}
			newIntakeSnapshots := make([]models.MessengerLabelSnapshot, 0, len(snapshots))
			for _, snapshot := range snapshots {
				previous, existed := byConversation[snapshot.ConversationID]
				if snapshot.IntakeLabels == nil || snapshot.IntakeLabelsCapturedAt == nil || existed && previous.IntakeLabelsCapturedAt != nil {
					continue
				}
				if err := tx.Model(&models.MessengerLabelSnapshot{}).
					Where("conversation_id = ? AND tenant_id = ? AND channel_id = ? AND intake_labels_captured_at IS NULL", snapshot.ConversationID, channel.TenantID, channel.ID).
					Updates(map[string]interface{}{
						"intake_labels":             *snapshot.IntakeLabels,
						"intake_labels_captured_at": *snapshot.IntakeLabelsCapturedAt,
					}).Error; err != nil {
					return err
				}
				newIntakeSnapshots = append(newIntakeSnapshots, snapshot)
			}
			if err := pruneNewIntakeRules(tx, &state, newIntakeSnapshots); err != nil {
				return err
			}
		}
		return tx.Model(&state).Updates(map[string]interface{}{"sync_cursor": cursor, "sync_failed": state.SyncFailed + failed, "lease_until": time.Now().Add(syncLeaseDuration)}).Error
	})
}

func finishSync(database *gorm.DB, channel models.Channel, token, status, message string) error {
	updates := map[string]interface{}{"sync_status": status, "sync_error": message, "sync_finished_at": time.Now(), "lease_until": nil, "sync_token": ""}
	if completedSync(status) {
		updates["sync_cursor"] = ""
		updates["sync_failed"] = 0
	}
	// Token fencing ensures a delayed old cleanup never releases a new claim.
	query := database.Model(&models.MessengerLabelState{}).Where("channel_id = ? AND tenant_id = ? AND sync_token = ?", channel.ID, channel.TenantID, token)
	if completedSync(status) {
		query = query.Where("enabled = ? AND sync_status = ? AND lease_until > ?", true, "syncing", time.Now())
	}
	result := query.Updates(updates)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return errLeaseLost
	}
	return nil
}

func saveSyncCatalog(database *gorm.DB, channel models.Channel, token string, catalog []channels.FacebookLabel) error {
	if catalog == nil {
		return &channels.LabelAPIError{Kind: "invalid_response"}
	}
	data, err := json.Marshal(catalog)
	if err != nil {
		return err
	}
	now := time.Now()
	result := database.Model(&models.MessengerLabelState{}).
		Where("channel_id = ? AND tenant_id = ? AND sync_token = ? AND enabled = ? AND sync_status = ? AND lease_until > ?", channel.ID, channel.TenantID, token, true, "syncing", now).
		Updates(map[string]interface{}{"catalog": string(data), "catalog_synced_at": now})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return errLeaseLost
	}
	return nil
}

func maintainLease(ctx context.Context, database *gorm.DB, channel models.Channel, token string, cancel context.CancelFunc) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				renewCtx, stop := context.WithTimeout(ctx, 15*time.Second)
				err := renewLease(database.WithContext(renewCtx), channel, token)
				stop()
				if err != nil {
					cancel()
					return
				}
			}
		}
	}()
	return done
}
