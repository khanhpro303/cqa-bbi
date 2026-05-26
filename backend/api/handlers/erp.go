package handlers

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/vietbui/chat-quality-agent/ai"
	"github.com/vietbui/chat-quality-agent/api/middleware"
	"github.com/vietbui/chat-quality-agent/channels"
	"github.com/vietbui/chat-quality-agent/config"
	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/db/models"
	"github.com/vietbui/chat-quality-agent/engine"
	"github.com/vietbui/chat-quality-agent/pkg"
	"gorm.io/gorm"
)

// ---------------------------------------------------------------------------
// ERPQuery — called by Langflow agent to query live Cloudify ERP data
// ---------------------------------------------------------------------------

// ERPQuery handles queries from the Langflow agent to fetch ERP data.
// Authentication is done via Agent Token (X-Agent-Token or Bearer header),
// which determines whether the caller is a public or private agent.
// Data is fetched live from Cloudify ERP; mock data is used only when no
// Cloudify credentials are configured (development fallback).
func ERPQuery(c *gin.Context) {
	tenantID := c.Param("tenantId")
	if tenantID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "tenant_id_required"})
		return
	}

	// ── 1. Authenticate via Agent Secure Token ────────────────────────────
	token := c.GetHeader("X-Agent-Token")
	if token == "" {
		authHeader := c.GetHeader("Authorization")
		if strings.HasPrefix(authHeader, "Bearer ") {
			token = strings.TrimPrefix(authHeader, "Bearer ")
		}
	}
	if token == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized", "message": "Missing Agent Token"})
		return
	}

	// Identify agent type by matching token to stored setting
	agentType := resolveAgentType(tenantID, token)
	if agentType == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized", "message": "Invalid Agent Token"})
		return
	}

	// ── 2. Check ERP integration is active for this agent type ────────────
	if !isERPActive(tenantID, agentType) {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "erp_inactive",
			"message": fmt.Sprintf("Tích hợp ERP cho loại Bot '%s' hiện đang tắt", agentType),
		})
		return
	}

	// ── 3. Parse request parameters ───────────────────────────────────────
	var req struct {
		Resource        string `json:"resource" form:"resource" binding:"required"` // products|inventory|orders|customers|debt
		Search          string `json:"search" form:"search"`
		Limit           int    `json:"limit" form:"limit"`
		PartnerID       string `json:"partner_id" form:"partner_id"`    // filter orders/debt by customer Cloudify ID
		ZaloUserID      string `json:"zalo_user_id" form:"zalo_user_id"` // reserved for user-level scoping
		PermissionToken string `json:"permission_token" form:"permission_token"`
		ParentCode      string `json:"parent_code" form:"parent_code"`
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

	// ── 4. Scope / permission check ───────────────────────────────────────
	var permCtx *engine.GroupPermissionContext
	var err error

	permissionToken := c.GetHeader("X-Permission-Token")
	if permissionToken == "" {
		permissionToken = req.PermissionToken
	}

	cfg, _ := config.Load()

	if permissionToken != "" {
		permCtx, err = engine.VerifyPermissionToken(permissionToken, cfg.EncryptionKey)
		if err != nil {
			c.JSON(http.StatusForbidden, gin.H{
				"error":   "forbidden_token",
				"message": fmt.Sprintf("Token phân quyền không hợp lệ hoặc đã hết hạn: %v", err),
			})
			return
		}
	} else {
		// Fallback: build permission context on-the-fly using DB configuration (legacy/direct calls compatibility)
		resolved := engine.ResolvePermissions(tenantID, req.ZaloUserID, "", agentType)
		permCtx = &resolved
	}

	// ── 4.5. Global HTTP Method & Custom Endpoint Path Validation ──────────────────
	methodAllowed := true
	var globalPermsSetting models.AppSetting
	if err := db.DB.Where("tenant_id = ? AND setting_key = 'erp_global_method_permissions'", tenantID).First(&globalPermsSetting).Error; err == nil && globalPermsSetting.ValuePlain != "" {
		type EndpointConfig struct {
			Get  bool   `json:"get"`
			Post bool   `json:"post"`
			Path string `json:"path"`
		}
		var globalPerms map[string]EndpointConfig
		if json.Unmarshal([]byte(globalPermsSetting.ValuePlain), &globalPerms) == nil {
			matchedSystemResource := ""
			var matchedPerms EndpointConfig
			
			// Find system resource key matching req.Resource by looking at custom paths
			for sysRes, config := range globalPerms {
				path := config.Path
				if path == "" {
					path = sysRes
				}
				if strings.EqualFold(path, req.Resource) {
					matchedSystemResource = sysRes
					matchedPerms = config
					break
				}
			}
			
			// If not matched dynamically, check if it matches the default resource keys
			if matchedSystemResource == "" {
				for sysRes, config := range globalPerms {
					if strings.EqualFold(sysRes, req.Resource) {
						matchedSystemResource = sysRes
						matchedPerms = config
						break
					}
				}
			}

			if matchedSystemResource != "" {
				// Re-route internally to system expected resource
				req.Resource = matchedSystemResource
				reqMethod := strings.ToLower(c.Request.Method) // "get" or "post"
				if reqMethod == "get" {
					methodAllowed = matchedPerms.Get
				} else if reqMethod == "post" {
					methodAllowed = matchedPerms.Post
				} else {
					methodAllowed = false
				}
			} else {
				methodAllowed = false
			}
		}
	}
	if !methodAllowed {
		c.JSON(http.StatusForbidden, gin.H{
			"error":   "forbidden_method",
			"message": fmt.Sprintf("Yêu cầu không hợp lệ hoặc HTTP Method %s không được cho phép đối với tài nguyên '%s' trên hệ thống ERP Gateway.", c.Request.Method, req.Resource),
		})
		return
	}

	// Enforce permitted resources & scope
	allowed, scopeType, productGroups := permCtx.IsResourceAllowed(req.Resource)
	if !allowed {
		c.JSON(http.StatusForbidden, gin.H{
			"error":   "forbidden_scope",
			"message": fmt.Sprintf("Quyền truy cập tài nguyên '%s' bị từ chối cho Agent hoặc khách hàng hiện tại.", req.Resource),
		})
		writeAuditLog(tenantID, permCtx, req.Resource, "none", productGroups, req.Search, http.StatusForbidden, 0, c.ClientIP())
		return
	}

	// ── 5. Apply Rate Limiting per-group ──────────────────────────────────
	if permCtx.AgentType != "private" && len(permCtx.Groups) > 0 {
		var exceededGroup string
		for _, grp := range permCtx.Groups {
			exceeded, err := checkAndIncrementRateLimit(tenantID, grp.GroupID, grp.GroupName)
			if err != nil {
				log.Printf("[erp_query] error checking rate limit: %v", err)
			}
			if exceeded {
				exceededGroup = grp.GroupName
				break
			}
		}
		if exceededGroup != "" {
			c.JSON(http.StatusTooManyRequests, gin.H{
				"error":   "rate_limit_exceeded",
				"message": fmt.Sprintf("Nhóm '%s' đã vượt quá giới hạn lượt truy cập ERP trong ngày.", exceededGroup),
			})
			writeAuditLog(tenantID, permCtx, req.Resource, scopeType, productGroups, req.Search, http.StatusTooManyRequests, 0, c.ClientIP())
			return
		}
	}

	// ── 6. Load Cloudify credentials ──────────────────────────────────────
	erpURL, erpDB, erpLogin, erpPassword, credErr := loadCloudifyCredentials(tenantID, cfg)
	if credErr != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "credential_error", "message": credErr.Error()})
		return
	}

	client := &pkg.CloudifyClient{
		BaseURL:  erpURL,
		DB:       erpDB,
		Login:    erpLogin,
		Password: erpPassword,
	}

	// ── 7. Apply Scope Type Filters ───────────────────────────────────────
	partnerFilterID := req.PartnerID

	if permCtx.AgentType != "private" {
		if scopeType == "own" {
			if req.Resource == "orders" || req.Resource == "debt" {
				ownPartnerID, err := resolveOwnPartnerID(client, permCtx.CustomerCode, erpURL == "")
				if err != nil {
					c.JSON(http.StatusForbidden, gin.H{
						"error":   "forbidden_scope",
						"message": fmt.Sprintf("Không thể xác thực mã khách hàng trên ERP: %v", err),
					})
					writeAuditLog(tenantID, permCtx, req.Resource, scopeType, productGroups, req.Search, http.StatusForbidden, 0, c.ClientIP())
					return
				}
				partnerFilterID = ownPartnerID
			}
		} else if scopeType == "assigned" {
			var groupIDs []string
			for _, grp := range permCtx.Groups {
				groupIDs = append(groupIDs, grp.GroupID)
			}
			allowedCodes, err := resolveGroupCustomerCodes(tenantID, groupIDs)
			if err != nil {
				log.Printf("[erp_query] error resolving group customer codes: %v", err)
			}

			if req.Resource == "orders" || req.Resource == "debt" {
				if partnerFilterID == "" {
					c.JSON(http.StatusForbidden, gin.H{
						"error":   "forbidden_scope",
						"message": "Scope 'assigned' yêu cầu truyền partner_id cụ thể của khách hàng trong nhóm.",
					})
					writeAuditLog(tenantID, permCtx, req.Resource, scopeType, productGroups, req.Search, http.StatusForbidden, 0, c.ClientIP())
					return
				}
				ok, err := isPartnerInAllowedCodes(client, partnerFilterID, allowedCodes, erpURL == "")
				if err != nil || !ok {
					c.JSON(http.StatusForbidden, gin.H{
						"error":   "forbidden_scope",
						"message": "Bạn không có quyền truy cập dữ liệu của khách hàng này (ngoài nhóm được phân công).",
					})
					writeAuditLog(tenantID, permCtx, req.Resource, scopeType, productGroups, req.Search, http.StatusForbidden, 0, c.ClientIP())
					return
				}
			}
		}
	}

	// ── 8. If no credentials → fallback mock (dev only) ──────────────────
	if erpURL == "" || erpLogin == "" || erpPassword == "" {
		var allowedGroups []string
		if permCtx.AgentType == "private" {
			allowedGroups = []string{}
		} else {
			allowedGroups = productGroups
		}
		respondWithMockDataV2(c, req.Resource, req.Search, req.Limit, allowedGroups, scopeType, permCtx.CustomerCode, tenantID, permCtx.Groups, permCtx.ZaloUserID)
		writeAuditLog(tenantID, permCtx, req.Resource, scopeType, productGroups, req.Search, http.StatusOK, 0, c.ClientIP())
		return
	}

	// ── 9. Check if resource is products and attempt cached search in Astra DB ──
	if req.Resource == "products" {
		cachedData, err := searchProductsFromAstraDB(c.Request.Context(), tenantID, req.Search, req.Limit)
		if err != nil {
			log.Printf("[erp_query] Astra DB cache search error: %v. Falling back to live Cloudify ERP.", err)
		} else if cachedData != nil {
			filteredCached := filterProductsByGroups(cachedData, productGroups)

			// Group by parent code check
			if req.Search != "" && len(filteredCached) > 0 {
				maChaCounts := make(map[string]int)
				for _, p := range filteredCached {
					maChaVal := getMapString(p, "MA_CHA", "ma_cha")
					maVal := getMapString(p, "MA", "ma_hang", "ma")
					if maChaVal != "" {
						maChaCounts[maChaVal]++
					} else if maVal != "" {
						maChaCounts[maVal]++
					}
				}

				if len(maChaCounts) > 1 {
					sent := sendProductRichMessage(c, tenantID, req.Search, maChaCounts, permCtx)
					if sent {
						c.JSON(http.StatusOK, gin.H{
							"status":          "success",
							"is_product_rich":  true,
							"data":            []map[string]interface{}{
								{
									"message": fmt.Sprintf("Đã gửi danh sách lựa chọn dòng sản phẩm cho từ khóa '%s' trực tiếp qua Zalo cho người dùng. Hãy hướng dẫn người dùng chọn dòng sản phẩm trên Zalo.", req.Search),
								},
							},
							"message":         "zalo_rich_message_sent_directly",
							"count":           1,
						})
						return
					}
				}
			}

			writeAuditLog(tenantID, permCtx, req.Resource, scopeType, productGroups, req.Search, http.StatusOK, len(filteredCached), c.ClientIP())
			c.JSON(http.StatusOK, gin.H{
				"status":   "success",
				"data":     filteredCached,
				"source":   "astradb_cache",
				"resource": req.Resource,
				"count":    len(filteredCached),
			})
			return
		}
	}

	// ── 10. Execute live Cloudify call with filters ───────────────────────
	respondWithLiveDataV2(c, client, req.Resource, req.Search, req.ParentCode, partnerFilterID, req.Limit, productGroups, scopeType, tenantID, permCtx)
}

// ---------------------------------------------------------------------------
// resolveAgentType — identifies public vs private from token
// ---------------------------------------------------------------------------

func resolveAgentType(tenantID, token string) string {
	// New split tokens (public / private)
	for _, agentType := range []string{"public", "private"} {
		key := "ai_agent_erp_token_" + agentType
		var setting models.AppSetting
		if err := db.DB.Where("tenant_id = ? AND setting_key = ?", tenantID, key).First(&setting).Error; err == nil {
			if subtle.ConstantTimeCompare([]byte(setting.ValuePlain), []byte(token)) == 1 {
				return agentType
			}
		}
	}

	// Legacy: single token (treated as private)
	var legacy models.AppSetting
	if err := db.DB.Where("tenant_id = ? AND setting_key = 'ai_agent_erp_token'", tenantID).First(&legacy).Error; err == nil {
		if subtle.ConstantTimeCompare([]byte(legacy.ValuePlain), []byte(token)) == 1 {
			return "private"
		}
	}

	return ""
}

// ---------------------------------------------------------------------------
// isERPActive — checks per-agent-type active flag
// ---------------------------------------------------------------------------

func isERPActive(tenantID, agentType string) bool {
	if agentType == "private" {
		activeKey := "erp_private_active"
		var s models.AppSetting
		if err := db.DB.Where("tenant_id = ? AND setting_key = ?", tenantID, activeKey).First(&s).Error; err == nil {
			return s.ValuePlain == "true"
		}
	}

	// Fallback to global flag
	var global models.AppSetting
	if err := db.DB.Where("tenant_id = ? AND setting_key = 'erp_integration_active'", tenantID).First(&global).Error; err == nil {
		return global.ValuePlain == "true"
	}

	// Check if API URL is configured
	var urlSetting models.AppSetting
	if err := db.DB.Where("tenant_id = ? AND setting_key = 'erp_api_url'", tenantID).First(&urlSetting).Error; err == nil {
		return urlSetting.ValuePlain != ""
	}

	return false
}

// ---------------------------------------------------------------------------
// isResourcePermitted — checks ERPEndpoint table for customer's GMF groups
// ---------------------------------------------------------------------------

func isResourcePermitted(tenantID, agentType, resource, zaloUserID string) bool {
	if agentType == "private" {
		return true
	}

	if zaloUserID == "" {
		return false
	}

	// 1. Find ZaloCustomer
	var customer models.ZaloCustomer
	if err := db.DB.Where("tenant_id = ? AND zalo_user_id = ? AND status = ?", tenantID, zaloUserID, "approved").First(&customer).Error; err != nil {
		return false
	}

	// 2. Find groups that customer belongs to
	var groupIDs []string
	db.DB.Table("crm_group_customers").Where("zalo_customer_id = ?", customer.ID).Pluck("group_id", &groupIDs)
	if len(groupIDs) == 0 {
		return false
	}

	// 3. Check if any group has this resource enabled
	var count int64
	db.DB.Model(&models.ERPEndpoint{}).
		Where("tenant_id = ? AND group_id IN (?) AND resource = ? AND is_enabled = ?", tenantID, groupIDs, resource, true).
		Count(&count)

	return count > 0
}

// ---------------------------------------------------------------------------
// loadCloudifyCredentials — reads ERP connection config from app_settings
// ---------------------------------------------------------------------------

func loadCloudifyCredentials(tenantID string, cfg *config.Config) (erpURL, erpDB, erpLogin, erpPassword string, err error) {
	settings := map[string]*string{
		"erp_api_url":      &erpURL,
		"erp_api_db":       &erpDB,
		"erp_api_username": &erpLogin,
	}
	for key, dst := range settings {
		var s models.AppSetting
		if e := db.DB.Where("tenant_id = ? AND setting_key = ?", tenantID, key).First(&s).Error; e == nil {
			*dst = strings.TrimSpace(s.ValuePlain)
		}
	}

	// Password is encrypted
	var pwSetting models.AppSetting
	if e := db.DB.Where("tenant_id = ? AND setting_key = 'erp_api_password'", tenantID).First(&pwSetting).Error; e == nil {
		if len(pwSetting.ValueEncrypted) > 0 {
			decrypted, decErr := pkg.Decrypt(pwSetting.ValueEncrypted, cfg.EncryptionKey)
			if decErr != nil {
				err = fmt.Errorf("decrypt ERP password: %w", decErr)
				return
			}
			erpPassword = string(decrypted)
		} else {
			erpPassword = pwSetting.ValuePlain
		}
	}

	return
}

// ---------------------------------------------------------------------------
// loadAllowedGroupsForCustomer — for mock data fallback filtering based on GMF groups
// ---------------------------------------------------------------------------

func loadAllowedGroupsForCustomer(tenantID, zaloUserID, resource string) []string {
	var groups []string
	if zaloUserID == "" {
		return groups
	}

	// 1. Find ZaloCustomer
	var customer models.ZaloCustomer
	if err := db.DB.Where("tenant_id = ? AND zalo_user_id = ? AND status = ?", tenantID, zaloUserID, "approved").First(&customer).Error; err != nil {
		return groups
	}

	// 2. Find groups that customer belongs to
	var groupIDs []string
	db.DB.Table("crm_group_customers").Where("zalo_customer_id = ?", customer.ID).Pluck("group_id", &groupIDs)
	if len(groupIDs) == 0 {
		return groups
	}

	// 3. Load enabled endpoints
	var endpoints []models.ERPEndpoint
	db.DB.Where("tenant_id = ? AND group_id IN (?) AND resource = ? AND is_enabled = ?", tenantID, groupIDs, resource, true).Find(&endpoints)

	var rawGroups []string
	for _, ep := range endpoints {
		if ep.ProductGroups != "" {
			rawGroups = append(rawGroups, ep.ProductGroups)
		}
	}

	for _, raw := range rawGroups {
		for _, g := range strings.Split(raw, ",") {
			if t := strings.TrimSpace(g); t != "" {
				groups = append(groups, strings.ToLower(t))
			}
		}
	}
	return groups
}

func isFullName(search string) bool {
	search = strings.TrimSpace(search)
	if search == "" {
		return false
	}
	return strings.Contains(search, " ")
}

// searchProductsFromAstraDB — queries the cached product collection in Astra DB.
// Uses vector search ($vectorize) if query is non-empty, with a text-regex search fallback.
// Returns (nil, nil) if Astra DB is not configured, allowing live fallback.
// ---------------------------------------------------------------------------
func searchProductsFromAstraDB(ctx context.Context, tenantID, search string, limit int) ([]map[string]interface{}, error) {
	var products []models.CachedProduct
	var err error

	// 1. If it's a full name, attempt LLM fuzzy matching immediately
	if isFullName(search) {
		matchedMaCha, llmErr := fuzzyMatchMaChaWithLLM(ctx, tenantID, search)
		if llmErr == nil && matchedMaCha != "" {
			log.Printf("[handler] Direct LLM fuzzy matched full name '%s' to ma_cha '%s'", search, matchedMaCha)
			err = db.DB.WithContext(ctx).
				Where("tenant_id = ? AND ma_cha = ?", tenantID, matchedMaCha).
				Limit(limit).
				Find(&products).Error
		}
	}

	// 2. If not a full name or fuzzy matched returned no products, run standard SQL LIKE search
	if len(products) == 0 {
		likePattern := "%" + search + "%"
		err = db.DB.WithContext(ctx).
			Where("tenant_id = ? AND (ten_dong_bo_web LIKE ? OR ma LIKE ? OR ten LIKE ? OR ma_cha LIKE ?)", tenantID, likePattern, likePattern, likePattern, likePattern).
			Limit(limit).
			Find(&products).Error
		if err != nil {
			return nil, fmt.Errorf("local MySQL cache query failed: %w", err)
		}

		// Fallback to LLM fuzzy matching if SQL LIKE returned no results
		if len(products) == 0 && search != "" {
			matchedMaCha, llmErr := fuzzyMatchMaChaWithLLM(ctx, tenantID, search)
			if llmErr != nil {
				log.Printf("[handler] LLM fuzzy matching failed for keyword '%s': %v", search, llmErr)
			} else if matchedMaCha != "" {
				log.Printf("[handler] LLM fuzzy matched keyword '%s' to ma_cha '%s'", search, matchedMaCha)
				// Query again using the matched parent product code
				err = db.DB.WithContext(ctx).
					Where("tenant_id = ? AND ma_cha = ?", tenantID, matchedMaCha).
					Limit(limit).
					Find(&products).Error
				if err != nil {
					return nil, fmt.Errorf("local MySQL cache query (LLM fallback) failed: %w", err)
				}
			}
		}
	}

	var results []map[string]interface{}
	for _, p := range products {
		results = append(results, mapCachedProductToAPIResponse(map[string]interface{}{
			"MA":                 p.MA,
			"ma":                 p.MA,
			"code":               p.MA,
			"ma_hang":            p.MA,
			"product_code":       p.MA,
			"TEN_DONG_BO_WEB":    p.TEN_DONG_BO_WEB,
			"TEN":                p.TEN,
			"ten":                p.TEN,
			"ten_hang":           p.TEN,
			"THUOC_TINH_1":       p.THUOC_TINH_1,
			"THUOC_TINH_2":       p.THUOC_TINH_2,
			"DON_GIA_BAN":        p.DON_GIA_BAN,
			"LINK_ANH":           p.LINK_ANH,
			"NHAN_HIEU_NAME":     p.NHAN_HIEU_NAME,
			"LIST_TEN_NHOM_VTHH": p.LIST_TEN_NHOM_VTHH,
			"KHO":                p.KHO,
			"MA_CHA":             p.MA_CHA,
			"ma_cha":             p.MA_CHA,
			"DVT":                p.DVT,
		}))
	}

	return results, nil
}

// searchProductsFromAstraDBWithFilter — queries the cached product collection in the local MySQL database with a parent code filter.
func searchProductsFromAstraDBWithFilter(ctx context.Context, tenantID, search, parentCode string, limit int) ([]map[string]interface{}, error) {
	var products []models.CachedProduct
	likePattern := "%" + search + "%"
	err := db.DB.WithContext(ctx).
		Where("tenant_id = ? AND ma_cha = ? AND (ten_dong_bo_web LIKE ? OR ma LIKE ? OR ten LIKE ?)", tenantID, parentCode, likePattern, likePattern, likePattern).
		Limit(limit).
		Find(&products).Error
	if err != nil {
		return nil, fmt.Errorf("local MySQL cache query with filter failed: %w", err)
	}

	var results []map[string]interface{}
	for _, p := range products {
		results = append(results, mapCachedProductToAPIResponse(map[string]interface{}{
			"MA":                 p.MA,
			"ma":                 p.MA,
			"code":               p.MA,
			"ma_hang":            p.MA,
			"product_code":       p.MA,
			"TEN_DONG_BO_WEB":    p.TEN_DONG_BO_WEB,
			"TEN":                p.TEN,
			"ten":                p.TEN,
			"ten_hang":           p.TEN,
			"THUOC_TINH_1":       p.THUOC_TINH_1,
			"THUOC_TINH_2":       p.THUOC_TINH_2,
			"DON_GIA_BAN":        p.DON_GIA_BAN,
			"LINK_ANH":           p.LINK_ANH,
			"NHAN_HIEU_NAME":     p.NHAN_HIEU_NAME,
			"LIST_TEN_NHOM_VTHH": p.LIST_TEN_NHOM_VTHH,
			"KHO":                p.KHO,
			"MA_CHA":             p.MA_CHA,
			"ma_cha":             p.MA_CHA,
			"DVT":                p.DVT,
		}))
	}

	return results, nil
}

func mapCachedProductToAPIResponse(p map[string]interface{}) map[string]interface{} {
	res := make(map[string]interface{})
	for k, v := range p {
		res[k] = v
	}

	// Extract values using helper for uppercase/lowercase keys
	maVal := getMapString(p, "MA", "ma_hang", "ma")
	tenVal := getMapString(p, "TEN", "ten_hang", "ten")
	webNameVal := getMapString(p, "TEN_DONG_BO_WEB", "ten_dong_bo_web")
	dvtVal := getMapString(p, "DVT", "dvt_chinh_id", "dvt")
	priceVal := getMapFloat(p, "DON_GIA_BAN", "don_gia_ban", "price")
	weightVal := getMapFloat(p, "WEIGHT", "weight", "khoi_luong", "trong_luong")
	if weightVal == 0 {
		weightVal = 1.0 // default/fallback weight
	}

	// Inject old keys to maintain backward compatibility
	res["ma_hang"] = maVal
	res["ten_hang"] = tenVal
	res["ten_dong_bo_web"] = webNameVal
	res["dvt_chinh_id"] = dvtVal
	res["don_gia_ban"] = priceVal
	res["weight"] = weightVal
	res["list_ten_nhom_vthh"] = getMapString(p, "LIST_TEN_NHOM_VTHH", "list_ten_nhom_vthh")

	res["code"] = maVal
	if webNameVal != "" {
		res["name"] = webNameVal
	} else {
		res["name"] = tenVal
	}
	res["unit"] = dvtVal
	res["price"] = priceVal
	res["group"] = getMapString(p, "NHAN_HIEU_NAME", "nhan_hieu_name", "LIST_TEN_NHOM_VTHH", "list_ten_nhom_vthh", "group")

	return res
}

func getMapString(m map[string]interface{}, keys ...string) string {
	for _, k := range keys {
		if val, ok := m[k]; ok && val != nil {
			if s, ok := val.(string); ok {
				return s
			}
			return fmt.Sprintf("%v", val)
		}
	}
	return ""
}

func getMapFloat(m map[string]interface{}, keys ...string) float64 {
	for _, k := range keys {
		if val, ok := m[k]; ok && val != nil {
			switch v := val.(type) {
			case float64:
				return v
			case float32:
				return float64(v)
			case int:
				return float64(v)
			case int64:
				return float64(v)
			case string:
				if f, err := strconv.ParseFloat(v, 64); err == nil {
					return f
				}
			}
		}
	}
	return 0
}

// ---------------------------------------------------------------------------
// respondWithLiveData — executes live Cloudify call and writes response
// ---------------------------------------------------------------------------

func respondWithLiveData(c *gin.Context, client *pkg.CloudifyClient, resource, search, partnerID string, limit int) {
	var (
		data []map[string]interface{}
		err  error
	)

	switch resource {
	case "products":
		data, err = client.SearchProducts(search, limit)
	case "inventory":
		data, err = client.SearchInventory(search, limit)
	case "orders":
		data, err = client.SearchSaleDocuments(partnerID, search, limit)
	case "customers":
		data, err = client.SearchPartners(search, limit)
	case "debt":
		data, err = client.SearchPartnerLedger(partnerID, search, limit)
	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": "unknown_resource"})
		return
	}

	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{
			"error":   "erp_upstream_error",
			"message": fmt.Sprintf("Không thể lấy dữ liệu từ Cloudify ERP: %s", err.Error()),
		})
		return
	}

	if data == nil {
		data = []map[string]interface{}{}
	}

	c.JSON(http.StatusOK, gin.H{
		"status":   "success",
		"data":     data,
		"source":   "cloudify_live",
		"resource": resource,
		"count":    len(data),
	})
}

// ---------------------------------------------------------------------------
// SaveERPSettings — persist ERP connection and per-agent-type config
// ---------------------------------------------------------------------------

func SaveERPSettings(c *gin.Context) {
	tenantID := c.Param("tenantId")
	var req struct {
		// Shared connection
		URL      string `json:"url"`
		DBName   string `json:"db"`      // Cloudify database name
		Username string `json:"username"`
		Password string `json:"password"`
		Token    string `json:"token"`   // optional API token (alternative to password)

		// Public bot config
		PublicActive        string `json:"public_active" binding:"required,oneof=true false"`
		PublicScopes        string `json:"public_scopes"`
		PublicProductGroups string `json:"public_product_groups"`

		// Private bot config
		PrivateActive        string `json:"private_active" binding:"required,oneof=true false"`
		PrivateScopes        string `json:"private_scopes"`
		PrivateProductGroups string `json:"private_product_groups"`

		// New: Global HTTP Method permissions per resource
		GlobalMethodPermissions map[string]struct {
			Get  bool   `json:"get"`
			Post bool   `json:"post"`
			Path string `json:"path"`
		} `json:"global_method_permissions"`

		// New: Private bot endpoint permissions
		PrivateEndpoints []struct {
			Resource      string `json:"resource"`
			IsEnabled     bool   `json:"is_enabled"`
			ScopeType     string `json:"scope_type"`
			ProductGroups string `json:"product_groups"`
		} `json:"private_endpoints"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request", "details": err.Error()})
		return
	}

	cfg, _ := config.Load()

	// ── Connection (shared) ──────────────────────────────────────────────
	if req.URL != "" {
		upsertSetting(tenantID, "erp_api_url", req.URL, nil)
	} else {
		db.DB.Where("tenant_id = ? AND setting_key = 'erp_api_url'", tenantID).Delete(&models.AppSetting{})
	}

	if req.DBName != "" {
		upsertSetting(tenantID, "erp_api_db", req.DBName, nil)
	} else {
		db.DB.Where("tenant_id = ? AND setting_key = 'erp_api_db'", tenantID).Delete(&models.AppSetting{})
	}

	if req.Username != "" {
		upsertSetting(tenantID, "erp_api_username", req.Username, nil)
	} else {
		db.DB.Where("tenant_id = ? AND setting_key = 'erp_api_username'", tenantID).Delete(&models.AppSetting{})
	}

	if req.Password != "" && !isMaskedSecret(req.Password) {
		encrypted, err := pkg.Encrypt([]byte(req.Password), cfg.EncryptionKey)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "encryption_failed"})
			return
		}
		upsertSetting(tenantID, "erp_api_password", "", encrypted)
	} else if req.Password == "" {
		db.DB.Where("tenant_id = ? AND setting_key = 'erp_api_password'", tenantID).Delete(&models.AppSetting{})
	}

	if req.Token != "" && !isMaskedSecret(req.Token) {
		encrypted, err := pkg.Encrypt([]byte(req.Token), cfg.EncryptionKey)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "encryption_failed"})
			return
		}
		upsertSetting(tenantID, "erp_api_token", "", encrypted)
	} else if req.Token == "" {
		db.DB.Where("tenant_id = ? AND setting_key = 'erp_api_token'", tenantID).Delete(&models.AppSetting{})
	}

	// ── Public bot ────────────────────────────────────────────────────────
	upsertSetting(tenantID, "erp_public_active", req.PublicActive, nil)
	upsertSetting(tenantID, "erp_public_scopes", req.PublicScopes, nil)
	upsertSetting(tenantID, "erp_public_product_groups", req.PublicProductGroups, nil)

	// ── Private bot ───────────────────────────────────────────────────────
	upsertSetting(tenantID, "erp_private_active", req.PrivateActive, nil)
	upsertSetting(tenantID, "erp_private_scopes", req.PrivateScopes, nil)
	upsertSetting(tenantID, "erp_private_product_groups", req.PrivateProductGroups, nil)

	// ── Global method permissions ─────────────────────────────────────────
	if req.GlobalMethodPermissions != nil {
		bytes, err := json.Marshal(req.GlobalMethodPermissions)
		if err == nil {
			upsertSetting(tenantID, "erp_global_method_permissions", string(bytes), nil)
		}
	}

	// ── Private bot endpoints ─────────────────────────────────────────────
	for _, ep := range req.PrivateEndpoints {
		var existing models.ERPEndpoint
		result := db.DB.Where("tenant_id = ? AND group_id = ? AND resource = ?",
			tenantID, "private_bot", ep.Resource).First(&existing)

		scopeType := ep.ScopeType
		if scopeType == "" {
			scopeType = "all"
		}

		if result.Error == nil {
			db.DB.Model(&existing).Updates(map[string]interface{}{
				"is_enabled":     ep.IsEnabled,
				"scope_type":     scopeType,
				"product_groups": ep.ProductGroups,
				"updated_at":     time.Now(),
			})
		} else {
			db.DB.Create(&models.ERPEndpoint{
				ID:            pkg.NewUUID(),
				TenantID:      tenantID,
				GroupID:       "private_bot",
				Resource:      ep.Resource,
				IsEnabled:     ep.IsEnabled,
				ScopeType:     scopeType,
				ProductGroups: ep.ProductGroups,
				CreatedAt:     time.Now(),
				UpdatedAt:     time.Now(),
			})
		}
	}

	// ── Backward-compat global active flag ────────────────────────────────
	overallActive := "false"
	if req.PublicActive == "true" || req.PrivateActive == "true" {
		overallActive = "true"
	}
	upsertSetting(tenantID, "erp_integration_active", overallActive, nil)

	c.JSON(http.StatusOK, gin.H{"message": "saved"})
}

// ---------------------------------------------------------------------------
// TestERPConnection — actually authenticates against Cloudify
// ---------------------------------------------------------------------------

func TestERPConnection(c *gin.Context) {
	tenantID := middleware.GetTenantID(c)

	var req struct {
		URL      string `json:"url"`
		DBName   string `json:"db"`
		Username string `json:"username"`
		Password string `json:"password"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request"})
		return
	}

	if req.URL == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "missing_url",
			"message": "Vui lòng nhập URL của Cloudify API (ví dụ: https://bbiapi.cloudify.vn)",
		})
		return
	}
	if req.Username == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing_username", "message": "Vui lòng nhập username"})
		return
	}

	// Resolve password: if masked → load from DB
	password := req.Password
	if isMaskedSecret(password) || password == "" {
		cfg, _ := config.Load()
		var pwSetting models.AppSetting
		if err := db.DB.Where("tenant_id = ? AND setting_key = 'erp_api_password'", tenantID).First(&pwSetting).Error; err != nil {
			c.JSON(http.StatusBadRequest, gin.H{
				"error":   "missing_password",
				"message": "Vui lòng nhập password hoặc lưu cấu hình trước",
			})
			return
		}
		if len(pwSetting.ValueEncrypted) > 0 {
			decrypted, err := pkg.Decrypt(pwSetting.ValueEncrypted, cfg.EncryptionKey)
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "decrypt_failed"})
				return
			}
			password = string(decrypted)
		} else {
			password = pwSetting.ValuePlain
		}
	}

	// Resolve DB name
	dbName := req.DBName
	if dbName == "" {
		var dbSetting models.AppSetting
		if err := db.DB.Where("tenant_id = ? AND setting_key = 'erp_api_db'", tenantID).First(&dbSetting).Error; err == nil {
			dbName = dbSetting.ValuePlain
		}
	}

	client := &pkg.CloudifyClient{
		BaseURL:  req.URL,
		DB:       dbName,
		Login:    req.Username,
		Password: password,
	}

	if err := client.TestConnection(); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"status":  "error",
			"message": fmt.Sprintf("Kết nối thất bại: %s", err.Error()),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"status":  "ok",
		"message": fmt.Sprintf("Kết nối thành công tới Cloudify ERP (%s). Xác thực hợp lệ.", req.URL),
	})
}

// ---------------------------------------------------------------------------
// respondWithMockData — development fallback (no ERP credentials configured)
// ---------------------------------------------------------------------------

func respondWithMockData(c *gin.Context, resource, search string, limit int, allowedGroups []string) {
	searchLower := strings.ToLower(search)

	isGroupAllowed := func(groupName string) bool {
		if len(allowedGroups) == 0 {
			return true
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
		}
		var filtered []gin.H
		for _, p := range allProducts {
			name := strings.ToLower(p["name"].(string))
			code := strings.ToLower(p["code"].(string))
			if search != "" && !strings.Contains(name, searchLower) && !strings.Contains(code, searchLower) {
				continue
			}
			if !isGroupAllowed(p["group"].(string)) {
				continue
			}
			filtered = append(filtered, p)
			if len(filtered) >= limit {
				break
			}
		}
		c.JSON(http.StatusOK, gin.H{"status": "success", "data": filtered, "source": "mock_erp", "count": len(filtered)})

	case "inventory":
		allInventory := []gin.H{
			{"code": "SP001", "name": "Nguyên Đầu Bò Mỹ", "group": "Nguyên Đầu", "stock": 45.5, "unit": "kg", "warehouse": "Kho Lạnh Quận 7"},
			{"code": "SP002", "name": "Nguyên Đầu Heo Tươi", "group": "Nguyên Đầu", "stock": 120.0, "unit": "kg", "warehouse": "Kho Lạnh Quận 7"},
			{"code": "SP003", "name": "Nửa Đầu Bò Úc", "group": "Nửa Đầu", "stock": 12.0, "unit": "kg", "warehouse": "Kho Lạnh Bình Tân"},
			{"code": "SP005", "name": "Ba Chỉ Bò Cuộn", "group": "Thịt Bò", "stock": 350.0, "unit": "khay", "warehouse": "Kho Lạnh Quận 7"},
		}
		var filtered []gin.H
		for _, item := range allInventory {
			name := strings.ToLower(item["name"].(string))
			code := strings.ToLower(item["code"].(string))
			if search != "" && !strings.Contains(name, searchLower) && !strings.Contains(code, searchLower) {
				continue
			}
			if !isGroupAllowed(item["group"].(string)) {
				continue
			}
			filtered = append(filtered, item)
			if len(filtered) >= limit {
				break
			}
		}
		c.JSON(http.StatusOK, gin.H{"status": "success", "data": filtered, "source": "mock_erp", "count": len(filtered)})

	case "orders":
		allOrders := []gin.H{
			{"order_id": "ORD-2026-001", "customer_name": "Nguyễn Văn A", "status": "Đang giao hàng", "total": 560000},
			{"order_id": "ORD-2026-002", "customer_name": "Trần Thị B", "status": "Đã hoàn thành", "total": 700000},
		}
		var filtered []gin.H
		for _, o := range allOrders {
			id := strings.ToLower(o["order_id"].(string))
			cust := strings.ToLower(o["customer_name"].(string))
			if search != "" && !strings.Contains(id, searchLower) && !strings.Contains(cust, searchLower) {
				continue
			}
			filtered = append(filtered, o)
			if len(filtered) >= limit {
				break
			}
		}
		c.JSON(http.StatusOK, gin.H{"status": "success", "data": filtered, "source": "mock_erp", "count": len(filtered)})

	case "customers":
		allCustomers := []gin.H{
			{"customer_id": "CUST-001", "name": "Nguyễn Văn A", "phone": "0901234567", "tier": "Gold"},
			{"customer_id": "CUST-002", "name": "Trần Thị B", "phone": "0987654321", "tier": "Platinum"},
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
		c.JSON(http.StatusOK, gin.H{"status": "success", "data": filtered, "source": "mock_erp", "count": len(filtered)})

	case "debt":
		c.JSON(http.StatusOK, gin.H{
			"status": "success",
			"data":   []gin.H{},
			"source": "mock_erp",
			"count":  0,
			"note":   "Dữ liệu công nợ chỉ khả dụng khi kết nối ERP thực",
		})

	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": "unknown_resource"})
	}
}

// ---------------------------------------------------------------------------
// Helper: Rate Limiting & Scope Enforcement & Audit Logs
// ---------------------------------------------------------------------------

func checkAndIncrementRateLimit(tenantID, groupID, groupName string) (bool, error) {
	dateKey := time.Now().Format("2006-01-02")
	var rateLimit models.ERPGroupRateLimit

	err := db.DB.Transaction(func(tx *gorm.DB) error {
		err := tx.Where("tenant_id = ? AND group_id = ? AND date_key = ?", tenantID, groupID, dateKey).
			First(&rateLimit).Error

		if err != nil {
			if err == gorm.ErrRecordNotFound {
				rateLimit = models.ERPGroupRateLimit{
					ID:        uuid.New().String(),
					TenantID:  tenantID,
					GroupID:   groupID,
					DateKey:   dateKey,
					CallCount: 1,
					DayLimit:  500,
					UpdatedAt: time.Now(),
				}
				return tx.Create(&rateLimit).Error
			}
			return err
		}

		if rateLimit.CallCount >= rateLimit.DayLimit {
			return fmt.Errorf("rate limit exceeded")
		}

		rateLimit.CallCount++
		rateLimit.UpdatedAt = time.Now()
		return tx.Save(&rateLimit).Error
	})

	if err != nil {
		if err.Error() == "rate limit exceeded" {
			return true, nil
		}
		return false, err
	}

	return false, nil
}

func resolveOwnPartnerID(client *pkg.CloudifyClient, customerCode string, isMock bool) (string, error) {
	if isMock {
		return "mock_partner_id", nil
	}
	if customerCode == "" {
		return "", fmt.Errorf("customer code is empty")
	}

	partners, err := client.SearchPartners(customerCode, 5)
	if err != nil {
		return "", fmt.Errorf("failed to search partners on ERP: %w", err)
	}

	for _, p := range partners {
		maVal := getMapString(p, "MA", "code", "ma")
		if strings.EqualFold(maVal, customerCode) {
			idVal := getMapString(p, "ID", "id")
			if idVal != "" {
				return idVal, nil
			}
		}
	}

	return "", fmt.Errorf("customer code %s not found on Cloudify ERP", customerCode)
}

func resolveGroupCustomerCodes(tenantID string, groupIDs []string) ([]string, error) {
	if len(groupIDs) == 0 {
		return []string{}, nil
	}
	var codes []string
	err := db.DB.Table("zalo_customers").
		Joins("join crm_group_customers on crm_group_customers.zalo_customer_id = zalo_customers.id").
		Where("crm_group_customers.group_id IN (?) AND zalo_customers.tenant_id = ? AND zalo_customers.status = ?", groupIDs, tenantID, "approved").
		Pluck("zalo_customers.customer_code", &codes).Error
	return codes, err
}

func isPartnerInAllowedCodes(client *pkg.CloudifyClient, partnerID string, allowedCodes []string, isMock bool) (bool, error) {
	if isMock {
		return true, nil
	}
	if len(allowedCodes) == 0 {
		return false, nil
	}

	partners, err := client.SearchPartners("", 100)
	if err != nil {
		return false, err
	}

	for _, p := range partners {
		idVal := getMapString(p, "ID", "id")
		if idVal == partnerID {
			maVal := getMapString(p, "MA", "code", "ma")
			for _, code := range allowedCodes {
				if strings.EqualFold(maVal, code) {
					return true, nil
				}
			}
			return false, nil
		}
	}

	return false, nil
}

func writeAuditLog(tenantID string, permCtx *engine.GroupPermissionContext, resource, scopeApplied string, productFilter []string, searchQuery string, status int, count int, ipAddress string) {
	var groupIDs []string
	for _, g := range permCtx.Groups {
		groupIDs = append(groupIDs, g.GroupID)
	}

	logRec := models.ERPAuditLog{
		ID:             uuid.New().String(),
		TenantID:       tenantID,
		AgentType:      permCtx.AgentType,
		ZaloUserID:     permCtx.ZaloUserID,
		CustomerCode:   permCtx.CustomerCode,
		GroupIDs:       strings.Join(groupIDs, ","),
		Resource:       resource,
		ScopeApplied:   scopeApplied,
		ProductFilter:  strings.Join(productFilter, ","),
		SearchQuery:    searchQuery,
		ResponseStatus: status,
		ResponseCount:  count,
		IPAddress:      ipAddress,
		CreatedAt:      time.Now(),
	}

	if err := db.DB.Create(&logRec).Error; err != nil {
		log.Printf("[audit_log] failed to write ERP audit log: %v", err)
	}
}

func filterProductsByGroups(products []map[string]interface{}, allowedGroups []string) []map[string]interface{} {
	if len(allowedGroups) == 0 {
		return products
	}
	var filtered []map[string]interface{}
	for _, p := range products {
		groupVal := getMapString(p, "NHAN_HIEU_NAME", "nhan_hieu_name", "LIST_TEN_NHOM_VTHH", "list_ten_nhom_vthh", "group")
		groupLower := strings.ToLower(groupVal)

		matched := false
		for _, allowed := range allowedGroups {
			if strings.Contains(groupLower, strings.ToLower(allowed)) {
				matched = true
				break
			}
		}
		if !matched {
			// Fallback: check if the product name contains the allowed group name (e.g., "Mũ Bảo Hiểm")
			nameVal := getMapString(p, "TEN_HANG", "ten_hang", "TEN", "ten", "name")
			nameLower := strings.ToLower(nameVal)
			for _, allowed := range allowedGroups {
				if strings.Contains(nameLower, strings.ToLower(allowed)) {
					matched = true
					break
				}
			}
		}
		if matched {
			filtered = append(filtered, p)
		}
	}
	return filtered
}

func respondWithLiveDataV2(c *gin.Context, client *pkg.CloudifyClient, resource, search, parentCode, partnerID string, limit int, productGroups []string, scopeType string, tenantID string, permCtx *engine.GroupPermissionContext) {
	var (
		data []map[string]interface{}
		err  error
	)

	switch resource {
	case "products":
		productsEndpoint := "product/search"
		usePostMethod := false
		var globalPermsSetting models.AppSetting
		if errSetting := db.DB.Where("tenant_id = ? AND setting_key = 'erp_global_method_permissions'", tenantID).First(&globalPermsSetting).Error; errSetting == nil && globalPermsSetting.ValuePlain != "" {
			type EndpointConfig struct {
				Get  bool   `json:"get"`
				Post bool   `json:"post"`
				Path string `json:"path"`
			}
			var globalPerms map[string]EndpointConfig
			if errUnmarshal := json.Unmarshal([]byte(globalPermsSetting.ValuePlain), &globalPerms); errUnmarshal == nil {
				if prodConfig, exists := globalPerms["products"]; exists && prodConfig.Path != "" && prodConfig.Path != "products" {
					path := prodConfig.Path
					path = strings.TrimPrefix(path, "/")
					path = strings.TrimPrefix(path, "rest_api/private/")
					path = strings.TrimPrefix(path, "/")
					productsEndpoint = path
					usePostMethod = prodConfig.Post
				}
			}
		}

		sanitizedSearch := sanitizeSearchQuery(search)
		if usePostMethod {
			bodyPayload := map[string]interface{}{
				"limit": limit,
			}
			if productsEndpoint == "product/search" {
				bodyPayload["keyword"] = sanitizedSearch
			} else {
				bodyPayload["MA_HANG"] = sanitizedSearch
			}
			data, err = client.SearchCustomEndpointWithBody(productsEndpoint, bodyPayload)
		} else {
			params := map[string]string{
				"limit": strconv.Itoa(limit),
			}
			if productsEndpoint == "product/search" {
				params["keyword"] = sanitizedSearch
			} else {
				params["MA_HANG"] = sanitizedSearch
			}
			data, err = client.SearchCustomEndpoint(productsEndpoint, params)
		}

		if err != nil {
			log.Printf("[erp_query] Cloudify product search error for search '%s' (sanitized: '%s'): %v. Overriding to empty list.", search, sanitizedSearch, err)
			err = nil
			data = []map[string]interface{}{}
		}

		if err == nil && data != nil {
			filtered := filterProductsByGroups(data, productGroups)
			if search != "" && len(filtered) > 0 {
				maChaCounts := make(map[string]int)
				for _, p := range filtered {
					maChaVal := getMapString(p, "MA_CHA", "ma_cha")
					maVal := getMapString(p, "MA", "ma_hang", "ma")
					if maChaVal != "" {
						maChaCounts[maChaVal]++
					} else if maVal != "" {
						maChaCounts[maVal]++
					}
				}

				if len(maChaCounts) > 1 {
					sent := sendProductRichMessage(c, tenantID, search, maChaCounts, permCtx)
					if sent {
						c.JSON(http.StatusOK, gin.H{
							"status":          "success",
							"is_product_rich":  true,
							"data":            []map[string]interface{}{
								{
									"message": fmt.Sprintf("Đã gửi danh sách lựa chọn dòng sản phẩm cho từ khóa '%s' trực tiếp qua Zalo cho người dùng. Hãy hướng dẫn người dùng chọn dòng sản phẩm trên Zalo.", search),
								},
							},
							"message":         "zalo_rich_message_sent_directly",
							"count":           1,
						})
						return
					}
				}
			}
		}
	case "inventory":
		// Load custom inventory endpoint config
		inventoryEndpoint := "inventory_receipt/search"
		usePostMethod := false
		var globalPermsSetting models.AppSetting
		if errSetting := db.DB.Where("tenant_id = ? AND setting_key = 'erp_global_method_permissions'", tenantID).First(&globalPermsSetting).Error; errSetting == nil && globalPermsSetting.ValuePlain != "" {
			type EndpointConfig struct {
				Get  bool   `json:"get"`
				Post bool   `json:"post"`
				Path string `json:"path"`
			}
			var globalPerms map[string]EndpointConfig
			if errUnmarshal := json.Unmarshal([]byte(globalPermsSetting.ValuePlain), &globalPerms); errUnmarshal == nil {
				if invConfig, exists := globalPerms["inventory"]; exists && invConfig.Path != "" && invConfig.Path != "inventory" {
					path := invConfig.Path
					path = strings.TrimPrefix(path, "/")
					path = strings.TrimPrefix(path, "rest_api/private/")
					path = strings.TrimPrefix(path, "/")
					inventoryEndpoint = path
					usePostMethod = invConfig.Post
				}
			}
		}

		if parentCode != "" && search != "" {
			matchedProducts, errSearch := searchProductsFromAstraDBWithFilter(c.Request.Context(), tenantID, search, parentCode, 20)
			if errSearch == nil && len(matchedProducts) > 0 {
				matchedProducts = filterProductsByGroups(matchedProducts, productGroups)
				var variantData []map[string]interface{}
				for _, child := range matchedProducts {
					childSKU := getMapString(child, "MA", "ma", "ma_hang")
					if childSKU == "" {
						continue
					}
					
					var skuInventory []map[string]interface{}
					var errQuery error
					if usePostMethod {
						bodyPayload := map[string]interface{}{
							"limit": 100,
						}
						if inventoryEndpoint == "inventory_receipt/search" {
							bodyPayload["keyword"] = childSKU
						} else {
							bodyPayload["MA_HANG"] = childSKU
						}
						skuInventory, errQuery = client.SearchCustomEndpointWithBody(inventoryEndpoint, bodyPayload)
					} else {
						params := map[string]string{
							"limit": "100",
						}
						if inventoryEndpoint == "inventory_receipt/search" {
							params["keyword"] = childSKU
						} else {
							params["MA_HANG"] = childSKU
						}
						skuInventory, errQuery = client.SearchCustomEndpoint(inventoryEndpoint, params)
					}
					if errQuery != nil {
						log.Printf("[inventory_query_filtered] error querying stock for SKU %s: %v", childSKU, errQuery)
						continue
					}
					
					var skuStock float64
					for _, invItem := range skuInventory {
						stockVal := getMapFloat(invItem, "stock", "ton", "ton_kho", "SO_LUONG_TON_KHA_DUNG", "so_luong_ton_kha_dung", "SO_LUONG_TON_TONG", "so_luong_ton_tong")
						skuStock += stockVal
					}
					
					record := map[string]interface{}{
						"MA":                 childSKU,
						"ma":                 childSKU,
						"code":               childSKU,
						"ma_hang":            childSKU,
						"product_code":       childSKU,
						"TEN":                getMapString(child, "TEN", "ten", "ten_hang"),
						"ten":                getMapString(child, "TEN", "ten", "ten_hang"),
						"TEN_DONG_BO_WEB":    getMapString(child, "TEN_DONG_BO_WEB", "ten_dong_bo_web"),
						"TON_KHO":            skuStock,
						"ton_kho":            skuStock,
						"MA_CHA":             parentCode,
						"ma_cha":             parentCode,
						"THUOC_TINH_1":       getMapString(child, "THUOC_TINH_1", "thuoc_tinh_1"),
						"THUOC_TINH_2":       getMapString(child, "THUOC_TINH_2", "thuoc_tinh_2"),
						"DON_GIA_BAN":        getMapFloat(child, "DON_GIA_BAN", "don_gia_ban"),
						"LINK_ANH":           getMapString(child, "LINK_ANH", "link_anh"),
						"DVT":                getMapString(child, "DVT", "dvt"),
						"LIST_TEN_NHOM_VTHH": getMapString(child, "LIST_TEN_NHOM_VTHH", "list_ten_nhom_vthh"),
						"list_ten_nhom_vthh": getMapString(child, "LIST_TEN_NHOM_VTHH", "list_ten_nhom_vthh"),
					}
					variantData = append(variantData, record)
				}
				c.JSON(http.StatusOK, gin.H{
					"status":   "success",
					"data":     variantData,
					"source":   "cloudify_live_filtered",
					"resource": resource,
					"count":    len(variantData),
				})
				return
			}
		}

		if search != "" {
			matchedProducts, errSearch := searchProductsByWebNameAstraDBNonVectorized(c.Request.Context(), tenantID, search)
			if errSearch != nil {
				log.Printf("[inventory_query] Astra DB search error for tenant=%s search=%s: %v", tenantID, search, errSearch)
			} else {
				log.Printf("[inventory_query] Astra DB search for tenant=%s search=%s returned %d products", tenantID, search, len(matchedProducts))
				if len(matchedProducts) > 1 {
					maChaCounts := make(map[string]int)
					for _, p := range matchedProducts {
						maChaVal := getMapString(p, "MA_CHA", "ma_cha")
						maVal := getMapString(p, "MA", "ma_hang", "ma")
						if maChaVal != "" {
							maChaCounts[maChaVal]++
						} else if maVal != "" {
							maChaCounts[maVal]++
						}
					}

					if len(maChaCounts) > 0 {
						var buttons []gin.H
						buttons = append(buttons, gin.H{
							"title":   "📦 Xem theo dòng sản phẩm",
							"type":    "oa.query.hide",
							"payload": "#choose_flow_type:dongsp:" + search,
						})
						buttons = append(buttons, gin.H{
							"title":   "🔍 Xem theo mã SKU cụ thể",
							"type":    "oa.query.hide",
							"payload": "#choose_flow_type:skucuthe:" + search,
						})

						promptMsg := gin.H{
							"recipient": gin.H{
								"user_id": permCtx.ZaloUserID,
							},
							"message": gin.H{
								"text": fmt.Sprintf("Bạn muốn kiểm tra tồn kho cho '%s' theo dòng sản phẩm hay mã SKU cụ thể?", search),
								"attachment": gin.H{
									"type": "template",
									"payload": gin.H{
										"buttons": buttons,
									},
								},
							},
						}

						// Send rich message directly to Zalo from backend
						var activeChannel *models.Channel
						var allChannels []models.Channel
						if errChan := db.DB.Where("tenant_id = ? AND channel_type = ? AND is_active = true", tenantID, "zalo_oa").Find(&allChannels).Error; errChan == nil && len(allChannels) > 0 {
							activeChannel = &allChannels[0]
						}

						if activeChannel != nil {
							cfg, _ := config.Load()
							credBytes, errDec := pkg.Decrypt(activeChannel.CredentialsEncrypted, cfg.EncryptionKey)
							if errDec == nil {
								var zaloCreds channels.ZaloOACredentials
								if errCreds := json.Unmarshal(credBytes, &zaloCreds); errCreds == nil {
									adapter := channels.NewZaloOAAdapter(zaloCreds)
									adapter.SetTokenRefreshCallback(func(newAccess, newRefresh string) {
										var ch models.Channel
										if db.DB.First(&ch, "id = ?", activeChannel.ID).Error == nil {
											credsMap := map[string]interface{}{
												"app_id":        zaloCreds.AppID,
												"app_secret":    zaloCreds.AppSecret,
												"access_token":  newAccess,
												"refresh_token": newRefresh,
												"oa_id":         zaloCreds.OAId,
											}
											newCredJSON, _ := json.Marshal(credsMap)
											encrypted, _ := pkg.Encrypt(newCredJSON, cfg.EncryptionKey)
											db.DB.Model(&ch).Update("credentials_encrypted", encrypted)
										}
									})

									promptMsgJSON, _ := json.Marshal(promptMsg)
									
									var matchedGroup models.CRMGroup
									hasGroup := false
									if len(permCtx.Groups) > 0 {
										for _, gp := range permCtx.Groups {
											if gp.GroupID != "private_bot" {
												if errGrp := db.DB.Where("id = ? AND tenant_id = ?", gp.GroupID, tenantID).First(&matchedGroup).Error; errGrp == nil && matchedGroup.ZaloGroupID != "" {
													hasGroup = true
													break
												}
											}
										}
									}

									var sendErr error
									if hasGroup {
										sendErr = adapter.SendGroupMessage(c.Request.Context(), matchedGroup.ZaloGroupID, string(promptMsgJSON))
									} else {
										sendErr = adapter.SendMessage(c.Request.Context(), permCtx.ZaloUserID, string(promptMsgJSON))
									}
									
									if sendErr != nil {
										log.Printf("[inventory_query] failed to send Zalo Rich Message directly: %v", sendErr)
									} else {
										log.Printf("[inventory_query] successfully sent Zalo Rich Message directly to %s", permCtx.ZaloUserID)
									}
								}
							}
						}

						c.JSON(http.StatusOK, gin.H{
							"status":            "success",
							"is_inventory_rich":  true,
							"data":              []map[string]interface{}{},
							"message":           "zalo_rich_message_sent_directly",
							"count":             0,
						})
						return
					}
				} else if len(matchedProducts) == 1 {
					skuVal := getMapString(matchedProducts[0], "MA", "ma_hang", "ma")
					if skuVal != "" {
						search = skuVal
					}
				}
			}
		}

		maCha, _, isMaCha := detectMaChaFromSearch(c.Request.Context(), tenantID, search, productGroups)
		if isMaCha {
			// Query for parent product line (ma_cha)
			childProducts, errVal := getProductsByMaChaFromAstraDB(c.Request.Context(), tenantID, maCha)
			if errVal != nil {
				err = fmt.Errorf("failed to fetch variants from cache: %w", errVal)
			} else if len(childProducts) > 0 {
				childProducts = filterProductsByGroups(childProducts, productGroups)
				
				// Sum stocks for each child variant
				var variantData []map[string]interface{}
				for _, child := range childProducts {
					childSKU := getMapString(child, "MA", "ma", "ma_hang")
					if childSKU == "" {
						continue
					}
					
					var skuInventory []map[string]interface{}
					var errQuery error
					if usePostMethod {
						bodyPayload := map[string]interface{}{
							"limit": 100,
						}
						if inventoryEndpoint == "inventory_receipt/search" {
							bodyPayload["keyword"] = childSKU
						} else {
							bodyPayload["MA_HANG"] = childSKU
						}
						skuInventory, errQuery = client.SearchCustomEndpointWithBody(inventoryEndpoint, bodyPayload)
					} else {
						params := map[string]string{
							"limit": "100",
						}
						if inventoryEndpoint == "inventory_receipt/search" {
							params["keyword"] = childSKU
						} else {
							params["MA_HANG"] = childSKU
						}
						skuInventory, errQuery = client.SearchCustomEndpoint(inventoryEndpoint, params)
					}
					if errQuery != nil {
						log.Printf("[inventory_query] error querying stock for SKU %s: %v", childSKU, errQuery)
						continue
					}
					
					var skuStock float64
					for _, invItem := range skuInventory {
						stockVal := getMapFloat(invItem, "stock", "ton", "ton_kho", "SO_LUONG_TON_KHA_DUNG", "so_luong_ton_kha_dung", "SO_LUONG_TON_TONG", "so_luong_ton_tong")
						skuStock += stockVal
					}
					
					record := map[string]interface{}{
						"MA":                 childSKU,
						"ma":                 childSKU,
						"code":               childSKU,
						"ma_hang":            childSKU,
						"product_code":       childSKU,
						"TEN":                getMapString(child, "TEN", "ten", "ten_hang"),
						"ten":                getMapString(child, "TEN", "ten", "ten_hang"),
						"TEN_DONG_BO_WEB":    getMapString(child, "TEN_DONG_BO_WEB", "ten_dong_bo_web"),
						"TON_KHO":            skuStock,
						"ton_kho":            skuStock,
						"MA_CHA":             maCha,
						"ma_cha":             maCha,
						"THUOC_TINH_1":       getMapString(child, "THUOC_TINH_1", "thuoc_tinh_1"),
						"THUOC_TINH_2":       getMapString(child, "THUOC_TINH_2", "thuoc_tinh_2"),
						"DON_GIA_BAN":        getMapFloat(child, "DON_GIA_BAN", "don_gia_ban"),
						"LINK_ANH":           getMapString(child, "LINK_ANH", "link_anh"),
						"DVT":                getMapString(child, "DVT", "dvt"),
						"LIST_TEN_NHOM_VTHH": getMapString(child, "LIST_TEN_NHOM_VTHH", "list_ten_nhom_vthh"),
						"list_ten_nhom_vthh": getMapString(child, "LIST_TEN_NHOM_VTHH", "list_ten_nhom_vthh"),
					}
					variantData = append(variantData, record)
				}
				data = variantData
			}
		} else {
			// Query for single SKU
			if usePostMethod {
				bodyPayload := map[string]interface{}{
					"limit": limit,
				}
				if inventoryEndpoint == "inventory_receipt/search" {
					bodyPayload["keyword"] = search
				} else {
					bodyPayload["MA_HANG"] = search
				}
				data, err = client.SearchCustomEndpointWithBody(inventoryEndpoint, bodyPayload)
			} else {
				params := map[string]string{
					"limit": strconv.Itoa(limit),
				}
				if inventoryEndpoint == "inventory_receipt/search" {
					params["keyword"] = search
				} else {
					params["MA_HANG"] = search
				}
				data, err = client.SearchCustomEndpoint(inventoryEndpoint, params)
			}

			if err != nil {
				errMsg := err.Error()
				if strings.Contains(errMsg, "HTTP 400") && (strings.Contains(errMsg, "Không tìm thấy hàng") || strings.Contains(errMsg, "Không tìm thấy")) {
					log.Printf("[inventory_query] overriding Cloudify 400 'product not found' error to success empty list for search: %s", search)
					err = nil
					data = []map[string]interface{}{}
				}
			}

			// Normalize stock keys in response for custom inventory endpoints
			if err == nil {
				for i, item := range data {
					if _, hasTon := item["ton_kho"]; !hasTon {
						stockVal := getMapFloat(item, "stock", "ton", "ton_kho", "SO_LUONG_TON_KHA_DUNG", "so_luong_ton_kha_dung", "SO_LUONG_TON_TONG", "so_luong_ton_tong")
						data[i]["ton_kho"] = stockVal
						data[i]["TON_KHO"] = stockVal
						data[i]["stock"] = stockVal
					}
					// Inject standard product keys for compatibility
					sku := getMapString(item, "MA_HANG", "ma_hang", "MA", "ma", "code", "product_code")
					if sku != "" {
						data[i]["MA"] = sku
						data[i]["ma"] = sku
						data[i]["code"] = sku
						data[i]["ma_hang"] = sku
						data[i]["product_code"] = sku
					}
					name := getMapString(item, "TEN_HANG", "ten_hang", "TEN", "ten", "name")
					if name != "" {
						data[i]["TEN"] = name
						data[i]["ten"] = name
						data[i]["name"] = name
						data[i]["ten_hang"] = name
					}
				}
			}
		}
	case "orders":
		if isGenericOrderSearch(search) {
			promptMsg := gin.H{
				"recipient": gin.H{
					"user_id": permCtx.ZaloUserID,
				},
				"message": gin.H{
					"text": "Bạn muốn xem các đơn hàng phát sinh trong khoảng thời gian nào dưới đây?",
					"attachment": gin.H{
						"type": "template",
						"payload": gin.H{
							"buttons": []gin.H{
								{
									"title":   "3 ngày gần đây",
									"type":    "oa.query.show",
									"payload": "đơn hàng 3 ngày gần đây",
								},
								{
									"title":   "5 ngày gần đây",
									"type":    "oa.query.show",
									"payload": "đơn hàng 5 ngày gần đây",
								},
								{
									"title":   "7 ngày gần đây",
									"type":    "oa.query.show",
									"payload": "đơn hàng 7 ngày gần đây",
								},
							},
						},
					},
				},
			}
			c.JSON(http.StatusOK, gin.H{
				"status":            "success",
				"is_orders_prompt":  true,
				"zalo_rich_message": promptMsg,
			})
			return
		}

		ordersEndpoint := "sale_document/search" // default
		var globalPermsSetting models.AppSetting
		if errSetting := db.DB.Where("tenant_id = ? AND setting_key = 'erp_global_method_permissions'", tenantID).First(&globalPermsSetting).Error; errSetting == nil && globalPermsSetting.ValuePlain != "" {
			type EndpointConfig struct {
				Get  bool   `json:"get"`
				Post bool   `json:"post"`
				Path string `json:"path"`
			}
			var globalPerms map[string]EndpointConfig
			if errUnmarshal := json.Unmarshal([]byte(globalPermsSetting.ValuePlain), &globalPerms); errUnmarshal == nil {
				if orderConfig, exists := globalPerms["orders"]; exists && orderConfig.Path != "" && orderConfig.Path != "orders" {
					path := orderConfig.Path
					path = strings.TrimPrefix(path, "/")
					path = strings.TrimPrefix(path, "rest_api/private/")
					path = strings.TrimPrefix(path, "/")
					ordersEndpoint = path
				}
			}
		}

		var allowedCodes []string
		if scopeType == "assigned" {
			var groupIDs []string
			for _, grp := range permCtx.Groups {
				groupIDs = append(groupIDs, grp.GroupID)
			}
			allowedCodes, _ = resolveGroupCustomerCodes(tenantID, groupIDs)
		}

		days := parseDaysFromSearch(search)
		if days > 0 {
			tuNgay := formatDate(time.Now().AddDate(0, 0, -days))
			denNgay := formatDate(time.Now())
			params := map[string]string{
				"TU_NGAY":  tuNgay,
				"DEN_NGAY": denNgay,
				"limit":    "200",
			}
			data, err = client.SearchCustomEndpoint(ordersEndpoint, params)
			if err == nil {
				var filteredData []map[string]interface{}
				for _, item := range data {
					itemCustCode := getMapString(item, "MA_KH", "ma_kh", "MA_DT", "ma_dt", "customer_code", "partner_code")

					// Filter by customer code match based on scope
					if scopeType == "own" && !strings.EqualFold(itemCustCode, permCtx.CustomerCode) {
						continue
					}
					if scopeType == "assigned" {
						matched := false
						for _, ac := range allowedCodes {
							if strings.EqualFold(ac, itemCustCode) {
								matched = true
								break
							}
						}
						if !matched {
							continue
						}
					}

					record := map[string]interface{}{
						"order_id":              getMapString(item, "MA_SO", "ma_so", "MA", "ma", "order_id", "name"),
						"customer_name":         getMapString(item, "TEN_KH", "ten_kh", "TEN_DT", "ten_dt", "customer_name"),
						"customer_code":         itemCustCode,
						"status":                getMapString(item, "TRANG_THAI", "trang_thai", "status"),
						"trang_thai":            getMapString(item, "TRANG_THAI", "trang_thai"),
						"ghi_chu":               getMapString(item, "GHI_CHU", "ghi_chu"),
						"don_dat_hang_chi_tiet": item["DON_DAT_HANG_CHI_TIET"],
						"total":                 getMapFloat(item, "TONG_TIEN", "tong_tien", "total"),
						"date":                  getMapString(item, "NGAY_LAP", "ngay_lap", "date"),
					}
					filteredData = append(filteredData, record)
				}
				data = filteredData
			}
		} else {
			params := map[string]string{
				"keyword": search,
				"limit":   strconv.Itoa(limit),
			}
			if partnerID != "" {
				params["partner_id"] = partnerID
			}
			data, err = client.SearchCustomEndpoint(ordersEndpoint, params)
		}
	case "customers":
		if scopeType == "own" {
			// 1. Get customer code from permission context
			customerCode := permCtx.CustomerCode
			if customerCode == "" && permCtx.ZaloUserID != "" {
				// Query zalo_customers for customerCode
				var customerRec models.ZaloCustomer
				if err := db.DB.Where("tenant_id = ? AND zalo_user_id = ? AND status = ?", tenantID, permCtx.ZaloUserID, "approved").First(&customerRec).Error; err == nil {
					customerCode = customerRec.CustomerCode
				}
			}

			if customerCode != "" {
				cfg, _ := config.Load()
				// 2. Query Postgres database to get ten_khach_hang
				tenKhachHang, errName := db.GetCloudifyCustomerNameByCode(cfg.PostgresURL, customerCode)
				if errName == nil && tenKhachHang != "" {
					// 3. Make API call with JSON body: {"name": tenKhachHang, "limit": 1000}
					apiPayload := map[string]interface{}{
						"name":  tenKhachHang,
						"limit": 1000,
					}

					customPath := "partner/search"
					var globalPermsSetting models.AppSetting
					if errSetting := db.DB.Where("tenant_id = ? AND setting_key = 'erp_global_method_permissions'", tenantID).First(&globalPermsSetting).Error; errSetting == nil && globalPermsSetting.ValuePlain != "" {
						type EndpointConfig struct {
							Get  bool   `json:"get"`
							Post bool   `json:"post"`
							Path string `json:"path"`
						}
						var globalPerms map[string]EndpointConfig
						if json.Unmarshal([]byte(globalPermsSetting.ValuePlain), &globalPerms) == nil {
							if config, exists := globalPerms["customers"]; exists && config.Path != "" {
								customPath = strings.TrimPrefix(config.Path, "/")
								customPath = strings.TrimPrefix(customPath, "rest_api/private/")
								customPath = strings.TrimPrefix(customPath, "/")
							}
						}
					}

					apiData, errCall := client.SearchCustomEndpointWithBody(customPath, apiPayload)
					if errCall == nil {
						// 4. Process response and return only: dia_chi, dien_thoai, create_date
						var filteredData []map[string]interface{}
						for _, item := range apiData {
							maVal := getMapString(item, "MA", "ma", "name", "code")
							if strings.EqualFold(maVal, customerCode) {
								record := map[string]interface{}{
									"dia_chi":     getMapString(item, "DIA_CHI", "dia_chi", "address"),
									"dien_thoai":  getMapString(item, "DIEN_THOAI", "dien_thoai", "phone"),
									"create_date": getMapString(item, "create_date", "CREATE_DATE"),
								}
								filteredData = append(filteredData, record)
							}
						}
						data = filteredData
					} else {
						err = errCall
					}
				} else {
					if errName != nil {
						err = fmt.Errorf("failed to fetch cloudify customer name from DB: %w", errName)
					} else {
						err = fmt.Errorf("cloudify customer name is empty for code %s", customerCode)
					}
				}
			} else {
				err = fmt.Errorf("customer code is empty or unverified")
			}
		} else {
			data, err = client.SearchPartners(search, limit)
		}
	case "debt":
		if isGenericDebtSearch(search) {
			promptMsg := gin.H{
				"recipient": gin.H{
					"user_id": permCtx.ZaloUserID,
				},
				"message": gin.H{
					"text": "Bạn muốn xem đối chiếu công nợ trong khoảng thời gian nào dưới đây?",
					"attachment": gin.H{
						"type": "template",
						"payload": gin.H{
							"buttons": []gin.H{
								{
									"title":   "Tháng này",
									"type":    "oa.query.show",
									"payload": "công nợ tháng này",
								},
								{
									"title":   "Tháng trước",
									"type":    "oa.query.show",
									"payload": "công nợ tháng trước",
								},
								{
									"title":   "Quý này",
									"type":    "oa.query.show",
									"payload": "công nợ quý này",
								},
							},
						},
					},
				},
			}
			c.JSON(http.StatusOK, gin.H{
				"status":            "success",
				"is_debt_prompt":    true,
				"zalo_rich_message": promptMsg,
			})
			return
		}

		var targetCustomerCodes []string
		if scopeType == "own" {
			if permCtx.CustomerCode != "" {
				targetCustomerCodes = []string{permCtx.CustomerCode}
			}
		} else {
			if partnerID != "" {
				code, errCode := resolveCustomerCodeFromPartnerID(client, partnerID)
				if errCode == nil && code != "" {
					targetCustomerCodes = []string{code}
				}
			} else if search != "" {
				_, _, isPeriod := parseDebtPeriodFromSearch(search)
				if !isPeriod {
					partners, errPartners := client.SearchPartners(search, 5)
					if errPartners == nil {
						for _, p := range partners {
							maVal := getMapString(p, "MA", "code", "ma")
							if maVal != "" {
								targetCustomerCodes = append(targetCustomerCodes, maVal)
							}
						}
					}
				}
			}

			if len(targetCustomerCodes) == 0 && scopeType == "assigned" {
				var groupIDs []string
				for _, grp := range permCtx.Groups {
					groupIDs = append(groupIDs, grp.GroupID)
				}
				targetCustomerCodes, _ = resolveGroupCustomerCodes(tenantID, groupIDs)
			}
		}

		tuNgay, denNgay, _ := parseDebtPeriodFromSearch(search)
		if tuNgay == "" || denNgay == "" {
			tuNgay, denNgay, _ = parseDebtPeriodFromSearch("công nợ tháng này")
		}

		branchName := "BBI NỘI BỘ"
		var branchSetting models.AppSetting
		if errSetting := db.DB.Where("tenant_id = ? AND setting_key = 'erp_branch_name'", tenantID).First(&branchSetting).Error; errSetting == nil && branchSetting.ValuePlain != "" {
			branchName = branchSetting.ValuePlain
		}

		debtEndpoint := "partner_ledger/search"
		usePost := false
		var globalPermsSetting models.AppSetting
		if errSetting := db.DB.Where("tenant_id = ? AND setting_key = 'erp_global_method_permissions'", tenantID).First(&globalPermsSetting).Error; errSetting == nil && globalPermsSetting.ValuePlain != "" {
			type EndpointConfig struct {
				Get  bool   `json:"get"`
				Post bool   `json:"post"`
				Path string `json:"path"`
			}
			var globalPerms map[string]EndpointConfig
			if errUnmarshal := json.Unmarshal([]byte(globalPermsSetting.ValuePlain), &globalPerms); errUnmarshal == nil {
				if debtConfig, exists := globalPerms["debt"]; exists {
					usePost = debtConfig.Post
					if debtConfig.Path != "" && debtConfig.Path != "debt" {
						path := debtConfig.Path
						path = strings.TrimPrefix(path, "/")
						path = strings.TrimPrefix(path, "rest_api/private/")
						path = strings.TrimPrefix(path, "/")
						debtEndpoint = path
					}
				}
			}
		}

		dsKhachHang := strings.Join(targetCustomerCodes, ",")

		if usePost {
			bodyPayload := map[string]interface{}{
				"CHI_NHANH":                           branchName,
				"TU_NGAY":                             tuNgay,
				"DEN_NGAY":                            denNgay,
				"BAO_GOM_SO_LIEU_CHI_NHANH_PHU_THUOC": true,
				"DS_KHACH_HANG":                       dsKhachHang,
			}
			data, err = client.SearchCustomEndpointWithBody(debtEndpoint, bodyPayload)
		} else {
			params := map[string]string{
				"CHI_NHANH":                           branchName,
				"TU_NGAY":                             tuNgay,
				"DEN_NGAY":                            denNgay,
				"BAO_GOM_SO_LIEU_CHI_NHANH_PHU_THUOC": "true",
				"DS_KHACH_HANG":                       dsKhachHang,
			}
			data, err = client.SearchCustomEndpoint(debtEndpoint, params)
		}

		if err == nil && data != nil {
			var mappedData []map[string]interface{}
			for _, item := range data {
				noDuCuoiKy := getMapFloat(item, "NO_SO_DU_CUOI_KY", "no_so_du_cuoi_ky", "NO_SAU", "no_sau")
				mappedItem := map[string]interface{}{
					"MA_KHACH_HANG":    getMapString(item, "MA_KHACH_HANG", "ma_khach_hang", "MA_KH", "ma_kh"),
					"TEN_KHACH_HANG":   getMapString(item, "TEN_KHACH_HANG", "ten_khach_hang", "TEN_KH", "ten_kh"),
					"NO_SO_DU_CUOI_KY": noDuCuoiKy,
					"no_so_du_cuoi_ky": noDuCuoiKy,
				}
				for k, v := range item {
					if k != "MA_KHACH_HANG" && k != "TEN_KHACH_HANG" && k != "NO_SO_DU_CUOI_KY" && k != "no_so_du_cuoi_ky" {
						mappedItem[k] = v
					}
				}
				mappedData = append(mappedData, mappedItem)
			}
			data = mappedData
		}
	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": "unknown_resource"})
		writeAuditLog(tenantID, permCtx, resource, scopeType, productGroups, search, http.StatusBadRequest, 0, c.ClientIP())
		return
	}

	if err != nil {
		log.Printf("[erp_query_error] tenant=%s resource=%s search=%s error=%v", tenantID, resource, search, err)
		c.JSON(http.StatusBadGateway, gin.H{
			"error":   "erp_upstream_error",
			"message": fmt.Sprintf("Không thể lấy dữ liệu từ Cloudify ERP: %s", err.Error()),
		})
		writeAuditLog(tenantID, permCtx, resource, scopeType, productGroups, search, http.StatusBadGateway, 0, c.ClientIP())
		return
	}

	if data == nil {
		data = []map[string]interface{}{}
	}

	if resource == "products" || resource == "inventory" {
		if resource == "inventory" {
			for i, p := range data {
				sku := getMapString(p, "code", "ma_hang", "ma", "product_code", "MA_HANG", "MA")
				group := getProductGroupFromAstra(c.Request.Context(), tenantID, sku)
				data[i]["list_ten_nhom_vthh"] = group
			}
		}
		data = filterProductsByGroups(data, productGroups)
	}

	if resource == "customers" && permCtx.AgentType != "private" && scopeType == "assigned" {
		var groupIDs []string
		for _, grp := range permCtx.Groups {
			groupIDs = append(groupIDs, grp.GroupID)
		}
		allowedCodes, _ := resolveGroupCustomerCodes(tenantID, groupIDs)

		var filteredCustomers []map[string]interface{}
		for _, cust := range data {
			codeVal := getMapString(cust, "MA", "code", "ma")
			codeLower := strings.ToLower(codeVal)

			matched := false
			for _, ac := range allowedCodes {
				if strings.ToLower(ac) == codeLower {
					matched = true
					break
				}
			}
			if matched {
				filteredCustomers = append(filteredCustomers, cust)
			}
		}
		data = filteredCustomers
	}

	writeAuditLog(tenantID, permCtx, resource, scopeType, productGroups, search, http.StatusOK, len(data), c.ClientIP())

	c.JSON(http.StatusOK, gin.H{
		"status":   "success",
		"data":     data,
		"source":   "cloudify_live",
		"resource": resource,
		"count":    len(data),
	})
}

func respondWithMockDataV2(c *gin.Context, resource, search string, limit int, allowedGroups []string, scopeType string, customerCode string, tenantID string, groups []engine.GroupPermission, zaloUserID string) {
	searchLower := strings.ToLower(search)

	isGroupAllowed := func(groupName string) bool {
		if len(allowedGroups) == 0 {
			return true
		}
		gLower := strings.ToLower(groupName)
		for _, allowed := range allowedGroups {
			if strings.Contains(gLower, allowed) || strings.Contains(allowed, gLower) {
				return true
			}
		}
		return false
	}

	var allowedCodes []string
	var groupIDs []string
	for _, gp := range groups {
		groupIDs = append(groupIDs, gp.GroupID)
	}
	if scopeType == "assigned" {
		allowedCodes, _ = resolveGroupCustomerCodes(tenantID, groupIDs)
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
		}
		var filtered []gin.H
		for _, p := range allProducts {
			name := strings.ToLower(p["name"].(string))
			code := strings.ToLower(p["code"].(string))
			if search != "" && !strings.Contains(name, searchLower) && !strings.Contains(code, searchLower) {
				continue
			}
			if !isGroupAllowed(p["group"].(string)) {
				continue
			}
			filtered = append(filtered, p)
			if len(filtered) >= limit {
				break
			}
		}
		c.JSON(http.StatusOK, gin.H{"status": "success", "data": filtered, "source": "mock_erp", "count": len(filtered)})

	case "inventory":
		allInventory := []gin.H{
			{"code": "SP001", "name": "Nguyên Đầu Bò Mỹ", "group": "Nguyên Đầu", "stock": 45.5, "unit": "kg", "warehouse": "Kho Lạnh Quận 7"},
			{"code": "SP002", "name": "Nguyên Đầu Heo Tươi", "group": "Nguyên Đầu", "stock": 120.0, "unit": "kg", "warehouse": "Kho Lạnh Quận 7"},
			{"code": "SP003", "name": "Nửa Đầu Bò Úc", "group": "Nửa Đầu", "stock": 12.0, "unit": "kg", "warehouse": "Kho Lạnh Bình Tân"},
			{"code": "SP005", "name": "Ba Chỉ Bò Cuộn", "group": "Thịt Bò", "stock": 350.0, "unit": "khay", "warehouse": "Kho Lạnh Quận 7"},
		}
		var filtered []gin.H
		for _, item := range allInventory {
			name := strings.ToLower(item["name"].(string))
			code := strings.ToLower(item["code"].(string))
			if search != "" && !strings.Contains(name, searchLower) && !strings.Contains(code, searchLower) {
				continue
			}
			if !isGroupAllowed(item["group"].(string)) {
				continue
			}
			filtered = append(filtered, item)
			if len(filtered) >= limit {
				break
			}
		}
		c.JSON(http.StatusOK, gin.H{"status": "success", "data": filtered, "source": "mock_erp", "count": len(filtered)})

	case "orders":
		if isGenericOrderSearch(search) {
			promptMsg := gin.H{
				"recipient": gin.H{
					"user_id": zaloUserID,
				},
				"message": gin.H{
					"text": "Bạn muốn xem các đơn hàng phát sinh trong khoảng thời gian nào dưới đây?",
					"attachment": gin.H{
						"type": "template",
						"payload": gin.H{
							"buttons": []gin.H{
								{
									"title":   "3 ngày gần đây",
									"type":    "oa.query.show",
									"payload": "đơn hàng 3 ngày gần đây",
								},
								{
									"title":   "5 ngày gần đây",
									"type":    "oa.query.show",
									"payload": "đơn hàng 5 ngày gần đây",
								},
								{
									"title":   "7 ngày gần đây",
									"type":    "oa.query.show",
									"payload": "đơn hàng 7 ngày gần đây",
								},
							},
						},
					},
				},
			}
			c.JSON(http.StatusOK, gin.H{
				"status":            "success",
				"is_orders_prompt":  true,
				"zalo_rich_message": promptMsg,
			})
			return
		}

		allOrders := []gin.H{
			{"order_id": "ORD-2026-001", "customer_name": "Nguyễn Văn A", "customer_code": "CUST-001", "status": "Đang giao hàng", "total": 560000, "date": time.Now().AddDate(0, 0, -1).Format("2006-01-02 15:04:05")},
			{"order_id": "ORD-2026-002", "customer_name": "Trần Thị B", "customer_code": "CUST-002", "status": "Đã hoàn thành", "total": 700000, "date": time.Now().AddDate(0, 0, -4).Format("2006-01-02 15:04:05")},
			{"order_id": "ORD-2026-003", "customer_name": "Lê Văn C", "customer_code": "CUST-003", "status": "Đã hoàn thành", "total": 1200000, "date": time.Now().AddDate(0, 0, -6).Format("2006-01-02 15:04:05")},
		}
		var filtered []gin.H
		days := parseDaysFromSearch(search)
		var cutoff time.Time
		if days > 0 {
			cutoff = time.Now().AddDate(0, 0, -days)
		}

		for _, o := range allOrders {
			id := strings.ToLower(o["order_id"].(string))
			cust := strings.ToLower(o["customer_name"].(string))
			code := strings.ToLower(o["customer_code"].(string))

			if scopeType == "own" && !strings.EqualFold(code, customerCode) {
				continue
			}
			if scopeType == "assigned" {
				matched := false
				for _, ac := range allowedCodes {
					if strings.EqualFold(ac, code) {
						matched = true
						break
					}
				}
				if !matched {
					continue
				}
			}

			if days > 0 {
				orderDateStr := o["date"].(string)
				orderDate, _ := time.Parse("2006-01-02 15:04:05", orderDateStr)
				if !orderDate.After(cutoff) {
					continue
				}
			} else {
				if search != "" && !strings.Contains(id, searchLower) && !strings.Contains(cust, searchLower) {
					continue
				}
			}

			filtered = append(filtered, o)
			if len(filtered) >= limit {
				break
			}
		}
		c.JSON(http.StatusOK, gin.H{"status": "success", "data": filtered, "source": "mock_erp", "count": len(filtered)})

	case "customers":
		allCustomers := []gin.H{
			{"customer_id": "CUST-001", "name": "Nguyễn Văn A", "customer_code": "CUST-001", "phone": "0901234567", "tier": "Gold"},
			{"customer_id": "CUST-002", "name": "Trần Thị B", "customer_code": "CUST-002", "phone": "0987654321", "tier": "Platinum"},
		}
		var filtered []gin.H
		for _, cust := range allCustomers {
			name := strings.ToLower(cust["name"].(string))
			phone := cust["phone"].(string)
			code := strings.ToLower(cust["customer_code"].(string))

			if scopeType == "own" {
				if !strings.EqualFold(code, customerCode) {
					continue
				}
				record := gin.H{
					"dia_chi":     "MM18 Trường sơn, P.15, Q10, TP HCM",
					"dien_thoai":  phone,
					"create_date": "2024-05-15 08:30:00",
				}
				filtered = append(filtered, record)
				break
			}

			if scopeType == "assigned" {
				matched := false
				for _, ac := range allowedCodes {
					if strings.EqualFold(ac, code) {
						matched = true
						break
					}
				}
				if !matched {
					continue
				}
			}

			if search != "" && !strings.Contains(name, searchLower) && !strings.Contains(phone, searchLower) {
				continue
			}
			filtered = append(filtered, cust)
			if len(filtered) >= limit {
				break
			}
		}
		c.JSON(http.StatusOK, gin.H{"status": "success", "data": filtered, "source": "mock_erp", "count": len(filtered)})

	case "debt":
		tuNgay, denNgay, _ := parseDebtPeriodFromSearch(search)
		if tuNgay == "" || denNgay == "" {
			tuNgay, denNgay, _ = parseDebtPeriodFromSearch("công nợ tháng này")
		}

		mockData := []map[string]interface{}{
			{
				"MA_KHACH_HANG":             "BBI - Đoàn Lâm Khải",
				"TEN_KHACH_HANG":            "BBI - Đoàn Lâm Khải",
				"TAI_KHOAN_CONG_NO":         "131",
				"NO_SO_DU_DAU_KY":           1050000.0,
				"CO_SO_DU_DAU_KY":           0.0,
				"NO_SO_PHAT_SINH":           0.0,
				"CO_SO_PHAT_SINH":           0.0,
				"NO_SO_DU_CUOI_KY":          1050000.0,
				"CO_SO_DU_CUOI_KY":          0.0,
				"NO_TRUOC":                  1050000.0,
				"NO_TRONG":                  0.0,
				"NO_SAU":                    1050000.0,
				"tu_ngay":                   tuNgay,
				"den_ngay":                  denNgay,
				"NO_SO_DU_DAU_KY_QUY_DOI":   1050000.0,
				"CO_SO_DU_DAU_KY_QUY_DOI":   0.0,
				"NO_SO_QUY_DOI":             0.0,
				"CO_SO_QUY_DOI":             0.0,
				"NO_SO_DU_CUOI_KY_QUY_DOI":  1050000.0,
				"CO_SO_DU_CUOI_KY_QUY_DOI":  0.0,
				"NO_SO_DU_DAU_KY_NGUYEN_TE": 1050000.0,
				"CO_SO_DU_DAU_KY_NGUYEN_TE": 0.0,
				"NO_SO_PHAT_SINH_NGUYEN_TE": 0.0,
				"CO_SO_PHAT_SINH_NGUYEN_TE": 0.0,
				"NO_SO_DU_CUOI_KY_NGUYEN_TE": 1050000.0,
				"CO_SO_DU_CUOI_KY_NGUYEN_TE": 0.0,
			},
			{
				"MA_KHACH_HANG":             "BBI - Nguyễn Đăng Khoa",
				"TEN_KHACH_HANG":            "BBI - Nguyễn Đăng Khoa",
				"TAI_KHOAN_CONG_NO":         "131",
				"NO_SO_DU_DAU_KY":           630000.0,
				"CO_SO_DU_DAU_KY":           0.0,
				"NO_SO_PHAT_SINH":           0.0,
				"CO_SO_PHAT_SINH":           0.0,
				"NO_SO_DU_CUOI_KY":          630000.0,
				"CO_SO_DU_CUOI_KY":          0.0,
				"NO_TRUOC":                  630000.0,
				"NO_TRONG":                  0.0,
				"NO_SAU":                    630000.0,
				"tu_ngay":                   tuNgay,
				"den_ngay":                  denNgay,
				"NO_SO_DU_DAU_KY_QUY_DOI":   630000.0,
				"CO_SO_DU_DAU_KY_QUY_DOI":   0.0,
				"NO_SO_QUY_DOI":             0.0,
				"CO_SO_QUY_DOI":             0.0,
				"NO_SO_DU_CUOI_KY_QUY_DOI":  630000.0,
				"CO_SO_DU_CUOI_KY_QUY_DOI":  0.0,
				"NO_SO_DU_DAU_KY_NGUYEN_TE": 630000.0,
				"CO_SO_DU_DAU_KY_NGUYEN_TE": 0.0,
				"NO_SO_PHAT_SINH_NGUYEN_TE": 0.0,
				"CO_SO_PHAT_SINH_NGUYEN_TE": 0.0,
				"NO_SO_DU_CUOI_KY_NGUYEN_TE": 630000.0,
				"CO_SO_DU_CUOI_KY_NGUYEN_TE": 0.0,
			},
		}

		var filtered []map[string]interface{}
		for _, item := range mockData {
			itemCustCode := item["MA_KHACH_HANG"].(string)
			if scopeType == "own" && !strings.Contains(strings.ToLower(itemCustCode), strings.ToLower(customerCode)) {
				continue
			}
			filtered = append(filtered, item)
		}

		c.JSON(http.StatusOK, gin.H{
			"status":   "success",
			"data":     filtered,
			"source":   "mock_erp",
			"count":    len(filtered),
		})

	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": "unknown_resource"})
	}
}

func getProductBySkuFromAstraDB(ctx context.Context, tenantID, sku string) (map[string]interface{}, error) {
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
	payload := map[string]interface{}{
		"findOne": map[string]interface{}{
			"filter": map[string]interface{}{
				"MA": sku,
			},
		},
	}
	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(bodyBytes))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Token", token)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return nil, fmt.Errorf("Astra DB HTTP status %d", resp.StatusCode)
	}

	var astraResp struct {
		Data struct {
			Document map[string]interface{} `json:"document"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&astraResp); err != nil {
		return nil, err
	}

	if len(astraResp.Errors) > 0 {
		return nil, fmt.Errorf("Astra DB error: %s", astraResp.Errors[0].Message)
	}

	return astraResp.Data.Document, nil
}

func getProductGroupFromAstra(ctx context.Context, tenantID, sku string) string {
	if sku == "" {
		return ""
	}

	// Try exact match first
	doc, err := getProductBySkuFromAstraDB(ctx, tenantID, sku)
	if err == nil && doc != nil {
		return getMapString(doc, "LIST_TEN_NHOM_VTHH", "list_ten_nhom_vthh", "group")
	}

	// Fallback to searching
	products, err := searchProductsFromAstraDB(ctx, tenantID, sku, 5)
	if err != nil || len(products) == 0 {
		return ""
	}

	for _, p := range products {
		maVal := getMapString(p, "MA", "code", "ma")
		if strings.EqualFold(maVal, sku) {
			return getMapString(p, "LIST_TEN_NHOM_VTHH", "list_ten_nhom_vthh", "group")
		}
	}

	return getMapString(products[0], "LIST_TEN_NHOM_VTHH", "list_ten_nhom_vthh", "group")
}

func getProductsByMaChaFromAstraDB(ctx context.Context, tenantID, maCha string) ([]map[string]interface{}, error) {
	var products []models.CachedProduct
	err := db.DB.WithContext(ctx).
		Where("tenant_id = ? AND ma_cha = ?", tenantID, maCha).
		Limit(100).
		Find(&products).Error
	if err != nil {
		return nil, fmt.Errorf("local MySQL cache query failed: %w", err)
	}

	var results []map[string]interface{}
	for _, p := range products {
		results = append(results, map[string]interface{}{
			"MA":                 p.MA,
			"ma":                 p.MA,
			"code":               p.MA,
			"ma_hang":            p.MA,
			"product_code":       p.MA,
			"TEN_DONG_BO_WEB":    p.TEN_DONG_BO_WEB,
			"TEN":                p.TEN,
			"ten":                p.TEN,
			"ten_hang":           p.TEN,
			"THUOC_TINH_1":       p.THUOC_TINH_1,
			"THUOC_TINH_2":       p.THUOC_TINH_2,
			"DON_GIA_BAN":        p.DON_GIA_BAN,
			"LINK_ANH":           p.LINK_ANH,
			"NHAN_HIEU_NAME":     p.NHAN_HIEU_NAME,
			"LIST_TEN_NHOM_VTHH": p.LIST_TEN_NHOM_VTHH,
			"KHO":                p.KHO,
			"MA_CHA":             p.MA_CHA,
			"ma_cha":             p.MA_CHA,
			"DVT":                p.DVT,
		})
	}
	return results, nil
}

func detectMaChaFromSearch(ctx context.Context, tenantID, search string, allowedGroups []string) (string, string, bool) {
	if search == "" {
		return "", "", false
	}

	// 1. Search products from Astra DB using the user's search query (fuzzy search)
	matchedProducts, err := searchProductsFromAstraDB(ctx, tenantID, search, 50)
	if err != nil || len(matchedProducts) == 0 {
		return "", "", false
	}

	// Filter matched products based on allowed product groups
	matchedProducts = filterProductsByGroups(matchedProducts, allowedGroups)
	if len(matchedProducts) == 0 {
		return "", "", false
	}

	// 2. Group the matched products by MA_CHA
	maChaCounts := make(map[string]int)
	for _, p := range matchedProducts {
		maCha := getMapString(p, "MA_CHA", "ma_cha")
		if maCha != "" {
			maChaCounts[maCha]++
		}
	}

	// Find the dominant MA_CHA
	var dominantMaCha string
	maxCount := 0
	for maCha, count := range maChaCounts {
		if count > maxCount {
			maxCount = count
			dominantMaCha = maCha
		}
	}

	// 3. If the dominant MA_CHA has multiple variants in the search results (> 1)
	if maxCount > 1 && dominantMaCha != "" {
		// Confirm it has multiple variants by fetching them
		allVariants, errVal := getProductsByMaChaFromAstraDB(ctx, tenantID, dominantMaCha)
		if errVal == nil && len(allVariants) > 1 {
			// Found the parent product! Return actual parent code (dominantMaCha)
			// and user's query as friendly name (search)
			return dominantMaCha, search, true
		}
	}

	return "", "", false
}

func isGenericOrderSearch(search string) bool {
	s := strings.ToLower(strings.TrimSpace(search))
	if s == "" || s == "đơn hàng" || s == "don hang" || s == "xem đơn hàng" || s == "xem don hang" || s == "check đơn hàng" || s == "check don hang" || s == "tra cứu đơn hàng" || s == "tra cuu don hang" || s == "đơn đặt hàng" || s == "don dat hang" {
		return true
	}
	return false
}

func parseDaysFromSearch(search string) int {
	s := strings.ToLower(search)
	if strings.Contains(s, "3 ngày") || strings.Contains(s, "3 ngay") {
		return 3
	}
	if strings.Contains(s, "5 ngày") || strings.Contains(s, "5 ngay") {
		return 5
	}
	if strings.Contains(s, "7 ngày") || strings.Contains(s, "7 ngay") || strings.Contains(s, "1 tuần") || strings.Contains(s, "1 tuan") {
		return 7
	}
	return 0
}

func formatDate(t time.Time) string {
	return t.Format("02/01/2006")
}

func parseOrderDate(item map[string]interface{}) (time.Time, bool) {
	keys := []string{"date", "create_date", "ngay_lap", "ngay_ct", "write_date", "NGAY_LAP", "NGAY_CT"}
	for _, k := range keys {
		if val, ok := item[k]; ok && val != nil {
			if str, ok := val.(string); ok && str != "" {
				// Try parsing different formats
				// Format 1: "2006-01-02 15:04:05"
				if t, err := time.Parse("2006-01-02 15:04:05", str); err == nil {
					return t, true
				}
				// Format 2: "2006-01-02T15:04:05Z"
				if t, err := time.Parse(time.RFC3339, str); err == nil {
					return t, true
				}
				// Format 3: "2006-01-02"
				if t, err := time.Parse("2006-01-02", str); err == nil {
					return t, true
				}
				// Format 4: "02/01/2006"
				if t, err := time.Parse("02/01/2006", str); err == nil {
					return t, true
				}
			}
		}
	}
	return time.Time{}, false
}

func isGenericDebtSearch(search string) bool {
	s := strings.ToLower(strings.TrimSpace(search))
	if s == "" || s == "công nợ" || s == "cong no" || s == "xem công nợ" || s == "xem cong no" || s == "check công nợ" || s == "check cong no" || s == "tra cứu công nợ" || s == "tra cuu cong no" || s == "đối chiếu công nợ" || s == "doi chieu cong no" || s == "nợ" || s == "no" {
		return true
	}
	return false
}

func parseDebtPeriodFromSearch(search string) (tuNgay, denNgay string, ok bool) {
	s := strings.ToLower(search)
	now := time.Now()

	if strings.Contains(s, "tháng này") || strings.Contains(s, "thang nay") {
		tuNgay = time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location()).Format("2006-01-02")
		denNgay = now.Format("2006-01-02")
		return tuNgay, denNgay, true
	}
	if strings.Contains(s, "tháng trước") || strings.Contains(s, "thang truoc") {
		firstOfThisMonth := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
		lastMonth := firstOfThisMonth.AddDate(0, -1, 0)
		tuNgay = time.Date(lastMonth.Year(), lastMonth.Month(), 1, 0, 0, 0, 0, now.Location()).Format("2006-01-02")
		denNgay = firstOfThisMonth.AddDate(0, 0, -1).Format("2006-01-02")
		return tuNgay, denNgay, true
	}
	if strings.Contains(s, "quý này") || strings.Contains(s, "quy nay") {
		currentMonth := int(now.Month())
		startMonth := ((currentMonth - 1) / 3) * 3 + 1
		tuNgay = time.Date(now.Year(), time.Month(startMonth), 1, 0, 0, 0, 0, now.Location()).Format("2006-01-02")
		denNgay = now.Format("2006-01-02")
		return tuNgay, denNgay, true
	}

	return "", "", false
}

func resolveCustomerCodeFromPartnerID(client *pkg.CloudifyClient, partnerID string) (string, error) {
	if partnerID == "" {
		return "", fmt.Errorf("partner ID is empty")
	}
	partners, err := client.SearchPartners(partnerID, 5)
	if err != nil {
		return "", err
	}
	for _, p := range partners {
		idVal := getMapString(p, "ID", "id")
		if idVal == partnerID {
			maVal := getMapString(p, "MA", "code", "ma")
			if maVal != "" {
				return maVal, nil
			}
		}
	}
	if len(partners) > 0 {
		maVal := getMapString(partners[0], "MA", "code", "ma")
		if maVal != "" {
			return maVal, nil
		}
	}
	return "", fmt.Errorf("partner %s not found on ERP", partnerID)
}

// getAIClient creates an AI provider client based on tenant settings.
func getAIClient(ctx context.Context, tenantID string) (ai.AIProvider, error) {
	provider := "claude"
	var providerSetting models.AppSetting
	if err := db.DB.Where("tenant_id = ? AND setting_key = 'ai_provider'", tenantID).First(&providerSetting).Error; err == nil && providerSetting.ValuePlain != "" {
		provider = providerSetting.ValuePlain
	}

	var setting models.AppSetting
	keyFound := false
	for _, key := range ai.ProviderAPIKeySettingKeys(provider) {
		if err := db.DB.Where("tenant_id = ? AND setting_key = ?", tenantID, key).First(&setting).Error; err == nil {
			keyFound = true
			break
		}
	}

	cfg, _ := config.Load()
	apiKey := ""
	if keyFound {
		if setting.ValueEncrypted != nil && len(setting.ValueEncrypted) > 0 {
			decrypted, err := pkg.Decrypt(setting.ValueEncrypted, cfg.EncryptionKey)
			if err != nil {
				return nil, fmt.Errorf("failed to decrypt API key: %w", err)
			}
			apiKey = string(decrypted)
		} else {
			apiKey = setting.ValuePlain
		}
	}

	if apiKey == "" {
		if provider == "openai" {
			apiKey = cfg.LangflowAPIKey
		}
	}

	if apiKey == "" {
		return nil, fmt.Errorf("API key not configured for provider %s", provider)
	}

	model := "claude-haiku-4-5"
	if provider == "gemini" {
		model = "gemini-2.0-flash"
	} else if provider == "openai" {
		model = "gpt-5-mini"
	}
	var modelSetting models.AppSetting
	if err := db.DB.Where("tenant_id = ? AND setting_key = 'ai_model'", tenantID).First(&modelSetting).Error; err == nil && modelSetting.ValuePlain != "" {
		model = modelSetting.ValuePlain
	}

	var baseURL string
	var baseURLSetting models.AppSetting
	if err := db.DB.Where("tenant_id = ? AND setting_key = 'ai_base_url'", tenantID).First(&baseURLSetting).Error; err == nil {
		baseURL = baseURLSetting.ValuePlain
	}

	switch provider {
	case "claude":
		return ai.NewClaudeProvider(apiKey, model, cfg.AIMaxTokens, baseURL), nil
	case "gemini":
		return ai.NewGeminiProvider(apiKey, model, baseURL), nil
	case "openai":
		return ai.NewOpenAIProvider(apiKey, model, baseURL), nil
	default:
		return nil, fmt.Errorf("unsupported AI provider: %s", provider)
	}
}

// fuzzyMatchMaChaWithLLM uses the LLM to find the best matching ma_cha
// from the local cached_products table when SQL LIKE returns no results.
func fuzzyMatchMaChaWithLLM(ctx context.Context, tenantID, keyword string) (string, error) {
	// 1. Fetch unique parent product codes (ma_cha) from cache
	var maChaList []string
	err := db.DB.Model(&models.CachedProduct{}).
		Where("tenant_id = ?", tenantID).
		Distinct("ma_cha").
		Where("ma_cha != ''").
		Pluck("ma_cha", &maChaList).Error
	if err != nil || len(maChaList) == 0 {
		return "", fmt.Errorf("no ma_cha values found: %w", err)
	}

	// 2. Create AI client
	aiClient, err := getAIClient(ctx, tenantID)
	if err != nil || aiClient == nil {
		return "", fmt.Errorf("AI client not available: %w", err)
	}

	// 3. Prompt LLM to match the keyword
	systemPrompt := fmt.Sprintf(`Bạn là trợ lý mapping mã sản phẩm.
Nhiệm vụ: Khách hàng tìm kiếm "%s". Hãy chọn MÃ SẢN PHẨM khớp nhất từ danh sách bên dưới.
Lưu ý:
- Bỏ qua dấu gạch ngang, dấu chấm, số 0 đầu khi so sánh (VD: E05 = E-5, E5 = E-5, E.5 = E-5)
- Trả lời CHỈ mã sản phẩm khớp nhất, không thêm bất kỳ từ ngữ hay dấu câu nào khác
- Nếu hoàn toàn không có mã nào khớp, trả về "NONE"

Danh sách mã sản phẩm:
%s`, keyword, strings.Join(maChaList, ", "))

	llmCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	resp, err := aiClient.AnalyzeChat(llmCtx, systemPrompt, keyword)
	if err != nil {
		return "", err
	}

	matched := strings.TrimSpace(resp.Content)
	if matched == "" || strings.EqualFold(matched, "NONE") {
		return "", nil
	}

	// Clean up markdown quotes or wrappers if any
	if strings.HasPrefix(matched, "`") && strings.HasSuffix(matched, "`") {
		matched = strings.Trim(matched, "`")
	}

	// Validate matched value exists in our list (case-insensitive)
	for _, mc := range maChaList {
		if strings.EqualFold(mc, matched) {
			return mc, nil
		}
	}

	return "", nil
}

func searchProductsByWebNameAstraDBNonVectorized(ctx context.Context, tenantID, keyword string) ([]map[string]interface{}, error) {
	var products []models.CachedProduct
	likePattern := "%" + keyword + "%"
	err := db.DB.WithContext(ctx).
		Where("tenant_id = ? AND (ten_dong_bo_web LIKE ? OR ma LIKE ? OR ten LIKE ? OR ma_cha LIKE ?)", tenantID, likePattern, likePattern, likePattern, likePattern).
		Limit(100).
		Find(&products).Error
	if err != nil {
		return nil, fmt.Errorf("local MySQL cache query failed: %w", err)
	}

	// Fallback to LLM fuzzy matching if SQL LIKE returned no results
	if len(products) == 0 {
		matchedMaCha, llmErr := fuzzyMatchMaChaWithLLM(ctx, tenantID, keyword)
		if llmErr != nil {
			log.Printf("[handler] LLM fuzzy matching failed for keyword '%s': %v", keyword, llmErr)
		} else if matchedMaCha != "" {
			log.Printf("[handler] LLM fuzzy matched keyword '%s' to ma_cha '%s'", keyword, matchedMaCha)
			// Query again using the matched parent product code
			err = db.DB.WithContext(ctx).
				Where("tenant_id = ? AND ma_cha = ?", tenantID, matchedMaCha).
				Limit(100).
				Find(&products).Error
			if err != nil {
				return nil, fmt.Errorf("local MySQL cache query (LLM fallback) failed: %w", err)
			}
		}
	}

	var results []map[string]interface{}
	for _, p := range products {
		results = append(results, map[string]interface{}{
			"MA":                 p.MA,
			"ma":                 p.MA,
			"code":               p.MA,
			"ma_hang":            p.MA,
			"product_code":       p.MA,
			"TEN_DONG_BO_WEB":    p.TEN_DONG_BO_WEB,
			"TEN":                p.TEN,
			"ten":                p.TEN,
			"ten_hang":           p.TEN,
			"THUOC_TINH_1":       p.THUOC_TINH_1,
			"THUOC_TINH_2":       p.THUOC_TINH_2,
			"DON_GIA_BAN":        p.DON_GIA_BAN,
			"LINK_ANH":           p.LINK_ANH,
			"NHAN_HIEU_NAME":     p.NHAN_HIEU_NAME,
			"LIST_TEN_NHOM_VTHH": p.LIST_TEN_NHOM_VTHH,
			"KHO":                p.KHO,
			"MA_CHA":             p.MA_CHA,
			"ma_cha":             p.MA_CHA,
			"DVT":                p.DVT,
		})
	}
	return results, nil
}

func sendProductRichMessage(c *gin.Context, tenantID, search string, maChaCounts map[string]int, permCtx *engine.GroupPermissionContext) bool {
	type maChaCount struct {
		code  string
		count int
	}
	var sortedMaChas []maChaCount
	for k, v := range maChaCounts {
		sortedMaChas = append(sortedMaChas, maChaCount{code: k, count: v})
	}
	sort.Slice(sortedMaChas, func(i, j int) bool {
		return sortedMaChas[i].count > sortedMaChas[j].count
	})

	var buttons []gin.H
	for i, mc := range sortedMaChas {
		if i >= 3 {
			break
		}
		buttons = append(buttons, gin.H{
			"title":   mc.code,
			"type":    "oa.query.hide",
			"payload": "#show_product_variants:" + mc.code,
		})
	}

	var activeChannel *models.Channel
	var allChannels []models.Channel
	if errChan := db.DB.Where("tenant_id = ? AND channel_type = ? AND is_active = true", tenantID, "zalo_oa").Find(&allChannels).Error; errChan == nil && len(allChannels) > 0 {
		activeChannel = &allChannels[0]
	}

	if activeChannel == nil || permCtx.ZaloUserID == "" {
		return false
	}

	cfg, _ := config.Load()
	credBytes, errDec := pkg.Decrypt(activeChannel.CredentialsEncrypted, cfg.EncryptionKey)
	if errDec != nil {
		return false
	}

	var zaloCreds channels.ZaloOACredentials
	if errCreds := json.Unmarshal(credBytes, &zaloCreds); errCreds != nil {
		return false
	}

	adapter := channels.NewZaloOAAdapter(zaloCreds)
	adapter.SetTokenRefreshCallback(func(newAccess, newRefresh string) {
		var ch models.Channel
		if db.DB.First(&ch, "id = ?", activeChannel.ID).Error == nil {
			credsMap := map[string]interface{}{
				"app_id":        zaloCreds.AppID,
				"app_secret":    zaloCreds.AppSecret,
				"access_token":  newAccess,
				"refresh_token": newRefresh,
				"oa_id":         zaloCreds.OAId,
			}
			newCredJSON, _ := json.Marshal(credsMap)
			encrypted, _ := pkg.Encrypt(newCredJSON, cfg.EncryptionKey)
			db.DB.Model(&ch).Update("credentials_encrypted", encrypted)
		}
	})

	promptMsg := gin.H{
		"recipient": gin.H{
			"user_id": permCtx.ZaloUserID,
		},
		"message": gin.H{
			"text": fmt.Sprintf("Tôi tìm thấy nhiều sản phẩm khớp với '%s'. Bạn muốn hỏi về dòng sản phẩm nào?", search),
			"attachment": gin.H{
				"type": "template",
				"payload": gin.H{
					"buttons": buttons,
				},
			},
		},
	}
	promptMsgJSON, _ := json.Marshal(promptMsg)

	var matchedGroup models.CRMGroup
	hasGroup := false
	if len(permCtx.Groups) > 0 {
		for _, gp := range permCtx.Groups {
			if gp.GroupID != "private_bot" {
				if errGrp := db.DB.Where("id = ? AND tenant_id = ?", gp.GroupID, tenantID).First(&matchedGroup).Error; errGrp == nil && matchedGroup.ZaloGroupID != "" {
					hasGroup = true
					break
				}
			}
		}
	}

	var sendErr error
	if hasGroup {
		sendErr = adapter.SendGroupMessage(c.Request.Context(), matchedGroup.ZaloGroupID, string(promptMsgJSON))
	} else {
		sendErr = adapter.SendMessage(c.Request.Context(), permCtx.ZaloUserID, string(promptMsgJSON))
	}

	if sendErr != nil {
		log.Printf("[erp_query] failed to send Zalo Rich Message for products: %v", sendErr)
		return false
	}

	log.Printf("[erp_query] successfully sent Zalo Rich Message for products to %s", permCtx.ZaloUserID)
	return true
}

func sanitizeSearchQuery(search string) string {
	search = strings.TrimSpace(search)
	if search == "" {
		return ""
	}
	// If it contains a separator " - ", take the part before it
	if idx := strings.Index(search, " - "); idx != -1 {
		part := strings.TrimSpace(search[:idx])
		if part != "" {
			return part
		}
	}
	return search
}


