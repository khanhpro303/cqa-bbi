package models

import "time"

// CachedCustomer stores the cached list of customers (khách hàng) pulled from
// Cloudify ERP (khach_hang/search) into local MySQL for fast lookup. Unlike
// products there is no exclusion/parent filtering, so this single table is the
// source of truth.
type CachedCustomer struct {
	ID              string    `gorm:"type:char(36);primaryKey" json:"id"`
	TenantID        string    `gorm:"type:char(36);not null;index:idx_cc_tenant_ma" json:"tenant_id"`
	ERP_ID          int64     `gorm:"type:bigint" json:"erp_id"`
	MA              string    `gorm:"type:varchar(100);not null;index:idx_cc_tenant_ma;index:idx_cc_ma" json:"ma"`
	HO_VA_TEN       string    `gorm:"type:varchar(255);index:idx_cc_name" json:"ho_va_ten"`
	EMAIL           string    `gorm:"type:varchar(255)" json:"email"`
	DIA_CHI         string    `gorm:"type:varchar(500)" json:"dia_chi"`
	DIEN_THOAI      string    `gorm:"type:varchar(50);index:idx_cc_phone" json:"dien_thoai"`
	ERP_CREATE_DATE string    `gorm:"type:varchar(50)" json:"erp_create_date"`
	CreatedAt       time.Time `gorm:"not null" json:"created_at"`
	UpdatedAt       time.Time `gorm:"not null" json:"updated_at"`
}

func (CachedCustomer) TableName() string {
	return "cached_customers"
}
