package models

import "time"

// ERPParentSKUExclusion lists parent SKUs (MA_CHA) excluded from the AI-facing
// CachedProduct table. The presence of a row means "excluded". Removing the row
// means "no longer excluded".
type ERPParentSKUExclusion struct {
	ID              string    `gorm:"type:char(36);primaryKey" json:"id"`
	TenantID        string    `gorm:"type:char(36);not null;uniqueIndex:uq_epse_tenant_parent" json:"tenant_id"`
	ParentSKU       string    `gorm:"type:varchar(100);not null;uniqueIndex:uq_epse_tenant_parent" json:"parent_sku"`
	UpdatedByUserID string    `gorm:"type:char(36)" json:"updated_by_user_id"`
	CreatedAt       time.Time `gorm:"not null" json:"created_at"`
	UpdatedAt       time.Time `gorm:"not null" json:"updated_at"`
}

func (ERPParentSKUExclusion) TableName() string {
	return "erp_parent_sku_exclusions"
}
