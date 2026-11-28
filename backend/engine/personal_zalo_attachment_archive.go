package engine

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/vietbui/chat-quality-agent/channels"
	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/db/models"
	"gorm.io/gorm"
)

const (
	personalZaloArchiveInterval = time.Minute
	personalZaloArchiveRetry    = 10 * time.Minute
	personalZaloArchiveBatch    = 20
	personalZaloArchiveState    = "$.personal_zalo_attachment_archive_due"
	personalZaloArchiveEnabled  = "is_active = ? AND channel_type = ? AND JSON_TYPE(JSON_EXTRACT(metadata, '$.sync_files')) = 'BOOLEAN' AND JSON_UNQUOTE(JSON_EXTRACT(metadata, '$.sync_files')) = 'true'"
)

type personalZaloAttachmentStore interface {
	Store(context.Context, string, string, string, int, channels.Attachment) (string, error)
	RemoveConversation(string, string) error
}

// RunPersonalZaloAttachmentArchive archives new and previously imported media
// independently of the gateway's request timeout and message deduplication.
func RunPersonalZaloAttachmentArchive(ctx context.Context) {
	archive := channels.NewPersonalZaloAttachmentArchive("")
	ticker := time.NewTicker(personalZaloArchiveInterval)
	defer ticker.Stop()
	for ctx.Err() == nil {
		if err := archivePersonalZaloAttachments(ctx, db.DB, archive); err != nil && ctx.Err() == nil {
			log.Printf("[personal-zalo] attachment archive scan failed: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func archivePersonalZaloAttachments(ctx context.Context, database *gorm.DB, archive personalZaloAttachmentStore) error {
	var enabledChannels []models.Channel
	if err := database.WithContext(ctx).Select("id", "tenant_id").
		Where(personalZaloArchiveEnabled, true, "personal_zalo_import").Find(&enabledChannels).Error; err != nil {
		return err
	}
	for _, channel := range enabledChannels {
		if err := archivePersonalZaloChannel(ctx, database, archive, channel); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			log.Printf("[personal-zalo] attachment archive failed for channel %s: %v", channel.ID, err)
		}
	}
	return ctx.Err()
}

func archivePersonalZaloChannel(ctx context.Context, database *gorm.DB, archive personalZaloAttachmentStore, channel models.Channel) error {
	// Give both ends of the history a share of each pass, so a steady flow
	// of incoming attachments cannot keep older conversations from advancing.
	for _, order := range []string{"messages.sent_at DESC, messages.id DESC", "messages.sent_at ASC, messages.id ASC"} {
		if err := archivePersonalZaloBatch(ctx, database, archive, channel, order); err != nil {
			return err
		}
	}
	return nil
}

func archivePersonalZaloBatch(ctx context.Context, database *gorm.DB, archive personalZaloAttachmentStore, channel models.Channel, order string) error {
	now := time.Now()
	var messages []models.Message
	if err := database.WithContext(ctx).Model(&models.Message{}).
		Select("messages.id", "messages.conversation_id", "messages.external_message_id", "messages.content", "messages.content_type", "messages.attachments").
		Joins("JOIN conversations ON conversations.id = messages.conversation_id AND conversations.tenant_id = messages.tenant_id").
		Where("conversations.channel_id = ? AND messages.tenant_id = ?", channel.ID, channel.TenantID).
		Where("messages.content_type IN ?", []string{"image", "file"}).
		Where("COALESCE(CAST(JSON_EXTRACT(messages.raw_data, ?) AS SIGNED), 0) BETWEEN 0 AND ?", personalZaloArchiveState, now.Unix()).
		Order(order).Limit(personalZaloArchiveBatch / 2).Find(&messages).Error; err != nil {
		return err
	}

	for _, message := range messages {
		if err := ctx.Err(); err != nil {
			return err
		}
		attachments, downloaded, retry := archivePersonalZaloMessage(ctx, archive, channel.TenantID, message)
		due := int64(-1)
		if retry {
			due = time.Now().Add(personalZaloArchiveRetry).Unix()
		}
		updates := map[string]any{
			"raw_data": gorm.Expr("JSON_SET(IF(JSON_TYPE(raw_data) = 'OBJECT', raw_data, JSON_OBJECT()), ?, ?)", personalZaloArchiveState, due),
		}
		if downloaded {
			data, err := json.Marshal(attachments)
			if err != nil {
				return err
			}
			updates["attachments"] = string(data)
		}
		// The channel may have been paused or disabled during the download.
		// Conditional publication also prevents reviving a deleted message.
		enabled := database.Model(&models.Channel{}).Select("1").
			Where("id = ? AND tenant_id = ?", channel.ID, channel.TenantID).
			Where(personalZaloArchiveEnabled, true, "personal_zalo_import")
		result := database.WithContext(ctx).Model(&models.Message{}).
			Where("id = ? AND tenant_id = ? AND conversation_id = ?", message.ID, channel.TenantID, message.ConversationID).
			Where("EXISTS (?)", enabled).
			Where("COALESCE(CAST(JSON_EXTRACT(raw_data, ?) AS SIGNED), 0) BETWEEN 0 AND ?", personalZaloArchiveState, now.Unix()).
			Updates(updates)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			if downloaded {
				var conversation models.Conversation
				err := database.WithContext(ctx).Select("id").
					Take(&conversation, "id = ? AND tenant_id = ?", message.ConversationID, channel.TenantID).Error
				if errors.Is(err, gorm.ErrRecordNotFound) {
					err = archive.RemoveConversation(channel.TenantID, message.ConversationID)
				}
				if err != nil {
					return err
				}
			}
			return nil
		}
	}
	return nil
}

func archivePersonalZaloMessage(ctx context.Context, archive personalZaloAttachmentStore, tenantID string, message models.Message) ([]channels.Attachment, bool, bool) {
	attachments := personalZaloMessageAttachments(message)
	downloaded, retry := false, false
	for index, attachment := range attachments {
		if attachment.LocalPath != "" || attachment.URL == "" || (attachment.Type != "image" && attachment.Type != "file") {
			continue
		}
		localPath, err := archive.Store(ctx, tenantID, message.ConversationID, message.ExternalMessageID, index, attachment)
		if err != nil {
			retry = retry || !errors.Is(err, channels.ErrPersonalZaloAttachmentUnavailable)
			continue
		}
		attachments[index].LocalPath = localPath
		downloaded = true
	}
	return attachments, downloaded, retry
}

func personalZaloMessageAttachments(message models.Message) []channels.Attachment {
	var attachments []channels.Attachment
	if err := json.Unmarshal([]byte(message.Attachments), &attachments); err == nil && len(attachments) > 0 {
		return attachments
	}

	// The personal gateway stringifies Zalo's content object for both live and
	// historical events. Older imports already contain this original payload.
	var media struct {
		Href     string `json:"href"`
		FileURL  string `json:"fileUrl"`
		RawURL   string `json:"rawUrl"`
		HDURL    string `json:"hdUrl"`
		OriURL   string `json:"oriUrl"`
		Title    string `json:"title"`
		FileName string `json:"fileName"`
	}
	if err := json.Unmarshal([]byte(message.Content), &media); err != nil {
		return nil
	}
	candidates := []string{media.Href, media.RawURL, media.HDURL, media.OriURL}
	if message.ContentType == "file" {
		candidates = []string{media.Href, media.FileURL}
	}
	var source string
	for _, candidate := range candidates {
		if channels.AllowedPersonalZaloAttachmentURL(candidate) {
			source = strings.TrimSpace(candidate)
			break
		}
	}
	if source == "" {
		return nil
	}
	name := strings.TrimSpace(media.FileName)
	if name == "" {
		name = strings.TrimSpace(media.Title)
	}
	if name == "" {
		if parsed, err := url.Parse(source); err == nil {
			name = path.Base(parsed.Path)
		}
	}
	name = path.Base(strings.ReplaceAll(name, "\\", "/"))
	if name == "." || name == "/" || name == ".." {
		name = message.ContentType
	}
	return []channels.Attachment{{Type: message.ContentType, URL: source, Name: name}}
}
