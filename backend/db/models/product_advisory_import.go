package models

import "time"

// ProductAdvisoryImportJob is the durable aggregate for one uploaded product
// link file. Item state is kept separately so work can resume after a restart.
type ProductAdvisoryImportJob struct {
	ID             string     `gorm:"type:char(36);primaryKey" json:"id"`
	TenantID       string     `gorm:"type:char(36);not null;index:idx_product_advisory_import_job_tenant_created,priority:1;index:idx_product_advisory_import_job_tenant_status,priority:1" json:"-"`
	CreatedBy      string     `gorm:"type:varchar(255)" json:"-"`
	FileName       string     `gorm:"type:varchar(255);not null" json:"file_name"`
	Status         string     `gorm:"type:varchar(20);not null;index:idx_product_advisory_import_job_tenant_status,priority:2" json:"status"`
	TotalRows      int        `gorm:"not null;default:0" json:"total_rows"`
	MatchedCount   int        `gorm:"not null;default:0" json:"matched_count"`
	SkippedCount   int        `gorm:"not null;default:0" json:"skipped_count"`
	UnmatchedCount int        `gorm:"not null;default:0" json:"unmatched_count"`
	SucceededCount int        `gorm:"not null;default:0" json:"succeeded_count"`
	FailedCount    int        `gorm:"not null;default:0" json:"failed_count"`
	StartedAt      time.Time  `gorm:"not null" json:"started_at"`
	FinishedAt     *time.Time `json:"finished_at,omitempty"`
	CreatedAt      time.Time  `gorm:"not null;index:idx_product_advisory_import_job_tenant_created,priority:2" json:"created_at"`
	UpdatedAt      time.Time  `gorm:"not null" json:"updated_at"`
}

func (ProductAdvisoryImportJob) TableName() string { return "product_advisory_import_jobs" }

type ProductAdvisoryImportItem struct {
	ID              string     `gorm:"type:char(36);primaryKey" json:"id"`
	JobID           string     `gorm:"type:char(36);not null;index:idx_product_advisory_import_item_job_row,priority:1;index:idx_product_advisory_import_item_claim,priority:1" json:"job_id"`
	TenantID        string     `gorm:"type:char(36);not null;index" json:"-"`
	RowNumber       int        `gorm:"not null;index:idx_product_advisory_import_item_job_row,priority:2" json:"row_number"`
	InputWebName    string     `gorm:"type:varchar(255)" json:"input_web_name"`
	InputParentCode string     `gorm:"type:varchar(100)" json:"input_parent_code"`
	SourceURL       string     `gorm:"type:varchar(2048)" json:"source_url"`
	ParentCode      string     `gorm:"type:varchar(100)" json:"parent_code"`
	WebName         string     `gorm:"type:varchar(255)" json:"web_name"`
	MatchedBy       string     `gorm:"type:varchar(20)" json:"matched_by"`
	Overwrite       bool       `gorm:"not null;default:false" json:"overwrite"`
	Status          string     `gorm:"type:varchar(20);not null;index:idx_product_advisory_import_item_claim,priority:2" json:"status"`
	ErrorCode       string     `gorm:"type:varchar(64)" json:"error_code,omitempty"`
	ErrorMessage    string     `gorm:"type:varchar(500)" json:"error_message,omitempty"`
	SummaryError    string     `gorm:"type:varchar(64)" json:"summary_error,omitempty"`
	ClaimToken      string     `gorm:"type:char(36)" json:"-"`
	ClaimedUntil    *time.Time `gorm:"index:idx_product_advisory_import_item_claim,priority:3" json:"-"`
	Attempts        int        `gorm:"not null;default:0" json:"-"`
	CreatedAt       time.Time  `gorm:"not null" json:"created_at"`
	UpdatedAt       time.Time  `gorm:"not null" json:"updated_at"`
}

func (ProductAdvisoryImportItem) TableName() string { return "product_advisory_import_items" }

// ProductAdvisoryImportImage ties an image to the exact import item that
// created it. Image approval can therefore never reach uploads or another run.
type ProductAdvisoryImportImage struct {
	ID        string    `gorm:"type:char(36);primaryKey" json:"id"`
	JobID     string    `gorm:"type:char(36);not null;uniqueIndex:idx_product_advisory_import_image_job_image,priority:1;index" json:"job_id"`
	ItemID    string    `gorm:"type:char(36);not null;index" json:"item_id"`
	TenantID  string    `gorm:"type:char(36);not null;index" json:"-"`
	ImageID   string    `gorm:"type:char(36);not null;uniqueIndex:idx_product_advisory_import_image_job_image,priority:2;index" json:"image_id"`
	CreatedAt time.Time `gorm:"not null" json:"created_at"`
}

func (ProductAdvisoryImportImage) TableName() string { return "product_advisory_import_images" }
