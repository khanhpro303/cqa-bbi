package models

import "time"

type CachedProduct struct {
	ID                 string    `gorm:"type:char(36);primaryKey" json:"id"`
	TenantID           string    `gorm:"type:char(36);not null;index:idx_cp_tenant_ma" json:"tenant_id"`
	MA                 string    `gorm:"type:varchar(100);not null;index:idx_cp_tenant_ma" json:"ma"`
	TEN_DONG_BO_WEB    string    `gorm:"type:varchar(255);index:idx_cp_web_name" json:"ten_dong_bo_web"`
	TEN                string    `gorm:"type:varchar(255)" json:"ten"`
	THUOC_TINH_1       string    `gorm:"type:varchar(100)" json:"thuoc_tinh_1"`
	THUOC_TINH_2       string    `gorm:"type:varchar(100)" json:"thuoc_tinh_2"`
	DON_GIA_BAN        float64   `gorm:"type:decimal(15,2)" json:"don_gia_ban"`
	LINK_ANH           string    `gorm:"type:varchar(1024)" json:"link_anh"`
	NHAN_HIEU_NAME     string    `gorm:"type:varchar(255)" json:"nhan_hieu_name"`
	LIST_TEN_NHOM_VTHH string    `gorm:"type:varchar(500)" json:"list_ten_nhom_vthh"`
	KHO                string    `gorm:"type:varchar(100)" json:"kho"`
	MA_CHA             string    `gorm:"type:varchar(100);index:idx_cp_ma_cha" json:"ma_cha"`
	DVT                string    `gorm:"type:varchar(50)" json:"dvt"`
	CreatedAt          time.Time `gorm:"not null" json:"created_at"`
	UpdatedAt          time.Time `gorm:"not null" json:"updated_at"`
}

func (CachedProduct) TableName() string {
	return "cached_products"
}
