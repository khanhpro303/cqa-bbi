package models

import "time"

// PancakeConnection stores one Pancake user session per tenant. The worker uses
// it only to listen to pancake.vn realtime events; it never writes to Pancake.
// The token is encrypted with ENCRYPTION_KEY and never returned by the API.
type PancakeConnection struct {
	TenantID           string `gorm:"type:char(36);primaryKey"`
	UserTokenEncrypted []byte `gorm:"type:varbinary(4096)"`
	PancakeUserID      string `gorm:"type:varchar(64);not null;default:''"`
	SocketStatus       string `gorm:"type:varchar(20);not null;default:'never'"`
	SocketError        string `gorm:"type:varchar(255);not null;default:''"`
	SocketConnectedAt  *time.Time
	SocketEventAt      *time.Time
	LeaseOwner         string `gorm:"type:char(36);not null;default:''"`
	LeaseUntil         *time.Time
	UpdatedAt          time.Time
}

// PancakeLabelState configures Pancake tag classification for one Facebook
// Page channel. It mirrors MessengerLabelState so both sources share rules.
type PancakeLabelState struct {
	ChannelID          string `gorm:"type:char(36);primaryKey"`
	TenantID           string `gorm:"type:char(36);not null;index"`
	PancakePageID      string `gorm:"type:varchar(64);not null;default:''"`
	PageTokenEncrypted []byte `gorm:"type:varbinary(4096)"`
	Enabled            bool   `gorm:"not null;default:false"`
	Rules              string `gorm:"type:json;not null"`
	Catalog            string `gorm:"type:json;not null"`
	CatalogSyncedAt    *time.Time
	SyncStatus         string `gorm:"type:varchar(20);not null;default:'never'"`
	SyncError          string `gorm:"type:varchar(255);not null;default:''"`
	SyncStartedAt      *time.Time
	SyncFinishedAt     *time.Time
	// ReconciledAt is the start time of the last complete inbox scan. The next
	// incremental scan stops at conversations updated before it minus overlap.
	ReconciledAt *time.Time
	FullScanAt   *time.Time
	LeaseUntil   *time.Time
	SyncToken    string  `gorm:"type:char(36);not null;default:''"`
	Channel      Channel `gorm:"foreignKey:ChannelID;references:ID;constraint:OnDelete:CASCADE"`
}

// PancakeLabelSnapshot is the latest observed Pancake tag set of one customer
// (Page + PSID). SourceTimestamp is Pancake's microsecond event clock; older
// events never overwrite newer ones. ConversationID links a CQA conversation
// when one exists; Pancake may see a customer before CQA syncs the thread.
type PancakeLabelSnapshot struct {
	ChannelID             string  `gorm:"type:char(36);primaryKey"`
	PSID                  string  `gorm:"column:psid;type:varchar(64);primaryKey"`
	TenantID              string  `gorm:"type:char(36);not null;index:idx_pancake_label_tenant_channel,priority:1"`
	ConversationID        *string `gorm:"type:char(36);index"`
	PancakeConversationID string  `gorm:"type:varchar(128);not null;default:''"`
	Labels                string  `gorm:"type:json;not null"`
	Source                string  `gorm:"type:varchar(20);not null"`
	SourceTimestamp       int64   `gorm:"not null;default:0"`
	CheckedAt             time.Time
	Channel               Channel `gorm:"foreignKey:ChannelID;references:ID;constraint:OnDelete:CASCADE"`
}
