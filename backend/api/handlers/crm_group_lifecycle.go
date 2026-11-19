package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/vietbui/chat-quality-agent/api/middleware"
	"github.com/vietbui/chat-quality-agent/channels"
	"github.com/vietbui/chat-quality-agent/channels/zalotoken"
	"github.com/vietbui/chat-quality-agent/config"
	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/db/models"
	"github.com/vietbui/chat-quality-agent/engine"
	"github.com/vietbui/chat-quality-agent/pkg"
	"gorm.io/gorm"
)

// CRM group lifecycle: archive → (restore | permanent delete).
//
// Archiving is the everyday "remove": the group gets archived_at (GORM soft
// delete), so every normal query stops seeing it and the bot, scheduled
// campaign sends, staff alerts, ERP scopes and order notifications skip it.
// Members, mã KH, ERP settings and all chat/order history stay in place, the
// real Zalo group is left alone, and the group can be restored.
//
// Permanent delete is owner-only, works only on an archived group, removes
// every config row that points at the group, and dissolves the Zalo group
// only when asked. Chat history, summaries, profiles, carts, order actions and
// order events are history keyed by Zalo group ID and mã KH: they are never
// removed with a group.

// crmGroupAlertChannels lists the channels that send staff alerts (handover
// or campaign failure) to this group. Archiving or deleting such a group would
// make those alerts fail silently, so callers refuse until it is changed.
func crmGroupAlertChannels(tenantID, groupID string) ([]string, error) {
	var chans []models.Channel
	if err := db.DB.Select("id, name, metadata").Where("tenant_id = ?", tenantID).Find(&chans).Error; err != nil {
		return nil, err
	}
	var names []string
	for _, ch := range chans {
		if channelAlertsToGroup(ch.Metadata, groupID) {
			names = append(names, ch.Name)
		}
	}
	return names, nil
}

// channelAlertsToGroup reports whether a channel's metadata routes handover
// alerts or (enabled) campaign alerts to the group. Pure for unit tests.
func channelAlertsToGroup(metadata, groupID string) bool {
	if strings.TrimSpace(metadata) == "" || groupID == "" {
		return false
	}
	var m map[string]interface{}
	if err := json.Unmarshal([]byte(metadata), &m); err != nil {
		return false
	}
	if v, _ := m["handover_alert_group_id"].(string); strings.TrimSpace(v) == groupID {
		return true
	}
	enabled, _ := m["campaign_alerts_enabled"].(bool)
	v, _ := m["campaign_alert_group_id"].(string)
	return enabled && strings.TrimSpace(v) == groupID
}

func refuseIfAlertTarget(c *gin.Context, tenantID, groupID string) bool {
	names, err := crmGroupAlertChannels(tenantID, groupID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed_to_check_alert_channels"})
		return true
	}
	if len(names) > 0 {
		c.JSON(http.StatusConflict, gin.H{
			"error":    "group_is_alert_target",
			"details":  "Nhóm đang nhận cảnh báo cho kênh: " + strings.Join(names, ", ") + ". Hãy đổi nhóm nhận cảnh báo trong cài đặt kênh trước.",
			"channels": names,
		})
		return true
	}
	return false
}

// stopGroupWork ends the bot's live work in a group: it cancels open carts,
// declines order actions still waiting for confirmation, and wipes the Redis
// session cache. History rows stay. Errors are logged, never fatal: the group
// is already out of every bot lookup by the time this runs.
func stopGroupWork(ctx context.Context, tenantID, zaloGroupID string) {
	if strings.TrimSpace(zaloGroupID) == "" {
		return
	}
	if err := db.DB.Model(&models.OrderCart{}).
		Where("tenant_id = ? AND zalo_group_id = ? AND status = ?", tenantID, zaloGroupID, "open").
		Updates(map[string]any{"status": "cancelled", "open_key": nil, "updated_at": time.Now()}).Error; err != nil {
		log.Printf("[crm] cancel open carts for group %s: %v", zaloGroupID, err)
	}
	if err := db.DB.Model(&models.OrderAction{}).
		Where("tenant_id = ? AND zalo_group_id = ? AND status = ?", tenantID, zaloGroupID, "pending").
		Updates(map[string]any{"status": "declined", "error": "group_archived", "updated_at": time.Now()}).Error; err != nil {
		log.Printf("[crm] decline pending order actions for group %s: %v", zaloGroupID, err)
	}
	if _, err := engine.ClearGroupSessionState(ctx, zaloGroupID); err != nil {
		log.Printf("[crm] clear session cache for group %s: %v", zaloGroupID, err)
	}
}

// ArchiveCRMGroup moves a group to the archive (soft delete). Reversible.
func ArchiveCRMGroup(c *gin.Context) {
	tenantID := middleware.GetTenantID(c)
	groupID := c.Param("id")

	var group models.CRMGroup
	if err := db.DB.Where("id = ? AND tenant_id = ?", groupID, tenantID).First(&group).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "group_not_found"})
		return
	}
	if refuseIfAlertTarget(c, tenantID, groupID) {
		return
	}
	if err := db.DB.Where("id = ? AND tenant_id = ?", groupID, tenantID).Delete(&models.CRMGroup{}).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed_to_archive_group"})
		return
	}
	stopGroupWork(c.Request.Context(), tenantID, group.ZaloGroupID)

	db.LogActivity(tenantID, middleware.GetUserID(c), middleware.GetUserEmail(c), "crm_group.archive", "crm_group", groupID,
		fmt.Sprintf("Lưu trữ nhóm '%s' (zalo_group_id=%s, khách=%s)", group.Name, group.ZaloGroupID, group.CustomerCode), "", c.ClientIP())
	c.JSON(http.StatusOK, gin.H{"message": "archived"})
}

// RestoreCRMGroup brings an archived group back with its members and settings.
func RestoreCRMGroup(c *gin.Context) {
	tenantID := middleware.GetTenantID(c)
	groupID := c.Param("id")

	var group models.CRMGroup
	if err := db.DB.Unscoped().Where("id = ? AND tenant_id = ? AND archived_at IS NOT NULL", groupID, tenantID).First(&group).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "archived_group_not_found"})
		return
	}
	// A Zalo group can back only one active CRM group: the bot resolves the
	// group by its Zalo ID.
	if zid := strings.TrimSpace(group.ZaloGroupID); zid != "" {
		var other models.CRMGroup
		if err := db.DB.Where("tenant_id = ? AND zalo_group_id = ? AND id <> ?", tenantID, zid, groupID).First(&other).Error; err == nil {
			c.JSON(http.StatusConflict, gin.H{
				"error":   "zalo_group_in_use",
				"details": fmt.Sprintf("Nhóm Zalo này đang thuộc nhóm CRM '%s'.", other.Name),
			})
			return
		}
	}
	if err := db.DB.Unscoped().Model(&models.CRMGroup{}).Where("id = ? AND tenant_id = ?", groupID, tenantID).
		Updates(map[string]any{"archived_at": nil, "updated_at": time.Now()}).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed_to_restore_group"})
		return
	}
	db.LogActivity(tenantID, middleware.GetUserID(c), middleware.GetUserEmail(c), "crm_group.restore", "crm_group", groupID,
		fmt.Sprintf("Khôi phục nhóm '%s' (zalo_group_id=%s)", group.Name, group.ZaloGroupID), "", c.ClientIP())
	c.JSON(http.StatusOK, gin.H{"message": "restored"})
}

// DeleteCRMGroup permanently deletes an ARCHIVED group and every config row
// that points at it. ?dissolve_zalo=true also dissolves the real Zalo group,
// which cannot be undone. Chat and order history is kept.
func DeleteCRMGroup(c *gin.Context) {
	tenantID := middleware.GetTenantID(c)
	groupID := c.Param("id")
	dissolve := c.Query("dissolve_zalo") == "true"

	var group models.CRMGroup
	if err := db.DB.Unscoped().Where("id = ? AND tenant_id = ?", groupID, tenantID).First(&group).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "group_not_found"})
		return
	}
	if !group.ArchivedAt.Valid {
		c.JSON(http.StatusConflict, gin.H{"error": "group_not_archived", "details": "Hãy lưu trữ nhóm trước khi xoá vĩnh viễn."})
		return
	}
	if refuseIfAlertTarget(c, tenantID, groupID) {
		return
	}

	if dissolve && strings.TrimSpace(group.ZaloGroupID) != "" {
		if err := dissolveZaloGroup(c.Request.Context(), tenantID, group); err != nil {
			log.Printf("[crm] failed to dissolve Zalo GMF group %s (%s): %v", group.Name, group.ZaloGroupID, err)
			c.JSON(http.StatusBadGateway, gin.H{"error": "failed_to_delete_zalo_group", "details": err.Error()})
			return
		}
		log.Printf("[crm] dissolved Zalo GMF group %s (%s)", group.Name, group.ZaloGroupID)
	}

	if err := db.DB.Transaction(func(tx *gorm.DB) error {
		return purgeCRMGroupConfig(tx, tenantID, groupID)
	}); err != nil {
		log.Printf("[crm] permanent delete of group %s failed: %v", groupID, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed_to_delete_group"})
		return
	}
	stopGroupWork(c.Request.Context(), tenantID, group.ZaloGroupID)

	detail := fmt.Sprintf("Xoá vĩnh viễn nhóm '%s' (zalo_group_id=%s, khách=%s)", group.Name, group.ZaloGroupID, group.CustomerCode)
	if dissolve {
		detail += ", đã giải tán nhóm Zalo"
	}
	db.LogActivity(tenantID, middleware.GetUserID(c), middleware.GetUserEmail(c), "crm_group.delete", "crm_group", groupID, detail, "", c.ClientIP())
	c.JSON(http.StatusOK, gin.H{"message": "deleted", "zalo_dissolved": dissolve && group.ZaloGroupID != ""})
}

// purgeCRMGroupConfig removes the group row and every config row keyed on its
// ID. Campaign run history keeps its rows (they carry the segment ID only).
func purgeCRMGroupConfig(tx *gorm.DB, tenantID, groupID string) error {
	steps := []struct {
		name string
		run  func() error
	}{
		{"employees", func() error { return tx.Where("group_id = ?", groupID).Delete(&models.CRMGroupEmployee{}).Error }},
		{"customers", func() error { return tx.Where("group_id = ?", groupID).Delete(&models.CRMGroupCustomer{}).Error }},
		{"customer_codes", func() error {
			return tx.Where("tenant_id = ? AND group_id = ?", tenantID, groupID).Delete(&models.CRMGroupCustomerCode{}).Error
		}},
		{"erp_endpoints", func() error {
			return tx.Where("tenant_id = ? AND group_id = ?", tenantID, groupID).Delete(&models.ERPEndpoint{}).Error
		}},
		{"erp_rate_limits", func() error {
			return tx.Where("tenant_id = ? AND group_id = ?", tenantID, groupID).Delete(&models.ERPGroupRateLimit{}).Error
		}},
		{"campaign_segments", func() error {
			return tx.Where("group_id = ? AND campaign_id IN (?)", groupID,
				tx.Model(&models.Campaign{}).Select("id").Where("tenant_id = ?", tenantID)).Delete(&models.CampaignSegment{}).Error
		}},
		{"group", func() error {
			return tx.Unscoped().Where("id = ? AND tenant_id = ?", groupID, tenantID).Delete(&models.CRMGroup{}).Error
		}},
	}
	for _, s := range steps {
		if err := s.run(); err != nil {
			return fmt.Errorf("%s: %w", s.name, err)
		}
	}
	return nil
}

// dissolveZaloGroup disbands the real Zalo GMF group through the group's OA
// channel. Unlike the old delete path it fails loudly when the channel or its
// credentials cannot be loaded, so the owner never believes a group was
// dissolved when it was not.
func dissolveZaloGroup(ctx context.Context, tenantID string, group models.CRMGroup) error {
	var channel models.Channel
	q := db.DB.Where("tenant_id = ? AND channel_type = ? AND is_active = ?", tenantID, "zalo_oa", true)
	if group.ChannelID != "" {
		q = q.Where("id = ?", group.ChannelID)
	}
	if err := q.First(&channel).Error; err != nil {
		return errors.New("không tìm thấy kênh Zalo OA đang hoạt động của nhóm")
	}
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	credBytes, err := pkg.Decrypt(channel.CredentialsEncrypted, cfg.EncryptionKey)
	if err != nil {
		return errors.New("không giải mã được thông tin kênh Zalo OA")
	}
	var creds channels.ZaloOACredentials
	if err := json.Unmarshal(credBytes, &creds); err != nil {
		return errors.New("thông tin kênh Zalo OA không hợp lệ")
	}
	return zalotoken.NewAdapter(channel.ID, creds, cfg.EncryptionKey).DeleteGMFGroup(ctx, group.ZaloGroupID)
}
