package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/vietbui/chat-quality-agent/api/middleware"
	"github.com/vietbui/chat-quality-agent/channels"
	"github.com/vietbui/chat-quality-agent/config"
	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/db/models"
	"github.com/vietbui/chat-quality-agent/pkg"
)

// CRM Group CRUD

func ListCRMGroups(c *gin.Context) {
	tenantID := middleware.GetTenantID(c)

	var groups []models.CRMGroup
	if err := db.DB.Preload("Employees").Preload("Customers").Preload("Channel").Where("tenant_id = ?", tenantID).Find(&groups).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed_to_list_groups"})
		return
	}

	c.JSON(http.StatusOK, groups)
}

func CreateCRMGroup(c *gin.Context) {
	tenantID := middleware.GetTenantID(c)

	var req struct {
		Name         string `json:"name" binding:"required"`
		Description  string `json:"description"`
		AssetID      string `json:"asset_id"`
		ChannelID    string `json:"channel_id"`
		CustomerCode string `json:"customer_code" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request", "details": err.Error()})
		return
	}

	// 1. Fetch active Zalo OA channel
	var channel models.Channel
	var err error
	if req.ChannelID != "" {
		err = db.DB.Where("id = ? AND tenant_id = ? AND channel_type = ? AND is_active = ?", req.ChannelID, tenantID, "zalo_oa", true).First(&channel).Error
	} else {
		err = db.DB.Where("tenant_id = ? AND channel_type = ? AND is_active = ?", tenantID, "zalo_oa", true).First(&channel).Error
	}
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "zalo_oa_not_configured", "details": "Vui lòng kết nối Zalo OA hoạt động trước khi tạo nhóm chat GMF."})
		return
	}

	cfg, _ := config.Load()
	credBytes, err := pkg.Decrypt(channel.CredentialsEncrypted, cfg.EncryptionKey)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed_to_decrypt_channel_credentials"})
		return
	}
	var zaloCreds channels.ZaloOACredentials
	if err := json.Unmarshal(credBytes, &zaloCreds); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed_to_parse_channel_credentials"})
		return
	}

	adapter := channels.NewZaloOAAdapter(zaloCreds)
	adapter.SetTokenRefreshCallback(func(newAccess, newRefresh string) {
		zaloCreds.AccessToken = newAccess
		zaloCreds.RefreshToken = newRefresh
		credsMap := map[string]interface{}{
			"app_id":        zaloCreds.AppID,
			"app_secret":    zaloCreds.AppSecret,
			"access_token":  newAccess,
			"refresh_token": newRefresh,
			"oa_id":         zaloCreds.OAId,
		}
		newCredJSON, _ := json.Marshal(credsMap)
		encrypted, _ := pkg.Encrypt(newCredJSON, cfg.EncryptionKey)
		db.DB.Model(&channel).Update("credentials_encrypted", encrypted)
	})

	// 2. Fetch or select GMF asset_id
	assetID := req.AssetID
	if assetID == "" {
		assets, err := adapter.GetGMFQuota(c.Request.Context())
		if err == nil {
			for _, a := range assets {
				if a.UsedGroup < a.TotalGroup {
					assetID = a.AssetID
					break
				}
			}
		}
		if assetID == "" {
			assetID = os.Getenv("ZALO_OA_GMF_ASSET_ID")
		}
		if assetID == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "gmf_package_required", "details": "Không thể tự động tìm thấy gói hạn mức GMF còn trống. Vui lòng chọn gói hoặc cấu hình ZALO_OA_GMF_ASSET_ID."})
			return
		}
	}

	// 3. Find Zalo User IDs as initial members. Zalo GMF requires member_user_ids
	// to contain at least one OA admin and no more than 99 users.
	var activeStaff []models.ZaloWhitelist
	if err := db.DB.
		Where("tenant_id = ? AND (channel_id = ? OR channel_id = '' OR channel_id IS NULL) AND status = ? AND zalo_user_id <> ''", tenantID, channel.ID, "active").
		Order("updated_at DESC").
		Limit(99).
		Find(&activeStaff).Error; err != nil {
		log.Printf("[crm] failed to load active Zalo whitelist members for tenant %s: %v", tenantID, err)
	}
	initialMembers := normalizeZaloWhitelistMemberUserIDs(activeStaff, 99)

	if len(initialMembers) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "zalo_member_required",
			"details": "Theo tài liệu Zalo GMF, member_user_ids không được rỗng, không quá 99 người và phải có ít nhất 1 người là admin của OA. Vui lòng liên kết ít nhất 1 nhân viên Zalo có quyền admin OA trước khi tạo nhóm.",
		})
		return
	}

	// 4. Create GMF Group on Zalo
	zGroupID, zGroupLink, err := adapter.CreateGMFGroup(c.Request.Context(), req.Name, req.Description, assetID, initialMembers)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "zalo_create_group_failed",
			"details": err.Error(),
		})
		return
	}

	// 5. Save to MySQL
	now := time.Now()
	group := models.CRMGroup{
		ID:            uuid.New().String(),
		TenantID:      tenantID,
		ChannelID:     channel.ID,
		Name:          req.Name,
		Description:   req.Description,
		ZaloGroupID:   zGroupID,
		ZaloGroupLink: zGroupLink,
		ZaloAssetID:   assetID,
		CustomerCode:  req.CustomerCode,
		CreatedAt:     now,
		UpdatedAt:     now,
	}

	tx := db.DB.Begin()
	if err := tx.Create(&group).Error; err != nil {
		tx.Rollback()
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed_to_create_group"})
		return
	}
	tx.Commit()

	// Preload associations for response
	db.DB.Preload("Employees").Preload("Customers").Preload("Channel").First(&group, "id = ?", group.ID)

	c.JSON(http.StatusCreated, group)
}

func UpdateCRMGroup(c *gin.Context) {
	tenantID := middleware.GetTenantID(c)
	groupID := c.Param("id")

	var req struct {
		Name         string `json:"name" binding:"required"`
		Description  string `json:"description"`
		CustomerCode string `json:"customer_code" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request", "details": err.Error()})
		return
	}

	var group models.CRMGroup
	if err := db.DB.Where("id = ? AND tenant_id = ?", groupID, tenantID).First(&group).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "group_not_found"})
		return
	}

	group.Name = req.Name
	group.Description = req.Description
	group.CustomerCode = req.CustomerCode
	group.UpdatedAt = time.Now()

	if err := db.DB.Save(&group).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed_to_update_group"})
		return
	}

	c.JSON(http.StatusOK, group)
}

func DeleteCRMGroup(c *gin.Context) {
	tenantID := middleware.GetTenantID(c)
	groupID := c.Param("id")

	var group models.CRMGroup
	if err := db.DB.Where("id = ? AND tenant_id = ?", groupID, tenantID).First(&group).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "group_not_found"})
		return
	}

	// 1. Disband on Zalo GMF if linked
	if group.ZaloGroupID != "" {
		var channel models.Channel
		var err error
		if group.ChannelID != "" {
			err = db.DB.Where("id = ? AND tenant_id = ? AND channel_type = ? AND is_active = ?", group.ChannelID, tenantID, "zalo_oa", true).First(&channel).Error
		} else {
			err = db.DB.Where("tenant_id = ? AND channel_type = ? AND is_active = ?", tenantID, "zalo_oa", true).First(&channel).Error
		}
		if err == nil {
			cfg, _ := config.Load()
			if credBytes, err := pkg.Decrypt(channel.CredentialsEncrypted, cfg.EncryptionKey); err == nil {
				var zaloCreds channels.ZaloOACredentials
				if json.Unmarshal(credBytes, &zaloCreds) == nil {
					adapter := channels.NewZaloOAAdapter(zaloCreds)
					adapter.SetTokenRefreshCallback(func(newAccess, newRefresh string) {
						zaloCreds.AccessToken = newAccess
						zaloCreds.RefreshToken = newRefresh
						credsMap := map[string]interface{}{
							"app_id":        zaloCreds.AppID,
							"app_secret":    zaloCreds.AppSecret,
							"access_token":  newAccess,
							"refresh_token": newRefresh,
							"oa_id":         zaloCreds.OAId,
						}
						newCredJSON, _ := json.Marshal(credsMap)
						encrypted, _ := pkg.Encrypt(newCredJSON, cfg.EncryptionKey)
						db.DB.Model(&channel).Update("credentials_encrypted", encrypted)
					})

					if deleteErr := adapter.DeleteGMFGroup(c.Request.Context(), group.ZaloGroupID); deleteErr != nil {
						log.Printf("[crm] failed to dissolve Zalo GMF group %s (%s): %v", group.Name, group.ZaloGroupID, deleteErr)
						c.JSON(http.StatusInternalServerError, gin.H{
							"error":   "failed_to_delete_zalo_group",
							"details": deleteErr.Error(),
						})
						return
					}
					log.Printf("[crm] successfully dissolved Zalo GMF group %s (%s)", group.Name, group.ZaloGroupID)
				}
			}
		}
	}

	tx := db.DB.Begin()
	// Delete group association
	if err := tx.Where("group_id = ?", groupID).Delete(&models.CRMGroupEmployee{}).Error; err != nil {
		tx.Rollback()
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed_to_delete_group_employees"})
		return
	}
	if err := tx.Where("group_id = ?", groupID).Delete(&models.CRMGroupCustomer{}).Error; err != nil {
		tx.Rollback()
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed_to_delete_group_customers"})
		return
	}
	// Delete group itself
	if err := tx.Where("id = ? AND tenant_id = ?", groupID, tenantID).Delete(&models.CRMGroup{}).Error; err != nil {
		tx.Rollback()
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed_to_delete_group"})
		return
	}

	tx.Commit()
	c.JSON(http.StatusOK, gin.H{"message": "deleted"})
}

func AddGroupMembers(c *gin.Context) {
	tenantID := middleware.GetTenantID(c)
	groupID := c.Param("id")

	var req struct {
		EmployeeIDs []string `json:"employee_ids"`
		CustomerIDs []string `json:"customer_ids"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request", "details": err.Error()})
		return
	}

	// Verify group exists for this tenant
	var group models.CRMGroup
	if err := db.DB.Where("id = ? AND tenant_id = ?", groupID, tenantID).First(&group).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "group_not_found"})
		return
	}

	tx := db.DB.Begin()
	// Add employees
	for _, empID := range req.EmployeeIDs {
		var count int64
		tx.Model(&models.CRMGroupEmployee{}).Where("group_id = ? AND user_id = ?", groupID, empID).Count(&count)
		if count == 0 {
			if err := tx.Create(&models.CRMGroupEmployee{GroupID: groupID, UserID: empID}).Error; err != nil {
				tx.Rollback()
				c.JSON(http.StatusInternalServerError, gin.H{"error": "failed_to_add_employee_to_group"})
				return
			}
		}
	}

	// Add customers
	for _, custID := range req.CustomerIDs {
		var count int64
		tx.Model(&models.CRMGroupCustomer{}).Where("group_id = ? AND zalo_customer_id = ?", groupID, custID).Count(&count)
		if count == 0 {
			if err := tx.Create(&models.CRMGroupCustomer{GroupID: groupID, ZaloCustomerID: custID}).Error; err != nil {
				tx.Rollback()
				c.JSON(http.StatusInternalServerError, gin.H{"error": "failed_to_add_customer_to_group"})
				return
			}
		}
		if group.CustomerCode != "" {
			if err := tx.Model(&models.ZaloCustomer{}).Where("id = ? AND tenant_id = ?", custID, tenantID).Update("customer_code", group.CustomerCode).Error; err != nil {
				tx.Rollback()
				c.JSON(http.StatusInternalServerError, gin.H{"error": "failed_to_update_customer_code"})
				return
			}
		}
	}

	tx.Commit()

	// Invite customers to Zalo GMF group chat if group has ZaloGroupID
	if group.ZaloGroupID != "" && len(req.CustomerIDs) > 0 {
		var zaloUserIDs []string
		var customers []models.ZaloCustomer
		if err := db.DB.Where("id IN ? AND tenant_id = ? AND zalo_user_id != ''", req.CustomerIDs, tenantID).Find(&customers).Error; err == nil {
			for _, cust := range customers {
				zaloUserIDs = append(zaloUserIDs, cust.ZaloUserID)
			}
		}

		if len(zaloUserIDs) > 0 {
			var channel models.Channel
			var err error
			if group.ChannelID != "" {
				err = db.DB.Where("id = ? AND tenant_id = ? AND channel_type = ? AND is_active = ?", group.ChannelID, tenantID, "zalo_oa", true).First(&channel).Error
			} else {
				err = db.DB.Where("tenant_id = ? AND channel_type = ? AND is_active = ?", tenantID, "zalo_oa", true).First(&channel).Error
			}
			if err == nil {
				cfg, _ := config.Load()
				if credBytes, err := pkg.Decrypt(channel.CredentialsEncrypted, cfg.EncryptionKey); err == nil {
					var zaloCreds channels.ZaloOACredentials
					if err := json.Unmarshal(credBytes, &zaloCreds); err == nil {
						adapter := channels.NewZaloOAAdapter(zaloCreds)
						adapter.SetTokenRefreshCallback(func(newAccess, newRefresh string) {
							zaloCreds.AccessToken = newAccess
							zaloCreds.RefreshToken = newRefresh
							credsMap := map[string]interface{}{
								"app_id":        zaloCreds.AppID,
								"app_secret":    zaloCreds.AppSecret,
								"access_token":  newAccess,
								"refresh_token": newRefresh,
								"oa_id":         zaloCreds.OAId,
							}
							newCredJSON, _ := json.Marshal(credsMap)
							encrypted, _ := pkg.Encrypt(newCredJSON, cfg.EncryptionKey)
							db.DB.Model(&channel).Update("credentials_encrypted", encrypted)
						})

						if inviteErr := adapter.InviteGMFGroupMembers(c.Request.Context(), group.ZaloGroupID, zaloUserIDs); inviteErr != nil {
							log.Printf("[crm] failed to invite customers %v to Zalo GMF group %s (%s): %v", zaloUserIDs, group.Name, group.ZaloGroupID, inviteErr)
						} else {
							log.Printf("[crm] successfully invited customers %v to Zalo GMF group %s (%s)", zaloUserIDs, group.Name, group.ZaloGroupID)
						}
					}
				}
			}
		}
	}

	c.JSON(http.StatusOK, gin.H{"message": "members_added"})
}

func RemoveGroupMembers(c *gin.Context) {
	tenantID := middleware.GetTenantID(c)
	groupID := c.Param("id")

	var req struct {
		EmployeeIDs []string `json:"employee_ids"`
		CustomerIDs []string `json:"customer_ids"`
		ZaloUserIDs []string `json:"zalo_user_ids"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request", "details": err.Error()})
		return
	}

	// Verify group exists for this tenant
	var group models.CRMGroup
	if err := db.DB.Where("id = ? AND tenant_id = ?", groupID, tenantID).First(&group).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "group_not_found"})
		return
	}

	// 1. Fetch Zalo User IDs of the customers to be removed from Zalo GMF group chat
	var zaloUserIDs []string
	if group.ZaloGroupID != "" {
		if len(req.CustomerIDs) > 0 {
			var customers []models.ZaloCustomer
			if err := db.DB.Where("id IN ? AND tenant_id = ? AND zalo_user_id != ''", req.CustomerIDs, tenantID).Find(&customers).Error; err == nil {
				for _, cust := range customers {
					zaloUserIDs = append(zaloUserIDs, cust.ZaloUserID)
				}
			}
		}
		for _, uid := range req.ZaloUserIDs {
			trimmed := strings.TrimSpace(uid)
			if trimmed != "" {
				zaloUserIDs = append(zaloUserIDs, trimmed)
			}
		}
	}

	// 2. Call Zalo API to remove members from the real group chat BEFORE local database changes.
	// This ensures we can report Zalo API errors back to the UI and avoid silent failures.
	if len(zaloUserIDs) > 0 {
		var channel models.Channel
		var err error
		if group.ChannelID != "" {
			err = db.DB.Where("id = ? AND tenant_id = ? AND channel_type = ? AND is_active = ?", group.ChannelID, tenantID, "zalo_oa", true).First(&channel).Error
		} else {
			err = db.DB.Where("tenant_id = ? AND channel_type = ? AND is_active = ?", tenantID, "zalo_oa", true).First(&channel).Error
		}
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "zalo_oa_not_configured", "details": "Vui lòng cấu hình Zalo OA hoạt động để xoá thành viên khỏi nhóm chat GMF."})
			return
		}

		cfg, _ := config.Load()
		credBytes, err := pkg.Decrypt(channel.CredentialsEncrypted, cfg.EncryptionKey)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed_to_decrypt_channel_credentials"})
			return
		}
		var zaloCreds channels.ZaloOACredentials
		if err := json.Unmarshal(credBytes, &zaloCreds); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed_to_parse_channel_credentials"})
			return
		}

		adapter := channels.NewZaloOAAdapter(zaloCreds)
		adapter.SetTokenRefreshCallback(func(newAccess, newRefresh string) {
			zaloCreds.AccessToken = newAccess
			zaloCreds.RefreshToken = newRefresh
			credsMap := map[string]interface{}{
				"app_id":        zaloCreds.AppID,
				"app_secret":    zaloCreds.AppSecret,
				"access_token":  newAccess,
				"refresh_token": newRefresh,
				"oa_id":         zaloCreds.OAId,
			}
			newCredJSON, _ := json.Marshal(credsMap)
			encrypted, _ := pkg.Encrypt(newCredJSON, cfg.EncryptionKey)
			db.DB.Model(&channel).Update("credentials_encrypted", encrypted)
		})

		if removeErr := adapter.RemoveGMFGroupMembers(c.Request.Context(), group.ZaloGroupID, zaloUserIDs); removeErr != nil {
			log.Printf("[crm] failed to remove customers %v from Zalo GMF group %s (%s): %v", zaloUserIDs, group.Name, group.ZaloGroupID, removeErr)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed_to_remove_zalo_members", "details": removeErr.Error()})
			return
		}
		log.Printf("[crm] successfully removed customers %v from Zalo GMF group %s (%s)", zaloUserIDs, group.Name, group.ZaloGroupID)
	}

	// 3. Update local database
	tx := db.DB.Begin()
	// Remove employees
	if len(req.EmployeeIDs) > 0 {
		if err := tx.Where("group_id = ? AND user_id IN ?", groupID, req.EmployeeIDs).Delete(&models.CRMGroupEmployee{}).Error; err != nil {
			tx.Rollback()
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed_to_remove_employees"})
			return
		}
	}

	// Remove customers
	if len(req.CustomerIDs) > 0 {
		if err := tx.Where("group_id = ? AND zalo_customer_id IN ?", groupID, req.CustomerIDs).Delete(&models.CRMGroupCustomer{}).Error; err != nil {
			tx.Rollback()
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed_to_remove_customers"})
			return
		}
	}

	// Remove customers by Zalo User IDs
	if len(req.ZaloUserIDs) > 0 {
		var customers []models.ZaloCustomer
		if err := tx.Where("zalo_user_id IN ? AND tenant_id = ?", req.ZaloUserIDs, tenantID).Find(&customers).Error; err == nil {
			var dbCustIDs []string
			for _, cust := range customers {
				dbCustIDs = append(dbCustIDs, cust.ID)
			}
			if len(dbCustIDs) > 0 {
				if err := tx.Where("group_id = ? AND zalo_customer_id IN ?", groupID, dbCustIDs).Delete(&models.CRMGroupCustomer{}).Error; err != nil {
					tx.Rollback()
					c.JSON(http.StatusInternalServerError, gin.H{"error": "failed_to_remove_customers"})
					return
				}
			}
		}
	}

	tx.Commit()

	c.JSON(http.StatusOK, gin.H{"message": "members_removed"})
}

// Zalo Customer Management

func ListZaloCustomers(c *gin.Context) {
	tenantID := middleware.GetTenantID(c)
	status := c.Query("status")

	var customers []models.ZaloCustomer
	query := db.DB.Where("tenant_id = ?", tenantID)
	if status != "" {
		query = query.Where("status = ?", status)
	}

	if err := query.Order("created_at DESC").Find(&customers).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed_to_list_customers"})
		return
	}

	// Try to sync Zalo profile details (avatar, correct name) for active customers if missing or placeholder
	var channel models.Channel
	if err := db.DB.Where("tenant_id = ? AND channel_type = ? AND is_active = ?", tenantID, "zalo_oa", true).First(&channel).Error; err == nil {
		cfg, _ := config.Load()
		if credBytes, err := pkg.Decrypt(channel.CredentialsEncrypted, cfg.EncryptionKey); err == nil {
			var zaloCreds channels.ZaloOACredentials
			if json.Unmarshal(credBytes, &zaloCreds) == nil {
				adapter := channels.NewZaloOAAdapter(zaloCreds)
				adapter.SetTokenRefreshCallback(func(newAccess, newRefresh string) {
					zaloCreds.AccessToken = newAccess
					zaloCreds.RefreshToken = newRefresh
					credsMap := map[string]interface{}{
						"app_id":        zaloCreds.AppID,
						"app_secret":    zaloCreds.AppSecret,
						"access_token":  newAccess,
						"refresh_token": newRefresh,
						"oa_id":         zaloCreds.OAId,
					}
					newCredJSON, _ := json.Marshal(credsMap)
					encrypted, _ := pkg.Encrypt(newCredJSON, cfg.EncryptionKey)
					db.DB.Model(&channel).Update("credentials_encrypted", encrypted)
				})

				for i := range customers {
					if customers[i].ZaloUserID != "" && (customers[i].Avatar == "" || customers[i].Name == "Marketing") {
						if profile, err := adapter.FetchUserProfile(c.Request.Context(), customers[i].ZaloUserID); err == nil {
							updatedFields := map[string]interface{}{}
							if profile.Avatar != "" && customers[i].Avatar != profile.Avatar {
								customers[i].Avatar = profile.Avatar
								updatedFields["avatar"] = profile.Avatar
							}
							if profile.DisplayName != "" && customers[i].Name != profile.DisplayName {
								customers[i].Name = profile.DisplayName
								updatedFields["name"] = profile.DisplayName
							}
							if len(updatedFields) > 0 {
								db.DB.Model(&customers[i]).Updates(updatedFields)
							}
						}
					}
				}
			}
		}
	}

	c.JSON(http.StatusOK, customers)
}

func InviteZaloCustomer(c *gin.Context) {
	tenantID := middleware.GetTenantID(c)

	var req struct {
		Name        string `json:"name" binding:"required"`
		PhoneNumber string `json:"phone_number"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request", "details": err.Error()})
		return
	}

	// Generate verification token (6 random uppercase alphanumeric characters)
	token := crmGenerateVerifyToken()

	now := time.Now()
	customer := models.ZaloCustomer{
		ID:          uuid.New().String(),
		TenantID:    tenantID,
		Name:        req.Name,
		PhoneNumber: req.PhoneNumber,
		VerifyToken: token,
		Status:      "pending_verify",
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	if err := db.DB.Create(&customer).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed_to_create_customer_invite"})
		return
	}

	c.JSON(http.StatusCreated, customer)
}

func ApproveZaloCustomer(c *gin.Context) {
	tenantID := middleware.GetTenantID(c)
	id := c.Param("id")

	var req struct {
		CustomerCode string   `json:"customer_code" binding:"required"`
		GroupIDs     []string `json:"group_ids"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request", "details": err.Error()})
		return
	}

	var customer models.ZaloCustomer
	if err := db.DB.Where("id = ? AND tenant_id = ?", id, tenantID).First(&customer).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "customer_not_found"})
		return
	}

	tx := db.DB.Begin()
	customer.Status = "approved"
	customer.CustomerCode = req.CustomerCode
	customer.UpdatedAt = time.Now()

	if err := tx.Save(&customer).Error; err != nil {
		tx.Rollback()
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed_to_approve_customer"})
		return
	}

	// Add to groups if provided
	for _, groupID := range req.GroupIDs {
		var grp models.CRMGroup
		if err := tx.Where("id = ? AND tenant_id = ?", groupID, tenantID).First(&grp).Error; err == nil {
			var count int64
			tx.Model(&models.CRMGroupCustomer{}).Where("group_id = ? AND zalo_customer_id = ?", groupID, customer.ID).Count(&count)
			if count == 0 {
				if err := tx.Create(&models.CRMGroupCustomer{GroupID: groupID, ZaloCustomerID: customer.ID}).Error; err != nil {
					tx.Rollback()
					c.JSON(http.StatusInternalServerError, gin.H{"error": "failed_to_link_customer_to_groups"})
					return
				}
			}
		}
	}

	tx.Commit()

	// Invite customer to the selected Zalo GMF groups if they have a linked Zalo account
	if customer.ZaloUserID != "" && len(req.GroupIDs) > 0 {
		for _, groupID := range req.GroupIDs {
			var grp models.CRMGroup
			if err := db.DB.Where("id = ? AND tenant_id = ?", groupID, tenantID).First(&grp).Error; err == nil && grp.ZaloGroupID != "" {
				var channel models.Channel
				var err error
				if grp.ChannelID != "" {
					err = db.DB.Where("id = ? AND tenant_id = ? AND channel_type = ? AND is_active = ?", grp.ChannelID, tenantID, "zalo_oa", true).First(&channel).Error
				} else {
					err = db.DB.Where("tenant_id = ? AND channel_type = ? AND is_active = ?", tenantID, "zalo_oa", true).First(&channel).Error
				}
				if err == nil {
					cfg, _ := config.Load()
					if credBytes, err := pkg.Decrypt(channel.CredentialsEncrypted, cfg.EncryptionKey); err == nil {
						var zaloCreds channels.ZaloOACredentials
						if err := json.Unmarshal(credBytes, &zaloCreds); err == nil {
							adapter := channels.NewZaloOAAdapter(zaloCreds)
							adapter.SetTokenRefreshCallback(func(newAccess, newRefresh string) {
								zaloCreds.AccessToken = newAccess
								zaloCreds.RefreshToken = newRefresh
								credsMap := map[string]interface{}{
									"app_id":        zaloCreds.AppID,
									"app_secret":    zaloCreds.AppSecret,
									"access_token":  newAccess,
									"refresh_token": newRefresh,
									"oa_id":         zaloCreds.OAId,
								}
								newCredJSON, _ := json.Marshal(credsMap)
								encrypted, _ := pkg.Encrypt(newCredJSON, cfg.EncryptionKey)
								db.DB.Model(&channel).Update("credentials_encrypted", encrypted)
							})

							if inviteErr := adapter.InviteGMFGroupMembers(c.Request.Context(), grp.ZaloGroupID, []string{customer.ZaloUserID}); inviteErr != nil {
								log.Printf("[crm] failed to invite customer %s to Zalo GMF group %s (%s): %v", customer.ID, grp.Name, grp.ZaloGroupID, inviteErr)
							} else {
								log.Printf("[crm] successfully invited customer %s to Zalo GMF group %s (%s)", customer.ID, grp.Name, grp.ZaloGroupID)
							}
						}
					}
				}
			}
		}
	}

	c.JSON(http.StatusOK, customer)
}

func DeleteZaloCustomer(c *gin.Context) {
	tenantID := middleware.GetTenantID(c)
	id := c.Param("id")

	tx := db.DB.Begin()
	// Delete group references
	if err := tx.Where("zalo_customer_id = ?", id).Delete(&models.CRMGroupCustomer{}).Error; err != nil {
		tx.Rollback()
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed_to_delete_group_links"})
		return
	}

	result := tx.Where("id = ? AND tenant_id = ?", id, tenantID).Delete(&models.ZaloCustomer{})
	if result.Error != nil {
		tx.Rollback()
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed_to_delete_customer"})
		return
	}
	if result.RowsAffected == 0 {
		tx.Rollback()
		c.JSON(http.StatusNotFound, gin.H{"error": "customer_not_found"})
		return
	}

	tx.Commit()
	c.JSON(http.StatusOK, gin.H{"message": "deleted"})
}

// PostgreSQL Profiles Dropdown

func ListCustomerCodes(cfg *config.Config) gin.HandlerFunc {
	return func(c *gin.Context) {
		codes, err := db.GetCustomerCodes(cfg.PostgresURL)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed_to_fetch_postgres_customer_codes", "details": err.Error()})
			return
		}

		c.JSON(http.StatusOK, codes)
	}
}

func ListCloudifyCustomers(cfg *config.Config) gin.HandlerFunc {
	return func(c *gin.Context) {
		tenantID := middleware.GetTenantID(c)

		// 1. Fetch Cloudify customer profiles from Postgres
		profiles, err := db.GetCloudifyCustomerProfiles(cfg.PostgresURL)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed_to_fetch_cloudify_customers", "details": err.Error()})
			return
		}

		// 2. Fetch all local ZaloCustomer records from MySQL for this tenant
		var localCustomers []models.ZaloCustomer
		if err := db.DB.Where("tenant_id = ?", tenantID).Find(&localCustomers).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed_to_fetch_local_customers", "details": err.Error()})
			return
		}

		// Map local customers by customer_code
		localMap := make(map[string]models.ZaloCustomer)
		for _, lc := range localCustomers {
			if lc.CustomerCode != "" {
				localMap[lc.CustomerCode] = lc
			}
		}

		// 3. Merge profiles with local database info
		type MergedCustomerProfile struct {
			CustomerCode  string `json:"customer_code"`
			Name          string `json:"name"`
			Address       string `json:"address"`
			Region        string `json:"region"`
			CloudifyPhone string `json:"cloudify_phone"`
			CqaPhone      string `json:"cqa_phone"`
			ZaloUserID    string `json:"zalo_user_id"`
			Status        string `json:"status"`
			VerifyToken   string `json:"verify_token"`
			Avatar        string `json:"avatar"`
			CustomerID    string `json:"customer_id"`
		}

		var response []MergedCustomerProfile
		for _, p := range profiles {
			merged := MergedCustomerProfile{
				CustomerCode:  p.CustomerCode,
				Name:          p.Name,
				Address:       p.Address,
				Region:        p.Region,
				CloudifyPhone: p.PhoneNumber,
			}

			// If mapped locally
			if lc, found := localMap[p.CustomerCode]; found {
				merged.CqaPhone = lc.PhoneNumber
				merged.ZaloUserID = lc.ZaloUserID
				merged.Status = lc.Status
				merged.VerifyToken = lc.VerifyToken
				merged.Avatar = lc.Avatar
				merged.CustomerID = lc.ID
			}

			response = append(response, merged)
		}

		c.JSON(http.StatusOK, response)
	}
}

func AssignCloudifyCustomerPhone(cfg *config.Config) gin.HandlerFunc {
	return func(c *gin.Context) {
		tenantID := middleware.GetTenantID(c)

		var req struct {
			CustomerCode string `json:"customer_code" binding:"required"`
			PhoneNumber  string `json:"phone_number" binding:"required"`
			Name         string `json:"name" binding:"required"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request", "details": err.Error()})
			return
		}

		// Clean phone number (strip whitespace)
		req.PhoneNumber = strings.TrimSpace(req.PhoneNumber)

		var customer models.ZaloCustomer
		err := db.DB.Where("tenant_id = ? AND customer_code = ?", tenantID, req.CustomerCode).First(&customer).Error

		now := time.Now()
		if err != nil {
			// Not found - create new pre-approved ZaloCustomer record
			token := crmGenerateVerifyToken()
			customer = models.ZaloCustomer{
				ID:           uuid.New().String(),
				TenantID:     tenantID,
				CustomerCode: req.CustomerCode,
				Name:         req.Name,
				PhoneNumber:  req.PhoneNumber,
				VerifyToken:  token,
				Status:       "approved", // Pre-approved so it's ready to be added to groups and shown in dropdowns
				CreatedAt:    now,
				UpdatedAt:    now,
			}
			if err := db.DB.Create(&customer).Error; err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "failed_to_create_customer_mapping", "details": err.Error()})
				return
			}
		} else {
			// Found - update phone number, name, and ensure status is approved
			customer.PhoneNumber = req.PhoneNumber
			customer.Name = req.Name
			customer.Status = "approved"
			customer.UpdatedAt = now
			if err := db.DB.Save(&customer).Error; err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "failed_to_update_customer_mapping", "details": err.Error()})
				return
			}
		}

		c.JSON(http.StatusOK, customer)
	}
}

func crmGenerateVerifyToken() string {
	const charset = "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	rand.Seed(time.Now().UnixNano())
	b := make([]byte, 6)
	for i := range b {
		b[i] = charset[rand.Intn(len(charset))]
	}
	return string(b)
}

func normalizeZaloWhitelistMemberUserIDs(staff []models.ZaloWhitelist, limit int) []string {
	if limit <= 0 {
		return []string{}
	}

	seen := make(map[string]bool, len(staff))
	members := make([]string, 0, len(staff))
	for _, item := range staff {
		userID := strings.TrimSpace(item.ZaloUserID)
		if userID == "" || seen[userID] {
			continue
		}
		seen[userID] = true
		members = append(members, userID)
		if len(members) >= limit {
			break
		}
	}
	return members
}

func ListGMFPackages(c *gin.Context) {
	tenantID := middleware.GetTenantID(c)
	channelID := c.Query("channel_id")

	var channel models.Channel
	var err error
	if channelID != "" {
		err = db.DB.Where("id = ? AND tenant_id = ? AND channel_type = ? AND is_active = ?", channelID, tenantID, "zalo_oa", true).First(&channel).Error
	} else {
		err = db.DB.Where("tenant_id = ? AND channel_type = ? AND is_active = ?", tenantID, "zalo_oa", true).First(&channel).Error
	}
	if err != nil {
		c.JSON(http.StatusOK, []interface{}{}) // Return empty if no Zalo OA
		return
	}

	cfg, _ := config.Load()
	credBytes, err := pkg.Decrypt(channel.CredentialsEncrypted, cfg.EncryptionKey)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed_to_decrypt_channel_credentials"})
		return
	}
	var zaloCreds channels.ZaloOACredentials
	if err := json.Unmarshal(credBytes, &zaloCreds); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed_to_parse_channel_credentials"})
		return
	}

	adapter := channels.NewZaloOAAdapter(zaloCreds)
	adapter.SetTokenRefreshCallback(func(newAccess, newRefresh string) {
		zaloCreds.AccessToken = newAccess
		zaloCreds.RefreshToken = newRefresh
		credsMap := map[string]interface{}{
			"app_id":        zaloCreds.AppID,
			"app_secret":    zaloCreds.AppSecret,
			"access_token":  newAccess,
			"refresh_token": newRefresh,
			"oa_id":         zaloCreds.OAId,
		}
		newCredJSON, _ := json.Marshal(credsMap)
		encrypted, _ := pkg.Encrypt(newCredJSON, cfg.EncryptionKey)
		db.DB.Model(&channel).Update("credentials_encrypted", encrypted)
	})

	assets, err := adapter.GetGMFQuota(c.Request.Context())
	if err != nil {
		envAsset := os.Getenv("ZALO_OA_GMF_ASSET_ID")
		if envAsset != "" {
			c.JSON(http.StatusOK, []channels.GMFAssetInfo{
				{
					AssetID:    envAsset,
					AssetType:  "Mặc định (ENV)",
					TotalGroup: 999,
					UsedGroup:  0,
				},
			})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed_to_fetch_gmf_packages", "details": err.Error()})
		return
	}

	c.JSON(http.StatusOK, assets)
}

func InviteGMFGroupCustomer(c *gin.Context) {
	tenantID := middleware.GetTenantID(c)
	groupID := c.Param("id")

	var req struct {
		CustomerID string `json:"customer_id" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request"})
		return
	}

	// 1. Fetch group
	var group models.CRMGroup
	if err := db.DB.Where("id = ? AND tenant_id = ?", groupID, tenantID).First(&group).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "group_not_found"})
		return
	}
	if group.ZaloGroupLink == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "group_has_no_zalo_link"})
		return
	}

	// 2. Fetch customer
	var customer models.ZaloCustomer
	if err := db.DB.Where("id = ? AND tenant_id = ?", req.CustomerID, tenantID).First(&customer).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "customer_not_found"})
		return
	}
	if customer.ZaloUserID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "customer_not_linked_zalo"})
		return
	}

	// 3. Send message via Zalo OA
	var channel models.Channel
	var err error
	if group.ChannelID != "" {
		err = db.DB.Where("id = ? AND tenant_id = ? AND channel_type = ? AND is_active = ?", group.ChannelID, tenantID, "zalo_oa", true).First(&channel).Error
	} else {
		err = db.DB.Where("tenant_id = ? AND channel_type = ? AND is_active = ?", tenantID, "zalo_oa", true).First(&channel).Error
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "zalo_oa_channel_not_found"})
		return
	}

	cfg, _ := config.Load()
	credBytes, err := pkg.Decrypt(channel.CredentialsEncrypted, cfg.EncryptionKey)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed_to_decrypt_channel_credentials"})
		return
	}
	var zaloCreds channels.ZaloOACredentials
	if err := json.Unmarshal(credBytes, &zaloCreds); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed_to_parse_channel_credentials"})
		return
	}

	adapter := channels.NewZaloOAAdapter(zaloCreds)
	adapter.SetTokenRefreshCallback(func(newAccess, newRefresh string) {
		zaloCreds.AccessToken = newAccess
		zaloCreds.RefreshToken = newRefresh
		credsMap := map[string]interface{}{
			"app_id":        zaloCreds.AppID,
			"app_secret":    zaloCreds.AppSecret,
			"access_token":  newAccess,
			"refresh_token": newRefresh,
			"oa_id":         zaloCreds.OAId,
		}
		newCredJSON, _ := json.Marshal(credsMap)
		encrypted, _ := pkg.Encrypt(newCredJSON, cfg.EncryptionKey)
		db.DB.Model(&channel).Update("credentials_encrypted", encrypted)
	})

	inviteMsg := fmt.Sprintf("Mời quý khách tham gia nhóm chat hỗ trợ: %s\nLiên kết tham gia: %s", group.Name, group.ZaloGroupLink)
	if err := adapter.SendMessage(c.Request.Context(), customer.ZaloUserID, inviteMsg); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed_to_send_invite_message", "details": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "invite_sent"})
}

func ListGroupMembers(c *gin.Context) {
	tenantID := middleware.GetTenantID(c)
	groupID := c.Param("id")

	// 1. Fetch group
	var group models.CRMGroup
	if err := db.DB.Where("id = ? AND tenant_id = ?", groupID, tenantID).First(&group).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "group_not_found"})
		return
	}

	if group.ZaloGroupID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "group_not_linked_to_zalo"})
		return
	}

	// 2. Fetch credentials
	var channel models.Channel
	var err error
	if group.ChannelID != "" {
		err = db.DB.Where("id = ? AND tenant_id = ? AND channel_type = ? AND is_active = ?", group.ChannelID, tenantID, "zalo_oa", true).First(&channel).Error
	} else {
		err = db.DB.Where("tenant_id = ? AND channel_type = ? AND is_active = ?", tenantID, "zalo_oa", true).First(&channel).Error
	}
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "zalo_oa_not_configured"})
		return
	}

	cfg, _ := config.Load()
	credBytes, err := pkg.Decrypt(channel.CredentialsEncrypted, cfg.EncryptionKey)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed_to_decrypt_channel_credentials"})
		return
	}
	var zaloCreds channels.ZaloOACredentials
	if err := json.Unmarshal(credBytes, &zaloCreds); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed_to_parse_channel_credentials"})
		return
	}

	adapter := channels.NewZaloOAAdapter(zaloCreds)
	adapter.SetTokenRefreshCallback(func(newAccess, newRefresh string) {
		zaloCreds.AccessToken = newAccess
		zaloCreds.RefreshToken = newRefresh
		credsMap := map[string]interface{}{
			"app_id":        zaloCreds.AppID,
			"app_secret":    zaloCreds.AppSecret,
			"access_token":  newAccess,
			"refresh_token": newRefresh,
			"oa_id":         zaloCreds.OAId,
		}
		newCredJSON, _ := json.Marshal(credsMap)
		encrypted, _ := pkg.Encrypt(newCredJSON, cfg.EncryptionKey)
		db.DB.Model(&channel).Update("credentials_encrypted", encrypted)
	})

	// Parse query params for pagination
	offsetStr := c.DefaultQuery("offset", "0")
	countStr := c.DefaultQuery("count", "50")
	offset, _ := strconv.Atoi(offsetStr)
	count, _ := strconv.Atoi(countStr)
	if count > 50 {
		count = 50
	}

	// 3. Fetch members from Zalo GMF
	zaloMembers, err := adapter.GetGMFGroupMembers(c.Request.Context(), group.ZaloGroupID, offset, count)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed_to_fetch_zalo_members", "details": err.Error()})
		return
	}

	// 4. Fetch all active staff in the tenant to identify who is an employee
	var staff []models.ZaloWhitelist
	if err := db.DB.Where("tenant_id = ? AND status = ? AND zalo_user_id <> ''", tenantID, "active").Find(&staff).Error; err != nil {
		log.Printf("[crm] failed to fetch whitelist: %v", err)
	}

	staffMap := make(map[string]bool)
	for _, s := range staff {
		staffMap[strings.TrimSpace(s.ZaloUserID)] = true
	}

	// Fetch all approved customers in this tenant to match their customer_code
	var customers []models.ZaloCustomer
	if err := db.DB.Where("tenant_id = ? AND status = ? AND zalo_user_id <> ''", tenantID, "approved").Find(&customers).Error; err != nil {
		log.Printf("[crm] failed to fetch customers: %v", err)
	}

	customerMap := make(map[string]models.ZaloCustomer)
	for _, cust := range customers {
		customerMap[strings.TrimSpace(cust.ZaloUserID)] = cust
	}

	// Categorize members
	var responseEmployees []interface{}
	var responseCustomers []interface{}

	for _, member := range zaloMembers {
		// If it's the OA itself (has oa_id), categorize it as employee/admin.
		// Since OA is not a customer, we should exclude it from customers. Let's classify it as employee.
		if member.OAID != "" {
			responseEmployees = append(responseEmployees, gin.H{
				"oa_id":        member.OAID,
				"name":         member.Name,
				"avatar":       member.Avatar,
				"is_oa":        true,
				"is_employee":  true,
			})
			continue
		}

		uID := strings.TrimSpace(member.UserID)
		if uID == "" {
			continue
		}

		// Check if it is an employee
		if staffMap[uID] {
			responseEmployees = append(responseEmployees, gin.H{
				"zalo_user_id": uID,
				"name":         member.Name,
				"avatar":       member.Avatar,
				"is_employee":  true,
			})
		} else {
			// Check if we have this customer in database to append customer_code
			customerCode := ""
			dbCustomerID := ""
			if cust, ok := customerMap[uID]; ok {
				customerCode = cust.CustomerCode
				dbCustomerID = cust.ID
			}

			responseCustomers = append(responseCustomers, gin.H{
				"zalo_user_id":  uID,
				"id":            dbCustomerID, // use DB customer ID so actions like remove still work
				"name":          member.Name,
				"avatar":        member.Avatar,
				"customer_code": customerCode,
				"is_employee":   false,
			})
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"employees": responseEmployees,
		"customers": responseCustomers,
	})
}

func ListGroupPendingInvites(c *gin.Context) {
	tenantID := middleware.GetTenantID(c)
	groupID := c.Param("id")

	// 1. Fetch group
	var group models.CRMGroup
	if err := db.DB.Where("id = ? AND tenant_id = ?", groupID, tenantID).First(&group).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "group_not_found"})
		return
	}

	if group.ZaloGroupID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "group_not_linked_to_zalo"})
		return
	}

	// 2. Fetch credentials
	var channel models.Channel
	var err error
	if group.ChannelID != "" {
		err = db.DB.Where("id = ? AND tenant_id = ? AND channel_type = ? AND is_active = ?", group.ChannelID, tenantID, "zalo_oa", true).First(&channel).Error
	} else {
		err = db.DB.Where("tenant_id = ? AND channel_type = ? AND is_active = ?", tenantID, "zalo_oa", true).First(&channel).Error
	}
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "zalo_oa_not_configured"})
		return
	}

	cfg, _ := config.Load()
	credBytes, err := pkg.Decrypt(channel.CredentialsEncrypted, cfg.EncryptionKey)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed_to_decrypt_channel_credentials"})
		return
	}
	var zaloCreds channels.ZaloOACredentials
	if err := json.Unmarshal(credBytes, &zaloCreds); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed_to_parse_channel_credentials"})
		return
	}

	adapter := channels.NewZaloOAAdapter(zaloCreds)
	adapter.SetTokenRefreshCallback(func(newAccess, newRefresh string) {
		zaloCreds.AccessToken = newAccess
		zaloCreds.RefreshToken = newRefresh
		credsMap := map[string]interface{}{
			"app_id":        zaloCreds.AppID,
			"app_secret":    zaloCreds.AppSecret,
			"access_token":  newAccess,
			"refresh_token": newRefresh,
			"oa_id":         zaloCreds.OAId,
		}
		newCredJSON, _ := json.Marshal(credsMap)
		encrypted, _ := pkg.Encrypt(newCredJSON, cfg.EncryptionKey)
		db.DB.Model(&channel).Update("credentials_encrypted", encrypted)
	})

	// Parse query params for pagination
	offsetStr := c.DefaultQuery("offset", "0")
	countStr := c.DefaultQuery("count", "50")
	offset, _ := strconv.Atoi(offsetStr)
	count, _ := strconv.Atoi(countStr)
	if count > 50 {
		count = 50
	}

	// Fetch pending invites
	pendingInvites, err := adapter.GetGMFGroupPendingInvites(c.Request.Context(), group.ZaloGroupID, offset, count)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed_to_fetch_pending_invites", "details": err.Error()})
		return
	}

	c.JSON(http.StatusOK, pendingInvites)
}

func AcceptGroupPendingInvite(c *gin.Context) {
	tenantID := middleware.GetTenantID(c)
	groupID := c.Param("id")

	var req struct {
		MemberUserIDs []string `json:"member_user_ids" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request", "details": err.Error()})
		return
	}

	// 1. Fetch group
	var group models.CRMGroup
	if err := db.DB.Where("id = ? AND tenant_id = ?", groupID, tenantID).First(&group).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "group_not_found"})
		return
	}

	if group.ZaloGroupID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "group_not_linked_to_zalo"})
		return
	}

	// 2. Fetch credentials
	var channel models.Channel
	var err error
	if group.ChannelID != "" {
		err = db.DB.Where("id = ? AND tenant_id = ? AND channel_type = ? AND is_active = ?", group.ChannelID, tenantID, "zalo_oa", true).First(&channel).Error
	} else {
		err = db.DB.Where("tenant_id = ? AND channel_type = ? AND is_active = ?", tenantID, "zalo_oa", true).First(&channel).Error
	}
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "zalo_oa_not_configured"})
		return
	}

	cfg, _ := config.Load()
	credBytes, err := pkg.Decrypt(channel.CredentialsEncrypted, cfg.EncryptionKey)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed_to_decrypt_channel_credentials"})
		return
	}
	var zaloCreds channels.ZaloOACredentials
	if err := json.Unmarshal(credBytes, &zaloCreds); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed_to_parse_channel_credentials"})
		return
	}

	adapter := channels.NewZaloOAAdapter(zaloCreds)
	adapter.SetTokenRefreshCallback(func(newAccess, newRefresh string) {
		zaloCreds.AccessToken = newAccess
		zaloCreds.RefreshToken = newRefresh
		credsMap := map[string]interface{}{
			"app_id":        zaloCreds.AppID,
			"app_secret":    zaloCreds.AppSecret,
			"access_token":  newAccess,
			"refresh_token": newRefresh,
			"oa_id":         zaloCreds.OAId,
		}
		newCredJSON, _ := json.Marshal(credsMap)
		encrypted, _ := pkg.Encrypt(newCredJSON, cfg.EncryptionKey)
		db.DB.Model(&channel).Update("credentials_encrypted", encrypted)
	})

	if err := adapter.AcceptGMFGroupPendingInvites(c.Request.Context(), group.ZaloGroupID, req.MemberUserIDs); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed_to_accept_invite", "details": err.Error()})
		return
	}

	// Link recognized Zalo users as CRM group customers
	var customers []models.ZaloCustomer
	if err := db.DB.Where("zalo_user_id IN ? AND tenant_id = ?", req.MemberUserIDs, tenantID).Find(&customers).Error; err == nil {
		for _, cust := range customers {
			var count int64
			db.DB.Model(&models.CRMGroupCustomer{}).Where("group_id = ? AND zalo_customer_id = ?", groupID, cust.ID).Count(&count)
			if count == 0 {
				db.DB.Create(&models.CRMGroupCustomer{GroupID: groupID, ZaloCustomerID: cust.ID})
			}
			if group.CustomerCode != "" {
				db.DB.Model(&cust).Update("customer_code", group.CustomerCode)
			}
		}
	}

	c.JSON(http.StatusOK, gin.H{"message": "invite_accepted"})
}

func RejectGroupPendingInvite(c *gin.Context) {
	tenantID := middleware.GetTenantID(c)
	groupID := c.Param("id")

	var req struct {
		MemberUserIDs []string `json:"member_user_ids" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request", "details": err.Error()})
		return
	}

	// 1. Fetch group
	var group models.CRMGroup
	if err := db.DB.Where("id = ? AND tenant_id = ?", groupID, tenantID).First(&group).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "group_not_found"})
		return
	}

	if group.ZaloGroupID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "group_not_linked_to_zalo"})
		return
	}

	// 2. Fetch credentials
	var channel models.Channel
	var err error
	if group.ChannelID != "" {
		err = db.DB.Where("id = ? AND tenant_id = ? AND channel_type = ? AND is_active = ?", group.ChannelID, tenantID, "zalo_oa", true).First(&channel).Error
	} else {
		err = db.DB.Where("tenant_id = ? AND channel_type = ? AND is_active = ?", tenantID, "zalo_oa", true).First(&channel).Error
	}
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "zalo_oa_not_configured"})
		return
	}

	cfg, _ := config.Load()
	credBytes, err := pkg.Decrypt(channel.CredentialsEncrypted, cfg.EncryptionKey)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed_to_decrypt_channel_credentials"})
		return
	}
	var zaloCreds channels.ZaloOACredentials
	if err := json.Unmarshal(credBytes, &zaloCreds); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed_to_parse_channel_credentials"})
		return
	}

	adapter := channels.NewZaloOAAdapter(zaloCreds)
	adapter.SetTokenRefreshCallback(func(newAccess, newRefresh string) {
		zaloCreds.AccessToken = newAccess
		zaloCreds.RefreshToken = newRefresh
		credsMap := map[string]interface{}{
			"app_id":        zaloCreds.AppID,
			"app_secret":    zaloCreds.AppSecret,
			"access_token":  newAccess,
			"refresh_token": newRefresh,
			"oa_id":         zaloCreds.OAId,
		}
		newCredJSON, _ := json.Marshal(credsMap)
		encrypted, _ := pkg.Encrypt(newCredJSON, cfg.EncryptionKey)
		db.DB.Model(&channel).Update("credentials_encrypted", encrypted)
	})

	if err := adapter.RejectGMFGroupPendingInvites(c.Request.Context(), group.ZaloGroupID, req.MemberUserIDs); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed_to_reject_invite", "details": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "invite_rejected"})
}

// GetListTenNhomVthh - GET /crm/list-ten-nhom-vthh
// Returns the list of unique product groups from the product cache.
// Returns HTTP 400 Bad Request with error: "product_cache_not_synced" if cache not found.
func GetListTenNhomVthh(c *gin.Context) {
	tenantID := middleware.GetTenantID(c)
	refresh := c.Query("refresh") == "true"

	if !refresh {
		var s models.AppSetting
		err := db.DB.Where("tenant_id = ? AND setting_key = 'list_ten_nhom_vthh'", tenantID).First(&s).Error
		if err == nil && s.ValuePlain != "" {
			var groups []string
			if err := json.Unmarshal([]byte(s.ValuePlain), &groups); err == nil {
				c.JSON(http.StatusOK, groups)
				return
			}
			// Fallback split by comma if not valid JSON
			parts := strings.Split(s.ValuePlain, ",")
			var cleanedParts []string
			for _, p := range parts {
				trimmed := strings.TrimSpace(p)
				if trimmed != "" {
					cleanedParts = append(cleanedParts, trimmed)
				}
			}
			if len(cleanedParts) > 0 {
				c.JSON(http.StatusOK, cleanedParts)
				return
			}
		}
	}

	// Dynamic fallback: query unique groups directly from Astra DB product cache
	uniqueGroups, err := fetchUniqueGroupsFromAstraDB(c.Request.Context(), tenantID)
	if err == nil && len(uniqueGroups) > 0 {
		// Save to app_settings cache for faster future lookups
		groupJSON, errMarshal := json.Marshal(uniqueGroups)
		if errMarshal == nil {
			var setting models.AppSetting
			errFind := db.DB.Where("tenant_id = ? AND setting_key = 'list_ten_nhom_vthh'", tenantID).First(&setting).Error
			if errFind == nil {
				db.DB.Model(&setting).Updates(map[string]interface{}{
					"value_plain": string(groupJSON),
					"updated_at":  time.Now(),
				})
			} else {
				db.DB.Create(&models.AppSetting{
					ID:         pkg.NewUUID(),
					TenantID:   tenantID,
					SettingKey: "list_ten_nhom_vthh",
					ValuePlain: string(groupJSON),
					CreatedAt:  time.Now(),
					UpdatedAt:  time.Now(),
				})
			}
		}

		c.JSON(http.StatusOK, uniqueGroups)
		return
	}

	c.JSON(http.StatusBadRequest, gin.H{
		"error":   "product_cache_not_synced",
		"message": "Danh mục sản phẩm chưa được đồng bộ. Vui lòng chạy Job đồng bộ danh mục sản phẩm trước.",
	})
}

// fetchUniqueGroupsFromAstraDB queries unique product groups cached in Astra DB.
func fetchUniqueGroupsFromAstraDB(ctx context.Context, tenantID string) ([]string, error) {
	// 1. Load Astra DB credentials
	cfg, _ := config.Load()
	apiEndpoint := cfg.AstraDBAPIEndpoint
	token := cfg.AstraDBToken

	keyspace := "cache_product"
	if cfg.AstraDBKeyspace != "" {
		keyspace = cfg.AstraDBKeyspace
	}

	collection := "erp_product_bbi"
	if cfg.AstraDBProductCollection != "" {
		collection = cfg.AstraDBProductCollection
	}

	var endpointSetting models.AppSetting
	if err := db.DB.Where("tenant_id = ? AND setting_key = ?", tenantID, "astradb_api_endpoint").First(&endpointSetting).Error; err == nil && endpointSetting.ValuePlain != "" {
		apiEndpoint = endpointSetting.ValuePlain
	}
	var tokenSetting models.AppSetting
	if err := db.DB.Where("tenant_id = ? AND setting_key = ?", tenantID, "astradb_token").First(&tokenSetting).Error; err == nil {
		if len(tokenSetting.ValueEncrypted) > 0 {
			if decrypted, err := pkg.Decrypt(tokenSetting.ValueEncrypted, cfg.EncryptionKey); err == nil {
				token = string(decrypted)
			}
		} else if tokenSetting.ValuePlain != "" {
			token = tokenSetting.ValuePlain
		}
	}
	var keyspaceSetting models.AppSetting
	if err := db.DB.Where("tenant_id = ? AND setting_key = ?", tenantID, "astradb_keyspace").First(&keyspaceSetting).Error; err == nil && keyspaceSetting.ValuePlain != "" {
		keyspace = keyspaceSetting.ValuePlain
	}
	var collectionSetting models.AppSetting
	if err := db.DB.Where("tenant_id = ? AND setting_key = ?", tenantID, "astradb_product_collection").First(&collectionSetting).Error; err == nil && collectionSetting.ValuePlain != "" {
		collection = collectionSetting.ValuePlain
	}

	if apiEndpoint == "" || token == "" {
		return nil, fmt.Errorf("Astra DB is not configured")
	}

	url := fmt.Sprintf("%s/api/json/v1/%s/%s", apiEndpoint, keyspace, collection)

	groupMap := make(map[string]bool)
	var uniqueGroups []string
	pageState := ""

	// Loop to fetch all pages (limit to 100 pages / 100k items safety cap)
	for page := 0; page < 100; page++ {
		options := map[string]interface{}{
			"limit": 1000,
		}
		if pageState != "" {
			options["pageState"] = pageState
		}

		payload := map[string]interface{}{
			"find": map[string]interface{}{
				"filter": map[string]interface{}{},
				"projection": map[string]interface{}{
					"LIST_TEN_NHOM_VTHH": 1,
					"list_ten_nhom_vthh": 1,
				},
				"options": options,
			},
		}

		bodyBytes, err := json.Marshal(payload)
		if err != nil {
			return nil, fmt.Errorf("marshal payload: %w", err)
		}

		req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(bodyBytes))
		if err != nil {
			return nil, fmt.Errorf("create request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Token", token)

		client := &http.Client{Timeout: 15 * time.Second}
		resp, err := client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("execute HTTP post: %w", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
			return nil, fmt.Errorf("HTTP status %d", resp.StatusCode)
		}

		var astraResp struct {
			Data struct {
				Documents     []map[string]interface{} `json:"documents"`
				NextPageState string                   `json:"nextPageState"`
			} `json:"data"`
			Errors []struct {
				Message string `json:"message"`
			} `json:"errors"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&astraResp); err != nil {
			return nil, fmt.Errorf("decode response: %w", err)
		}

		if len(astraResp.Errors) > 0 {
			return nil, fmt.Errorf("Astra DB error: %s", astraResp.Errors[0].Message)
		}

		// Process this page of documents
		for _, doc := range astraResp.Data.Documents {
			val, ok := doc["LIST_TEN_NHOM_VTHH"]
			if !ok || val == nil {
				val, ok = doc["list_ten_nhom_vthh"]
			}
			if ok && val != nil {
				var listTenNhomVthh string
				switch v := val.(type) {
				case string:
					listTenNhomVthh = v
				case []interface{}:
					if len(v) > 1 {
						if s, ok := v[1].(string); ok {
							listTenNhomVthh = s
						}
					} else if len(v) > 0 {
						listTenNhomVthh = fmt.Sprintf("%v", v[0])
					}
				default:
					listTenNhomVthh = fmt.Sprintf("%v", val)
				}

				if listTenNhomVthh != "" {
					parts := strings.Split(listTenNhomVthh, ",")
					for _, part := range parts {
						trimmed := strings.TrimSpace(part)
						if trimmed != "" && !groupMap[trimmed] {
							groupMap[trimmed] = true
							uniqueGroups = append(uniqueGroups, trimmed)
						}
					}
				}
			}
		}

		pageState = astraResp.Data.NextPageState
		if pageState == "" {
			break
		}
	}

	return uniqueGroups, nil
}

