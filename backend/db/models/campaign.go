package models

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"time"
)

// JSONStringSlice persists a []string as a JSON column (MySQL `json`). It keeps
// the campaign image list inline on the campaigns row without a child table.
type JSONStringSlice []string

// Scan implements sql.Scanner — reads a JSON array from the DB into the slice.
func (s *JSONStringSlice) Scan(v any) error {
	if v == nil {
		*s = nil
		return nil
	}
	var raw []byte
	switch t := v.(type) {
	case []byte:
		raw = t
	case string:
		raw = []byte(t)
	default:
		return fmt.Errorf("JSONStringSlice: unsupported scan type %T", v)
	}
	if len(raw) == 0 {
		*s = nil
		return nil
	}
	return json.Unmarshal(raw, s)
}

// Value implements driver.Valuer — writes the slice as a JSON array string.
func (s JSONStringSlice) Value() (driver.Value, error) {
	if len(s) == 0 {
		return "[]", nil
	}
	b, err := json.Marshal(s)
	if err != nil {
		return nil, err
	}
	return string(b), nil
}

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
	Status      string `gorm:"type:varchar(20);not null;default:draft" json:"status"`
	MessageText string `gorm:"type:text" json:"-"`
	MessageLink string `gorm:"type:text" json:"-"`
	// MessageReminderText is an optional text-only "nhắc lại" message. When set, the
	// first successful send to each segment's group uses the main message and every
	// later (recurring) send uses this reminder instead — see engine.fireSegment.
	MessageReminderText string `gorm:"type:text" json:"-"`
	// MessageImageName is the legacy single-image column, kept only so existing
	// rows can be backfilled into MessageImages. New writes leave it empty.
	MessageImageName string          `gorm:"type:varchar(255)" json:"-"`
	MessageImages    JSONStringSlice `gorm:"type:json" json:"-"`
	CreatedAt        time.Time       `gorm:"not null" json:"created_at"`
	UpdatedAt        time.Time       `gorm:"not null" json:"updated_at"`

	Segments []CampaignSegment `gorm:"foreignKey:CampaignID" json:"segments,omitempty"`
}

func (Campaign) TableName() string {
	return "campaigns"
}

// Images returns the effective image list for the campaign, falling back to the
// legacy single-image column for rows not yet backfilled.
func (c *Campaign) Images() []string {
	if len(c.MessageImages) > 0 {
		return c.MessageImages
	}
	if c.MessageImageName != "" {
		return []string{c.MessageImageName}
	}
	return nil
}

// CampaignSegment is one "lượt gửi": a GMF group (CRMGroup) paired with its schedule.
type CampaignSegment struct {
	ID         string `gorm:"type:char(36);primaryKey" json:"id"`
	CampaignID string `gorm:"type:char(36);not null;index" json:"campaign_id"`
	GroupID    string `gorm:"type:char(36);index" json:"group_id"` // -> crm_groups.id
	// ScheduleKind: recurring | once
	ScheduleKind string     `gorm:"type:varchar(20);not null;default:recurring" json:"schedule_kind"`
	Cron         string     `gorm:"type:varchar(120)" json:"cron,omitempty"` // when recurring
	RunAt        *time.Time `gorm:"" json:"run_at,omitempty"`                // when once
	NextRunAt    *time.Time `gorm:"" json:"next_run_at,omitempty"`           // computed
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
