package handlers

import (
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"os"
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
		Name        string `json:"name" binding:"required"`
		Description string `json:"description"`
		AssetID     string `json:"asset_id"`
		ChannelID   string `json:"channel_id"`
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

	// 3. Find at least one Zalo User ID as initial member
	var initialMembers []string
	var activeStaff models.ZaloWhitelist
	if err := db.DB.Where("tenant_id = ? AND status = ?", tenantID, "active").First(&activeStaff).Error; err == nil && activeStaff.ZaloUserID != "" {
		initialMembers = append(initialMembers, activeStaff.ZaloUserID)
	}
	if len(initialMembers) == 0 {
		var approvedCust models.ZaloCustomer
		if err := db.DB.Where("tenant_id = ? AND status = ?", tenantID, "approved").First(&approvedCust).Error; err == nil && approvedCust.ZaloUserID != "" {
			initialMembers = append(initialMembers, approvedCust.ZaloUserID)
		}
	}

	if len(initialMembers) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "zalo_member_required",
			"details": "Để tạo nhóm GMF trên Zalo, cần ít nhất 1 nhân viên hoặc khách hàng đã liên kết Zalo trong hệ thống.",
		})
		return
	}

	// 4. Create GMF Group on Zalo
	zGroupID, zGroupLink, err := adapter.CreateGMFGroup(c.Request.Context(), req.Name, req.Description, assetID, initialMembers)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "zalo_create_group_failed", "details": err.Error()})
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
		CreatedAt:     now,
		UpdatedAt:     now,
	}

	tx := db.DB.Begin()
	if err := tx.Create(&group).Error; err != nil {
		tx.Rollback()
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed_to_create_group"})
		return
	}

	// Link initial customer if any
	for _, mID := range initialMembers {
		var cust models.ZaloCustomer
		if tx.Where("tenant_id = ? AND zalo_user_id = ?", tenantID, mID).First(&cust).Error == nil {
			tx.Create(&models.CRMGroupCustomer{GroupID: group.ID, ZaloCustomerID: cust.ID})
		}
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
		Name        string `json:"name" binding:"required"`
		Description string `json:"description"`
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
					// Disband group, ignore error to avoid blocking local delete
					_ = adapter.DeleteGMFGroup(c.Request.Context(), group.ZaloGroupID)
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

	tx.Commit()

	// Remove customers from Zalo GMF group chat if group has ZaloGroupID
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

						if removeErr := adapter.RemoveGMFGroupMembers(c.Request.Context(), group.ZaloGroupID, zaloUserIDs); removeErr != nil {
							log.Printf("[crm] failed to remove customers %v from Zalo GMF group %s (%s): %v", zaloUserIDs, group.Name, group.ZaloGroupID, removeErr)
						} else {
							log.Printf("[crm] successfully removed customers %v from Zalo GMF group %s (%s)", zaloUserIDs, group.Name, group.ZaloGroupID)
						}
					}
				}
			}
		}
	}

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

func crmGenerateVerifyToken() string {
	const charset = "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	rand.Seed(time.Now().UnixNano())
	b := make([]byte, 6)
	for i := range b {
		b[i] = charset[rand.Intn(len(charset))]
	}
	return string(b)
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
