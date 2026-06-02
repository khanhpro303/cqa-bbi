package models

import "time"

// ERPCustomerCodeExclusion lists customer codes (MA) excluded from the cached
// customer table. The presence of a row means "excluded" — the customer cache
// job skips these codes and they are pruned from cached_customers. Removing the
// row means "no longer excluded" (restored on the next cache sync).
type ERPCustomerCodeExclusion struct {
	ID              string    `gorm:"type:char(36);primaryKey" json:"id"`
	TenantID        string    `gorm:"type:char(36);not null;uniqueIndex:uq_ecce_tenant_code" json:"tenant_id"`
	CustomerCode    string    `gorm:"type:varchar(100);not null;uniqueIndex:uq_ecce_tenant_code" json:"customer_code"`
	UpdatedByUserID string    `gorm:"type:char(36)" json:"updated_by_user_id"`
	CreatedAt       time.Time `gorm:"not null" json:"created_at"`
	UpdatedAt       time.Time `gorm:"not null" json:"updated_at"`
}

func (ERPCustomerCodeExclusion) TableName() string {
	return "erp_customer_code_exclusions"
}
