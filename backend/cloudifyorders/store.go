package cloudifyorders

import (
	"context"
	"errors"
	"time"

	"github.com/vietbui/chat-quality-agent/db/models"
	"github.com/vietbui/chat-quality-agent/pkg"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const snapshotChunk = 500

// IngestResult summarizes one Ingest call.
type IngestResult struct {
	Fetched int
	Stored  int
	Events  []models.CloudifyOrderEvent
}

// Ingest diffs raw saorders/search rows against the stored snapshots, then
// stores the new snapshots and one outbox event per change in a single
// transaction. Events whose DedupKey already exists are skipped, so calling
// Ingest twice with the same rows (or from two sources) is harmless.
func Ingest(ctx context.Context, database *gorm.DB, tenantID string, rows []map[string]any, source string, opts DiffOptions, now time.Time) (IngestResult, error) {
	orders := make([]Order, 0, len(rows))
	for _, row := range rows {
		if order, ok := ParseOrder(row); ok {
			orders = append(orders, order)
		}
	}
	result := IngestResult{Fetched: len(orders)}
	if len(orders) == 0 {
		return result, nil
	}
	prev, err := loadPrevious(ctx, database, tenantID, orders)
	if err != nil {
		return result, err
	}
	changes, updates := Diff(prev, orders, opts)
	if len(updates) == 0 {
		return result, nil
	}
	err = database.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		snapshots := make([]models.CloudifyOrderSnapshot, 0, len(updates))
		for _, order := range updates {
			snapshots = append(snapshots, models.CloudifyOrderSnapshot{
				TenantID: tenantID, OrderID: order.ID, OrderCode: truncate(order.Code, 64),
				CustomerCode: truncate(order.CustomerCode, 64), Status: truncate(order.Status, 16),
				OrderDate: truncate(order.Date, 32), Total: order.Total, Fingerprint: order.Fingerprint,
				FirstSeenAt: now, UpdatedAt: now,
			})
		}
		if err := tx.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "tenant_id"}, {Name: "order_id"}},
			DoUpdates: clause.AssignmentColumns([]string{"order_code", "customer_code", "status", "order_date", "total", "fingerprint", "updated_at"}),
		}).CreateInBatches(&snapshots, 200).Error; err != nil {
			return err
		}
		result.Stored = len(snapshots)
		for _, change := range changes {
			event := models.CloudifyOrderEvent{
				ID: pkg.NewUUID(), TenantID: tenantID, DedupKey: DedupKey(tenantID, change),
				Kind: change.Kind, Source: source, OrderID: change.Order.ID,
				OrderCode: truncate(change.Order.Code, 64), CustomerCode: truncate(change.Order.CustomerCode, 64),
				CustomerName: truncate(change.Order.CustomerName, 255), PrevStatus: truncate(change.PrevStatus, 16),
				Status: truncate(change.Order.Status, 16), OrderDate: truncate(change.Order.Date, 32),
				Total: change.Order.Total, DeliveryStatus: DeliveryPending, CreatedAt: now,
			}
			res := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "dedup_key"}}, DoNothing: true}).Create(&event)
			if res.Error != nil {
				return res.Error
			}
			if res.RowsAffected == 1 {
				result.Events = append(result.Events, event)
			}
		}
		return nil
	})
	if err != nil {
		result.Stored = 0
		result.Events = nil
	}
	return result, err
}

func loadPrevious(ctx context.Context, database *gorm.DB, tenantID string, orders []Order) (map[int64]Previous, error) {
	prev := make(map[int64]Previous, len(orders))
	ids := make([]int64, 0, len(orders))
	for _, order := range orders {
		ids = append(ids, order.ID)
	}
	for start := 0; start < len(ids); start += snapshotChunk {
		end := start + snapshotChunk
		if end > len(ids) {
			end = len(ids)
		}
		var rows []models.CloudifyOrderSnapshot
		if err := database.WithContext(ctx).Select("order_id", "status", "fingerprint").
			Where("tenant_id = ? AND order_id IN ?", tenantID, ids[start:end]).Find(&rows).Error; err != nil {
			return nil, err
		}
		for _, row := range rows {
			prev[row.OrderID] = Previous{Status: row.Status, Fingerprint: row.Fingerprint}
		}
	}
	return prev, nil
}

// LoadWatch returns the tenant's watch row, creating it when missing.
func LoadWatch(database *gorm.DB, tenantID string) (models.CloudifyOrderWatch, error) {
	var watch models.CloudifyOrderWatch
	err := database.Where("tenant_id = ?", tenantID).First(&watch).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		watch = models.CloudifyOrderWatch{TenantID: tenantID, UpdatedAt: time.Now()}
		if err := database.Clauses(clause.OnConflict{DoNothing: true}).Create(&watch).Error; err != nil {
			return watch, err
		}
		err = database.Where("tenant_id = ?", tenantID).First(&watch).Error
	}
	return watch, err
}

// Claim takes or renews the tenant lease. It returns false when another
// replica holds a live lease.
func Claim(database *gorm.DB, tenantID, owner string, now time.Time, lease time.Duration) bool {
	watch, err := LoadWatch(database, tenantID)
	if err != nil {
		return false
	}
	// Still ours for more than half the lease: skip the write.
	if watch.LeaseOwner == owner && watch.LeaseUntil != nil && watch.LeaseUntil.After(now.Add(lease/2)) {
		return true
	}
	res := database.Model(&models.CloudifyOrderWatch{}).
		Where("tenant_id = ? AND (lease_owner = ? OR lease_until IS NULL OR lease_until < ?)", tenantID, owner, now).
		Updates(map[string]any{"lease_owner": owner, "lease_until": now.Add(lease)})
	return res.Error == nil && res.RowsAffected == 1
}

// Release drops the lease so another replica can take over at once.
func Release(database *gorm.DB, tenantID, owner string) {
	database.Model(&models.CloudifyOrderWatch{}).Where("tenant_id = ? AND lease_owner = ?", tenantID, owner).
		Updates(map[string]any{"lease_owner": "", "lease_until": nil})
}

// Nudge asks the replica that owns the tenant to poll right away.
func Nudge(database *gorm.DB, tenantID string, now time.Time) error {
	if _, err := LoadWatch(database, tenantID); err != nil {
		return err
	}
	return database.Model(&models.CloudifyOrderWatch{}).Where("tenant_id = ?", tenantID).Update("nudged_at", now).Error
}

func truncate(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max])
}
