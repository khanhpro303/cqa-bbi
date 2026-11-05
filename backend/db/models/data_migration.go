package models

import "time"

// DataMigration records one-time data backfills that must not run again after
// users edit the migrated data.
type DataMigration struct {
	Key       string    `gorm:"type:varchar(191);primaryKey" json:"key"`
	AppliedAt time.Time `gorm:"not null;autoCreateTime" json:"applied_at"`
}

func (DataMigration) TableName() string {
	return "data_migrations"
}
