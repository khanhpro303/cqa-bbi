package models

import "time"

// CloudifyOrderWatch is the per-tenant state of the Cloudify order watcher.
// Cloudify's REST API has no webhooks and no "modified at" field, so CQA
// notices new and changed orders by comparing what saorders/search returns
// with the last snapshot it stored. A database lease makes sure only one
// replica polls a tenant at a time.
type CloudifyOrderWatch struct {
	TenantID string `gorm:"type:char(36);primaryKey"`
	// SeededAt is when the first full snapshot was taken. Orders seen during
	// seeding never produce events, so turning the watcher on does not message
	// every customer about their old orders.
	SeededAt   *time.Time
	LastPollAt *time.Time
	LastOKAt   *time.Time
	LastError  string `gorm:"type:varchar(255);not null;default:''"`
	LastCount  int    `gorm:"not null;default:0"`
	// NudgedAt is set by the webhook (or an admin) to ask the owner replica to
	// poll right away instead of waiting for the next interval.
	NudgedAt   *time.Time
	LeaseOwner string `gorm:"type:char(36);not null;default:''"`
	LeaseUntil *time.Time
	UpdatedAt  time.Time
}

func (CloudifyOrderWatch) TableName() string { return "cloudify_order_watches" }

// CloudifyOrderSnapshot is the last observed state of one Cloudify order.
// Fingerprint is a hash of the whole order row, so any edit (status, lines,
// note, discount) changes it.
type CloudifyOrderSnapshot struct {
	TenantID     string `gorm:"type:char(36);primaryKey"`
	OrderID      int64  `gorm:"primaryKey;autoIncrement:false"`
	OrderCode    string `gorm:"type:varchar(64);not null;default:'';index"`
	CustomerCode string `gorm:"type:varchar(64);not null;default:''"`
	Status       string `gorm:"type:varchar(16);not null;default:''"`
	OrderDate    string `gorm:"type:varchar(32);not null;default:''"`
	Total        float64
	Fingerprint  string `gorm:"type:char(64);not null"`
	FirstSeenAt  time.Time
	UpdatedAt    time.Time
}

func (CloudifyOrderSnapshot) TableName() string { return "cloudify_order_snapshots" }

// CloudifyOrderEvent is one detected change (an outbox row). DedupKey makes
// detection idempotent: the poller and the webhook can both see the same
// change, but only one event row is stored and delivered.
type CloudifyOrderEvent struct {
	ID           string `gorm:"type:char(36);primaryKey"`
	TenantID     string `gorm:"type:char(36);not null;index:idx_coe_tenant_status,priority:1"`
	DedupKey     string `gorm:"type:char(64);not null;uniqueIndex"`
	Kind         string `gorm:"type:varchar(20);not null"` // created | status_changed | updated
	Source       string `gorm:"type:varchar(20);not null"` // poll | webhook
	OrderID      int64  `gorm:"not null"`
	OrderCode    string `gorm:"type:varchar(64);not null;default:''"`
	CustomerCode string `gorm:"type:varchar(64);not null;default:'';index"`
	CustomerName string `gorm:"type:varchar(255);not null;default:''"`
	PrevStatus   string `gorm:"type:varchar(16);not null;default:''"`
	Status       string `gorm:"type:varchar(16);not null;default:''"`
	OrderDate    string `gorm:"type:varchar(32);not null;default:''"`
	Total        float64
	// DeliveryStatus: pending | sent | skipped | failed.
	DeliveryStatus string `gorm:"type:varchar(16);not null;default:'pending';index:idx_coe_tenant_status,priority:2"`
	DeliveryNote   string `gorm:"type:varchar(255);not null;default:''"`
	Attempts       int    `gorm:"not null;default:0"`
	NextAttemptAt  *time.Time
	DeliveredAt    *time.Time
	CreatedAt      time.Time `gorm:"index"`
}

func (CloudifyOrderEvent) TableName() string { return "cloudify_order_events" }
