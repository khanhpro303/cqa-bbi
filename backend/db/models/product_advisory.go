package models

import "time"

// Product advisory groups use the parent code, synchronized web name, and
// ERP product category. Blank web names are grouped separately.
const (
	ProductAdvisoryParentSQL   = "COALESCE(NULLIF(TRIM(ma_cha), ''), TRIM(ma))"
	productAdvisoryLineNameSQL = "COALESCE(TRIM(ten_dong_bo_web), '')"
	ProductAdvisoryCategorySQL = "COALESCE(TRIM(list_ten_nhom_vthh), '')"
	ProductAdvisoryGroupKeySQL = "SHA2(CONCAT(CHAR_LENGTH(" + ProductAdvisoryParentSQL + "), ':', LOWER(" + ProductAdvisoryParentSQL + "), CHAR_LENGTH(" + productAdvisoryLineNameSQL + "), ':', LOWER(" + productAdvisoryLineNameSQL + "), LOWER(" + ProductAdvisoryCategorySQL + ")), 256)"
	ProductAdvisoryWebNameSQL  = "COALESCE(MAX(NULLIF(" + productAdvisoryLineNameSQL + ", '')), MAX(NULLIF(TRIM(ten), '')), MAX(" + ProductAdvisoryParentSQL + "))"
)

type ProductAdvisory struct {
	ID              string    `gorm:"type:char(36);primaryKey" json:"id"`
	TenantID        string    `gorm:"type:char(36);not null;uniqueIndex:idx_product_advisory_tenant_group,priority:1;index:idx_product_advisory_parent,priority:1" json:"tenant_id"`
	ParentCode      string    `gorm:"type:varchar(100);not null;index:idx_product_advisory_parent,priority:2" json:"parent_code"`
	GroupKey        string    `gorm:"type:char(64);default:null;uniqueIndex:idx_product_advisory_tenant_group,priority:2" json:"group_key"`
	WebName         string    `gorm:"type:varchar(255);not null" json:"web_name"`
	ProductCategory string    `gorm:"type:varchar(500)" json:"product_category"`
	Notes           string    `gorm:"type:mediumtext" json:"notes"`
	SourceURL       string    `gorm:"type:varchar(2048)" json:"source_url"`
	SourceTitle     string    `gorm:"type:varchar(500)" json:"source_title"`
	ScrapedContent  string    `gorm:"type:longtext" json:"scraped_content"`
	SourceSummary   string    `gorm:"type:text" json:"source_summary"`
	CreatedAt       time.Time `gorm:"not null" json:"created_at"`
	UpdatedAt       time.Time `gorm:"not null;index" json:"updated_at"`
}

func (ProductAdvisory) TableName() string { return "product_advisories" }

type ProductAdvisoryImage struct {
	ID                string    `gorm:"type:char(36);primaryKey" json:"id"`
	ProductAdvisoryID string    `gorm:"type:char(36);not null;index" json:"-"`
	TenantID          string    `gorm:"type:char(36);not null;index:idx_product_advisory_image_tenant_source" json:"-"`
	URL               string    `gorm:"type:varchar(2048)" json:"url"`
	Source            string    `gorm:"type:varchar(20);not null;index:idx_product_advisory_image_tenant_source" json:"source"`
	Caption           string    `gorm:"type:varchar(500)" json:"caption"`
	MediaType         string    `gorm:"type:varchar(32)" json:"media_type,omitempty"`
	Data              []byte    `gorm:"type:mediumblob" json:"-"`
	Enabled           bool      `gorm:"not null" json:"enabled"`
	SortOrder         int       `gorm:"not null;default:0" json:"sort_order"`
	CreatedAt         time.Time `gorm:"not null" json:"created_at"`
	UpdatedAt         time.Time `gorm:"not null" json:"updated_at"`

	ProductAdvisory ProductAdvisory `gorm:"foreignKey:ProductAdvisoryID;references:ID;constraint:OnDelete:CASCADE" json:"-"`
}

func (ProductAdvisoryImage) TableName() string { return "product_advisory_images" }
