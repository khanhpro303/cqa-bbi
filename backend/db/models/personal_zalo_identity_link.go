package models

import "time"

// PersonalZaloIdentityLink binds one personal-Zalo peer to a CRM identity.
// Invites store only a digest. A successful match consumes it and sets SenderID.
type PersonalZaloIdentityLink struct {
	ID                string     `gorm:"type:char(36);primaryKey" json:"id"`
	TenantID          string     `gorm:"type:char(36);not null;uniqueIndex:ux_pz_identity_target,priority:1;uniqueIndex:ux_pz_identity_sender,priority:1" json:"-"`
	ChannelID         string     `gorm:"type:char(36);not null;uniqueIndex:ux_pz_identity_target,priority:2;uniqueIndex:ux_pz_identity_sender,priority:2" json:"-"`
	AccountExternalID string     `gorm:"type:varchar(64);not null;uniqueIndex:ux_pz_identity_target,priority:3;uniqueIndex:ux_pz_identity_sender,priority:3" json:"-"`
	Kind              string     `gorm:"type:varchar(16);not null;uniqueIndex:ux_pz_identity_target,priority:4;uniqueIndex:ux_pz_identity_sender,priority:4" json:"kind"`
	TargetID          string     `gorm:"type:char(36);not null;uniqueIndex:ux_pz_identity_target,priority:5" json:"target_id"`
	SenderID          *string    `gorm:"type:varchar(64);uniqueIndex:ux_pz_identity_sender,priority:5" json:"sender_id,omitempty"`
	InviteDigest      *string    `gorm:"type:char(64);uniqueIndex" json:"-"`
	VerifiedAt        *time.Time `json:"verified_at,omitempty"`
	ExpiresAt         time.Time  `gorm:"not null" json:"expires_at"`
	CreatedAt         time.Time  `gorm:"not null" json:"-"`

	Channel *Channel `gorm:"foreignKey:ChannelID;constraint:OnDelete:CASCADE" json:"-"`
}
