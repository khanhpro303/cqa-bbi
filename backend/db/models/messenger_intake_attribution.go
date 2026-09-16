package models

import "time"

// MessengerIntakeAttribution is the immutable first referral Meta sends for a
// Page-scoped user. It is deliberately separate from manual tracking labels.
type MessengerIntakeAttribution struct {
	ID             string    `gorm:"type:char(36);primaryKey" json:"id"`
	TenantID       string    `gorm:"type:char(36);not null;index:idx_mia_tenant" json:"tenant_id"`
	ChannelID      string    `gorm:"type:char(36);not null;uniqueIndex:idx_mia_channel_psid,priority:1" json:"channel_id"`
	ConversationID *string   `gorm:"type:char(36);index:idx_mia_conversation" json:"conversation_id,omitempty"`
	PageID         string    `gorm:"type:varchar(64);not null;index:idx_mia_page" json:"page_id"`
	PSID           string    `gorm:"type:varchar(64);not null;uniqueIndex:idx_mia_channel_psid,priority:2" json:"psid"`
	AdID           string    `gorm:"type:varchar(128);index:idx_mia_ad" json:"ad_id,omitempty"`
	Ref            string    `gorm:"type:varchar(512)" json:"ref,omitempty"`
	Source         string    `gorm:"type:varchar(64)" json:"source,omitempty"`
	ReferralType   string    `gorm:"type:varchar(64)" json:"referral_type,omitempty"`
	MessageID      string    `gorm:"type:varchar(255)" json:"message_id,omitempty"`
	RawReferral    string    `gorm:"type:json;not null" json:"raw_referral"`
	CapturedAt     time.Time `gorm:"type:datetime(3);not null" json:"captured_at"`
	CreatedAt      time.Time `gorm:"not null" json:"created_at"`
	UpdatedAt      time.Time `gorm:"not null" json:"updated_at"`
}
