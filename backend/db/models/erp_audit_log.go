package models

import "time"

// ERPAuditLog records every ERP query call for compliance and debugging.
// Includes group context, applied scope, and product filters.
type ERPAuditLog struct {
	ID             string    `gorm:"type:char(36);primaryKey" json:"id"`
	TenantID       string    `gorm:"type:char(36);not null;index:idx_erp_audit_tenant_time" json:"tenant_id"`
	AgentType      string    `gorm:"type:varchar(20)" json:"agent_type"`            // public|private
	ZaloUserID     string    `gorm:"type:varchar(100);index" json:"zalo_user_id"`
	CustomerCode   string    `gorm:"type:varchar(100)" json:"customer_code"`
	GroupIDs       string    `gorm:"type:varchar(500)" json:"group_ids"`             // comma-separated group IDs used
	Resource       string    `gorm:"type:varchar(50);index" json:"resource"`         // products|inventory|orders|customers|debt
	ScopeApplied   string    `gorm:"type:varchar(20)" json:"scope_applied"`          // all|own|assigned
	ProductFilter  string    `gorm:"type:varchar(500)" json:"product_filter"`        // applied product groups filter
	SearchQuery    string    `gorm:"type:text" json:"search_query"`
	ResponseStatus int       `gorm:"type:int" json:"response_status"`
	ResponseCount  int       `gorm:"type:int" json:"response_count"`
	IPAddress      string    `gorm:"type:varchar(45)" json:"ip_address"`
	CreatedAt      time.Time `gorm:"not null;index:idx_erp_audit_tenant_time" json:"created_at"`
}

func (ERPAuditLog) TableName() string {
	return "erp_audit_logs"
}

// ERPGroupRateLimit tracks daily API call counts per CRM group for rate limiting.
type ERPGroupRateLimit struct {
	ID        string    `gorm:"type:char(36);primaryKey" json:"id"`
	TenantID  string    `gorm:"type:char(36);not null;uniqueIndex:idx_rate_tenant_group_date" json:"tenant_id"`
	GroupID   string    `gorm:"type:char(36);not null;uniqueIndex:idx_rate_tenant_group_date" json:"group_id"`
	DateKey   string    `gorm:"type:char(10);not null;uniqueIndex:idx_rate_tenant_group_date" json:"date_key"` // "2026-05-25"
	CallCount int       `gorm:"type:int;default:0" json:"call_count"`
	DayLimit  int       `gorm:"type:int;default:500" json:"day_limit"` // configurable per group
	UpdatedAt time.Time `gorm:"not null" json:"updated_at"`
}

func (ERPGroupRateLimit) TableName() string {
	return "erp_group_rate_limits"
}
