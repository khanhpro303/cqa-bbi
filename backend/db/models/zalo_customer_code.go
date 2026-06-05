package models

import "time"

// ZaloCustomerCode maps one approved Zalo customer (one Zalo user) to one
// Cloudify customer code. A store owner who runs several shops has multiple
// rows here (one per mã KH), letting the bot aggregate công nợ / đơn hàng across
// all their shops under scope "own" without forcing one group per shop.
//
// The single ZaloCustomer.CustomerCode field is kept as the PRIMARY code for
// backward-compat (every existing single-store path keeps working); this table
// holds the FULL set (primary included). IsPrimary flags the canonical code.
type ZaloCustomerCode struct {
	ID             string    `gorm:"type:char(36);primaryKey" json:"id"`
	TenantID       string    `gorm:"type:char(36);not null;index" json:"tenant_id"`
	ZaloCustomerID string    `gorm:"type:char(36);not null;index;uniqueIndex:uq_zcc_customer_code" json:"zalo_customer_id"`
	CustomerCode   string    `gorm:"type:varchar(50);not null;index;uniqueIndex:uq_zcc_customer_code" json:"customer_code"`
	IsPrimary      bool      `gorm:"not null;default:false" json:"is_primary"`
	CreatedAt      time.Time `gorm:"not null" json:"created_at"`
	UpdatedAt      time.Time `gorm:"not null" json:"updated_at"`
}

func (ZaloCustomerCode) TableName() string {
	return "zalo_customer_codes"
}
