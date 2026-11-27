package models

import "time"

type PersonalZaloThreadApproval struct {
	ChannelID         string     `gorm:"type:char(36);primaryKey" json:"-"`
	AccountExternalID string     `gorm:"type:varchar(64);primaryKey" json:"-"`
	ThreadType        string     `gorm:"type:varchar(5);primaryKey" json:"thread_type"`
	ThreadID          string     `gorm:"type:varchar(64);primaryKey" json:"thread_id"`
	DisplayName       string     `gorm:"type:varchar(255);not null" json:"display_name"`
	Status            string     `gorm:"type:varchar(8);not null" json:"status"`
	Present           bool       `gorm:"not null" json:"-"`
	FirstSeenAt       time.Time  `gorm:"type:datetime(3);not null" json:"first_seen_at"`
	DecidedAt         *time.Time `gorm:"type:datetime(3)" json:"decided_at,omitempty"`
	DecidedBy         string     `gorm:"type:char(36)" json:"-"`

	Channel Channel `gorm:"foreignKey:ChannelID;constraint:OnDelete:CASCADE" json:"-"`
}

type PersonalZaloRosterState struct {
	ChannelID         string    `gorm:"type:char(36);primaryKey"`
	AccountExternalID string    `gorm:"type:varchar(64);primaryKey"`
	CapturedAt        time.Time `gorm:"type:datetime(3);not null"`

	Channel Channel `gorm:"foreignKey:ChannelID;constraint:OnDelete:CASCADE"`
}
