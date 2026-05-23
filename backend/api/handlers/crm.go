package handlers

import (
	"math/rand"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/vietbui/chat-quality-agent/api/middleware"
	"github.com/vietbui/chat-quality-agent/config"
	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/db/models"
)

// CRM Group CRUD

func ListCRMGroups(c *gin.Context) {
	tenantID := middleware.GetTenantID(c)

	var groups []models.CRMGroup
	if err := db.DB.Preload("Employees").Preload("Customers").Where("tenant_id = ?", tenantID).Find(&groups).Error; err != nil {
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
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request", "details": err.Error()})
		return
	}

	now := time.Now()
	group := models.CRMGroup{
		ID:          uuid.New().String(),
		TenantID:    tenantID,
		Name:        req.Name,
		Description: req.Description,
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	if err := db.DB.Create(&group).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed_to_create_group"})
		return
	}

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
	result := tx.Where("id = ? AND tenant_id = ?", groupID, tenantID).Delete(&models.CRMGroup{})
	if result.Error != nil {
		tx.Rollback()
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed_to_delete_group"})
		return
	}
	if result.RowsAffected == 0 {
		tx.Rollback()
		c.JSON(http.StatusNotFound, gin.H{"error": "group_not_found"})
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
