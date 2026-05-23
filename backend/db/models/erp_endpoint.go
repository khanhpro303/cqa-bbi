package models

import "time"

// ERPEndpoint stores which ERP resources a specific agent type (public/private)
// is allowed to query, with optional data scoping.
// This replaces the legacy erp_{agentType}_scopes CSV setting approach.
type ERPEndpoint struct {
	ID            string    `gorm:"type:char(36);primaryKey" json:"id"`
	TenantID      string    `gorm:"type:char(36);not null;uniqueIndex:idx_erp_ep_tenant_agent_res" json:"tenant_id"`
	AgentType     string    `gorm:"type:varchar(20);not null;uniqueIndex:idx_erp_ep_tenant_agent_res" json:"agent_type"` // "public" | "private"
	Resource      string    `gorm:"type:varchar(50);not null;uniqueIndex:idx_erp_ep_tenant_agent_res" json:"resource"`   // "products" | "inventory" | "orders" | "customers" | "debt"
	IsEnabled     bool      `gorm:"default:false;not null" json:"is_enabled"`
	ScopeType     string    `gorm:"type:varchar(20);default:'all'" json:"scope_type"`         // "all" | "own" | "assigned"
	ProductGroups string    `gorm:"type:varchar(500)" json:"product_groups"`                  // comma-separated group filter (optional)
	CreatedAt     time.Time `gorm:"not null" json:"created_at"`
	UpdatedAt     time.Time `gorm:"not null" json:"updated_at"`
}

func (ERPEndpoint) TableName() string {
	return "erp_endpoints"
}
