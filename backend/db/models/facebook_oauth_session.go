package models

import "time"

// FacebookOAuthSession is a short-lived, one-use server-side OAuth handoff.
// Page access tokens are encrypted in PagesEncrypted and are never returned to
// the browser.
type FacebookOAuthSession struct {
	ID             string     `gorm:"type:char(36);primaryKey" json:"id"`
	TenantID       string     `gorm:"type:char(36);not null;index:idx_fb_oauth_tenant_user" json:"-"`
	UserID         string     `gorm:"type:char(36);not null;index:idx_fb_oauth_tenant_user" json:"-"`
	StateHash      string     `gorm:"type:char(64);not null;uniqueIndex" json:"-"`
	BrowserHash    string     `gorm:"type:char(64);not null" json:"-"`
	RedirectURI    string     `gorm:"type:varchar(2048)" json:"-"`
	Status         string     `gorm:"type:varchar(24);not null;index" json:"-"`
	SelectedPageID string     `gorm:"type:varchar(255)" json:"-"`
	PagesEncrypted []byte     `gorm:"type:mediumblob" json:"-"`
	ExpiresAt      time.Time  `gorm:"not null;index" json:"expires_at"`
	CompletedAt    *time.Time `json:"-"`
	CreatedAt      time.Time  `gorm:"not null" json:"-"`
	UpdatedAt      time.Time  `gorm:"not null" json:"-"`
}

func (FacebookOAuthSession) TableName() string { return "facebook_oauth_sessions" }
