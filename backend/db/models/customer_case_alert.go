package models

import "time"

// CustomerCaseAlert records one attempt to notify an internal Zalo group
// about a complaint case. It keeps the resolved destination so staff can see
// where an alert actually went, including stream-to-handover fallbacks.
type CustomerCaseAlert struct {
	ID          string    `gorm:"type:char(36);primaryKey" json:"id"`
	TenantID    string    `gorm:"type:char(36);not null;index:idx_ccalert_tenant_case_time,priority:1" json:"tenant_id"`
	CaseID      string    `gorm:"type:char(36);not null;index:idx_ccalert_tenant_case_time,priority:2" json:"case_id"`
	Kind        string    `gorm:"type:varchar(20);not null" json:"kind"`
	Status      string    `gorm:"type:varchar(20);not null" json:"status"`
	GroupID     string    `gorm:"type:char(36);not null;default:''" json:"group_id"`
	GroupName   string    `gorm:"type:varchar(255);not null;default:''" json:"group_name"`
	ZaloGroupID string    `gorm:"type:varchar(255);not null;default:''" json:"zalo_group_id"`
	Error       string    `gorm:"type:text" json:"error,omitempty"`
	CreatedAt   time.Time `gorm:"not null;index:idx_ccalert_tenant_case_time,priority:3" json:"created_at"`

	Case CustomerCase `gorm:"foreignKey:CaseID;references:ID;constraint:OnDelete:CASCADE" json:"-"`
}

func (CustomerCaseAlert) TableName() string { return "customer_case_alerts" }
