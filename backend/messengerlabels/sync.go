package messengerlabels

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/vietbui/chat-quality-agent/channels"
	"github.com/vietbui/chat-quality-agent/db/models"
	"gorm.io/gorm"
)

type Reader interface {
	FetchPageLabels(context.Context) ([]channels.FacebookLabel, error)
	FetchUserLabels(context.Context, string) ([]channels.FacebookLabel, error)
	ResolveConversationPSID(context.Context, string) (string, error)
}

func ErrorKind(err error) string {
	var apiError *channels.LabelAPIError
	if errors.As(err, &apiError) {
		return apiError.Kind
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return "transport"
	}
	return "storage"
}

func ErrorMessage(kind string) string {
	switch kind {
	case "auth":
		return "Token Meta không còn hợp lệ; hãy kết nối lại Fanpage."
	case "permission":
		return "Meta từ chối quyền đọc nhãn; kiểm tra quyền ứng dụng và điều khoản liên hệ của Page."
	case "rate_limit":
		return "Meta đang giới hạn lượt gọi; hãy thử lại sau."
	case "unsupported":
		return "Meta không cho đọc nhãn của đối tượng này; có thể do quyền hoặc loại tài khoản Messenger."
	case "invalid_response":
		return "Meta trả dữ liệu nhãn chưa đầy đủ hoặc không xác định được khách hàng."
	case "transport":
		return "Kết nối Meta bị lỗi hoặc quá thời gian; hãy đồng bộ lại."
	default:
		return "Không lưu hoặc tải được dữ liệu nhãn; hãy thử lại."
	}
}

// Observe reads every supplied conversation regardless of last-message time.
// At most four Meta requests are in flight. A Page-wide auth/rate failure aborts
// the sweep; per-person unsupported reads remain individual unknown results.
func Observe(ctx context.Context, reader Reader, convs []models.Conversation, pageID string, save func(models.MessengerLabelSnapshot) error) (int, int, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	type observation struct {
		snapshot models.MessengerLabelSnapshot
		err      error
	}
	jobs := make(chan models.Conversation)
	results := make(chan observation, 4)
	var workers sync.WaitGroup
	for i := 0; i < 4; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for conv := range jobs {
				if ctx.Err() != nil {
					return
				}
				psid := ParticipantPSID(conv, pageID)
				var err error
				if psid == "" {
					psid, err = reader.ResolveConversationPSID(ctx, conv.ExternalConversationID)
				}
				var labels []channels.FacebookLabel
				if err == nil {
					if !digits.MatchString(psid) || psid == pageID {
						err = &channels.LabelAPIError{Kind: "invalid_response"}
					} else {
						labels, err = reader.FetchUserLabels(ctx, psid)
					}
				}
				now := time.Now()
				snapshot := models.MessengerLabelSnapshot{ConversationID: conv.ID, TenantID: conv.TenantID, ChannelID: conv.ChannelID, PSID: psid, Labels: "[]", Status: "error", AttemptedAt: now}
				if err == nil && labels == nil {
					err = &channels.LabelAPIError{Kind: "invalid_response"}
				}
				if err != nil {
					snapshot.ErrorKind = ErrorKind(err)
				} else {
					data, _ := json.Marshal(labels)
					snapshot.Labels = string(data)
					snapshot.Status = "success"
					snapshot.CheckedAt = &now
				}
				select {
				case results <- observation{snapshot, err}:
				case <-ctx.Done():
					return
				}
			}
		}()
	}
	go func() {
		defer close(jobs)
		for _, conv := range convs {
			select {
			case jobs <- conv:
			case <-ctx.Done():
				return
			}
		}
	}()
	go func() { workers.Wait(); close(results) }()
	succeeded, failed := 0, 0
	var fatal error
	for result := range results {
		if fatal != nil {
			continue
		}
		if err := save(result.snapshot); err != nil {
			fatal = err
			cancel()
			continue
		}
		if result.err == nil {
			succeeded++
		} else {
			failed++
			switch ErrorKind(result.err) {
			case "permission", "auth", "rate_limit":
				fatal = result.err
				cancel()
			}
		}
	}
	if fatal == nil {
		fatal = ctx.Err()
	}
	return succeeded, failed, fatal
}

// RunClaimedSync must follow ClaimSync. Completed batches survive interruption;
// the next scheduled/manual run resumes them. Renewed leases are token-fenced.
func RunClaimedSync(ctx context.Context, database *gorm.DB, channel models.Channel, pageID, token string, reader Reader) (runErr error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Hour)
	defer cancel()
	status, message := "success", ""
	defer func() {
		if runErr != nil {
			status = "error"
			message = ErrorMessage(ErrorKind(runErr))
			if errors.Is(runErr, ErrConfiguration) || errors.Is(runErr, ErrTooLarge) {
				message = runErr.Error()
			}
		}
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cleanupCancel()
		err := finishSync(database.WithContext(cleanupCtx), channel, token, status, message)
		if runErr == nil && err != nil {
			runErr = err
		}
	}()
	leaseDone := maintainLease(ctx, database, channel, token, cancel)
	defer func() { cancel(); <-leaseDone }()
	select {
	case pageSyncSlots <- struct{}{}:
		defer func() { <-pageSyncSlots }()
	case <-ctx.Done():
		return ctx.Err()
	}
	database = database.WithContext(ctx)
	var state models.MessengerLabelState
	if err := database.Where("channel_id = ? AND tenant_id = ? AND sync_token = ?", channel.ID, channel.TenantID, token).First(&state).Error; err != nil {
		return err
	}
	if !ownsLease(state, token, time.Now()) {
		return errLeaseLost
	}
	catalog, err := reader.FetchPageLabels(ctx)
	if err != nil {
		return err
	}
	if err = saveSyncCatalog(database, channel, token, catalog); err != nil {
		return err
	}
	var rules []Rule
	if json.Unmarshal([]byte(state.Rules), &rules) != nil || (Policy{Enabled: true, Rules: rules}).Validate(catalog) != nil {
		return ErrConfiguration
	}
	var count int64
	if err = database.Model(&models.Conversation{}).Where("tenant_id = ? AND channel_id = ?", channel.TenantID, channel.ID).Count(&count).Error; err != nil {
		return err
	}
	if count > MaxConversations {
		return ErrTooLarge
	}
	cursor, failed := state.SyncCursor, state.SyncFailed
	for {
		var convs []models.Conversation
		if err = database.Select("id,tenant_id,channel_id,external_conversation_id,metadata").Where("tenant_id = ? AND channel_id = ? AND id > ?", channel.TenantID, channel.ID, cursor).Order("id ASC").Limit(syncBatchSize).Find(&convs).Error; err != nil {
			return err
		}
		if len(convs) == 0 {
			break
		}
		snapshots := make([]models.MessengerLabelSnapshot, 0, len(convs))
		_, batchFailed, observeErr := Observe(ctx, reader, convs, pageID, func(snapshot models.MessengerLabelSnapshot) error {
			snapshots = append(snapshots, snapshot)
			return nil
		})
		if observeErr != nil {
			return observeErr
		}
		cursor = convs[len(convs)-1].ID
		if err = saveBatch(database, channel, token, cursor, snapshots, batchFailed); err != nil {
			return err
		}
		failed += batchFailed
	}
	// Re-read the catalog after a potentially long sweep. Individual snapshots
	// retain their actual observation times and expire independently.
	catalog, err = reader.FetchPageLabels(ctx)
	if err != nil {
		return err
	}
	if (Policy{Enabled: true, Rules: rules}).Validate(catalog) != nil {
		return ErrConfiguration
	}
	if err = saveSyncCatalog(database, channel, token, catalog); err != nil {
		return err
	}
	if failed > 0 {
		status = "partial"
		message = fmt.Sprintf("%d hội thoại chưa đọc được nhãn; không tính các hội thoại này là chưa phân loại.", failed)
	}
	return nil
}
