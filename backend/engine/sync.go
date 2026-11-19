package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/vietbui/chat-quality-agent/channels"
	"github.com/vietbui/chat-quality-agent/channels/zalotoken"
	"github.com/vietbui/chat-quality-agent/config"
	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/db/models"
	"github.com/vietbui/chat-quality-agent/pkg"
)

// SyncEngine handles pulling messages from external channels into the database.
type SyncEngine struct {
	cfg      *config.Config
	ingestor ConversationIngester
}

func NewSyncEngine(cfg *config.Config) *SyncEngine {
	return &SyncEngine{
		cfg:      cfg,
		ingestor: NewIngestService(),
	}
}

// SyncChannel syncs a single channel: fetches conversations + messages and upserts into DB.
func (s *SyncEngine) SyncChannel(ctx context.Context, channel models.Channel) error {
	if channels.IsExternallyManagedImport(channel.ChannelType) {
		log.Printf("[sync] skipping channel %s (%s): sync is managed by the external import gateway", channel.Name, channel.ChannelType)
		return nil
	}

	log.Printf("[sync] starting sync for channel %s (%s)", channel.Name, channel.ChannelType)

	// Decrypt credentials
	credBytes, err := pkg.Decrypt(channel.CredentialsEncrypted, s.cfg.EncryptionKey)
	if err != nil {
		return s.updateSyncStatus(channel.ID, "error", fmt.Sprintf("decrypt failed: %v", err))
	}

	adapter, err := channels.NewAdapter(channel.ChannelType, credBytes)
	if err != nil {
		return s.updateSyncStatus(channel.ID, "error", fmt.Sprintf("adapter init failed: %v", err))
	}

	// Route Zalo token rotation through the channel token store so rotated
	// tokens are persisted and shared with every other adapter of this channel.
	if zaloAdapter, ok := adapter.(*channels.ZaloOAAdapter); ok {
		zaloAdapter.SetTokenStore(zalotoken.ChannelStore{ChannelID: channel.ID, EncryptionKey: s.cfg.EncryptionKey})
	}

	// Determine since — use last_sync_at or default to 7 days ago
	// Subtract 1 hour buffer to avoid missing messages near the boundary
	since := time.Now().AddDate(0, 0, -7)
	if channel.LastSyncAt != nil {
		since = channel.LastSyncAt.Add(-1 * time.Hour)
	}

	// Fetch recent conversations. Facebook returns them newest first and the
	// adapter stops at `since`, so every conversation updated in the window is
	// read. A fixed cap silently dropped the oldest ones after a busy period.
	// Zalo pages are 10 conversations, so its cap bounds API quota on a long
	// window (a new channel reads 7 days).
	conversationLimit := zaloConversationLimit
	if channel.ChannelType == "facebook" {
		conversationLimit = 0
	}
	startedAt := time.Now()
	conversations, err := adapter.FetchRecentConversations(ctx, since, conversationLimit)
	if err != nil {
		return s.updateSyncStatus(channel.ID, "error", fmt.Sprintf("fetch conversations failed: %v", err))
	}
	if conversationLimit > 0 && len(conversations) >= conversationLimit {
		log.Printf("[sync] channel %s: reached the %d-conversation cap; older conversations in the window are not read", channel.Name, conversationLimit)
	}

	log.Printf("[sync] channel %s: found %d conversations", channel.Name, len(conversations))

	// Check if file sync is enabled for this channel
	syncFiles := false
	if channel.Metadata != "" {
		var meta map[string]interface{}
		if json.Unmarshal([]byte(channel.Metadata), &meta) == nil {
			if sf, ok := meta["sync_files"]; ok {
				syncFiles, _ = sf.(bool)
			}
		}
	}
	log.Printf("[sync] channel %s: sync_files=%v, metadata=%s", channel.Name, syncFiles, channel.Metadata)

	retries := parseSyncRetryState(channel.SyncRetryState)
	work := planSyncWork(conversations, retries, since)

	totalMessages := 0
	totalInserted := 0
	var failures, abandoned []conversationSyncFailure
	var quotaErr error
	nextRetries := map[string]syncRetryEntry{}
	carry := func(item syncWorkItem, attempts int, err error) {
		conv := item.conv
		nextRetries[conv.ExternalID] = syncRetryEntry{
			Since:          item.since,
			Attempts:       attempts,
			LastError:      truncateRunes(err.Error(), 300),
			ExternalUserID: conv.ExternalUserID,
			CustomerName:   conv.CustomerName,
			LastMessageAt:  conv.LastMessageAt,
			Metadata:       conv.Metadata,
		}
	}
	for _, item := range work {
		conv := item.conv
		// Once Zalo says the OA's API quota is used up, every further call
		// fails the same way and only delays the reset. The rest of the
		// conversations wait for the next run without spending an attempt.
		if quotaErr != nil {
			carry(item, item.attempts, quotaErr)
			continue
		}
		if err := ctx.Err(); err != nil {
			carry(item, item.attempts, err)
			failures = append(failures, conversationSyncFailure{ExternalID: conv.ExternalID, Err: err})
			continue
		}
		processed, inserted, err := s.syncConversation(ctx, channel, adapter, conv, item.since, syncFiles)
		if err == nil {
			totalMessages += processed
			totalInserted += inserted
			continue
		}
		if channels.IsZaloQuotaError(err) {
			log.Printf("[sync] channel %s: Zalo API quota reached at conversation %s; stopping this run: %v", channel.Name, conv.ExternalID, err)
			quotaErr = err
			carry(item, item.attempts, err)
			continue
		}
		attempts := item.attempts + 1
		log.Printf("[sync] channel %s: conversation %s failed (attempt %d/%d): %v", channel.Name, conv.ExternalID, attempts, maxConversationSyncAttempts, err)
		failure := conversationSyncFailure{ExternalID: conv.ExternalID, Err: err}
		if attempts >= maxConversationSyncAttempts {
			abandoned = append(abandoned, failure)
			continue
		}
		failures = append(failures, failure)
		carry(item, attempts, err)
	}

	log.Printf("[sync] channel %s: synced %d conversations, %d messages (%d new)", channel.Name, len(work), totalMessages, totalInserted)

	// Log activity only when there are new messages, OR if triggered manually
	isManual, _ := ctx.Value("manual_sync").(bool)
	userID, _ := ctx.Value("user_id").(string)
	userEmail, _ := ctx.Value("user_email").(string)
	clientIP, _ := ctx.Value("client_ip").(string)

	if totalInserted > 0 || isManual {
		actorID := "system"
		if userID != "" {
			actorID = userID
		}

		detail := fmt.Sprintf("Sync '%s': %d conversations, %d messages (%d new)", channel.Name, len(work), totalMessages, totalInserted)
		if isManual {
			if userEmail != "" {
				detail += fmt.Sprintf(" (Thủ công bởi %s)", userEmail)
			} else {
				detail += " (Thủ công)"
			}
		}

		db.LogActivity(channel.TenantID, actorID, userEmail, "sync.completed", "channel", channel.ID, detail, "", clientIP)
	}

	// Trigger after-sync jobs for this channel
	if sched := GetDefaultScheduler(); sched != nil {
		sched.TriggerAfterSyncJobs(channel.TenantID, channel.ID)
	}

	// The window always moves forward once the conversation list was read: a
	// failed conversation is carried in sync_retry_state with its own window
	// and retried next run. Holding the whole channel back instead meant one
	// conversation that always fails (Zalo -1 "Error fetching messages: -2")
	// froze the window, every run re-read the same 100 conversations, and
	// that used up the OA's API quota (-32 "reached limit call api").
	var errMsg string
	switch {
	case quotaErr != nil:
		errMsg = fmt.Sprintf("Zalo API quota reached (%v); %d conversation(s) wait for the next run", quotaErr, len(nextRetries))
		if len(failures) > 0 {
			errMsg += fmt.Sprintf("; %d failed (%s)", len(failures), summarizeSyncFailures(failures))
		}
	case len(failures) > 0:
		errMsg = fmt.Sprintf("%d/%d conversations failed to sync; retrying them next run (%s)", len(failures), len(work), summarizeSyncFailures(failures))
	}
	if len(abandoned) > 0 {
		// A conversation that failed maxConversationSyncAttempts runs in a row
		// is dropped so it cannot keep the channel in error. It is synced
		// again if a new message moves it back into the window.
		var ids []string
		for _, f := range abandoned {
			ids = append(ids, f.ExternalID)
		}
		log.Printf("[sync] channel %s: gave up on conversations %s", channel.Name, strings.Join(ids, ","))
		db.LogActivity(channel.TenantID, "", "system", "sync.error", "channel", channel.ID, "Sync skipped conversations: "+channel.Name,
			fmt.Sprintf("Skipped %d conversation(s) after %d failed attempts (%s)", len(abandoned), maxConversationSyncAttempts, summarizeSyncFailures(abandoned)), "")
	}
	status := "success"
	if errMsg != "" {
		status = "error"
	}
	syncMessengerLabelsAfterMessages(ctx, channel, adapter, credBytes)
	return s.recordSyncResult(channel.ID, status, errMsg, &startedAt, encodeSyncRetryState(nextRetries))
}

// syncConversation reads one conversation's messages since `since` and stores
// them. It returns the processed and inserted message counts.
func (s *SyncEngine) syncConversation(ctx context.Context, channel models.Channel, adapter channels.ChannelAdapter, conv channels.SyncedConversation, since time.Time, syncFiles bool) (int, int, error) {
	convID, err := s.ingestor.EnsureConversation(channel.TenantID, channel.ID, conv)
	if err != nil {
		return 0, 0, fmt.Errorf("upsert conversation: %w", err)
	}
	messages, err := adapter.FetchMessages(ctx, conv.ExternalID, since)
	if err != nil {
		return 0, 0, fmt.Errorf("fetch messages: %w", err)
	}
	for _, msg := range messages {
		if syncFiles {
			s.downloadAttachments(channel.TenantID, convID, &msg)
		}
	}
	ingestResult, err := s.ingestor.IngestMessages(channel.TenantID, convID, messages)
	if err != nil {
		return 0, 0, fmt.Errorf("ingest messages: %w", err)
	}
	if err := s.ingestor.RecountConversationMessages(convID); err != nil {
		return 0, 0, fmt.Errorf("recount messages: %w", err)
	}
	return ingestResult.MessagesProcessed, ingestResult.MessagesInserted, nil
}

// SyncAllChannels syncs all active channels for a tenant.
func (s *SyncEngine) SyncAllChannels(ctx context.Context, tenantID string) error {
	var chans []models.Channel
	db.DB.Where("tenant_id = ? AND is_active = true", tenantID).Find(&chans)

	for _, ch := range chans {
		if err := s.SyncChannel(ctx, ch); err != nil {
			log.Printf("[sync] channel %s failed: %v", ch.Name, err)
		}
	}
	return nil
}

// updateSyncStatus records a sync that failed before any conversation was
// read (credentials, adapter, conversation list). It never moves last_sync_at,
// so the next run reads the same window again, and keeps sync_retry_state.
func (s *SyncEngine) updateSyncStatus(channelID, status, errMsg string) error {
	var syncedAt *time.Time
	if status == "success" {
		now := time.Now()
		syncedAt = &now
	}
	return s.recordSyncResult(channelID, status, errMsg, syncedAt, nil)
}

// recordSyncResult stores a sync result. A non-nil syncedAt moves last_sync_at
// (the next run reads from it minus a one-hour buffer); a non-nil retryState
// replaces sync_retry_state ("" clears it). updated_at always records the
// attempt, which the scheduler uses for backoff.
func (s *SyncEngine) recordSyncResult(channelID, status, errMsg string, syncedAt *time.Time, retryState *string) error {
	updates := map[string]interface{}{
		"last_sync_status": status,
		"last_sync_error":  errMsg,
		"updated_at":       time.Now(),
	}
	if syncedAt != nil {
		updates["last_sync_at"] = syncedAt
	}
	if retryState != nil {
		if *retryState == "" {
			updates["sync_retry_state"] = nil
		} else {
			updates["sync_retry_state"] = *retryState
		}
	}
	db.DB.Model(&models.Channel{}).Where("id = ?", channelID).Updates(updates)
	if errMsg != "" {
		// Log error to activity logs
		var ch models.Channel
		if db.DB.Where("id = ?", channelID).First(&ch).Error == nil {
			db.LogActivity(ch.TenantID, "", "system", "sync.error", "channel", channelID, "Sync failed: "+ch.Name, errMsg, "")
		}
		return fmt.Errorf("sync failed: %s", errMsg)
	}
	return nil
}

// downloadAttachments downloads attachment files from URLs to local storage.
func (s *SyncEngine) downloadAttachments(tenantID, convID string, msg *channels.SyncedMessage) {
	for i, att := range msg.Attachments {
		if att.URL == "" {
			continue
		}
		// Create directory
		dir := filepath.Join("/var/lib/cqa/files", tenantID, convID)
		if err := os.MkdirAll(dir, 0755); err != nil {
			log.Printf("[sync] mkdir failed for %s: %v", dir, err)
			continue
		}

		// Generate filename — sanitize to prevent path traversal from external API data
		name := filepath.Base(att.Name)
		if name == "" || name == "." || name == "/" {
			name = fmt.Sprintf("%s-%d", att.Type, time.Now().UnixMilli())
		}
		localPath := filepath.Join(dir, name)
		// Verify path stays within intended directory
		if !strings.HasPrefix(filepath.Clean(localPath), filepath.Clean(dir)+string(filepath.Separator)) {
			log.Printf("[security] path traversal blocked: att.Name=%s resolved=%s", att.Name, localPath)
			continue
		}

		// Download file
		client := &http.Client{Timeout: 30 * time.Second}
		resp, err := client.Get(att.URL)
		if err != nil {
			log.Printf("[sync] download failed for %s: %v", att.URL, err)
			continue
		}

		if resp.StatusCode != 200 {
			resp.Body.Close()
			log.Printf("[sync] download failed for %s: status %d", att.URL, resp.StatusCode)
			continue
		}

		f, err := os.Create(localPath)
		if err != nil {
			resp.Body.Close()
			log.Printf("[sync] create file failed %s: %v", localPath, err)
			continue
		}
		_, err = io.Copy(f, resp.Body)
		resp.Body.Close()
		f.Close()
		if err != nil {
			log.Printf("[sync] write file failed %s: %v", localPath, err)
			continue
		}

		// Update attachment with local path (relative for serving)
		msg.Attachments[i].LocalPath = filepath.Join(tenantID, convID, name)
		log.Printf("[sync] downloaded %s → %s", att.URL, localPath)
	}
}
