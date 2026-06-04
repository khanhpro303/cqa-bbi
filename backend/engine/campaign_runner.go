package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/robfig/cron/v3"

	"github.com/vietbui/chat-quality-agent/channels"
	"github.com/vietbui/chat-quality-agent/config"
	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/db/models"
	"github.com/vietbui/chat-quality-agent/pkg"
)

// Sentinel errors so HTTP callers can map to the same status codes/payloads the
// old inline SendNow used, and the scheduler can react differently (e.g. log a
// channel_inactive run instead of returning an HTTP error).
var (
	ErrCampaignNotFound        = errors.New("campaign_not_found")
	ErrCampaignChannelNotFound = errors.New("channel_not_found")
	ErrCampaignChannelInactive = errors.New("channel_inactive")
)

// campaignBroadcast bundles everything needed to send a campaign to its groups:
// the loaded campaign (with Segments preloaded), the resolved Zalo OA channel,
// a ready adapter, and the prebuilt message content.
type campaignBroadcast struct {
	campaign *models.Campaign
	channel  *models.Channel
	adapter  *channels.ZaloOAAdapter
	content  string
	// attachmentIDs are Zalo attachment_ids for the campaign's images, uploaded
	// once per broadcast (not per group) so each group send just references them.
	attachmentIDs []string
	// reminderContent is the optional text-only "nhắc lại" message. When non-empty,
	// a segment that already has a successful run sends this instead of the main
	// content (and without images) — see fireSegment.
	reminderContent string
	tenantID        string
}

// prepareCampaignBroadcast loads the campaign + segments, runs the channel guard
// (must exist + be an active zalo_oa channel), decrypts credentials, builds the
// Zalo adapter (with the same token-refresh persistence callback the handler
// used), and builds the broadcast content.
func prepareCampaignBroadcast(ctx context.Context, campaignID, tenantID string) (*campaignBroadcast, error) {
	var campaign models.Campaign
	if err := db.DB.Preload("Segments").Where("id = ? AND tenant_id = ?", campaignID, tenantID).First(&campaign).Error; err != nil {
		return nil, ErrCampaignNotFound
	}

	// Guard: the channel must exist and be active, otherwise webhook/automation is off.
	var channel models.Channel
	if err := db.DB.Where("id = ? AND tenant_id = ? AND channel_type = ?", campaign.ChannelID, tenantID, "zalo_oa").First(&channel).Error; err != nil {
		return nil, ErrCampaignChannelNotFound
	}
	if !channel.IsActive {
		return nil, ErrCampaignChannelInactive
	}

	cfg, _ := config.Load()
	credBytes, err := pkg.Decrypt(channel.CredentialsEncrypted, cfg.EncryptionKey)
	if err != nil {
		return nil, errors.New("failed_to_decrypt_channel_credentials")
	}
	var zaloCreds channels.ZaloOACredentials
	if err := json.Unmarshal(credBytes, &zaloCreds); err != nil {
		return nil, errors.New("failed_to_parse_channel_credentials")
	}
	adapter := channels.NewZaloOAAdapter(zaloCreds)
	adapter.SetTokenRefreshCallback(func(newAccess, newRefresh string) {
		credsMap := map[string]interface{}{
			"app_id":        zaloCreds.AppID,
			"app_secret":    zaloCreds.AppSecret,
			"access_token":  newAccess,
			"refresh_token": newRefresh,
			"oa_id":         zaloCreds.OAId,
		}
		newCredJSON, _ := json.Marshal(credsMap)
		if encrypted, e := pkg.Encrypt(newCredJSON, cfg.EncryptionKey); e == nil {
			db.DB.Model(&channel).Update("credentials_encrypted", encrypted)
		}
	})

	return &campaignBroadcast{
		campaign:        &campaign,
		channel:         &channel,
		adapter:         adapter,
		content:         buildBroadcastContent(&campaign),
		attachmentIDs:   uploadCampaignImages(ctx, adapter, campaign.Images()),
		reminderContent: strings.TrimSpace(campaign.MessageReminderText),
		tenantID:        tenantID,
	}, nil
}

// campaignFilesBase is the on-disk root where campaign images are stored
// (mirrors handlers.campaignFilesBase / the /api/v1/files file server).
const campaignFilesBase = "/var/lib/cqa/files"

// uploadCampaignImages reads each stored campaign image and uploads it to Zalo,
// returning the resulting attachment_ids. A single image that can't be read or
// uploaded is logged and skipped so it never blocks the whole broadcast.
func uploadCampaignImages(ctx context.Context, adapter *channels.ZaloOAAdapter, paths []string) []string {
	if len(paths) == 0 {
		return nil
	}
	ids := make([]string, 0, len(paths))
	for _, rel := range paths {
		rel = strings.TrimSpace(rel)
		if rel == "" {
			continue
		}
		fullPath := filepath.Join(campaignFilesBase, filepath.Clean("/"+rel))
		data, err := os.ReadFile(fullPath)
		if err != nil {
			log.Printf("[campaign] skip image %q: read failed: %v", rel, err)
			continue
		}
		id, err := adapter.UploadGroupImage(ctx, data, filepath.Base(rel))
		if err != nil {
			log.Printf("[campaign] skip image %q: zalo upload failed: %v", rel, err)
			continue
		}
		ids = append(ids, id)
	}
	return ids
}

// fireSegment sends the campaign content to one segment's GMF group and writes a
// CampaignRun row. Mirrors the inner loop of the original SendNow handler.
func (b *campaignBroadcast) fireSegment(ctx context.Context, seg models.CampaignSegment) (sent, fail int) {
	run := models.CampaignRun{
		ID:         uuid.New().String(),
		TenantID:   b.tenantID,
		CampaignID: b.campaign.ID,
		SegmentID:  seg.ID,
		StartedAt:  time.Now(),
		Status:     "running",
	}

	// Pick the message for this send: the main content (+images) on the first
	// successful delivery to this group, the text-only reminder on every later
	// send. A campaign with no reminder text always sends the main content.
	content, attachmentIDs := pickContent(
		b.content, b.attachmentIDs, b.reminderContent,
		segmentHasSuccessfulRun(b.tenantID, seg.ID),
	)

	var group models.CRMGroup
	gErr := db.DB.Where("id = ? AND tenant_id = ?", seg.GroupID, b.tenantID).First(&group).Error
	switch {
	case gErr != nil:
		run.Status, run.FailCount, run.ErrorMessage = "error", 1, "group_not_found"
		fail = 1
	case group.ZaloGroupID == "":
		run.Status, run.FailCount, run.ErrorMessage = "error", 1, "group_has_no_zalo_group"
		fail = 1
	default:
		if sErr := b.sendToGroup(ctx, group.ZaloGroupID, content, attachmentIDs); sErr != nil {
			run.Status, run.FailCount, run.ErrorMessage = "error", 1, sErr.Error()
			fail = 1
		} else {
			run.Status, run.SentCount = "success", 1
			sent = 1
		}
	}
	finished := time.Now()
	run.FinishedAt = &finished
	db.DB.Create(&run)
	return sent, fail
}

// sendToGroup sends the campaign's text (if any) then each uploaded image in
// order to one GMF group. A failed text send aborts immediately. Image sends
// continue past a single failure so one bad image never drops the rest; any
// image errors are aggregated into a single descriptive error so the caller
// records the run as failed with an actionable message ("text sent but N image(s)
// failed: ..."). A fully successful send returns nil.
func (b *campaignBroadcast) sendToGroup(ctx context.Context, zaloGroupID, content string, attachmentIDs []string) error {
	if strings.TrimSpace(content) != "" {
		if err := b.adapter.SendGroupMessage(ctx, zaloGroupID, content); err != nil {
			return err
		}
	}
	var imgErrs []string
	for i, attachmentID := range attachmentIDs {
		if err := b.adapter.SendGroupImage(ctx, zaloGroupID, attachmentID); err != nil {
			imgErrs = append(imgErrs, fmt.Sprintf("image %d/%d: %v", i+1, len(attachmentIDs), err))
		}
	}
	if len(imgErrs) > 0 {
		return fmt.Errorf("text sent but %d/%d image(s) failed: %s",
			len(imgErrs), len(attachmentIDs), strings.Join(imgErrs, "; "))
	}
	return nil
}

// pickContent chooses the message + images for a single send. The text-only
// reminder is used only when it is non-empty AND the segment already delivered
// successfully at least once; otherwise the main content (with its images) is
// used. Reminders never carry images. Kept pure (no DB) so it is unit-testable.
func pickContent(mainContent string, mainAttachments []string, reminderContent string, hasPriorSuccess bool) (content string, attachmentIDs []string) {
	if reminderContent != "" && hasPriorSuccess {
		return reminderContent, nil
	}
	return mainContent, mainAttachments
}

// segmentHasSuccessfulRun reports whether a segment already has at least one
// successful broadcast (sent_count > 0). Used to decide between the main message
// (first delivery) and the reminder message (every later send).
func segmentHasSuccessfulRun(tenantID, segmentID string) bool {
	var n int64
	db.DB.Model(&models.CampaignRun{}).
		Where("tenant_id = ? AND segment_id = ? AND sent_count > 0", tenantID, segmentID).
		Limit(1).Count(&n)
	return n > 0
}

// SendCampaignNow fires every segment of a campaign immediately. Used by the
// "Gửi ngay" HTTP handler. A sent campaign becomes active (unchanged behavior).
func SendCampaignNow(ctx context.Context, campaignID, tenantID string) (sent, fail int, err error) {
	b, err := prepareCampaignBroadcast(ctx, campaignID, tenantID)
	if err != nil {
		return 0, 0, err
	}

	consumedOnce := false
	hasFutureRun := false
	for i := range b.campaign.Segments {
		seg := b.campaign.Segments[i]
		s, f := b.fireSegment(ctx, seg)
		sent += s
		fail += f

		if seg.ScheduleKind == "once" {
			// Sending manually completes the one-time job: mark it consumed so the
			// scheduler won't fire it again at its RunAt (mirrors fireCampaignSegment).
			db.DB.Model(&models.CampaignSegment{}).Where("id = ?", seg.ID).Update("next_run_at", nil)
			consumedOnce = true
		} else if strings.TrimSpace(seg.Cron) != "" {
			hasFutureRun = true
		}
	}

	// A campaign whose only schedule was a one-time send is finished once that send
	// is consumed; one that still has recurring segments keeps running.
	status := "active"
	if consumedOnce && !hasFutureRun {
		status = "done"
	}
	db.DB.Model(b.campaign).Updates(map[string]interface{}{"status": status, "updated_at": time.Now()})

	// Drop any pending OneTimeJob for the once-segments we just consumed.
	if consumedOnce {
		if sched := GetDefaultScheduler(); sched != nil {
			sched.ReloadCampaignJobs()
		}
	}
	return sent, fail, nil
}

// buildBroadcastContent joins the campaign's message text and optional link.
func buildBroadcastContent(c *models.Campaign) string {
	parts := []string{strings.TrimSpace(c.MessageText)}
	if link := strings.TrimSpace(c.MessageLink); link != "" {
		parts = append(parts, link)
	}
	return strings.TrimSpace(strings.Join(parts, "\n"))
}

// nextCampaignRun computes the next fire time for a 5-field cron expression in
// loc. Mirrors handlers.nextCronRun (engine cannot import handlers).
func nextCampaignRun(expr string, loc *time.Location) *time.Time {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return nil
	}
	parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
	sched, err := parser.Parse(expr)
	if err != nil {
		return nil
	}
	next := sched.Next(time.Now().In(loc))
	if next.IsZero() {
		return nil
	}
	return &next
}
