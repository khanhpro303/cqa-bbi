package models

import "time"

type ProductAdvisory struct {
	ID             string    `gorm:"type:char(36);primaryKey" json:"id"`
	TenantID       string    `gorm:"type:char(36);not null;uniqueIndex:idx_product_advisory_tenant_parent" json:"tenant_id"`
	ParentCode     string    `gorm:"type:varchar(100);not null;uniqueIndex:idx_product_advisory_tenant_parent" json:"parent_code"`
	WebName        string    `gorm:"type:varchar(255);not null" json:"web_name"`
	Notes          string    `gorm:"type:mediumtext" json:"notes"`
	SourceURL      string    `gorm:"type:varchar(2048)" json:"source_url"`
	SourceTitle    string    `gorm:"type:varchar(500)" json:"source_title"`
	ScrapedContent string    `gorm:"type:longtext" json:"scraped_content"`
	CreatedAt      time.Time `gorm:"not null" json:"created_at"`
	UpdatedAt      time.Time `gorm:"not null;index" json:"updated_at"`
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
