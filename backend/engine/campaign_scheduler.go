package engine

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/go-co-op/gocron/v2"
	"github.com/google/uuid"

	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/db/models"
)

// campaignJobPrefix tags scheduler jobs that belong to CRM campaign segments, so
// ReloadCampaignJobs can remove only those (without touching analysis "job-" jobs).
const campaignJobPrefix = "campaign-"

// campaignFireTimeout caps a single segment broadcast.
const campaignFireTimeout = 5 * time.Minute

// loadCampaignJobs registers a scheduler job for each segment of every active
// campaign. recurring segments use a cron job; once segments use a one-time job
// (skipped when RunAt is missing or already in the past — missed runs are not
// caught up).
func (s *Scheduler) loadCampaignJobs() {
	var campaigns []models.Campaign
	db.DB.Preload("Segments").Where("status = ?", "active").Find(&campaigns)

	now := time.Now()
	loaded := 0
	for _, campaign := range campaigns {
		tz := tenantTimezone(campaign.TenantID)
		for _, seg := range campaign.Segments {
			segID := seg.ID // capture
			name := gocron.WithName(campaignJobPrefix + segID)
			task := gocron.NewTask(func() { s.fireCampaignSegment(segID) })

			var def gocron.JobDefinition
			switch {
			case seg.ScheduleKind == "once":
				// NextRunAt==nil marks a consumed once-segment (fired on schedule or
				// completed early via "Gửi ngay") — never re-register it.
				if seg.RunAt == nil || !seg.RunAt.After(now) || seg.NextRunAt == nil {
					continue // missed, unscheduled, or already consumed once-segment: skip
				}
				def = gocron.OneTimeJob(gocron.OneTimeJobStartDateTime(*seg.RunAt))
			default: // recurring
				if strings.TrimSpace(seg.Cron) == "" {
					continue
				}
				def = gocron.CronJob(fmt.Sprintf("TZ=%s %s", tz, seg.Cron), false)
			}

			if _, err := s.scheduler.NewJob(def, task, name); err != nil {
				log.Printf("[scheduler] failed to schedule campaign segment %s: %v", segID, err)
				continue
			}
			loaded++
		}
	}

	log.Printf("[scheduler] loaded %d campaign jobs", loaded)
}

// ReloadCampaignJobs removes all campaign segment jobs and reloads from DB.
// Call this after creating, updating, deleting, or changing the status of a campaign.
func (s *Scheduler) ReloadCampaignJobs() {
	for _, j := range s.scheduler.Jobs() {
		if strings.HasPrefix(j.Name(), campaignJobPrefix) {
			if err := s.scheduler.RemoveJob(j.ID()); err != nil {
				log.Printf("[scheduler] error removing campaign job %s: %v", j.Name(), err)
			}
		}
	}
	s.loadCampaignJobs()
	log.Println("[scheduler] campaign jobs reloaded")
}

// fireCampaignSegment is the worker invoked when a segment's schedule fires. It
// re-reads fresh state (the captured segment may be stale by fire time), skips
// non-active campaigns, broadcasts, and advances NextRunAt.
func (s *Scheduler) fireCampaignSegment(segmentID string) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[scheduler] panic in campaign segment %s: %v", segmentID, r)
		}
	}()

	var seg models.CampaignSegment
	if err := db.DB.Where("id = ?", segmentID).First(&seg).Error; err != nil {
		log.Printf("[scheduler] campaign segment %s gone, skipping: %v", segmentID, err)
		return
	}

	var campaign models.Campaign
	if err := db.DB.Where("id = ?", seg.CampaignID).First(&campaign).Error; err != nil {
		log.Printf("[scheduler] campaign %s for segment %s gone, skipping: %v", seg.CampaignID, segmentID, err)
		return
	}
	if campaign.Status != "active" {
		log.Printf("[scheduler] campaign %s not active (status=%s), skipping segment %s", campaign.ID, campaign.Status, segmentID)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), campaignFireTimeout)
	defer cancel()

	b, err := prepareCampaignBroadcast(ctx, campaign.ID, campaign.TenantID)
	if err != nil {
		if errors.Is(err, ErrCampaignChannelInactive) {
			// Mirror the warning-bell semantics: record why the send was skipped.
			now := time.Now()
			db.DB.Create(&models.CampaignRun{
				ID:           uuid.New().String(),
				TenantID:     campaign.TenantID,
				CampaignID:   campaign.ID,
				SegmentID:    seg.ID,
				StartedAt:    now,
				FinishedAt:   &now,
				FailCount:    1,
				Status:       "error",
				ErrorMessage: "channel_inactive",
			})
			log.Printf("[scheduler] campaign %s segment %s skipped: channel_inactive", campaign.ID, segmentID)
			return
		}
		log.Printf("[scheduler] campaign %s segment %s prepare failed: %v", campaign.ID, segmentID, err)
		return
	}

	sent, fail := b.fireSegment(ctx, seg)
	log.Printf("[scheduler] campaign %s segment %s fired: sent=%d fail=%d", campaign.ID, segmentID, sent, fail)

	// Advance NextRunAt so the dashboard "upcoming runs" stays accurate.
	if seg.ScheduleKind == "once" {
		db.DB.Model(&seg).Update("next_run_at", nil) // consumed
	} else if loc, lerr := time.LoadLocation(tenantTimezone(campaign.TenantID)); lerr == nil {
		db.DB.Model(&seg).Update("next_run_at", nextCampaignRun(seg.Cron, loc))
	}
}
