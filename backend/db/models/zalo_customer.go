package models

import "time"

type ZaloCustomer struct {
	ID           string    `gorm:"type:char(36);primaryKey" json:"id"`
	TenantID     string    `gorm:"type:char(36);not null;index" json:"tenant_id"`
	ZaloUserID   string    `gorm:"type:varchar(255);index" json:"zalo_user_id"`
	Name         string    `gorm:"type:varchar(255);not null" json:"name"`
	PhoneNumber  string    `gorm:"type:varchar(20)" json:"phone_number"`
	Avatar       string    `gorm:"type:text" json:"avatar"`
	VerifyToken  string    `gorm:"type:varchar(50);index" json:"verify_token"`
	Status       string    `gorm:"type:varchar(30);default:'pending_verify';not null" json:"status"` // pending_verify | pending_approval | approved | rejected
	CustomerCode string    `gorm:"type:varchar(50);index" json:"customer_code"`
	CreatedAt    time.Time `gorm:"not null" json:"created_at"`
	UpdatedAt    time.Time `gorm:"not null" json:"updated_at"`

	// CustomerCodes is the full mã KH set (owners of multiple shops), populated
	// by list handlers from zalo_customer_codes. Not a DB column.
	CustomerCodes []string `gorm:"-" json:"customer_codes,omitempty"`
}

func (ZaloCustomer) TableName() string {
	return "zalo_customers"
}
