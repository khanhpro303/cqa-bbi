package models

import "time"

// CustomerImageFile stores a single signed Zalo attachment. Its bytes are read
// only when staff request the image through a case that references it.
type CustomerImageFile struct {
	ID        string    `gorm:"type:char(36);primaryKey"`
	TenantID  string    `gorm:"type:char(36);not null"`
	ChannelID string    `gorm:"type:char(36);not null"`
	SenderID  string    `gorm:"type:varchar(255);not null"`
	MediaType string    `gorm:"type:varchar(32);not null"`
	Data      []byte    `gorm:"type:mediumblob;not null" json:"-"`
	CreatedAt time.Time `gorm:"not null;index"`
	Tenant    Tenant    `gorm:"foreignKey:TenantID;references:ID;constraint:OnDelete:CASCADE" json:"-"`
}

// CustomerCaseImageLink gives the case access to a stored image without
// duplicating the bytes if the report is linked to more than one case.
type CustomerCaseImageLink struct {
	CaseID  string            `gorm:"type:char(36);primaryKey"`
	ImageID string            `gorm:"type:char(36);primaryKey;index"`
	Case    CustomerCase      `gorm:"foreignKey:CaseID;references:ID;constraint:OnDelete:CASCADE" json:"-"`
	Image   CustomerImageFile `gorm:"foreignKey:ImageID;references:ID;constraint:OnDelete:CASCADE" json:"-"`
}
