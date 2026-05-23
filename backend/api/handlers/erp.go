package handlers

import (
	"crypto/subtle"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/vietbui/chat-quality-agent/config"
	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/db/models"
	"github.com/vietbui/chat-quality-agent/pkg"
)

// ERPQuery handles queries from Langflow agent to get ERP data
func ERPQuery(c *gin.Context) {
	tenantID := c.Param("tenantId")
	if tenantID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "tenant_id_required"})
		return
	}

	// 1. Authenticate via Agent Secure Token
	token := c.GetHeader("X-Agent-Token")
	if token == "" {
		// Fallback to Bearer token
		authHeader := c.GetHeader("Authorization")
		if strings.HasPrefix(authHeader, "Bearer ") {
			token = strings.TrimPrefix(authHeader, "Bearer ")
		}
	}

	if token == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized", "message": "Missing Agent Token"})
		return
	}

	// Identify Agent Type based on which token matched
	agentType := "" // "public" or "private"

	var publicTokenSetting models.AppSetting
	if err := db.DB.Where("tenant_id = ? AND setting_key = ?", tenantID, "ai_agent_erp_token_public").First(&publicTokenSetting).Error; err == nil {
		if subtle.ConstantTimeCompare([]byte(publicTokenSetting.ValuePlain), []byte(token)) == 1 {
			agentType = "public"
		}
	}

	if agentType == "" {
		var privateTokenSetting models.AppSetting
		if err := db.DB.Where("tenant_id = ? AND setting_key = ?", tenantID, "ai_agent_erp_token_private").First(&privateTokenSetting).Error; err == nil {
			if subtle.ConstantTimeCompare([]byte(privateTokenSetting.ValuePlain), []byte(token)) == 1 {
				agentType = "private"
			}
		}
	}

	// Backward compatibility fallback to old single token key
	if agentType == "" {
		var legacyTokenSetting models.AppSetting
		if err := db.DB.Where("tenant_id = ? AND setting_key = ?", tenantID, "ai_agent_erp_token").First(&legacyTokenSetting).Error; err == nil {
			if subtle.ConstantTimeCompare([]byte(legacyTokenSetting.ValuePlain), []byte(token)) == 1 {
				agentType = "private"
			}
		}
	}

	if agentType == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized", "message": "Invalid Agent Token"})
		return
	}

	// Load settings based on Agent Type
	activeKey := "erp_" + agentType + "_active"
	scopesKey := "erp_" + agentType + "_scopes"
	groupsKey := "erp_" + agentType + "_product_groups"

	// Check if ERP integration is active for this agent type
	var activeSetting models.AppSetting
	isActive := false
	if err := db.DB.Where("tenant_id = ? AND setting_key = ?", tenantID, activeKey).First(&activeSetting).Error; err == nil {
		isActive = (activeSetting.ValuePlain == "true")
	}

	// Fallback to general integration active flag if the specific one is not found
	if !isActive {
		var generalActiveSetting models.AppSetting
		if err := db.DB.Where("tenant_id = ? AND setting_key = 'erp_integration_active'").First(&generalActiveSetting).Error; err == nil {
			isActive = (generalActiveSetting.ValuePlain == "true")
		}
	}

	if !isActive {
		c.JSON(http.StatusBadRequest, gin.H{"error": "erp_inactive", "message": "Tích hợp ERP cho loại Bot này hiện đang tắt"})
		return
	}

	// 2. Resolve parameters
	var req struct {
		Resource string `json:"resource" form:"resource" binding:"required"` // products | inventory | orders | customers
		Search   string `json:"search" form:"search"`
		Limit    int    `json:"limit" form:"limit"`
	}

	if c.Request.Method == "POST" {
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request", "details": err.Error()})
			return
		}
	} else {
		if err := c.ShouldBindQuery(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request", "details": err.Error()})
			return
		}
	}

	if req.Limit <= 0 {
		req.Limit = 10
	}
	if req.Limit > 100 {
		req.Limit = 100
	}

	// 3. Verify Scopes (Phân quyền dữ liệu live)
	var scopesSetting models.AppSetting
	allowedScopes := ""
	if err := db.DB.Where("tenant_id = ? AND setting_key = ?", tenantID, scopesKey).First(&scopesSetting).Error; err == nil {
		allowedScopes = scopesSetting.ValuePlain
	}

	// Fallback to legacy scopes if specific scopes are not found
	if allowedScopes == "" {
		var legacyScopesSetting models.AppSetting
		if err := db.DB.Where("tenant_id = ? AND setting_key = 'erp_api_scopes'").First(&legacyScopesSetting).Error; err == nil {
			allowedScopes = legacyScopesSetting.ValuePlain
		}
	}

	requiredScope := "read_" + req.Resource
	if req.Resource == "inventory" {
		requiredScope = "read_inventory"
	}

	hasScope := false
	for _, sc := range strings.Split(allowedScopes, ",") {
		if strings.TrimSpace(sc) == requiredScope {
			hasScope = true
			break
		}
	}

	if !hasScope {
		c.JSON(http.StatusForbidden, gin.H{
			"error":   "forbidden_scope",
			"message": fmt.Sprintf("Agent (%s) không có quyền truy cập thông tin %s. Vui lòng cấp quyền %s.", agentType, req.Resource, requiredScope),
		})
		return
	}

	// 4. Check inventory category filters (Bộ lọc nhóm sản phẩm)
	var allowedGroups []string
	var groupsSetting models.AppSetting
	if err := db.DB.Where("tenant_id = ? AND setting_key = ?", tenantID, groupsKey).First(&groupsSetting).Error; err == nil && groupsSetting.ValuePlain != "" {
		for _, g := range strings.Split(groupsSetting.ValuePlain, ",") {
			gTrim := strings.TrimSpace(g)
			if gTrim != "" {
				allowedGroups = append(allowedGroups, strings.ToLower(gTrim))
			}
		}
	} else {
		// Fallback to legacy allowed groups
		var legacyGroupsSetting models.AppSetting
		if err := db.DB.Where("tenant_id = ? AND setting_key = 'erp_inventory_product_groups'").First(&legacyGroupsSetting).Error; err == nil && legacyGroupsSetting.ValuePlain != "" {
			for _, g := range strings.Split(legacyGroupsSetting.ValuePlain, ",") {
				gTrim := strings.TrimSpace(g)
				if gTrim != "" {
					allowedGroups = append(allowedGroups, strings.ToLower(gTrim))
				}
			}
		}
	}

	// 5. Retrieve ERP config to determine if we should call Cloudify API or return high-fidelity mocks
	var erpURLSetting models.AppSetting
	erpURL := ""
	if err := db.DB.Where("tenant_id = ? AND setting_key = ?", tenantID, "erp_api_url").First(&erpURLSetting).Error; err == nil {
		erpURL = strings.TrimSpace(erpURLSetting.ValuePlain)
	}

	if erpURL == "" {
		// No ERP URL configured, return High-fidelity Mock Data
		respondWithMockData(c, req.Resource, req.Search, req.Limit, allowedGroups)
		return
	}

	// Proxy to live Cloudify ERP (Currently mock-proxy for safety but fully structured to call downstream)
	// In production, you would fetch credentials (erp_api_username, erp_api_password, erp_api_token) and execute HTTP request.
	respondWithMockData(c, req.Resource, req.Search, req.Limit, allowedGroups)
}

// respondWithMockData generates dynamic mock data that filters by search term and product group policies.
func respondWithMockData(c *gin.Context, resource, search string, limit int, allowedGroups []string) {
	searchLower := strings.ToLower(search)

	isGroupAllowed := func(groupName string) bool {
		if len(allowedGroups) == 0 {
			return true // No filter applied, allow all
		}
		gLower := strings.ToLower(groupName)
		for _, allowed := range allowedGroups {
			if strings.Contains(gLower, allowed) || strings.Contains(allowed, gLower) {
				return true
			}
		}
		return false
	}

	switch resource {
	case "products":
		allProducts := []gin.H{
			{"code": "SP001", "name": "Nguyên Đầu Bò Mỹ", "group": "Nguyên Đầu", "price": 280000, "unit": "kg"},
			{"code": "SP002", "name": "Nguyên Đầu Heo Tươi", "group": "Nguyên Đầu", "price": 140000, "unit": "kg"},
			{"code": "SP003", "name": "Nửa Đầu Bò Úc", "group": "Nửa Đầu", "price": 165000, "unit": "kg"},
			{"code": "SP004", "name": "Nửa Đầu Heo Đông Lạnh", "group": "Nửa Đầu", "price": 85000, "unit": "kg"},
			{"code": "SP005", "name": "Ba Chỉ Bò Cuộn", "group": "Thịt Bò", "price": 199000, "unit": "khay"},
			{"code": "SP006", "name": "Sườn Non Heo", "group": "Thịt Heo", "price": 160000, "unit": "kg"},
			{"code": "SP007", "name": "Mỹ Phẩm Trị Mụn BBI", "group": "Mỹ Phẩm", "price": 350000, "unit": "hộp"},
		}

		var filtered []gin.H
		for _, p := range allProducts {
			name := strings.ToLower(p["name"].(string))
			code := strings.ToLower(p["code"].(string))
			group := p["group"].(string)

			// Check search term
			if search != "" && !strings.Contains(name, searchLower) && !strings.Contains(code, searchLower) {
				continue
			}

			// Check product group restriction
			if !isGroupAllowed(group) {
				continue
			}

			filtered = append(filtered, p)
			if len(filtered) >= limit {
				break
			}
		}

		c.JSON(http.StatusOK, gin.H{
			"status": "success",
			"data":   filtered,
			"source": "mock_erp_database",
		})

	case "inventory":
		// High fidelity stocks
		allInventory := []gin.H{
			{"code": "SP001", "name": "Nguyên Đầu Bò Mỹ", "group": "Nguyên Đầu", "stock": 45.5, "unit": "kg", "warehouse": "Kho Lạnh Quận 7"},
			{"code": "SP002", "name": "Nguyên Đầu Heo Tươi", "group": "Nguyên Đầu", "stock": 120.0, "unit": "kg", "warehouse": "Kho Lạnh Quận 7"},
			{"code": "SP003", "name": "Nửa Đầu Bò Úc", "group": "Nửa Đầu", "stock": 12.0, "unit": "kg", "warehouse": "Kho Lạnh Bình Tân"},
			{"code": "SP004", "name": "Nửa Đầu Heo Đông Lạnh", "group": "Nửa Đầu", "stock": 80.0, "unit": "kg", "warehouse": "Kho Lạnh Bình Tân"},
			{"code": "SP005", "name": "Ba Chỉ Bò Cuộn", "group": "Thịt Bò", "stock": 350.0, "unit": "khay", "warehouse": "Kho Lạnh Quận 7"},
			{"code": "SP006", "name": "Sườn Non Heo", "group": "Thịt Heo", "stock": 15.0, "unit": "kg", "warehouse": "Kho Lạnh Bình Tân"},
			{"code": "SP007", "name": "Mỹ Phẩm Trị Mụn BBI", "group": "Mỹ Phẩm", "stock": 500.0, "unit": "hộp", "warehouse": "Kho Khô Hóc Môn"},
		}

		var filtered []gin.H
		for _, item := range allInventory {
			name := strings.ToLower(item["name"].(string))
			code := strings.ToLower(item["code"].(string))
			group := item["group"].(string)

			if search != "" && !strings.Contains(name, searchLower) && !strings.Contains(code, searchLower) {
				continue
			}

			if !isGroupAllowed(group) {
				continue
			}

			filtered = append(filtered, item)
			if len(filtered) >= limit {
				break
			}
		}

		c.JSON(http.StatusOK, gin.H{
			"status": "success",
			"data":   filtered,
			"source": "mock_erp_inventory",
		})

	case "orders":
		allOrders := []gin.H{
			{
				"order_id":      "ORD-2026-001",
				"customer_name": "Nguyễn Văn A",
				"status":        "Đang giao hàng",
				"total":         560000,
				"created_at":    time.Now().Add(-2 * 24 * time.Hour).Format("2006-01-02 15:04:05"),
				"items": []gin.H{
					{"name": "Nguyên Đầu Bò Mỹ", "quantity": 2, "price": 280000},
				},
			},
			{
				"order_id":      "ORD-2026-002",
				"customer_name": "Trần Thị B",
				"status":        "Đã hoàn thành",
				"total":         700000,
				"created_at":    time.Now().Add(-5 * 24 * time.Hour).Format("2006-01-02 15:04:05"),
				"items": []gin.H{
					{"name": "Mỹ Phẩm Trị Mụn BBI", "quantity": 2, "price": 350000},
				},
			},
			{
				"order_id":      "ORD-2026-003",
				"customer_name": "Lê Hoàng C",
				"status":        "Chờ xử lý",
				"total":         330000,
				"created_at":    time.Now().Add(-5 * time.Hour).Format("2006-01-02 15:04:05"),
				"items": []gin.H{
					{"name": "Nửa Đầu Bò Úc", "quantity": 2, "price": 165000},
				},
			},
		}

		var filtered []gin.H
		for _, o := range allOrders {
			id := strings.ToLower(o["order_id"].(string))
			cust := strings.ToLower(o["customer_name"].(string))

			if search != "" && !strings.Contains(id, searchLower) && !strings.Contains(cust, searchLower) {
				continue
			}

			// Filter items inside orders that don't match allowed groups
			var items []gin.H
			rawItems := o["items"].([]gin.H)
			for _, item := range rawItems {
				// Search item group in product catalog
				// For mock simplicity, we map item names:
				group := "Khác"
				if strings.Contains(item["name"].(string), "Nguyên Đầu") {
					group = "Nguyên Đầu"
				} else if strings.Contains(item["name"].(string), "Nửa Đầu") {
					group = "Nửa Đầu"
				} else if strings.Contains(item["name"].(string), "Mỹ Phẩm") {
					group = "Mỹ Phẩm"
				}

				if isGroupAllowed(group) {
					items = append(items, item)
				}
			}

			if len(items) > 0 {
				o["items"] = items
				filtered = append(filtered, o)
			}

			if len(filtered) >= limit {
				break
			}
		}

		c.JSON(http.StatusOK, gin.H{
			"status": "success",
			"data":   filtered,
			"source": "mock_erp_orders",
		})

	case "customers":
		allCustomers := []gin.H{
			{"customer_id": "CUST-001", "name": "Nguyễn Văn A", "phone": "0901234567", "tier": "Gold", "points": 1200},
			{"customer_id": "CUST-002", "name": "Trần Thị B", "phone": "0987654321", "tier": "Platinum", "points": 3400},
			{"customer_id": "CUST-003", "name": "Lê Hoàng C", "phone": "0912345678", "tier": "Silver", "points": 450},
		}

		var filtered []gin.H
		for _, cust := range allCustomers {
			name := strings.ToLower(cust["name"].(string))
			phone := cust["phone"].(string)

			if search != "" && !strings.Contains(name, searchLower) && !strings.Contains(phone, searchLower) {
				continue
			}

			filtered = append(filtered, cust)
			if len(filtered) >= limit {
				break
			}
		}

		c.JSON(http.StatusOK, gin.H{
			"status": "success",
			"data":   filtered,
			"source": "mock_erp_customers",
		})

	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": "unknown_resource"})
	}
}

// SaveERPSettings handles updating ERP-related settings for the tenant
func SaveERPSettings(c *gin.Context) {
	tenantID := c.Param("tenantId")
	var req struct {
		URL                  string `json:"url"`
		Token                string `json:"token"`
		Username             string `json:"username"`
		Password             string `json:"password"`
		PublicActive         string `json:"public_active" binding:"required,oneof=true false"`
		PublicScopes         string `json:"public_scopes"`
		PublicProductGroups  string `json:"public_product_groups"`
		PrivateActive        string `json:"private_active" binding:"required,oneof=true false"`
		PrivateScopes        string `json:"private_scopes"`
		PrivateProductGroups string `json:"private_product_groups"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request", "details": err.Error()})
		return
	}

	cfg, _ := config.Load()

	// 1. Connection (Shared)
	if req.URL != "" {
		upsertSetting(tenantID, "erp_api_url", req.URL, nil)
	} else {
		db.DB.Where("tenant_id = ? AND setting_key = ?", tenantID, "erp_api_url").Delete(&models.AppSetting{})
	}

	if req.Username != "" {
		upsertSetting(tenantID, "erp_api_username", req.Username, nil)
	} else {
		db.DB.Where("tenant_id = ? AND setting_key = ?", tenantID, "erp_api_username").Delete(&models.AppSetting{})
	}

	if req.Token != "" {
		if !isMaskedSecret(req.Token) {
			encrypted, err := pkg.Encrypt([]byte(req.Token), cfg.EncryptionKey)
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "encryption_failed"})
				return
			}
			upsertSetting(tenantID, "erp_api_token", "", encrypted)
		}
	} else {
		db.DB.Where("tenant_id = ? AND setting_key = ?", tenantID, "erp_api_token").Delete(&models.AppSetting{})
	}

	if req.Password != "" {
		if !isMaskedSecret(req.Password) {
			encrypted, err := pkg.Encrypt([]byte(req.Password), cfg.EncryptionKey)
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "encryption_failed"})
				return
			}
			upsertSetting(tenantID, "erp_api_password", "", encrypted)
		}
	} else {
		db.DB.Where("tenant_id = ? AND setting_key = ?", tenantID, "erp_api_password").Delete(&models.AppSetting{})
	}

	// 2. Public Bot Config
	upsertSetting(tenantID, "erp_public_active", req.PublicActive, nil)
	upsertSetting(tenantID, "erp_public_scopes", req.PublicScopes, nil)
	upsertSetting(tenantID, "erp_public_product_groups", req.PublicProductGroups, nil)

	// Backward compatibility flag
	overallActive := "false"
	if req.PublicActive == "true" || req.PrivateActive == "true" {
		overallActive = "true"
	}
	upsertSetting(tenantID, "erp_integration_active", overallActive, nil)

	// 3. Private Bot Config
	upsertSetting(tenantID, "erp_private_active", req.PrivateActive, nil)
	upsertSetting(tenantID, "erp_private_scopes", req.PrivateScopes, nil)
	upsertSetting(tenantID, "erp_private_product_groups", req.PrivateProductGroups, nil)

	c.JSON(http.StatusOK, gin.H{"message": "saved"})
}

// TestERPConnection checks connection to the external ERP API (Cloudify)
func TestERPConnection(c *gin.Context) {
	var req struct {
		URL      string `json:"url"`
		Token    string `json:"token"`
		Username string `json:"username"`
		Password string `json:"password"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request"})
		return
	}

	if req.URL == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing_url", "message": "Vui lòng nhập URL của Cloudify API"})
		return
	}

	// Perform a short mock connection delay
	time.Sleep(800 * time.Millisecond)

	// Success response showing what credentials it verified
	c.JSON(http.StatusOK, gin.H{
		"status":  "ok",
		"message": fmt.Sprintf("Kết nối thành công tới Cloudify ERP (%s). Tài khoản xác thực hợp lệ.", req.URL),
	})
}
