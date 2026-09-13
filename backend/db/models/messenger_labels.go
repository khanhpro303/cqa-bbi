package models

import "time"

// MessengerLabelState configures read-only classification observation per Page.
// The lease is shared by scheduled and manual syncs, including across replicas.
type MessengerLabelState struct {
	ChannelID       string `gorm:"type:char(36);primaryKey"`
	TenantID        string `gorm:"type:char(36);not null;index"`
	Enabled         bool   `gorm:"not null;default:false"`
	Rules           string `gorm:"type:json;not null"`
	Catalog         string `gorm:"type:json;not null"`
	CatalogSyncedAt *time.Time
	SyncStatus      string `gorm:"type:varchar(20);not null"`
	SyncError       string `gorm:"type:varchar(255)"`
	SyncStartedAt   *time.Time
	SyncFinishedAt  *time.Time
	LeaseUntil      *time.Time
	SyncToken       string  `gorm:"type:char(36)"`
	SyncCursor      string  `gorm:"type:char(36);not null;default:''"`
	SyncFailed      int     `gorm:"not null;default:0"`
	Channel         Channel `gorm:"foreignKey:ChannelID;references:ID;constraint:OnDelete:CASCADE"`
}

// A successful empty Labels array means observed no labels. An error or missing
// row never means unclassified. No employee identity is inferred from labels.
type MessengerLabelSnapshot struct {
	ConversationID string `gorm:"type:char(36);primaryKey"`
	TenantID       string `gorm:"type:char(36);not null;index:idx_meta_label_tenant_channel,priority:1"`
	ChannelID      string `gorm:"type:char(36);not null;index:idx_meta_label_tenant_channel,priority:2"`
	PSID           string `gorm:"column:psid;type:varchar(255)"`
	Labels         string `gorm:"type:json;not null"`
	Status         string `gorm:"type:varchar(20);not null"`
	ErrorKind      string `gorm:"type:varchar(32)"`
	CheckedAt      *time.Time
	AttemptedAt    time.Time
	Conversation   Conversation `gorm:"foreignKey:ConversationID;references:ID;constraint:OnDelete:CASCADE"`
}
