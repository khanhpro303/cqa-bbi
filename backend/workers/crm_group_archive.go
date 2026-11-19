package workers

import (
	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/db/models"
)

// crmGroupArchived reports whether the Zalo group belongs only to archived CRM
// groups (an active group with the same Zalo ID wins).
func crmGroupArchived(zaloGroupID string) bool {
	var active int64
	if db.DB.Model(&models.CRMGroup{}).Where("zalo_group_id = ?", zaloGroupID).Count(&active); active > 0 {
		return false
	}
	var archived int64
	db.DB.Unscoped().Model(&models.CRMGroup{}).Where("zalo_group_id = ? AND archived_at IS NOT NULL", zaloGroupID).Count(&archived)
	return archived > 0
}
