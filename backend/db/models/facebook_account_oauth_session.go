package models

import "time"

// FacebookAccountOAuthSession binds a one-use account-linking OAuth flow to
// the authenticated local user and browser that started it. No Facebook token
// or password is persisted in this table.
type FacebookAccountOAuthSession struct {
	ID             string     `gorm:"type:char(36);primaryKey" json:"-"`
	UserID         string     `gorm:"type:char(36);not null;index" json:"-"`
	CredentialHash string     `gorm:"type:char(64);not null" json:"-"`
	StateHash      string     `gorm:"type:char(64);not null;uniqueIndex" json:"-"`
	BrowserHash    string     `gorm:"type:char(64);not null" json:"-"`
	RedirectURI    string     `gorm:"type:varchar(2048);not null" json:"-"`
	ReturnPath     string     `gorm:"type:varchar(512);not null" json:"-"`
	Status         string     `gorm:"type:varchar(24);not null;index" json:"-"`
	ExpiresAt      time.Time  `gorm:"not null;index" json:"-"`
	CompletedAt    *time.Time `json:"-"`
	CreatedAt      time.Time  `gorm:"not null" json:"-"`
	UpdatedAt      time.Time  `gorm:"not null" json:"-"`
}

func (FacebookAccountOAuthSession) TableName() string {
	return "facebook_account_oauth_sessions"
}
