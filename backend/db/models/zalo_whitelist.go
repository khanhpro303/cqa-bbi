package models

import "time"

type ZaloWhitelist struct {
	ID          string    `gorm:"type:char(36);primaryKey" json:"id"`
	TenantID    string    `gorm:"type:char(36);not null;index" json:"tenant_id"`
	ChannelID   string    `gorm:"type:char(36);index" json:"channel_id"`
	ZaloUserID  string    `gorm:"type:varchar(255);index" json:"zalo_user_id"`
	Name        string    `gorm:"type:varchar(255)" json:"name"`
	Avatar      string    `gorm:"type:text" json:"avatar"`
	VerifyToken string    `gorm:"type:varchar(50);index" json:"verify_token"`
	Status      string    `gorm:"type:varchar(20);default:'pending';not null" json:"status"` // pending | active
	CreatedAt   time.Time `gorm:"not null" json:"created_at"`
	UpdatedAt   time.Time `gorm:"not null" json:"updated_at"`

	Channel *Channel `gorm:"foreignKey:ChannelID" json:"channel,omitempty"`
}

func (ZaloWhitelist) TableName() string {
	return "zalo_whitelist"
}
