package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/vietbui/chat-quality-agent/api/middleware"
	"github.com/vietbui/chat-quality-agent/config"
	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/db/models"
	"github.com/vietbui/chat-quality-agent/pkg"
)

// SaveERPSettings persists the tenant's Cloudify ERP connection plus the
// per-agent-type (public / private bot) activation, scope, and per-resource
// endpoint configuration. Secrets (password, token) are encrypted before
// storage and skipped when the incoming value is a masked placeholder, so
// re-saving the form without re-typing secrets is non-destructive.
//
// Wire format mirrors what the admin UI posts; see api/router.go for the
// route binding and the request struct below for the exact field set.
func SaveERPSettings(c *gin.Context) {
	tenantID := c.Param("tenantId")
	var req struct {
		// Shared connection
		URL      string `json:"url"`
		DBName   string `json:"db"` // Cloudify database name
		Username string `json:"username"`
		Password string `json:"password"`
		Token    string `json:"token"` // optional API token (alternative to password)

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
			Brands        string `json:"brands"`     // comma-separated brand (nhãn hiệu) filter
			AllBrands     bool   `json:"all_brands"` // true = no brand restriction
		} `json:"private_endpoints"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request", "details": err.Error()})
		return
	}

	// Validate before persisting anything: an enabled product/inventory
	// endpoint must carry a non-empty VTHH group filter, and must make an
	// explicit brand choice (AllBrands, or a non-empty brand list). Mirrors
	// SaveGroupERPEndpoints; runtime still treats an empty list as "all".
	for _, ep := range req.PrivateEndpoints {
		if ep.IsEnabled && resourceRequiresProductGroups(ep.Resource) {
			if strings.TrimSpace(ep.ProductGroups) == "" {
				c.JSON(http.StatusBadRequest, gin.H{
					"error":    "product_groups_required",
					"resource": ep.Resource,
				})
				return
			}
			if !ep.AllBrands && strings.TrimSpace(ep.Brands) == "" {
				c.JSON(http.StatusBadRequest, gin.H{
					"error":    "brands_required",
					"resource": ep.Resource,
				})
				return
			}
		}
	}

	cfg, _ := config.Load()

	// Audit: chụp giá trị cũ trước khi ghi đè để so sánh cũ→mới.
	oldURL := getPlainSetting(tenantID, "erp_api_url")
	oldDB := getPlainSetting(tenantID, "erp_api_db")
	oldUsername := getPlainSetting(tenantID, "erp_api_username")
	oldPublicActive := getPlainSetting(tenantID, "erp_public_active")
	oldPrivateActive := getPlainSetting(tenantID, "erp_private_active")
	oldMethodPerms := getPlainSetting(tenantID, "erp_global_method_permissions")
	passwordChanged := req.Password != "" && !isMaskedSecret(req.Password)
	tokenChanged := req.Token != "" && !isMaskedSecret(req.Token)

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

		// When AllBrands is set, clear any stale brand list so the stored row is
		// unambiguous ("all" + empty). Runtime treats both as "no restriction".
		brands := ep.Brands
		if ep.AllBrands {
			brands = ""
		}

		if result.Error == nil {
			db.DB.Model(&existing).Updates(map[string]interface{}{
				"is_enabled":     ep.IsEnabled,
				"scope_type":     scopeType,
				"product_groups": ep.ProductGroups,
				"brands":         brands,
				"all_brands":     ep.AllBrands,
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
				Brands:        brands,
				AllBrands:     ep.AllBrands,
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

	// ── Audit ──────────────────────────────────────────────────────────────
	uid, uemail, ip := middleware.GetUserID(c), middleware.GetUserEmail(c), c.ClientIP()

	var changed []string
	if req.URL != oldURL {
		changed = append(changed, "url")
	}
	if req.DBName != oldDB {
		changed = append(changed, "db")
	}
	if req.Username != oldUsername {
		changed = append(changed, "username")
	}
	if passwordChanged {
		changed = append(changed, "password (đã đổi)")
	}
	if tokenChanged {
		changed = append(changed, "token (đã đổi)")
	}
	if len(changed) > 0 {
		db.LogActivity(tenantID, uid, uemail, "erp.settings_updated", "settings", "erp",
			"Cập nhật kết nối ERP: "+strings.Join(changed, ", "), "", ip)
	}

	newMethodPerms := oldMethodPerms
	if req.GlobalMethodPermissions != nil {
		if b, err := json.Marshal(req.GlobalMethodPermissions); err == nil {
			newMethodPerms = string(b)
		}
	}
	if newMethodPerms != oldMethodPerms {
		db.LogActivity(tenantID, uid, uemail, "erp.method_permissions_updated", "settings", "erp_global_method_permissions",
			"Cập nhật quyền GET/POST các endpoint ERP", "", ip)
	}

	if req.PublicActive != oldPublicActive {
		db.LogActivity(tenantID, uid, uemail, "erp.toggle_active", "settings", "erp_public_active",
			fmt.Sprintf("ERP public bot: %s→%s", oldPublicActive, req.PublicActive), "", ip)
	}
	if req.PrivateActive != oldPrivateActive {
		db.LogActivity(tenantID, uid, uemail, "erp.toggle_active", "settings", "erp_private_active",
			fmt.Sprintf("ERP private bot: %s→%s", oldPrivateActive, req.PrivateActive), "", ip)
	}

	c.JSON(http.StatusOK, gin.H{"message": "saved"})
}

// TestERPConnection actually authenticates against Cloudify with the
// posted creds (falling back to the saved password when the form sends a
// masked placeholder). Returns a customer-friendly Vietnamese error on
// failure so the admin UI can render it directly.
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
