package models

import "time"

// ServiceResolution records a user's acknowledgement of an unanswered request.
// It is append-only so historical response metrics never invent a Page reply.
type ServiceResolution struct {
	ID               string       `gorm:"type:char(36);primaryKey" json:"id"`
	TenantID         string       `gorm:"type:char(36);not null;index:idx_service_resolution_tenant_conv,priority:1" json:"tenant_id"`
	ConversationID   string       `gorm:"type:char(36);not null;index:idx_service_resolution_tenant_conv,priority:2" json:"conversation_id"`
	ThroughMessageID string       `gorm:"type:char(36);not null;uniqueIndex:idx_service_resolution_message" json:"through_message_id"`
	ThroughAt        time.Time    `gorm:"not null" json:"through_at"`
	ResolvedAt       time.Time    `gorm:"not null" json:"resolved_at"`
	ResolvedBy       string       `gorm:"type:char(36);not null" json:"resolved_by"`
	Note             string       `gorm:"type:varchar(500);not null" json:"note"`
	Conversation     Conversation `gorm:"foreignKey:ConversationID;references:ID;constraint:OnDelete:CASCADE" json:"-"`
}
