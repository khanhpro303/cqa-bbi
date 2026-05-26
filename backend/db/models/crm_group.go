package models

import "time"

type CRMGroup struct {
	ID          string         `gorm:"type:char(36);primaryKey" json:"id"`
	TenantID    string         `gorm:"type:char(36);not null;index" json:"tenant_id"`
	Name        string         `gorm:"type:varchar(255);not null" json:"name"`
	Description string         `gorm:"type:text" json:"description"`
	ZaloGroupID   string         `gorm:"type:varchar(255)" json:"zalo_group_id,omitempty"`
	ZaloGroupLink string         `gorm:"type:text" json:"zalo_group_link,omitempty"`
	ZaloAssetID   string         `gorm:"type:varchar(255)" json:"zalo_asset_id,omitempty"`
	ChannelID     string         `gorm:"type:char(36);index" json:"channel_id,omitempty"`
	CustomerCode  string         `gorm:"type:varchar(50);index" json:"customer_code,omitempty"`
	CreatedAt     time.Time      `gorm:"not null" json:"created_at"`
	UpdatedAt     time.Time      `gorm:"not null" json:"updated_at"`

	Employees []ZaloWhitelist `gorm:"many2many:crm_group_employees;joinForeignKey:GroupID;joinReferences:ZaloWhitelistID" json:"employees,omitempty"`
	Customers []ZaloCustomer  `gorm:"many2many:crm_group_customers;joinForeignKey:GroupID;joinReferences:ZaloCustomerID" json:"customers,omitempty"`
	Channel   *Channel        `gorm:"foreignKey:ChannelID" json:"channel,omitempty"`
}

func (CRMGroup) TableName() string {
	return "crm_groups"
}

type CRMGroupEmployee struct {
	GroupID         string `gorm:"type:char(36);primaryKey" json:"group_id"`
	ZaloWhitelistID string `gorm:"type:char(36);primaryKey" json:"zalo_whitelist_id"`
}

func (CRMGroupEmployee) TableName() string {
	return "crm_group_employees"
}

type CRMGroupCustomer struct {
	GroupID        string `gorm:"type:char(36);primaryKey" json:"group_id"`
	ZaloCustomerID string `gorm:"type:char(36);primaryKey" json:"zalo_customer_id"`
}

func (CRMGroupCustomer) TableName() string {
	return "crm_group_customers"
}
