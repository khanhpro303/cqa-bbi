package models

import "time"

type Job struct {
	ID          string `gorm:"type:char(36);primaryKey" json:"id"`
	TenantID    string `gorm:"type:char(36);not null;index:idx_job_tenant_active" json:"tenant_id"`
	Name        string `gorm:"type:varchar(255);not null" json:"name"`
	Description string `gorm:"type:text" json:"description"`
	JobType     string `gorm:"type:varchar(30);not null" json:"job_type"` // qc_analysis | classification

	// Input
	InputChannelIDs string `gorm:"type:json;not null" json:"input_channel_ids"` // JSON array of channel UUIDs

	// Rules
	RulesContent   string `gorm:"type:text" json:"rules_content"`   // Markdown for QC
	RulesConfig    string `gorm:"type:json" json:"rules_config"`    // JSON array for classification
	SkipConditions string `gorm:"type:text" json:"skip_conditions"` // Conditions to skip evaluation (QC only)

	// AI
	AIProvider string `gorm:"column:ai_provider;type:varchar(20);default:'claude'" json:"ai_provider"` // claude | gemini | openai
	AIModel    string `gorm:"type:varchar(100)" json:"ai_model"`

	// Output
	Outputs        string     `gorm:"type:json;not null" json:"outputs"`                         // [{type, config...}]
	OutputSchedule string     `gorm:"type:varchar(20);default:'instant'" json:"output_schedule"` // instant | scheduled | cron
	OutputCron     string     `gorm:"type:varchar(100)" json:"output_cron"`
	OutputAt       *time.Time `json:"output_at"`
	NotifyOnError  bool       `gorm:"default:true" json:"notify_on_error"` // gửi thông báo qua outputs khi job chạy lỗi

	// Analysis schedule
	ScheduleType string `gorm:"type:varchar(20);default:'cron'" json:"schedule_type"` // cron | after_sync | manual
	ScheduleCron string `gorm:"type:varchar(100)" json:"schedule_cron"`

	// State
	IsActive      bool       `gorm:"default:true;index:idx_job_tenant_active" json:"is_active"`
	LastRunAt     *time.Time `json:"last_run_at"`
	LastRunStatus string     `gorm:"type:varchar(20)" json:"last_run_status"`
	CreatedAt     time.Time  `gorm:"not null" json:"created_at"`
	UpdatedAt     time.Time  `gorm:"not null" json:"updated_at"`

	Tenant Tenant `gorm:"foreignKey:TenantID" json:"tenant,omitempty"`
}

type JobRun struct {
	ID           string     `gorm:"type:char(36);primaryKey" json:"id"`
	JobID        string     `gorm:"type:char(36);not null;index:idx_jobrun_job_started" json:"job_id"`
	TenantID     string     `gorm:"type:char(36);not null" json:"tenant_id"`
	StartedAt    time.Time  `gorm:"not null;index:idx_jobrun_job_started" json:"started_at"`
	FinishedAt   *time.Time `json:"finished_at"`
	Status       string     `gorm:"type:varchar(20);default:'running'" json:"status"` // running | success | error
	Summary      string     `gorm:"type:json" json:"summary"`
	ErrorMessage string     `gorm:"type:text" json:"error_message,omitempty"`
	CreatedAt    time.Time  `gorm:"not null" json:"created_at"`

	Job Job `gorm:"foreignKey:JobID" json:"job,omitempty"`
}

type JobResult struct {
	ID             string     `gorm:"type:char(36);primaryKey" json:"id"`
	JobRunID       string     `gorm:"type:char(36);not null;index:idx_result_run" json:"job_run_id"`
	TenantID       string     `gorm:"type:char(36);not null;index:idx_result_tenant_type" json:"tenant_id"`
	ConversationID string     `gorm:"type:char(36);not null;index:idx_result_tenant_conv" json:"conversation_id"`
	ResultType     string     `gorm:"type:varchar(30);not null;index:idx_result_tenant_type" json:"result_type"` // qc_violation | classification_tag
	Severity       string     `gorm:"type:varchar(30)" json:"severity"`
	RuleName       string     `gorm:"type:varchar(255)" json:"rule_name"`
	Evidence       string     `gorm:"type:text" json:"evidence"`
	Detail         string     `gorm:"type:json" json:"detail"`
	AIRawResponse  string     `gorm:"type:text" json:"ai_raw_response,omitempty"`
	Confidence     float64    `json:"confidence"`
	NotifiedAt     *time.Time `json:"notified_at"`
	CreatedAt      time.Time  `gorm:"not null;index:idx_result_tenant_type" json:"created_at"`
}
