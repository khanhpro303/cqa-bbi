package models

import "time"

// FacebookLoginOAuthSession binds a one-use Facebook sign-in attempt to the
// browser that started it. Provider tokens and application JWTs are never
// persisted in this table.
type FacebookLoginOAuthSession struct {
	ID          string     `gorm:"type:char(36);primaryKey" json:"-"`
	StateHash   string     `gorm:"type:char(64);not null;uniqueIndex" json:"-"`
	BrowserHash string     `gorm:"type:char(64);not null" json:"-"`
	RedirectURI string     `gorm:"type:varchar(2048);not null" json:"-"`
	Status      string     `gorm:"type:varchar(24);not null;index" json:"-"`
	ExpiresAt   time.Time  `gorm:"not null;index" json:"-"`
	CompletedAt *time.Time `json:"-"`
	CreatedAt   time.Time  `gorm:"not null" json:"-"`
	UpdatedAt   time.Time  `gorm:"not null" json:"-"`
}

func (FacebookLoginOAuthSession) TableName() string {
	return "facebook_login_oauth_sessions"
}
