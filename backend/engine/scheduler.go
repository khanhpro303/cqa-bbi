package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/go-co-op/gocron/v2"
	"github.com/vietbui/chat-quality-agent/channels"
	"github.com/vietbui/chat-quality-agent/config"
	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/db/models"
)

// Scheduler manages periodic tasks: channel sync, job analysis, output delivery.
type Scheduler struct {
	scheduler  gocron.Scheduler
	syncEngine *SyncEngine
	cfg        *config.Config
}

// defaultScheduler is the global scheduler instance, accessible from handlers.
var defaultScheduler *Scheduler

// SetDefaultScheduler sets the global scheduler (called once from main).
func SetDefaultScheduler(s *Scheduler) {
	defaultScheduler = s
}

// GetDefaultScheduler returns the global scheduler.
func GetDefaultScheduler() *Scheduler {
	return defaultScheduler
}

func NewScheduler(cfg *config.Config) (*Scheduler, error) {
	s, err := gocron.NewScheduler()
	if err != nil {
		return nil, err
	}

	return &Scheduler{
		scheduler:  s,
		syncEngine: NewSyncEngine(cfg),
		cfg:        cfg,
	}, nil
}

// Start begins the scheduler. Call this once at app startup.
func (s *Scheduler) Start() {
	// Check channels for sync every 5 minutes (per-channel interval in metadata)
	_, err := s.scheduler.NewJob(
		gocron.DurationJob(5*time.Minute),
		gocron.NewTask(s.syncAllChannelsTask),
		gocron.WithName("sync-all-channels"),
	)
	if err != nil {
		log.Printf("[scheduler] failed to create sync job: %v", err)
	}

	// Cleanup old sync activity logs daily (keep success 90d, error 180d)
	_, err = s.scheduler.NewJob(
		gocron.DurationJob(24*time.Hour),
		gocron.NewTask(func() {
			CleanupOldSyncLogs(90, 180)
		}),
		gocron.WithName("cleanup-sync-logs"),
	)
	if err != nil {
		log.Printf("[scheduler] failed to create cleanup job: %v", err)
	}

	// Cleanup old Astra DB chat history daily (keep N days retention)
	_, err = s.scheduler.NewJob(
		gocron.DurationJob(24*time.Hour),
		gocron.NewTask(s.cleanupAstraDBChatHistory),
		gocron.WithName("cleanup-astradb-chat-history"),
	)
	if err != nil {
		log.Printf("[scheduler] failed to create Astra DB cleanup job: %v", err)
	}

	// Load and schedule cron-based analysis jobs
	s.loadCronJobs()

	// Load and schedule CRM campaign segment jobs
	s.loadCampaignJobs()

	// Safety net: mark any stuck "running" jobs as failed on startup
	cleanupStuckRuns()

	s.scheduler.Start()
	log.Println("[scheduler] started")
}

// cleanupStuckRuns marks any job_runs stuck in "running" status as failed.
// This happens when the app crashes or restarts while a job is processing.
func cleanupStuckRuns() {
	// On startup, any "running" job is stuck because the goroutine died with the previous process
	var stuckRuns []models.JobRun
	if err := db.DB.Where("status = ?", "running").Find(&stuckRuns).Error; err != nil {
		log.Printf("[scheduler] error querying stuck runs: %v", err)
		return
	}
	for _, run := range stuckRuns {
		now := time.Now()
		if err := db.DB.Model(&run).Updates(map[string]interface{}{
			"status":        "failed",
			"finished_at":   &now,
			"error_message": "Job bị gián đoạn do hệ thống khởi động lại. Vui lòng chạy lại.",
		}).Error; err != nil {
			log.Printf("[scheduler] error marking stuck run %s as failed: %v", run.ID, err)
		} else {
			log.Printf("[scheduler] marked stuck run %s as failed (started: %v)", run.ID, run.StartedAt)
		}
	}
	if len(stuckRuns) > 0 {
		log.Printf("[scheduler] cleaned up %d stuck job runs", len(stuckRuns))
	}
}

// Stop gracefully shuts down the scheduler.
func (s *Scheduler) Stop() {
	if err := s.scheduler.Shutdown(); err != nil {
		log.Printf("[scheduler] shutdown error: %v", err)
	}
	log.Println("[scheduler] stopped")
}

func (s *Scheduler) syncAllChannelsTask() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	var chans []models.Channel
	db.DB.Where("is_active = true").Find(&chans)

	now := time.Now()
	synced := 0
	for _, ch := range chans {
		if channels.IsExternallyManagedImport(ch.ChannelType) {
			continue
		}

		// Check per-channel sync interval from metadata
		interval := 15 // default 15 minutes
		if ch.Metadata != "" {
			var meta map[string]interface{}
			if json.Unmarshal([]byte(ch.Metadata), &meta) == nil {
				if si, ok := meta["sync_interval"]; ok {
					if v, ok := si.(float64); ok && v > 0 {
						interval = int(v)
					}
				}
			}
		}

		// Skip if last sync was too recent
		if ch.LastSyncAt != nil {
			elapsed := now.Sub(*ch.LastSyncAt)
			if elapsed < time.Duration(interval)*time.Minute {
				continue
			}
		}

		if err := s.syncEngine.SyncChannel(ctx, ch); err != nil {
			log.Printf("[scheduler] sync channel %s failed: %v", ch.Name, err)
		} else {
			synced++
		}
	}
	if synced > 0 {
		log.Printf("[scheduler] synced %d/%d channels", synced, len(chans))
	}
}

// tenantTimezone returns the timezone configured for a tenant, defaulting to Asia/Ho_Chi_Minh.
func tenantTimezone(tenantID string) string {
	var setting models.AppSetting
	db.DB.Where("tenant_id = ? AND setting_key = 'timezone'", tenantID).First(&setting)
	if setting.ValuePlain != "" {
		return setting.ValuePlain
	}
	return "Asia/Ho_Chi_Minh"
}

// loadCronJobs loads active jobs with cron schedules and registers them.
func (s *Scheduler) loadCronJobs() {
	var jobs []models.Job
	db.DB.Where("is_active = true AND schedule_type = 'cron' AND schedule_cron != ''").Find(&jobs)

	for _, job := range jobs {
		j := job // capture
		tz := tenantTimezone(j.TenantID)
		cronExpr := fmt.Sprintf("TZ=%s %s", tz, j.ScheduleCron)
		_, err := s.scheduler.NewJob(
			gocron.CronJob(cronExpr, false),
			gocron.NewTask(func() {
				log.Printf("[scheduler] running analysis job %s (%s)", j.Name, j.ID)
				analyzer := NewAnalyzer(s.cfg)
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
				defer cancel()
				if _, err := analyzer.RunJob(ctx, j); err != nil {
					log.Printf("[scheduler] job %s failed: %v", j.Name, err)
				}
			}),
			gocron.WithName("job-"+j.ID),
		)
		if err != nil {
			log.Printf("[scheduler] failed to schedule job %s: %v", j.Name, err)
		}
	}

	log.Printf("[scheduler] loaded %d cron jobs", len(jobs))
}

// ReloadJobs removes all cron analysis jobs and reloads from DB.
// Call this after creating, updating, or deleting a job.
func (s *Scheduler) ReloadJobs() {
	// Remove existing cron analysis jobs
	jobs := s.scheduler.Jobs()
	for _, j := range jobs {
		if len(j.Name()) > 4 && j.Name()[:4] == "job-" {
			if err := s.scheduler.RemoveJob(j.ID()); err != nil {
				log.Printf("[scheduler] error removing job %s: %v", j.Name(), err)
			}
		}
	}
	// Reload from DB
	s.loadCronJobs()
	log.Println("[scheduler] cron jobs reloaded")
}

// TriggerAfterSyncJobs triggers all active after_sync jobs for a tenant+channel.
func (s *Scheduler) TriggerAfterSyncJobs(tenantID, channelID string) {
	var jobs []models.Job
	if err := db.DB.Where("tenant_id = ? AND is_active = true AND schedule_type = 'after_sync'",
		tenantID).Find(&jobs).Error; err != nil {
		log.Printf("[scheduler] error querying after_sync jobs: %v", err)
		return
	}

	for _, job := range jobs {
		// Check if this job uses the synced channel
		var channelIDs []string
		if err := json.Unmarshal([]byte(job.InputChannelIDs), &channelIDs); err != nil {
			continue
		}
		found := false
		for _, id := range channelIDs {
			if id == channelID {
				found = true
				break
			}
		}
		if !found {
			continue
		}

		j := job // capture
		log.Printf("[scheduler] after-sync trigger: job=%s tenant=%s channel=%s", j.Name, tenantID, channelID)
		go func() {
			defer func() {
				if r := recover(); r != nil {
					log.Printf("[security] panic in after-sync job %s: %v", j.Name, r)
				}
			}()
			analyzer := NewAnalyzer(s.cfg)
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
			defer cancel()
			if _, err := analyzer.RunJob(ctx, j); err != nil {
				log.Printf("[scheduler] after-sync job %s failed: %v", j.Name, err)
			}
		}()
	}
}

// SyncEngine returns the sync engine for manual trigger.
func (s *Scheduler) SyncEngine() *SyncEngine {
	return s.syncEngine
}

// cleanupAstraDBChatHistory removes old chat logs from Astra DB daily.
func (s *Scheduler) cleanupAstraDBChatHistory() {
	if s.cfg.AstraDBAPIEndpoint == "" || s.cfg.AstraDBToken == "" || s.cfg.AstraDBCollection == "" {
		return
	}

	retentionDays := s.cfg.ChatbotHistoryRetentionDays
	if retentionDays <= 0 {
		retentionDays = 30 // fallback
	}

	cutoff := time.Now().AddDate(0, 0, -retentionDays).Unix()

	url := fmt.Sprintf("%s/api/json/v1/%s/%s", s.cfg.AstraDBAPIEndpoint, s.cfg.AstraDBKeyspace, s.cfg.AstraDBCollection)

	// Build the JSON payload to delete all documents older than the cutoff
	payload := map[string]interface{}{
		"deleteMany": map[string]interface{}{
			"filter": map[string]interface{}{
				"created_at": map[string]interface{}{
					"$lt": cutoff,
				},
			},
		},
	}

	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		log.Printf("[scheduler] failed to marshal Astra DB cleanup payload: %v", err)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(bodyBytes))
	if err != nil {
		log.Printf("[scheduler] failed to create Astra DB cleanup request: %v", err)
		return
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Token", s.cfg.AstraDBToken)

	client := &http.Client{Timeout: 50 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("[scheduler] Astra DB cleanup request failed: %v", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		log.Printf("[scheduler] Astra DB cleanup returned error status %d", resp.StatusCode)
		return
	}

	log.Printf("[scheduler] successfully cleaned up Astra DB chat history older than %d days (cutoff: %v)", retentionDays, time.Unix(cutoff, 0))
}
