package models

import "time"

type Channel struct {
	ID                   string     `gorm:"type:char(36);primaryKey" json:"id"`
	TenantID             string     `gorm:"type:char(36);not null;index:idx_channel_tenant_active" json:"tenant_id"`
	ChannelType          string     `gorm:"type:varchar(32);not null" json:"channel_type"` // zalo_oa | facebook | personal_zalo_import
	Name                 string     `gorm:"type:varchar(255);not null" json:"name"`
	ExternalID           string     `gorm:"type:varchar(255)" json:"external_id"`
	CredentialsEncrypted []byte     `gorm:"type:varbinary(2048);not null" json:"-"`
	IsActive             bool       `gorm:"default:true;index:idx_channel_tenant_active" json:"is_active"`
	AutoReplyEnabled     bool       `gorm:"column:auto_reply_enabled;not null;default:true" json:"auto_reply_enabled"` // per-channel chatbot auto-reply switch, orthogonal to IsActive
	LastSyncAt           *time.Time `json:"last_sync_at"`
	LastSyncStatus       string     `gorm:"type:varchar(20)" json:"last_sync_status"`
	LastSyncError        string     `gorm:"type:text" json:"last_sync_error,omitempty"`
	Metadata             string     `gorm:"type:json" json:"metadata"`
	CreatedAt            time.Time  `gorm:"not null" json:"created_at"`
	UpdatedAt            time.Time  `gorm:"not null" json:"updated_at"`

	Tenant Tenant `gorm:"foreignKey:TenantID" json:"tenant,omitempty"`
}

func (Channel) TableName() string {
	return "channels"
}

// Unique constraint: tenant + type + external_id
// Added via migration hook
