package handlers

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/vietbui/chat-quality-agent/api/middleware"
	"github.com/vietbui/chat-quality-agent/config"
	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/db/models"
	"github.com/vietbui/chat-quality-agent/pkg"
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
		Resource   string `json:"resource" form:"resource" binding:"required"` // products|inventory|orders|customers|debt
		Search     string `json:"search" form:"search"`
		Limit      int    `json:"limit" form:"limit"`
		PartnerID  string `json:"partner_id" form:"partner_id"`    // filter orders/debt by customer Cloudify ID
		ZaloUserID string `json:"zalo_user_id" form:"zalo_user_id"` // reserved for future user-level scoping
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
	if !isResourcePermitted(tenantID, agentType, req.Resource, req.ZaloUserID) {
		c.JSON(http.StatusForbidden, gin.H{
			"error":   "forbidden_scope",
			"message": fmt.Sprintf("Quyền truy cập tài nguyên '%s' bị từ chối cho Agent hoặc khách hàng hiện tại.", req.Resource),
		})
		return
	}

	// ── 5. Load Cloudify credentials ──────────────────────────────────────
	cfg, _ := config.Load()
	erpURL, erpDB, erpLogin, erpPassword, credErr := loadCloudifyCredentials(tenantID, cfg)

	if credErr != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "credential_error", "message": credErr.Error()})
		return
	}

	// ── 6. If no credentials → fallback mock (dev only) ──────────────────
	if erpURL == "" || erpLogin == "" || erpPassword == "" {
		// Load allowed product groups for mock filtering
		var allowedGroups []string
		if agentType == "private" {
			allowedGroups = []string{}
		} else {
			allowedGroups = loadAllowedGroupsForCustomer(tenantID, req.ZaloUserID, req.Resource)
		}
		respondWithMockData(c, req.Resource, req.Search, req.Limit, allowedGroups)
		return
	}

	// ── 7. Check if resource is products and attempt cached search in Astra DB ──
	if req.Resource == "products" {
		cachedData, err := searchProductsFromAstraDB(c.Request.Context(), tenantID, req.Search, req.Limit)
		if err != nil {
			log.Printf("[erp_query] Astra DB cache search error: %v. Falling back to live Cloudify ERP.", err)
		} else if cachedData != nil {
			c.JSON(http.StatusOK, gin.H{
				"status":   "success",
				"data":     cachedData,
				"source":   "astradb_cache",
				"resource": req.Resource,
				"count":    len(cachedData),
			})
			return
		}
	}

	// ── 8. Execute live Cloudify call ─────────────────────────────────────
	client := &pkg.CloudifyClient{
		BaseURL:  erpURL,
		DB:       erpDB,
		Login:    erpLogin,
		Password: erpPassword,
	}

	respondWithLiveData(c, client, req.Resource, req.Search, req.PartnerID, req.Limit)
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

// ---------------------------------------------------------------------------
// searchProductsFromAstraDB — queries the cached product collection in Astra DB.
// Uses vector search ($vectorize) if query is non-empty, with a text-regex search fallback.
// Returns (nil, nil) if Astra DB is not configured, allowing live fallback.
// ---------------------------------------------------------------------------
func searchProductsFromAstraDB(ctx context.Context, tenantID, search string, limit int) ([]map[string]interface{}, error) {
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
		return nil, nil // Not configured, fallback to live
	}

	url := fmt.Sprintf("%s/api/json/v1/%s/%s", apiEndpoint, keyspace, collection)

	// Helper function to execute POST request to Astra DB and parse documents
	executeAstraQuery := func(payload interface{}) ([]map[string]interface{}, error) {
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

		client := &http.Client{Timeout: 10 * time.Second}
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
				Documents []map[string]interface{} `json:"documents"`
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

		return astraResp.Data.Documents, nil
	}

	var documents []map[string]interface{}
	var err error

	// A. Try Vector Search first if search query is provided
	if search != "" {
		payload := map[string]interface{}{
			"find": map[string]interface{}{
				"sort": map[string]interface{}{
					"$vectorize": search,
				},
				"options": map[string]interface{}{
					"limit": limit,
				},
			},
		}
		documents, err = executeAstraQuery(payload)
		if err != nil {
			log.Printf("[erp_search] Astra DB vector search failed for query '%s': %v. Trying text fallback...", search, err)
			// B. Fallback to regex text search over TEN, MA, TEN_DONG_BO_WEB
			regexPattern := "(?i)" + search
			payloadFallback := map[string]interface{}{
				"find": map[string]interface{}{
					"filter": map[string]interface{}{
						"$or": []map[string]interface{}{
							{"TEN": map[string]interface{}{"$regex": regexPattern}},
							{"MA": map[string]interface{}{"$regex": regexPattern}},
							{"TEN_DONG_BO_WEB": map[string]interface{}{"$regex": regexPattern}},
						},
					},
					"options": map[string]interface{}{
						"limit": limit,
					},
				},
			}
			documents, err = executeAstraQuery(payloadFallback)
			if err != nil {
				return nil, fmt.Errorf("text fallback search failed: %w", err)
			}
		}
	} else {
		// No search string, do plain find query
		payload := map[string]interface{}{
			"find": map[string]interface{}{
				"options": map[string]interface{}{
					"limit": limit,
				},
			},
		}
		documents, err = executeAstraQuery(payload)
		if err != nil {
			return nil, fmt.Errorf("plain query failed: %w", err)
		}
	}

	// 3. Map returned documents to compatibility output format
	mappedResults := make([]map[string]interface{}, 0, len(documents))
	for _, doc := range documents {
		mappedResults = append(mappedResults, mapCachedProductToAPIResponse(doc))
	}

	return mappedResults, nil
}

// mapCachedProductToAPIResponse maps a cached product document to include aliases
// for backward compatibility with chatbot prompts/HTTP Request Nodes.
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

	// Inject old keys to maintain backward compatibility
	res["ma_hang"] = maVal
	res["ten_hang"] = tenVal
	res["ten_dong_bo_web"] = webNameVal
	res["dvt_chinh_id"] = dvtVal
	res["don_gia_ban"] = priceVal

	res["code"] = maVal
	if webNameVal != "" {
		res["name"] = webNameVal
	} else {
		res["name"] = tenVal
	}
	res["unit"] = dvtVal
	res["price"] = priceVal
	res["group"] = getMapString(p, "NHAN_HIEU_NAME", "nhan_hieu_name", "list_ten_nhom_vthh", "group")

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
