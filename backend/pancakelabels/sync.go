package pancakelabels

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/vietbui/chat-quality-agent/db/models"
	"github.com/vietbui/chat-quality-agent/pancake"
	"github.com/vietbui/chat-quality-agent/pkg"
	"gorm.io/gorm"
)

const (
	syncLease        = 2 * time.Hour
	incrementalEvery = 6 * time.Hour
	fullScanEvery    = 24 * time.Hour
	scanOverlap      = time.Hour
)

// Reader is the Pancake Page API surface used by a reconcile scan.
type Reader interface {
	FetchTags(context.Context) ([]pancake.Tag, error)
	ListInboxConversations(context.Context, string) (pancake.ConversationPage, error)
}

func ErrorMessage(kind string) string {
	switch kind {
	case "auth":
		return "Token Pancake không hợp lệ hoặc đã hết hạn; hãy dán token mới."
	case "permission":
		return "Pancake từ chối quyền đọc dữ liệu Page này."
	case "rate_limit":
		return "Pancake đang giới hạn lượt gọi (5 lệnh/giây/Page); hệ thống sẽ thử lại sau."
	case "not_found":
		return "Pancake không tìm thấy Page này; kiểm tra Page đã kết nối Pancake."
	case "invalid_response":
		return "Pancake trả dữ liệu không đúng định dạng."
	case "transport":
		return "Kết nối Pancake bị lỗi hoặc quá thời gian; hãy thử lại."
	default:
		return "Không lưu được dữ liệu nhãn Pancake; hãy thử lại."
	}
}

// ClaimSync starts one reconcile scan per Page across replicas. A Page token
// is required; tracking need not be enabled, so staff can preview tags first.
func ClaimSync(database *gorm.DB, channel models.Channel, now time.Time) (string, error) {
	state, err := LoadState(database, channel)
	if err != nil {
		return "", err
	}
	if len(state.PageTokenEncrypted) == 0 {
		return "", ErrNoPageToken
	}
	if err := ensureState(database, channel); err != nil {
		return "", err
	}
	token := pkg.NewUUID()
	result := database.Model(&models.PancakeLabelState{}).
		Where("channel_id = ? AND tenant_id = ? AND (lease_until IS NULL OR lease_until < ?) AND (sync_finished_at IS NULL OR sync_finished_at <= ?)", channel.ID, channel.TenantID, now, now.Add(-time.Minute)).
		Updates(map[string]any{"sync_status": "syncing", "sync_error": "", "sync_started_at": now, "lease_until": now.Add(syncLease), "sync_token": token})
	if result.Error != nil {
		return "", result.Error
	}
	if result.RowsAffected != 1 {
		return "", ErrBusy
	}
	return token, nil
}

// RunClaimedSync refreshes the tag catalogue, then scans inbox conversations
// newest-updated first. A full scan reads every conversation; an incremental
// scan stops after conversations older than the last scan minus one hour.
func RunClaimedSync(ctx context.Context, database *gorm.DB, channel models.Channel, token string, reader Reader, index *PSIDIndex) (runErr error) {
	ctx, cancel := context.WithTimeout(ctx, syncLease-5*time.Minute)
	defer cancel()
	started := time.Now()
	status, message := "success", ""
	scanned, changed := 0, 0
	full := false
	defer func() {
		if runErr != nil {
			status = "error"
			message = ErrorMessage(pancake.ErrorKind(runErr))
		} else if message == "" {
			message = fmt.Sprintf("Đã đọc %d khách, %d khách đổi nhãn.", scanned, changed)
		}
		updates := map[string]any{"sync_status": status, "sync_error": truncate(message, 250), "sync_finished_at": time.Now(), "lease_until": nil}
		if runErr == nil {
			updates["reconciled_at"] = started
			if full {
				updates["full_scan_at"] = started
			}
		}
		cleanup, cancelCleanup := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancelCleanup()
		database.WithContext(cleanup).Model(&models.PancakeLabelState{}).
			Where("channel_id = ? AND tenant_id = ? AND sync_token = ?", channel.ID, channel.TenantID, token).Updates(updates)
	}()
	var state models.PancakeLabelState
	if err := database.WithContext(ctx).Where("channel_id = ? AND tenant_id = ? AND sync_token = ?", channel.ID, channel.TenantID, token).First(&state).Error; err != nil {
		return err
	}
	tags, err := reader.FetchTags(ctx)
	if err != nil {
		return err
	}
	if err := SaveCatalog(database.WithContext(ctx), channel, CatalogFromTags(tags), time.Now()); err != nil {
		return err
	}
	full = state.FullScanAt == nil || started.Sub(*state.FullScanAt) >= fullScanEvery || state.ReconciledAt == nil
	var stopBefore time.Time
	if !full {
		stopBefore = state.ReconciledAt.Add(-scanOverlap)
	}
	cursor := ""
	seenCursor := map[string]bool{}
	for page := 0; page < pancake.MaxConversationPages; page++ {
		fetchedAt := time.Now()
		result, err := reader.ListInboxConversations(ctx, cursor)
		if err != nil {
			return err
		}
		for _, update := range result.Updates {
			if update.PageID == "" {
				update.PageID = channel.ExternalID
			}
			convID := ""
			if index != nil {
				convID = index.Lookup(database, channel, update.PSID, 10*time.Minute)
			}
			didChange, err := Apply(ctx, database, channel, update, SourceReconcile, convID, fetchedAt)
			if err != nil {
				return err
			}
			scanned++
			if didChange {
				changed++
			}
		}
		if result.NextCursor == "" || seenCursor[result.NextCursor] {
			return nil
		}
		if !full && !result.Oldest.IsZero() && result.Oldest.Before(stopBefore) {
			return nil
		}
		seenCursor[result.NextCursor] = true
		cursor = result.NextCursor
	}
	message = fmt.Sprintf("Đã đọc %d khách (chạm giới hạn %d trang mỗi lượt).", scanned, pancake.MaxConversationPages)
	return nil
}

func truncate(value string, max int) string {
	runes := []rune(value)
	if len(runes) <= max {
		return value
	}
	return string(runes[:max])
}

// DuePages lists Page channels whose reconcile is due (enabled + token).
func DuePages(database *gorm.DB, now time.Time, limit int) ([]models.PancakeLabelState, error) {
	var states []models.PancakeLabelState
	err := database.Model(&models.PancakeLabelState{}).
		Joins("JOIN channels ON channels.id = pancake_label_states.channel_id AND channels.tenant_id = pancake_label_states.tenant_id").
		Where("pancake_label_states.enabled = ? AND channels.is_active = ? AND channels.channel_type = ?", true, true, "facebook").
		Where("pancake_label_states.page_token_encrypted IS NOT NULL AND LENGTH(pancake_label_states.page_token_encrypted) > 0").
		Where("(pancake_label_states.lease_until IS NULL OR pancake_label_states.lease_until <= ?)", now).
		Where("(pancake_label_states.reconciled_at IS NULL OR pancake_label_states.reconciled_at <= ?)", now.Add(-incrementalEvery)).
		Order("COALESCE(pancake_label_states.reconciled_at, '1970-01-01') ASC, pancake_label_states.channel_id ASC").
		Limit(limit).Find(&states).Error
	return states, err
}

// RunScheduled claims and runs one due Page. It is safe to call every minute.
func RunScheduled(ctx context.Context, database *gorm.DB, key string, index *PSIDIndex) error {
	states, err := DuePages(database, time.Now(), 2)
	if err != nil {
		return err
	}
	for _, state := range states {
		var channel models.Channel
		if err := database.Where("id = ? AND tenant_id = ? AND channel_type = ? AND is_active = ?", state.ChannelID, state.TenantID, "facebook", true).First(&channel).Error; err != nil {
			continue
		}
		pageToken := PageToken(state, key)
		if pageToken == "" {
			continue
		}
		token, err := ClaimSync(database, channel, time.Now())
		if errors.Is(err, ErrBusy) {
			continue
		}
		if err != nil {
			return err
		}
		pageID := state.PancakePageID
		if pageID == "" {
			pageID = channel.ExternalID
		}
		if err := RunClaimedSync(ctx, database, channel, token, pancake.NewClient(pageID, pageToken), index); err != nil {
			return err
		}
	}
	return nil
}
