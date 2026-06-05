package db

import (
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/vietbui/chat-quality-agent/db/models"
	"gorm.io/gorm"
)

// ---------------------------------------------------------------------------
// Multi customer-code mapping (one Zalo user / one CRM group → many mã KH).
//
// The single CustomerCode field on ZaloCustomer / CRMGroup is the PRIMARY code
// (backward-compat); the zalo_customer_codes / crm_group_customer_codes tables
// hold the full set. Readers fall back to the primary field when the join table
// is empty (rows approved before this feature), so existing single-store data
// keeps resolving without a backfill.
// ---------------------------------------------------------------------------

// normalizeCustomerCodes trims, drops empties, and de-duplicates a list of codes
// (case-insensitive on the leading code) while preserving input order so the
// caller's intended primary (first element) stays first.
func normalizeCustomerCodes(codes []string) []string {
	seen := make(map[string]bool, len(codes))
	out := make([]string, 0, len(codes))
	for _, c := range codes {
		v := strings.TrimSpace(c)
		if v == "" {
			continue
		}
		key := strings.ToLower(v)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, v)
	}
	return out
}

// GetZaloCustomerCodes returns every Cloudify customer code linked to a Zalo
// customer, primary first. Falls back to [fallback] when the join table has no
// rows yet. Returns nil when neither source yields a code.
func GetZaloCustomerCodes(tenantID, zaloCustomerID, fallback string) []string {
	var rows []models.ZaloCustomerCode
	if err := DB.Where("zalo_customer_id = ? AND tenant_id = ?", zaloCustomerID, tenantID).
		Order("is_primary DESC, customer_code ASC").Find(&rows).Error; err == nil && len(rows) > 0 {
		out := make([]string, 0, len(rows))
		for _, r := range rows {
			out = append(out, r.CustomerCode)
		}
		return normalizeCustomerCodes(out)
	}
	if c := strings.TrimSpace(fallback); c != "" {
		return []string{c}
	}
	return nil
}

// GetGroupCustomerCodes returns every customer code assigned to the given CRM
// groups, falling back to each group's primary CRMGroup.CustomerCode when the
// join table is empty for that group. De-duplicated and sorted for determinism.
func GetGroupCustomerCodes(tenantID string, groupIDs []string) []string {
	if len(groupIDs) == 0 {
		return nil
	}
	var rows []models.CRMGroupCustomerCode
	out := []string{}
	if err := DB.Where("group_id IN ? AND tenant_id = ?", groupIDs, tenantID).Find(&rows).Error; err == nil {
		for _, r := range rows {
			out = append(out, r.CustomerCode)
		}
	}
	if len(out) == 0 {
		// Fallback: read the primary single field off each group.
		var groups []models.CRMGroup
		DB.Where("id IN ? AND tenant_id = ?", groupIDs, tenantID).Find(&groups)
		for _, g := range groups {
			if c := strings.TrimSpace(g.CustomerCode); c != "" {
				out = append(out, c)
			}
		}
	}
	out = normalizeCustomerCodes(out)
	sort.Strings(out)
	return out
}

// SetZaloCustomerCodes replaces the full code set for a Zalo customer in one
// transaction-friendly call: it rewrites the zalo_customer_codes rows and syncs
// the primary onto ZaloCustomer.CustomerCode. The first non-empty code (or an
// explicit primary present in the list) becomes primary. Returns the primary.
func SetZaloCustomerCodes(tx *gorm.DB, tenantID, zaloCustomerID string, codes []string, primary string) (string, error) {
	codes = normalizeCustomerCodes(codes)
	primary = orderPrimaryFirst(&codes, primary)

	if err := tx.Where("zalo_customer_id = ? AND tenant_id = ?", zaloCustomerID, tenantID).
		Delete(&models.ZaloCustomerCode{}).Error; err != nil {
		return "", err
	}
	now := time.Now()
	for i, code := range codes {
		row := models.ZaloCustomerCode{
			ID:             uuid.New().String(),
			TenantID:       tenantID,
			ZaloCustomerID: zaloCustomerID,
			CustomerCode:   code,
			IsPrimary:      i == 0,
			CreatedAt:      now,
			UpdatedAt:      now,
		}
		if err := tx.Create(&row).Error; err != nil {
			return "", err
		}
	}
	if err := tx.Model(&models.ZaloCustomer{}).
		Where("id = ? AND tenant_id = ?", zaloCustomerID, tenantID).
		Update("customer_code", primary).Error; err != nil {
		return "", err
	}
	return primary, nil
}

// SetGroupCustomerCodes mirrors SetZaloCustomerCodes for a CRM group, syncing
// the primary onto CRMGroup.CustomerCode.
func SetGroupCustomerCodes(tx *gorm.DB, tenantID, groupID string, codes []string, primary string) (string, error) {
	codes = normalizeCustomerCodes(codes)
	primary = orderPrimaryFirst(&codes, primary)

	if err := tx.Where("group_id = ? AND tenant_id = ?", groupID, tenantID).
		Delete(&models.CRMGroupCustomerCode{}).Error; err != nil {
		return "", err
	}
	now := time.Now()
	for i, code := range codes {
		row := models.CRMGroupCustomerCode{
			ID:           uuid.New().String(),
			TenantID:     tenantID,
			GroupID:      groupID,
			CustomerCode: code,
			IsPrimary:    i == 0,
			CreatedAt:    now,
			UpdatedAt:    now,
		}
		if err := tx.Create(&row).Error; err != nil {
			return "", err
		}
	}
	if err := tx.Model(&models.CRMGroup{}).
		Where("id = ? AND tenant_id = ?", groupID, tenantID).
		Update("customer_code", primary).Error; err != nil {
		return "", err
	}
	return primary, nil
}

// MergeZaloCustomerCodes unions addCodes into a customer's existing code set,
// preserving the current primary, then rewrites it. Used when adding a customer
// to a group so the group's codes extend (not overwrite) the customer's shops.
func MergeZaloCustomerCodes(tx *gorm.DB, tenantID, zaloCustomerID string, addCodes []string) error {
	existing := GetZaloCustomerCodes(tenantID, zaloCustomerID, "")
	primary := ""
	if len(existing) > 0 {
		primary = existing[0]
	}
	merged := normalizeCustomerCodes(append(append([]string{}, existing...), addCodes...))
	_, err := SetZaloCustomerCodes(tx, tenantID, zaloCustomerID, merged, primary)
	return err
}

// orderPrimaryFirst moves primary to the front of codes (adding it if missing),
// returning the effective primary. When primary is empty it defaults to the
// current first element. Mutates *codes in place.
func orderPrimaryFirst(codes *[]string, primary string) string {
	list := *codes
	primary = strings.TrimSpace(primary)
	if primary == "" {
		if len(list) > 0 {
			return list[0]
		}
		return ""
	}
	reordered := []string{primary}
	for _, c := range list {
		if !strings.EqualFold(c, primary) {
			reordered = append(reordered, c)
		}
	}
	*codes = normalizeCustomerCodes(reordered)
	return primary
}
