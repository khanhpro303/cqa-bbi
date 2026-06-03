package models

import "time"

// Campaign is a CRM marketing campaign (Chiến dịch CRM) broadcast over a Zalo OA
// channel to one or more GMF groups (each group paired with its own schedule via
// CampaignSegment). Mirrors the frontend shape in
// frontend/src/components/crm-campaigns/types.ts.
type Campaign struct {
	ID          string `gorm:"type:char(36);primaryKey" json:"id"`
	TenantID    string `gorm:"type:char(36);not null;index" json:"tenant_id"`
	Name        string `gorm:"type:varchar(255);not null" json:"name"`
	Description string `gorm:"type:text" json:"description"`
	// ChannelID points at a Zalo OA channel (channels.id). When that channel is
	// is_active=false the campaign cannot run — the UI surfaces a warning bell.
	ChannelID string `gorm:"type:char(36);index" json:"channel_id"`
	// Status: draft | active | paused | done
	Status           string    `gorm:"type:varchar(20);not null;default:draft" json:"status"`
	MessageText      string    `gorm:"type:text" json:"-"`
	MessageLink      string    `gorm:"type:text" json:"-"`
	MessageImageName string    `gorm:"type:varchar(255)" json:"-"`
	CreatedAt        time.Time `gorm:"not null" json:"created_at"`
	UpdatedAt        time.Time `gorm:"not null" json:"updated_at"`

	Segments []CampaignSegment `gorm:"foreignKey:CampaignID" json:"segments,omitempty"`
}

func (Campaign) TableName() string {
	return "campaigns"
}

// CampaignSegment is one "lượt gửi": a GMF group (CRMGroup) paired with its schedule.
type CampaignSegment struct {
	ID         string `gorm:"type:char(36);primaryKey" json:"id"`
	CampaignID string `gorm:"type:char(36);not null;index" json:"campaign_id"`
	GroupID    string `gorm:"type:char(36);index" json:"group_id"` // -> crm_groups.id
	// ScheduleKind: recurring | once
	ScheduleKind string     `gorm:"type:varchar(20);not null;default:recurring" json:"schedule_kind"`
	Cron         string     `gorm:"type:varchar(120)" json:"cron,omitempty"`     // when recurring
	RunAt        *time.Time `gorm:"" json:"run_at,omitempty"`                    // when once
	NextRunAt    *time.Time `gorm:"" json:"next_run_at,omitempty"`               // computed
}

func (CampaignSegment) TableName() string {
	return "campaign_segments"
}

// CampaignRun logs a single broadcast attempt (one per segment per send). It is the
// real source of truth for the campaign dashboard stats.
type CampaignRun struct {
	ID           string     `gorm:"type:char(36);primaryKey" json:"id"`
	TenantID     string     `gorm:"type:char(36);not null;index" json:"tenant_id"`
	CampaignID   string     `gorm:"type:char(36);not null;index" json:"campaign_id"`
	SegmentID    string     `gorm:"type:char(36);index" json:"segment_id"`
	StartedAt    time.Time  `gorm:"not null;index" json:"started_at"`
	FinishedAt   *time.Time `gorm:"" json:"finished_at,omitempty"`
	SentCount    int        `gorm:"not null;default:0" json:"sent_count"`
	FailCount    int        `gorm:"not null;default:0" json:"fail_count"`
	Status       string     `gorm:"type:varchar(20);not null;default:running" json:"status"` // running | success | error
	ErrorMessage string     `gorm:"type:text" json:"error_message,omitempty"`
}

func (CampaignRun) TableName() string {
	return "campaign_runs"
}
