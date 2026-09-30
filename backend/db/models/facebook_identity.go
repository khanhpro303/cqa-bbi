package models

import "time"

// FacebookIdentity links one app-scoped Facebook user ID to one local user.
// Access tokens are verified and discarded; they are never persisted.
type FacebookIdentity struct {
	UserID         string    `gorm:"type:char(36);primaryKey" json:"user_id"`
	FacebookAppID  string    `gorm:"type:varchar(255);not null;uniqueIndex:uq_facebook_app_user,priority:1" json:"-"`
	FacebookUserID string    `gorm:"type:varchar(255);not null;uniqueIndex:uq_facebook_app_user,priority:2" json:"-"`
	FacebookName   string    `gorm:"type:varchar(255)" json:"facebook_name"`
	CreatedAt      time.Time `gorm:"not null" json:"created_at"`
	UpdatedAt      time.Time `gorm:"not null" json:"updated_at"`

	User User `gorm:"foreignKey:UserID;references:ID;constraint:OnUpdate:CASCADE,OnDelete:CASCADE" json:"-"`
}

func (FacebookIdentity) TableName() string {
	return "facebook_identities"
}
