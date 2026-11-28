package db

import (
	"strings"

	"github.com/vietbui/chat-quality-agent/db/models"
	"gorm.io/gorm"
)

// MigrateProductAdvisoryGroups attaches existing profiles without changing
// their IDs, so saved advice and all image relationships survive the split.
func MigrateProductAdvisoryGroups(database *gorm.DB) error {
	const oldIndex = "idx_product_advisory_tenant_parent"
	if err := database.Transaction(func(tx *gorm.DB) error {
		var oldProfiles []models.ProductAdvisory
		if err := tx.Select("id", "tenant_id", "parent_code", "web_name").
			Where("group_key IS NULL").Find(&oldProfiles).Error; err != nil {
			return err
		}
		for _, profile := range oldProfiles {
			var groups []struct {
				GroupKey        string
				WebName         string
				ProductCategory string
			}
			if err := tx.Model(&models.CachedProduct{}).
				Select(models.ProductAdvisoryGroupKeySQL+" AS group_key, "+models.ProductAdvisoryWebNameSQL+" AS web_name, MAX("+models.ProductAdvisoryCategorySQL+") AS product_category").
				Where("tenant_id = ? AND TRIM(ma) <> '' AND "+models.ProductAdvisoryParentSQL+" = ?", profile.TenantID, strings.TrimSpace(profile.ParentCode)).
				Where("RIGHT(UPPER(TRIM(ma)), 3) <> '_DK' AND RIGHT(UPPER(TRIM(COALESCE(ma_cha, ''))), 3) <> '_DK'").
				Group(models.ProductAdvisoryGroupKeySQL).Scan(&groups).Error; err != nil {
				return err
			}
			var key, category string
			for _, group := range groups {
				if strings.EqualFold(strings.TrimSpace(profile.WebName), group.WebName) {
					if key != "" {
						key = ""
						break
					}
					key = group.GroupKey
					category = group.ProductCategory
				}
			}
			if key == "" {
				// The current catalog cannot identify the legacy target.
				continue
			}
			var taken int64
			if err := tx.Model(&models.ProductAdvisory{}).
				Where("tenant_id = ? AND group_key = ?", profile.TenantID, key).Count(&taken).Error; err != nil {
				return err
			}
			if taken > 0 {
				continue
			}
			if err := tx.Model(&models.ProductAdvisory{}).
				Where("id = ? AND group_key IS NULL", profile.ID).
				UpdateColumns(map[string]any{"group_key": key, "product_category": category}).Error; err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return err
	}
	if database.Migrator().HasIndex(&models.ProductAdvisory{}, oldIndex) {
		return database.Migrator().DropIndex(&models.ProductAdvisory{}, oldIndex)
	}
	return nil
}
