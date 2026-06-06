package models

import "time"

type Message struct {
	ID                string `gorm:"type:char(36);primaryKey" json:"id"`
	TenantID          string `gorm:"type:char(36);not null;index:idx_msg_tenant_sender,priority:1" json:"tenant_id"`
	ConversationID    string `gorm:"type:char(36);not null;index:idx_msg_conv_time" json:"conversation_id"`
	ExternalMessageID string `gorm:"type:varchar(255);not null" json:"external_message_id"`
	SenderType        string `gorm:"type:varchar(20);not null" json:"sender_type"` // customer | agent | system
	SenderName        string `gorm:"type:varchar(500)" json:"sender_name"`
	// Indexed with tenant_id so the per-employee AI-cost name fallback (lookup of a
	// sender's cached display name) stays fast on large message tables.
	SenderExternalID string    `gorm:"type:varchar(255);index:idx_msg_tenant_sender,priority:2" json:"sender_external_id"`
	Content          string    `gorm:"type:text" json:"content"`
	ContentType      string    `gorm:"type:varchar(50);default:'text'" json:"content_type"` // text | image | file | sticker
	Attachments      string    `gorm:"type:json" json:"attachments"`
	SentAt           time.Time `gorm:"not null;index:idx_msg_conv_time" json:"sent_at"`
	RawData          string    `gorm:"type:json" json:"raw_data,omitempty"`
	CreatedAt        time.Time `gorm:"not null" json:"created_at"`
}
