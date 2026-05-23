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

	// Set token refresh callback for Zalo — persist new tokens to DB after refresh
	if zaloAdapter, ok := adapter.(*channels.ZaloOAAdapter); ok {
		chID := channel.ID
		encKey := s.cfg.EncryptionKey
		zaloAdapter.SetTokenRefreshCallback(func(newAccess, newRefresh string) {
			var ch models.Channel
			if db.DB.First(&ch, "id = ?", chID).Error != nil {
				return
			}
			oldCreds, err := pkg.Decrypt(ch.CredentialsEncrypted, encKey)
			if err != nil {
				log.Printf("[sync] decrypt failed for token persist: %v", err)
				return
			}
			var credsMap map[string]interface{}
			if err := json.Unmarshal(oldCreds, &credsMap); err != nil {
				log.Printf("[sync] unmarshal creds failed: %v", err)
				return
			}
			credsMap["access_token"] = newAccess
			credsMap["refresh_token"] = newRefresh
			newCredJSON, _ := json.Marshal(credsMap)
			encrypted, err := pkg.Encrypt(newCredJSON, encKey)
			if err != nil {
				log.Printf("[sync] encrypt failed for token persist: %v", err)
				return
			}
			db.DB.Model(&ch).Update("credentials_encrypted", encrypted)
			log.Printf("[sync] persisted refreshed Zalo tokens for channel %s", chID)
		})
	}

	// Determine since — use last_sync_at or default to 7 days ago
	// Subtract 1 hour buffer to avoid missing messages near the boundary
	since := time.Now().AddDate(0, 0, -7)
	if channel.LastSyncAt != nil {
		since = channel.LastSyncAt.Add(-1 * time.Hour)
	}

	// Fetch recent conversations
	conversations, err := adapter.FetchRecentConversations(ctx, since, 100)
	if err != nil {
		return s.updateSyncStatus(channel.ID, "error", fmt.Sprintf("fetch conversations failed: %v", err))
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

	totalMessages := 0
	totalInserted := 0
	for _, conv := range conversations {
		convID, err := s.ingestor.EnsureConversation(channel.TenantID, channel.ID, conv)
		if err != nil {
			log.Printf("[sync] error upserting conversation %s: %v", conv.ExternalID, err)
			continue
		}

		// Fetch messages for this conversation
		messages, err := adapter.FetchMessages(ctx, conv.ExternalID, since)
		if err != nil {
			log.Printf("[sync] error fetching messages for %s: %v", conv.ExternalID, err)
			continue
		}

		// Upsert messages
		for _, msg := range messages {
			if syncFiles {
				s.downloadAttachments(channel.TenantID, convID, &msg)
			}
		}
		ingestResult, err := s.ingestor.IngestMessages(channel.TenantID, convID, messages)
		if err != nil {
			log.Printf("[sync] error ingesting conversation %s: %v", conv.ExternalID, err)
			continue
		}
		if err := s.ingestor.RecountConversationMessages(convID); err != nil {
			log.Printf("[sync] error recounting conversation %s: %v", conv.ExternalID, err)
			continue
		}
		totalMessages += ingestResult.MessagesProcessed
		totalInserted += ingestResult.MessagesInserted
	}

	log.Printf("[sync] channel %s: synced %d conversations, %d messages (%d new)", channel.Name, len(conversations), totalMessages, totalInserted)

	// Log activity only when there are new messages
	if totalInserted > 0 {
		db.LogActivity(channel.TenantID, "", "system", "sync.completed", "channel", channel.ID,
			fmt.Sprintf("Sync '%s': %d conversations, %d messages (%d new)", channel.Name, len(conversations), totalMessages, totalInserted), "", "")
	}

	// Trigger after-sync jobs for this channel
	if sched := GetDefaultScheduler(); sched != nil {
		sched.TriggerAfterSyncJobs(channel.TenantID, channel.ID)
	}

	return s.updateSyncStatus(channel.ID, "success", "")
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

func (s *SyncEngine) updateSyncStatus(channelID, status, errMsg string) error {
	now := time.Now()
	updates := map[string]interface{}{
		"last_sync_at":     &now,
		"last_sync_status": status,
		"last_sync_error":  errMsg,
		"updated_at":       now,
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
