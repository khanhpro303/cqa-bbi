package engine

import (
	"context"
	"encoding/json"
	"errors"
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
	tenantID string
}

// prepareCampaignBroadcast loads the campaign + segments, runs the channel guard
// (must exist + be an active zalo_oa channel), decrypts credentials, builds the
// Zalo adapter (with the same token-refresh persistence callback the handler
// used), and builds the broadcast content.
func prepareCampaignBroadcast(_ context.Context, campaignID, tenantID string) (*campaignBroadcast, error) {
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
		campaign: &campaign,
		channel:  &channel,
		adapter:  adapter,
		content:  buildBroadcastContent(&campaign),
		tenantID: tenantID,
	}, nil
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
		if sErr := b.adapter.SendGroupMessage(ctx, group.ZaloGroupID, b.content); sErr != nil {
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

// SendCampaignNow fires every segment of a campaign immediately. Used by the
// "Gửi ngay" HTTP handler. A sent campaign becomes active (unchanged behavior).
func SendCampaignNow(ctx context.Context, campaignID, tenantID string) (sent, fail int, err error) {
	b, err := prepareCampaignBroadcast(ctx, campaignID, tenantID)
	if err != nil {
		return 0, 0, err
	}

	for i := range b.campaign.Segments {
		s, f := b.fireSegment(ctx, b.campaign.Segments[i])
		sent += s
		fail += f
	}

	db.DB.Model(b.campaign).Updates(map[string]interface{}{"status": "active", "updated_at": time.Now()})
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
