package models

import "time"

// CRMGroupCustomerCode maps one CRM/GMF group to one Cloudify customer code.
// A single management group can serve several shops (mã KH) — the bot then
// aggregates their data under scope "own" for members, so the operator runs one
// group instead of one per shop.
//
// CRMGroup.CustomerCode stays as the PRIMARY code for backward-compat; this
// table holds the FULL set (primary included). IsPrimary flags the canonical
// code.
type CRMGroupCustomerCode struct {
	ID           string    `gorm:"type:char(36);primaryKey" json:"id"`
	TenantID     string    `gorm:"type:char(36);not null;index" json:"tenant_id"`
	GroupID      string    `gorm:"type:char(36);not null;index;uniqueIndex:uq_gcc_group_code" json:"group_id"`
	CustomerCode string    `gorm:"type:varchar(50);not null;index;uniqueIndex:uq_gcc_group_code" json:"customer_code"`
	IsPrimary    bool      `gorm:"not null;default:false" json:"is_primary"`
	CreatedAt    time.Time `gorm:"not null" json:"created_at"`
	UpdatedAt    time.Time `gorm:"not null" json:"updated_at"`
}

func (CRMGroupCustomerCode) TableName() string {
	return "crm_group_customer_codes"
}
