package models

import "time"

// ERPEndpoint stores which ERP resources a specific CRM GMF group
// is allowed to query, with optional data scoping.
type ERPEndpoint struct {
	ID            string    `gorm:"type:char(36);primaryKey" json:"id"`
	TenantID      string    `gorm:"type:char(36);not null;uniqueIndex:idx_erp_ep_tenant_group_res" json:"tenant_id"`
	GroupID       string    `gorm:"type:char(36);not null;uniqueIndex:idx_erp_ep_tenant_group_res" json:"group_id"` // reference to crm_groups.id
	Resource      string    `gorm:"type:varchar(50);not null;uniqueIndex:idx_erp_ep_tenant_group_res" json:"resource"`   // "products" | "inventory" | "orders" | "customers" | "debt"
	IsEnabled     bool      `gorm:"default:false;not null" json:"is_enabled"`
	ScopeType     string    `gorm:"type:varchar(20);default:'all'" json:"scope_type"`         // "all" | "own" | "assigned"
	ProductGroups string    `gorm:"type:varchar(500)" json:"product_groups"`                  // comma-separated group filter (optional)
	CreatedAt     time.Time `gorm:"not null" json:"created_at"`
	UpdatedAt     time.Time `gorm:"not null" json:"updated_at"`
}

func (ERPEndpoint) TableName() string {
	return "erp_endpoints"
}
