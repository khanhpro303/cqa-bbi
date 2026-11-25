package cloudifyorders

import (
	"context"
	"errors"
	"log"
	"sync"
	"time"

	"github.com/vietbui/chat-quality-agent/db/models"
	"github.com/vietbui/chat-quality-agent/pkg"
	"github.com/vietbui/chat-quality-agent/realtime"
	"gorm.io/gorm"
)

const (
	reconcileEvery  = 30 * time.Second
	tenantTick      = 3 * time.Second
	leaseFor        = 90 * time.Second
	pollTimeout     = 2 * time.Minute
	deliveryTimeout = 40 * time.Second
)

// Watcher runs one polling loop per tenant that turned the watcher on. A
// database lease keeps it to one replica per tenant.
type Watcher struct {
	DB         *gorm.DB
	NewFetcher func(tenantID string) (Fetcher, error)
	Sender     Sender
	Now        func() time.Time

	owner   string
	mu      sync.Mutex
	running map[string]context.CancelFunc
}

// NewWatcher builds a Watcher that reads Cloudify with the tenant's ERP
// settings and messages customers' Zalo groups.
func NewWatcher(database *gorm.DB, encryptionKey string) *Watcher {
	return &Watcher{
		DB: database,
		NewFetcher: func(tenantID string) (Fetcher, error) {
			return NewCloudifyFetcher(database, tenantID, encryptionKey)
		},
		Sender: ZaloGroupSender{DB: database},
		Now:    time.Now,
	}
}

// Run blocks until ctx ends.
func (w *Watcher) Run(ctx context.Context) {
	w.owner = pkg.NewUUID()
	w.running = map[string]context.CancelFunc{}
	ticker := time.NewTicker(reconcileEvery)
	defer ticker.Stop()
	for {
		w.reconcile(ctx)
		select {
		case <-ctx.Done():
			w.mu.Lock()
			for _, cancel := range w.running {
				cancel()
			}
			w.mu.Unlock()
			return
		case <-ticker.C:
		}
	}
}

func (w *Watcher) reconcile(ctx context.Context) {
	tenants, err := EnabledTenants(w.DB)
	if err != nil {
		log.Printf("[cloudify-orders] cannot list tenants: %v", err)
		return
	}
	wanted := map[string]bool{}
	for _, id := range tenants {
		wanted[id] = true
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	for id, cancel := range w.running {
		if !wanted[id] {
			cancel()
			delete(w.running, id)
		}
	}
	for id := range wanted {
		if _, ok := w.running[id]; ok {
			continue
		}
		tenantCtx, cancel := context.WithCancel(ctx)
		w.running[id] = cancel
		go w.runTenant(tenantCtx, id)
	}
}

func (w *Watcher) runTenant(ctx context.Context, tenantID string) {
	defer Release(w.DB, tenantID, w.owner)
	ticker := time.NewTicker(tenantTick)
	defer ticker.Stop()
	for {
		w.Tick(ctx, tenantID)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// Tick polls when due (interval passed or a webhook nudged), then delivers
// pending events. Exported for tests and manual triggers.
func (w *Watcher) Tick(ctx context.Context, tenantID string) {
	now := w.Now()
	if !Claim(w.DB, tenantID, w.owner, now, leaseFor) {
		return
	}
	settings, err := LoadSettings(w.DB, tenantID)
	if err != nil || !settings.WatchEnabled {
		return
	}
	watch, err := LoadWatch(w.DB, tenantID)
	if err != nil {
		return
	}
	if pollDue(watch, settings, now) {
		w.poll(ctx, tenantID, watch, settings, now)
		// Polling can outlive the lease or a settings edit.
		now = w.Now()
		if !Claim(w.DB, tenantID, w.owner, now, leaseFor) {
			return
		}
		settings, err = LoadSettings(w.DB, tenantID)
		if err != nil || !settings.WatchEnabled {
			return
		}
	}
	// Finish the delivery batch within the remaining lease window.
	deliveryCtx, cancel := context.WithTimeout(ctx, deliveryTimeout)
	defer cancel()
	if delivered, err := DeliverPending(deliveryCtx, w.DB, tenantID, settings, w.Sender, now); err != nil {
		log.Printf("[cloudify-orders] tenant %s delivery failed: %v", tenantID, err)
	} else if delivered > 0 {
		realtime.Publish(ctx, tenantID, realtime.EventCloudifyOrdersUpdated)
	}
}

func pollDue(watch models.CloudifyOrderWatch, settings Settings, now time.Time) bool {
	if watch.LastPollAt == nil {
		return true
	}
	if watch.NudgedAt != nil && watch.NudgedAt.After(*watch.LastPollAt) {
		return true
	}
	return now.Sub(*watch.LastPollAt) >= settings.PollInterval
}

func (w *Watcher) poll(ctx context.Context, tenantID string, watch models.CloudifyOrderWatch, settings Settings, now time.Time) {
	// Record the attempt first so a failing Cloudify is retried at the poll
	// interval, not every tick.
	w.DB.Model(&models.CloudifyOrderWatch{}).Where("tenant_id = ?", tenantID).Update("last_poll_at", now)
	fetcher, err := w.NewFetcher(tenantID)
	if err != nil {
		w.fail(ctx, tenantID, err)
		return
	}
	pollCtx, cancel := context.WithTimeout(ctx, pollTimeout)
	defer cancel()
	rows, err := fetchWithContext(pollCtx, func() ([]map[string]any, error) {
		return fetcher.ByWindow(now.AddDate(0, 0, -settings.WindowDays), now)
	})
	if err != nil {
		w.fail(ctx, tenantID, err)
		return
	}
	seeding := watch.SeededAt == nil
	result, err := Ingest(ctx, w.DB, tenantID, rows, SourcePoll, diffOptions(watch), now)
	if err != nil {
		w.fail(ctx, tenantID, err)
		return
	}
	updates := map[string]any{"last_ok_at": now, "last_error": "", "last_count": result.Fetched}
	if seeding {
		updates["seeded_at"] = now
		log.Printf("[cloudify-orders] tenant %s seeded with %d orders", tenantID, result.Fetched)
	}
	w.updatePollStatus(ctx, tenantID, updates)
	if len(result.Events) > 0 {
		log.Printf("[cloudify-orders] tenant %s: %d order change(s) detected", tenantID, len(result.Events))
	}
}

// diffOptions derives the diff behaviour from the watch state: no events until
// the first snapshot exists, and orders dated before seeding are backfill.
func diffOptions(watch models.CloudifyOrderWatch) DiffOptions {
	if watch.SeededAt == nil {
		return DiffOptions{Seeding: true}
	}
	return DiffOptions{CreatedCutoff: watch.SeededAt.AddDate(0, 0, -1)}
}

func (w *Watcher) fail(ctx context.Context, tenantID string, err error) {
	message := "Không đọc được đơn hàng từ Cloudify."
	if errors.Is(err, ErrNotConfigured) {
		message = "Chưa cấu hình tài khoản Cloudify (Cài đặt → ERP)."
	}
	log.Printf("[cloudify-orders] tenant %s poll failed: %v", tenantID, err)
	w.updatePollStatus(ctx, tenantID, map[string]any{"last_error": message})
}

func (w *Watcher) updatePollStatus(ctx context.Context, tenantID string, updates map[string]any) {
	if err := w.DB.Model(&models.CloudifyOrderWatch{}).Where("tenant_id = ?", tenantID).Updates(updates).Error; err != nil {
		log.Printf("[cloudify-orders] tenant %s could not persist poll status: %v", tenantID, err)
		return
	}
	realtime.Publish(ctx, tenantID, realtime.EventCloudifyOrdersUpdated)
}

// fetchWithContext stops waiting when ctx ends. The Cloudify client has its
// own HTTP timeout; this only bounds how long one poll can block the loop.
func fetchWithContext(ctx context.Context, fetch func() ([]map[string]any, error)) ([]map[string]any, error) {
	type out struct {
		rows []map[string]any
		err  error
	}
	done := make(chan out, 1)
	go func() {
		rows, err := fetch()
		done <- out{rows, err}
	}()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case res := <-done:
		return res.rows, res.err
	}
}

// IngestByCode fetches one order by code and ingests it right away. The
// webhook uses it so a change is noticed in about a second instead of at the
// next poll.
func IngestByCode(ctx context.Context, database *gorm.DB, fetcher Fetcher, tenantID, code string, now time.Time) (IngestResult, error) {
	watch, err := LoadWatch(database, tenantID)
	if err != nil {
		return IngestResult{}, err
	}
	rows, err := fetchWithContext(ctx, func() ([]map[string]any, error) { return fetcher.ByCode(code) })
	if err != nil {
		return IngestResult{}, err
	}
	return Ingest(ctx, database, tenantID, rows, SourceWebhook, diffOptions(watch), now)
}
