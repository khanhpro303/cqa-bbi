package models

import "time"

// PersonalZaloInternbotRoster records group membership independently of the
// decisions that allow Chatbot to reply to individual conversations.
type PersonalZaloInternbotRoster struct {
	ChannelID         string    `gorm:"type:char(36);primaryKey"`
	AccountExternalID string    `gorm:"type:varchar(64);not null"`
	CapturedAt        time.Time `gorm:"type:datetime(3);not null"`
	Groups            string    `gorm:"type:json;not null"`

	Channel Channel `gorm:"foreignKey:ChannelID;constraint:OnDelete:CASCADE" json:"-"`
}
