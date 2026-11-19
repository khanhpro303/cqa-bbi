package db

import (
	"github.com/vietbui/chat-quality-agent/db/models"
	"gorm.io/gorm"
)

// ActiveCRMGroupIDs is a subquery of the IDs of CRM groups that are not
// archived. Member links (crm_group_customers / crm_group_employees) stay in
// place while a group is archived so it can be restored; any read that goes
// from a link to a group's permissions or data must filter with
//
//	Where("group_id IN (?)", db.ActiveCRMGroupIDs())
func ActiveCRMGroupIDs() *gorm.DB {
	return DB.Model(&models.CRMGroup{}).Select("id")
}
