package models

import "time"

// ActivityLog tracks all system activities for audit trail.
type ActivityLog struct {
	ID           string    `gorm:"type:char(36);primaryKey" json:"id"`
	TenantID     string    `gorm:"type:char(36);not null;index:idx_activity_tenant_created;index:idx_activity_resource" json:"tenant_id"`
	UserID       string    `gorm:"type:char(36)" json:"user_id"`
	UserEmail    string    `gorm:"type:varchar(255)" json:"user_email"`
	Action       string    `gorm:"type:varchar(50);not null;index" json:"action"` // job.create, job.run, ai.error, user.login, settings.update
	ResourceType string    `gorm:"type:varchar(50);index:idx_activity_resource" json:"resource_type"`  // job, user, channel, settings
	ResourceID   string    `gorm:"type:varchar(100);index:idx_activity_resource" json:"resource_id"`
	Detail       string    `gorm:"type:text" json:"detail"`
	ErrorMessage string    `gorm:"type:text" json:"error_message"`
	IPAddress    string    `gorm:"type:varchar(45)" json:"ip_address"`
	CreatedAt    time.Time `gorm:"not null;index:idx_activity_tenant_created;index:idx_activity_resource" json:"created_at"`
}
