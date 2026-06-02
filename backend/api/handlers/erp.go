package handlers

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/vietbui/chat-quality-agent/channels"
	"github.com/vietbui/chat-quality-agent/config"
	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/db/models"
	"github.com/vietbui/chat-quality-agent/engine"
	"github.com/vietbui/chat-quality-agent/pkg"
	"gorm.io/gorm"
)

// resolveGlobalMethodPermission matches a requested resource against the
// tenant's global HTTP-method config (setting key erp_global_method_permissions).
// It returns the system resource key to re-route to (empty if no match), whether
// the resource is permitted for ERP gateway access, and an error if the config
// JSON is malformed — in which case the caller MUST fail closed rather than allow
// the request.
//
// The httpMethod argument is no longer used to gate access: the get/post flags
// configure how the BACKEND calls the upstream Cloudify endpoint, while the
// Langflow ERP caller always reaches /erp/query over POST. A resource is allowed
// when it is enabled under ANY method (get||post); both off means disabled.
//
// Matching order is preserved from the original inline gate: custom Path first,
// then the system resource key.
func resolveGlobalMethodPermission(configJSON, reqResource, httpMethod string) (systemResource string, allowed bool, err error) {
	type endpointConfig struct {
		Get  bool   `json:"get"`
		Post bool   `json:"post"`
		Path string `json:"path"`
	}
	var perms map[string]endpointConfig
	if e := json.Unmarshal([]byte(configJSON), &perms); e != nil {
		return "", false, e
	}

	var matched endpointConfig
	for sysRes, cfg := range perms {
		path := cfg.Path
		if path == "" {
			path = sysRes
		}
		if strings.EqualFold(path, reqResource) {
			systemResource, matched = sysRes, cfg
			break
		}
	}
	if systemResource == "" {
		for sysRes, cfg := range perms {
			if strings.EqualFold(sysRes, reqResource) {
				systemResource, matched = sysRes, cfg
				break
			}
		}
	}
	if systemResource == "" {
		// Configured but the resource is not listed → treat as blocked.
		return "", false, nil
	}

	// The get/post flags configure how the BACKEND calls the upstream Cloudify
	// endpoint (e.g. debt → th_cong_no_phai_thu/search via GET, see usePost at
	// erp.go), NOT the trusted internal hop: the Langflow ERP caller always
	// reaches /erp/query over POST. Gating that incoming POST on the upstream
	// method rejected GET-only resources like debt with forbidden_method
	// ("lỗi kết nối ERP Gateway") even though the agent was permitted to query
	// them. The gate's real job is the resource whitelist, so a resource is
	// allowed when it is enabled under ANY method; both flags off = disabled.
	return systemResource, matched.Get || matched.Post, nil
}

// methodPermissionResource maps a requested resource to the resource key used
// for permission checks. product_variants is a finer-grained read against the
// same cache as products, so it inherits the products grant — both the global
// HTTP-method gate and the scope check resolve it under products. Centralizing
// the alias here stops the two gates from drifting: the method gate previously
// lacked it and blocked every product_variants call once a tenant configured a
// global method whitelist.
func methodPermissionResource(resource string) string {
	if resource == "product_variants" {
		return "products"
	}
	return resource
}

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
		PartnerID       string `json:"partner_id" form:"partner_id"`     // filter orders/debt by customer Cloudify ID
		ZaloUserID      string `json:"zalo_user_id" form:"zalo_user_id"` // reserved for user-level scoping
		PermissionToken string `json:"permission_token" form:"permission_token"`
		ParentCode      string `json:"parent_code" form:"parent_code"`
		ExactWebName    bool   `json:"exact_web_name" form:"exact_web_name"`
		Color           string `json:"color" form:"color"`
		Size            string `json:"size" form:"size"`
		Brand           string `json:"brand" form:"brand"`
		// Intent is the Agent's price-vs-stock classification of the ORIGINAL
		// product question (price|stock; anything else folds to stock). It is
		// baked into the #stockpick_web pending-option postbacks so a later
		// numeric pick replays the captured intent verbatim — the worker no
		// longer keyword-classifies. See engine.BuildStockPickPendingButtons.
		Intent string `json:"intent" form:"intent"`
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
		// product_variants inherits the products method grant (see
		// methodPermissionResource): same cache, finer-grained read, so tenants
		// do not configure a second whitelist entry. Resolve the method gate
		// under the aliased resource but keep req.Resource = product_variants so
		// the variant dispatch (§6b) still runs.
		methodResource := methodPermissionResource(req.Resource)
		variantAlias := methodResource != req.Resource
		sysRes, ok, perr := resolveGlobalMethodPermission(globalPermsSetting.ValuePlain, methodResource, c.Request.Method)
		if perr != nil {
			// Fail closed: a configured-but-corrupt global config must not
			// silently open every method/resource. Block and log for the admin.
			log.Printf("[erp_query] malformed erp_global_method_permissions for tenant %s: %v — blocking (fail-safe)", tenantID, perr)
			methodAllowed = false
		} else {
			if sysRes != "" && !variantAlias {
				// Re-route internally to the system expected resource. Skipped
				// for the product_variants alias so §6b keeps dispatching it.
				req.Resource = sysRes
			} else if sysRes == "" {
				// Surface why an otherwise-valid resource is blocked: once a
				// global config exists it acts as a whitelist, so anything not
				// listed is denied.
				log.Printf("[erp_query] resource %q not in global method config for tenant %s — blocked", req.Resource, tenantID)
			}
			methodAllowed = ok
		}
	}
	if !methodAllowed {
		c.JSON(http.StatusForbidden, gin.H{
			"error":   "forbidden_method",
			"message": fmt.Sprintf("Yêu cầu không hợp lệ hoặc HTTP Method %s không được cho phép đối với tài nguyên '%s' trên hệ thống ERP Gateway.", c.Request.Method, req.Resource),
		})
		return
	}

	// Enforce permitted resources & scope. product_variants inherits the
	// products grant via methodPermissionResource (same cache, finer read).
	allowed, scopeType, productGroups := permCtx.IsResourceAllowed(methodPermissionResource(req.Resource))
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

	// ── 6. Products are served from local cache only ──────────────────────
	if req.Resource == "products" {
		// Exact-match path: agent already resolved a specific web name from
		// the disambiguation history and is asking the backend NOT to push
		// the option list again (which would loop on prefix-overlapping web
		// names like "FF901" vs "FF901 Carbon").
		if req.ExactWebName && strings.TrimSpace(req.Search) != "" {
			search := strings.TrimSpace(req.Search)
			cachedData, err := searchProductsByExactWebNameFromCache(c.Request.Context(), tenantID, search, req.Limit)
			if err != nil {
				log.Printf("[erp_query] exact web-name search error: %v", err)
				c.JSON(http.StatusInternalServerError, gin.H{
					"error":   "product_cache_error",
					"message": fmt.Sprintf("Không thể lấy dữ liệu sản phẩm từ cache: %s", err.Error()),
				})
				writeAuditLog(tenantID, permCtx, req.Resource, scopeType, productGroups, search, http.StatusInternalServerError, 0, c.ClientIP())
				return
			}
			if cachedData == nil {
				cachedData = []map[string]interface{}{}
			}

			if os.Getenv("DEBUG_PUSH_FALLBACK_TO_ZALO") == "true" {
				go func(s string, payload []map[string]interface{}) {
					ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
					defer cancel()
					pushFallbackPayloadToZaloOA(ctx, tenantID, "exact_web", s, payload, permCtx)
				}(search, cachedData)
			}

			filteredCached := filterProductsByGroups(cachedData, productGroups)
			filteredCached = enrichProductsWithPriceRanges(c.Request.Context(), filteredCached, productGroups, func(ctx context.Context, maCha string) ([]map[string]interface{}, error) {
				return getProductsByMaChaFromCache(ctx, tenantID, maCha)
			})
			slim := slimProductsForLLM(filteredCached)
			writeAuditLog(tenantID, permCtx, req.Resource, scopeType, productGroups, search, http.StatusOK, len(slim), c.ClientIP())
			c.JSON(http.StatusOK, gin.H{
				"status":   "success",
				"data":     slim,
				"source":   "astradb_cache_exact_web",
				"resource": req.Resource,
				"count":    len(slim),
			})
			return
		}

		// Resolve product rows via B-driven flow:
		//   - B (searchProductWebGroupsFromCache) probes via LIKE web/ten
		//   - >1 group → disambiguation list, return
		//   - 1 group  → use B's ma_cha directly, no re-LIKE, no LLM
		//   - 0 group  → LLM fuzzy (name-aware, see fuzzyMatchMaChaWithLLM)
		// Empty search → return empty + source=empty_search_use_knowledge so
		// the agent falls back to the knowledge base (no catalog dump).
		var cachedData []map[string]interface{}
		// resolvedParent is the single product line (ma_cha) this search pinned
		// down — set by the LIKE single-group path or the fuzzy fallback. It
		// feeds the variant price-pivot below (shouldPivotToVariant).
		var resolvedParent string
		search := strings.TrimSpace(req.Search)

		if search != "" {
			webGroups, err := searchProductWebGroupsFromCache(c.Request.Context(), tenantID, req.Search, productGroups)
			if err != nil {
				log.Printf("[erp_query] product web-group search error: %v", err)
				webGroups = nil
			}

			if len(webGroups) > 1 {
				groupPayloads := make([]map[string]interface{}, 0, len(webGroups))
				for _, g := range webGroups {
					groupPayloads = append(groupPayloads, map[string]interface{}{
						"web_name":     g.WebName,
						"parent_codes": g.ParentCodes,
						// variant_count = số biến thể (màu×size) thuộc dòng này, KHÔNG phải tồn kho.
						// Đổi tên từ "count" để LLM không nhầm với SL tồn khi đọc danh sách disambiguation.
						"variant_count": g.Count,
						"is_fallback":   g.IsFallback,
					})
				}

				if os.Getenv("DEBUG_PUSH_FALLBACK_TO_ZALO") == "true" {
					go func(search string, payload []map[string]interface{}) {
						ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
						defer cancel()
						pushFallbackPayloadToZaloOA(ctx, tenantID, "raw_like_groups", search, payload, permCtx)
					}(req.Search, groupPayloads)
				}

				// Persist the picks under the shared worker session key so a later
				// bare-number reply ("1"/"2") is intercepted by the worker and routed
				// deterministically to the exact-web stock picker — no Agent
				// round-trip. This removes the LLM from the disambiguation pick loop,
				// which had been dropping exact_web_name=true (→ LIKE prefix-collision
				// re-disambiguation) and the [RICH_MESSAGE_SENT] sentinel (→ duplicate
				// prose). Order matches groupPayloads, i.e. the numbered list shown.
				webNames := make([]string, 0, len(webGroups))
				for _, g := range webGroups {
					webNames = append(webNames, g.WebName)
				}
				storePendingDisambiguationOptions(c.Request.Context(), tenantID, permCtx, engine.BuildStockPickPendingButtons(webNames, req.Intent))

				writeAuditLog(tenantID, permCtx, req.Resource, scopeType, productGroups, req.Search, http.StatusOK, len(groupPayloads), c.ClientIP())
				c.JSON(http.StatusOK, gin.H{
					"status":   "success",
					"data":     groupPayloads,
					"source":   "astradb_cache_web_groups",
					"resource": req.Resource,
					"count":    len(groupPayloads),
				})
				return
			}

			if len(webGroups) == 1 && len(webGroups[0].ParentCodes) > 0 {
				// B already pinpointed the parent SKU(s). Pull rows directly.
				if len(webGroups[0].ParentCodes) == 1 {
					resolvedParent = webGroups[0].ParentCodes[0]
				}
				for _, pc := range webGroups[0].ParentCodes {
					rows, fetchErr := getProductsByMaChaFromCache(c.Request.Context(), tenantID, pc)
					if fetchErr != nil {
						log.Printf("[erp_query] fetch by ma_cha=%s failed: %v", pc, fetchErr)
						continue
					}
					cachedData = append(cachedData, rows...)
				}
			} else {
				// B proved LIKE returns nothing. Resolve the product *family*
				// (ma_cha) via embedding fuzzy first (cheaper + robust against
				// name variations); fall back to the LLM-list matcher when
				// disabled, unavailable, or below the similarity threshold.
				//
				// products intentionally resolves at the FAMILY level only and
				// returns every variant so price_range covers the whole line.
				// Pinpointing one SKU from a color/size query (e.g. "FF800 trắng
				// L") is the job of the product_variants resource (erp_variants.go),
				// where the agent routes SPECIFIC-VARIANT intent. Keeping pinpoint
				// out of products avoids a duplicate hybrid-search engine and a
				// second Astra round trip when a color/size query lands here.
				// Resolve the family code via the shared embedding→LLM matcher
				// (resolveMaChaFuzzy). The same helper backs the inventory
				// web-name fallback, so both resources resolve identically and a
				// single env toggle (ERP_EMBEDDING_FUZZY_ENABLED) governs both.
				// This fuzzy step resolves the family code only — it does NOT pinpoint
				// a SKU. For a vague/stock query the family price_range is the answer;
				// for a variant PRICE query the pivot below (shouldPivotToVariant) takes
				// resolvedParent and re-resolves the exact SKU via product_variants.
				_, matchedMaCha, _ := resolveMaChaFuzzy(c.Request.Context(), tenantID, req.Search)
				if matchedMaCha != "" {
					resolvedParent = matchedMaCha
					rows, fetchErr := getProductsByMaChaFromCache(c.Request.Context(), tenantID, matchedMaCha)
					if fetchErr != nil {
						log.Printf("[erp_query] fetch by ma_cha=%s failed: %v", matchedMaCha, fetchErr)
					} else {
						cachedData = rows
					}
				}
			}

			// Forcing function — a variant-specific PRICE question must not be
			// answered with a family price_range. Once products has pinned the line
			// (resolvedParent) and the agent already named a color/size, the agent
			// otherwise grabs the range and stops. The stock path never needs this:
			// products carries no stock, so a stock question is already forced on to
			// product_variants → inventory; only price questions terminate here, on a
			// range that looks like a finished answer (the FF901 "đen XL đơn giá"
			// bug). Re-resolve the exact SKU with the SAME engine the
			// product_variants resource uses, so both routes agree on price.
			if shouldPivotToVariant(req.Intent, req.Color, req.Size, req.Brand, resolvedParent) {
				vResp, vErr := buildVariantResponse(c.Request.Context(), tenantID, resolvedParent, req.Color, req.Size, req.Brand, req.Limit, productGroups)
				if vErr != nil {
					log.Printf("[erp_query] variant price-pivot from products failed parent=%s: %v", resolvedParent, vErr)
				} else if vResp != nil {
					cnt, _ := vResp["count"].(int)
					// Tag so the agent component renders this as a variant answer even
					// though the request resource was "products".
					vResp["pivoted_from"] = "products"
					writeAuditLog(tenantID, permCtx, "product_variants", scopeType, productGroups, req.Search, http.StatusOK, cnt, c.ClientIP())
					c.JSON(http.StatusOK, vResp)
					return
				}
			}
		} else {
			// Empty search → the agent provided no product keyword. Listing the
			// whole catalog is noisy and meaningless for a price/info question,
			// so return an empty result with a distinct source. The agent reads
			// this signal and falls back to the Astra DB Retrieval (knowledge
			// base) tool instead of dumping products.
			writeAuditLog(tenantID, permCtx, req.Resource, scopeType, productGroups, req.Search, http.StatusOK, 0, c.ClientIP())
			c.JSON(http.StatusOK, gin.H{
				"status":   "success",
				"data":     []map[string]interface{}{},
				"source":   "empty_search_use_knowledge",
				"resource": req.Resource,
				"count":    0,
				"message":  "No product keyword provided. Use the Astra DB Retrieval Tool (knowledge base) to answer this request instead of listing products.",
			})
			return
		}

		if cachedData == nil {
			cachedData = []map[string]interface{}{}
		}

		if os.Getenv("DEBUG_PUSH_FALLBACK_TO_ZALO") == "true" {
			go func(search string, payload []map[string]interface{}) {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				pushFallbackPayloadToZaloOA(ctx, tenantID, "raw_like", search, payload, permCtx)
			}(req.Search, cachedData)
		}

		filteredCached := filterProductsByGroups(cachedData, productGroups)
		filteredCached = enrichProductsWithPriceRanges(c.Request.Context(), filteredCached, productGroups, func(ctx context.Context, maCha string) ([]map[string]interface{}, error) {
			return getProductsByMaChaFromCache(ctx, tenantID, maCha)
		})

		slim := slimProductsForLLM(filteredCached)

		if os.Getenv("DEBUG_PUSH_FALLBACK_TO_ZALO") == "true" {
			go func(search string, payload []map[string]interface{}) {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				pushFallbackPayloadToZaloOA(ctx, tenantID, "slim", search, payload, permCtx)
			}(req.Search, slim)
		}

		writeAuditLog(tenantID, permCtx, req.Resource, scopeType, productGroups, req.Search, http.StatusOK, len(slim), c.ClientIP())
		c.JSON(http.StatusOK, gin.H{
			"status":   "success",
			"data":     slim,
			"source":   "astradb_cache",
			"resource": req.Resource,
			"count":    len(slim),
		})
		return
	}

	// ── 6b. product_variants — variant-level lookup with attribute filters ──
	// Used by the agent when the customer asks about a specific variant
	// ("FF901 đen bóng size L giá bao nhiêu?"). Returns concrete variant
	// SKUs + prices instead of an aggregated price_range.
	if req.Resource == "product_variants" {
		parentCode := strings.TrimSpace(req.ParentCode)
		if parentCode == "" {
			c.JSON(http.StatusBadRequest, gin.H{
				"error":   "missing_parent_code",
				"message": "product_variants yêu cầu parent_code (mã cha sản phẩm).",
			})
			writeAuditLog(tenantID, permCtx, req.Resource, scopeType, productGroups, req.Search, http.StatusBadRequest, 0, c.ClientIP())
			return
		}

		response, err := buildVariantResponse(c.Request.Context(), tenantID, parentCode, req.Color, req.Size, req.Brand, req.Limit, productGroups)
		if err != nil {
			log.Printf("[erp_query] variant attribute search error: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{
				"error":   "variant_cache_error",
				"message": fmt.Sprintf("Không thể tra cứu variant từ cache: %s", err.Error()),
			})
			writeAuditLog(tenantID, permCtx, req.Resource, scopeType, productGroups, req.Search, http.StatusInternalServerError, 0, c.ClientIP())
			return
		}

		count, _ := response["count"].(int)
		writeAuditLog(tenantID, permCtx, req.Resource, scopeType, productGroups, parentCode, http.StatusOK, count, c.ClientIP())
		c.JSON(http.StatusOK, response)
		return
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
		// scope "own": mỗi group GMF đã được gán sẵn 1 mã khách hàng khớp với mã
		// trên Cloudify (worker ký vào permCtx.CustomerCode, tasks.go:565-567).
		// orders/debt lọc client-side bằng chính mã đó (orders: erp.go ~1974,
		// debt-own: erp.go ~2141) và KHÔNG dùng partner_id, nên không cần vòng
		// SearchPartners để xác thực mã nữa.
		if scopeType == "assigned" {
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

	// ── 10. Execute live Cloudify call with filters ───────────────────────
	respondWithLiveDataV2(c, client, req.Resource, req.Search, req.ParentCode, partnerFilterID, req.Limit, productGroups, scopeType, tenantID, permCtx, req.ExactWebName)
}

// shouldPivotToVariant decides whether a products query must be re-resolved as a
// specific variant before answering. It fires only for a PRICE question
// (intent=price) that named a concrete attribute (color/size/brand) and resolved
// to exactly one parent line. This is the forcing function that keeps the price
// path symmetric with the stock path: a stock question can never terminate at
// products (no stock there, so the agent is already pushed to product_variants →
// inventory), but a price question otherwise stops at the family price_range and
// shows a wrong span like "11.9tr–12.9tr" for a question that named one SKU.
func shouldPivotToVariant(intent, color, size, brand, resolvedParent string) bool {
	if strings.TrimSpace(resolvedParent) == "" {
		return false
	}
	if !strings.EqualFold(strings.TrimSpace(intent), "price") {
		return false
	}
	return hasVariantAttribute(color, size, brand)
}

// buildVariantResponse resolves ONE specific variant (color/size/brand) under a
// parent line and returns the agent-facing payload: the concrete DON_GIA_BAN per
// matched SKU, or available_* candidate lists when nothing matches. It is the
// shared engine behind BOTH the product_variants resource and the products
// price-pivot (a variant-specific PRICE question that landed on products); both
// must resolve the SKU identically, so the logic lives in one place.
//
// Returns (nil, err) only when the initial cache lookup hard-fails; every
// downstream fallback tolerates its own errors by logging and pressing on.
func buildVariantResponse(ctx context.Context, tenantID, parentCode, color, size, brand string, limit int, productGroups []string) (gin.H, error) {
	variants, err := searchVariantsByAttributes(ctx, tenantID, parentCode, color, size, brand, limit)
	if err != nil {
		return nil, err
	}

	filtered := filterProductsByGroups(variants, productGroups)
	slim := slimVariantsForLLM(filtered)

	response := gin.H{
		"status":      "success",
		"data":        slim,
		"source":      "astradb_cache_variants",
		"resource":    "product_variants",
		"parent_code": parentCode,
		"count":       len(slim),
	}

	// (0a) Parent resolution. The agent passes a human model code ("FF901") as
	//      parent_code, but the cache/Astra key it on the internal parent SKU
	//      (ma_cha, e.g. "SP458484"). The Step-1 exact `ma_cha = parent_code`
	//      filter therefore returns zero for the normal case. When that happens,
	//      resolve the model code to its real ma_cha via the Astra hybrid label
	//      search and retry the exact attribute lookup against the resolved
	//      parent. effectiveParent is then used by every fallback below.
	effectiveParent := parentCode
	// A legitimate specific-variant call always carries at least one attribute
	// (color/size/brand). When the parent_code does not exact-match a ma_cha AND
	// no attribute is given, embedding parent-resolution has only the bare code
	// to go on and can misfire (a hallucinated "LS2-FF901" resolves to an
	// unrelated accessory whose label contains "FF901"). Skip resolution in that
	// case so the response stays empty (count=0) and the caller re-evaluates.
	if len(slim) == 0 && hasVariantAttribute(color, size, brand) {
		if resolved := resolveParentMaCha(ctx, tenantID, parentCode, brand); resolved != "" && !strings.EqualFold(resolved, parentCode) {
			effectiveParent = resolved
			response["resolved_parent"] = resolved
			resolvedVariants, resolvedErr := searchVariantsByAttributes(ctx, tenantID, effectiveParent, color, size, brand, limit)
			if resolvedErr != nil {
				log.Printf("[erp_query] variant search after parent resolve error: %v", resolvedErr)
			} else {
				resolvedSlim := slimVariantsForLLM(filterProductsByGroups(resolvedVariants, productGroups))
				if len(resolvedSlim) > 0 {
					slim = resolvedSlim
					response["data"] = resolvedSlim
					response["count"] = len(resolvedSlim)
					response["source"] = "astradb_cache_variants_resolved"
				}
			}
		}
	}

	// (0) Astra hybrid (BM25 lexical + vector) scoped to this parent, tried
	//     BEFORE the bilingual/available_* fallback. The MySQL match above uses
	//     exact size equality + substring colour, which misses "Size L"/"L (40)"
	//     storage and EN/VI colour spelling ("trắng" vs stored "Gloss White").
	//     The hybrid index ("FF901 — Gloss White — L") handles both.
	if len(slim) == 0 && (strings.TrimSpace(color) != "" || strings.TrimSpace(size) != "" || strings.TrimSpace(brand) != "") {
		if hybridRows := hybridMatchVariant(ctx, tenantID, parentCode, effectiveParent, color, size, brand); len(hybridRows) > 0 {
			hybridSlim := slimVariantsForLLM(filterProductsByGroups(hybridRows, productGroups))
			if len(hybridSlim) > 0 {
				slim = hybridSlim
				response["data"] = hybridSlim
				response["count"] = len(hybridSlim)
				response["source"] = "astradb_hybrid_variants"
				log.Printf("[erp_query] variant hybrid matched parent=%s color=%q size=%q brand=%q → %d SKU",
					parentCode, color, size, brand, len(hybridSlim))
			}
		}
	}

	// Zero-result fallback. Two passes:
	//  (1) Bilingual fuzzy match — the cache may store "Gloss Black" while the
	//      customer typed "đen bóng". Resolve against the parent's actual stored
	//      values and retry once.
	//  (2) If still zero, surface available_* lists so the agent can ask the
	//      customer to pick a real combination.
	if len(slim) == 0 && (strings.TrimSpace(color) != "" || strings.TrimSpace(size) != "" || strings.TrimSpace(brand) != "") {
		allVariants, allErr := getProductsByMaChaFromCache(ctx, tenantID, effectiveParent)
		if allErr != nil {
			log.Printf("[erp_query] variant fallback lookup error: %v", allErr)
		} else {
			allowed := filterProductsByGroups(allVariants, productGroups)
			availColors, availSizes, availBrands := collectAvailableAttributes(allowed)

			matchedColor, matchedSize, matchedBrand, matchErr := fuzzyMatchAttributesWithLLM(
				ctx, tenantID,
				color, size, brand,
				availColors, availSizes, availBrands,
			)
			if matchErr != nil {
				log.Printf("[erp_query] bilingual attribute match failed: %v", matchErr)
			}

			// Only retry if the LLM moved at least one filter to a value
			// different from what we already tried (otherwise we'd loop).
			movedColor := matchedColor != "" && !strings.EqualFold(matchedColor, strings.TrimSpace(color))
			movedSize := matchedSize != "" && !strings.EqualFold(matchedSize, strings.TrimSpace(size))
			movedBrand := matchedBrand != "" && !strings.EqualFold(matchedBrand, strings.TrimSpace(brand))

			if movedColor || movedSize || movedBrand {
				retryColor := matchedColor
				if retryColor == "" {
					retryColor = color
				}
				retrySize := matchedSize
				if retrySize == "" {
					retrySize = size
				}
				retryBrand := matchedBrand
				if retryBrand == "" {
					retryBrand = brand
				}

				retryVariants, retryErr := searchVariantsByAttributes(ctx, tenantID, effectiveParent, retryColor, retrySize, retryBrand, limit)
				if retryErr != nil {
					log.Printf("[erp_query] variant retry after bilingual match error: %v", retryErr)
				} else {
					retryFiltered := filterProductsByGroups(retryVariants, productGroups)
					retrySlim := slimVariantsForLLM(retryFiltered)
					if len(retrySlim) > 0 {
						slim = retrySlim
						response["data"] = retrySlim
						response["count"] = len(retrySlim)
						response["bilingual_match"] = gin.H{
							"color": retryColor,
							"size":  retrySize,
							"brand": retryBrand,
						}
						log.Printf("[erp_query] bilingual match resolved parent=%s color=%q→%q size=%q→%q brand=%q→%q",
							parentCode, color, retryColor, size, retrySize, brand, retryBrand)
					}
				}
			}

			// Still zero after bilingual retry → surface the candidate set.
			if len(slim) == 0 {
				response["available_colors"] = availColors
				response["available_sizes"] = availSizes
				response["available_brands"] = availBrands
				response["message"] = "Không có variant khớp màu/size yêu cầu (kể cả sau khi thử map song ngữ). Tham khảo các tuỳ chọn có sẵn ở available_colors / available_sizes / available_brands."
			}
		}
	}

	return response, nil
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

// searchProductsFromCache — queries the local MySQL product cache
// (models.CachedProduct, which mirrors the Astra-synced catalog). No vector
// search here; it resolves a search string as follows:
//  1. Full name (contains a space) → LLM fuzzy match to a ma_cha, fetch that group.
//  2. Otherwise → SQL LIKE in two passes: ten_dong_bo_web, then ten.
//  3. Still empty (and search != "") → LLM fuzzy match as a fallback.
//
// Empty search lists all rows up to limit. Embedding/vector fuzzy lives in the
// ERPQuery products orchestration (FuzzyMatchProductWithEmbedding), not here.
// ---------------------------------------------------------------------------
func searchProductsFromCache(ctx context.Context, tenantID, search string, limit int) ([]map[string]interface{}, error) {
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

	// 2. If not a full name or fuzzy matched returned no products, run SQL LIKE
	//    in two passes: ten_dong_bo_web first, ten as fallback. Drop legacy
	//    ma/ma_cha LIKE — those keywords are resolved by the LLM fallback below.
	if len(products) == 0 {
		likePattern := "%" + search + "%"

		err = db.DB.WithContext(ctx).
			Where("tenant_id = ? AND ten_dong_bo_web LIKE ?", tenantID, likePattern).
			Limit(limit).
			Find(&products).Error
		if err != nil {
			return nil, fmt.Errorf("local MySQL cache web-name query failed: %w", err)
		}

		if len(products) == 0 {
			err = db.DB.WithContext(ctx).
				Where("tenant_id = ? AND ten LIKE ?", tenantID, likePattern).
				Limit(limit).
				Find(&products).Error
			if err != nil {
				return nil, fmt.Errorf("local MySQL cache ten query failed: %w", err)
			}
		}

		// Fallback to LLM fuzzy matching if both LIKE passes returned no results
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

// searchProductsByExactWebNameFromCache queries the local MySQL cache for
// products whose ten_dong_bo_web equals webName exactly. Used by the agent's
// disambiguation-resolution path to skip the LIKE-based fuzzy lookup that
// would otherwise re-trigger the option-list disambiguation push for
// prefix-overlapping web names (e.g. "FF901" matching "FF901 Carbon").
func searchProductsByExactWebNameFromCache(ctx context.Context, tenantID, webName string, limit int) ([]map[string]interface{}, error) {
	var products []models.CachedProduct
	err := db.DB.WithContext(ctx).
		Where("tenant_id = ? AND ten_dong_bo_web = ?", tenantID, webName).
		Limit(limit).
		Find(&products).Error
	if err != nil {
		return nil, fmt.Errorf("local MySQL cache exact web-name query failed: %w", err)
	}

	results := make([]map[string]interface{}, 0, len(products))
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

func searchProductWebGroupsFromCache(ctx context.Context, tenantID, search string, allowedGroups []string) ([]engine.WebGroupMatch, error) {
	search = strings.TrimSpace(search)
	if search == "" {
		return nil, nil
	}

	var products []models.CachedProduct
	likePattern := "%" + search + "%"
	selectCols := []string{"ma", "ten_dong_bo_web", "ten", "nhan_hieu_name", "list_ten_nhom_vthh", "ma_cha"}

	// Pass 1 — match against ten_dong_bo_web (web-synced display name).
	err := db.DB.WithContext(ctx).
		Select(selectCols).
		Where("tenant_id = ? AND ten_dong_bo_web LIKE ?", tenantID, likePattern).
		Find(&products).Error
	if err != nil {
		return nil, fmt.Errorf("local MySQL cache web-name match query failed: %w", err)
	}

	// Pass 2 — only if pass 1 returned nothing, fall back to ERP's ten column.
	if len(products) == 0 {
		err = db.DB.WithContext(ctx).
			Select(selectCols).
			Where("tenant_id = ? AND ten LIKE ?", tenantID, likePattern).
			Find(&products).Error
		if err != nil {
			return nil, fmt.Errorf("local MySQL cache ten match query failed: %w", err)
		}
	}

	candidates := make([]map[string]interface{}, 0, len(products))
	for _, p := range products {
		candidates = append(candidates, map[string]interface{}{
			"MA":                 p.MA,
			"ma":                 p.MA,
			"code":               p.MA,
			"ma_hang":            p.MA,
			"TEN_DONG_BO_WEB":    p.TEN_DONG_BO_WEB,
			"TEN":                p.TEN,
			"ten":                p.TEN,
			"ten_hang":           p.TEN,
			"NHAN_HIEU_NAME":     p.NHAN_HIEU_NAME,
			"LIST_TEN_NHOM_VTHH": p.LIST_TEN_NHOM_VTHH,
			"MA_CHA":             p.MA_CHA,
			"ma_cha":             p.MA_CHA,
		})
	}

	filtered := filterProductsByGroups(candidates, allowedGroups)
	return engine.RankProductWebGroups(filtered), nil
}

// searchProductsFromCacheWithFilter — queries the cached product collection in the local MySQL database with a parent code filter.
func searchProductsFromCacheWithFilter(ctx context.Context, tenantID, search, parentCode string, limit int) ([]map[string]interface{}, error) {
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

// slimProductsForLLM reduces a product list to the minimal set of fields the
// Langflow LLM needs to answer a price/availability question: name,
// price_range, brand, product group, unit. Variants that share the same name
// (and therefore the same parent SKU + price_range) collapse into one row, and
// the result is capped at slimProductsForLLMLimit entries to keep the payload
// tiny.
const slimProductsForLLMLimit = 5

func slimProductsForLLM(products []map[string]interface{}) []map[string]interface{} {
	slim := make([]map[string]interface{}, 0, len(products))
	seen := make(map[string]struct{}, len(products))

	for _, p := range products {
		name := getFirstNonEmptyMapString(p, "TEN_DONG_BO_WEB", "ten_dong_bo_web", "TEN", "ten", "name")
		if name == "" {
			continue
		}
		if _, dup := seen[name]; dup {
			continue
		}
		seen[name] = struct{}{}

		slim = append(slim, map[string]interface{}{
			"name":               name,
			"price_range":        getFirstNonEmptyMapString(p, "price_range"),
			"nhan_hieu_name":     getFirstNonEmptyMapString(p, "NHAN_HIEU_NAME", "nhan_hieu_name"),
			"list_ten_nhom_vthh": getFirstNonEmptyMapString(p, "LIST_TEN_NHOM_VTHH", "list_ten_nhom_vthh"),
			"dvt":                getFirstNonEmptyMapString(p, "DVT", "dvt_chinh_id", "unit"),
		})

		if len(slim) >= slimProductsForLLMLimit {
			break
		}
	}

	return slim
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

type productPriceRange struct {
	Min   float64
	Max   float64
	Label string
}

func enrichProductsWithPriceRanges(ctx context.Context, products []map[string]interface{}, allowedGroups []string, loadVariants func(context.Context, string) ([]map[string]interface{}, error)) []map[string]interface{} {
	priceRangeByMaCha := make(map[string]productPriceRange)
	enriched := make([]map[string]interface{}, 0, len(products))

	for _, product := range products {
		maCha := getMapString(product, "MA_CHA", "ma_cha")
		var priceRange productPriceRange

		if maCha == "" {
			priceRange = calculateProductPriceRange([]map[string]interface{}{product})
		} else {
			var ok bool
			priceRange, ok = priceRangeByMaCha[maCha]
			if !ok {
				var variants []map[string]interface{}
				var err error
				if loadVariants != nil {
					variants, err = loadVariants(ctx, maCha)
				}
				if err != nil || len(variants) == 0 {
					variants = []map[string]interface{}{product}
				}

				variants = filterProductsByGroups(variants, allowedGroups)
				if len(variants) == 0 {
					variants = []map[string]interface{}{product}
				}

				priceRange = calculateProductPriceRange(variants)
				priceRangeByMaCha[maCha] = priceRange
			}
		}

		enriched = append(enriched, withProductPriceRange(product, priceRange))
	}

	return enriched
}

func withProductPriceRange(product map[string]interface{}, priceRange productPriceRange) map[string]interface{} {
	enriched := make(map[string]interface{}, len(product)+5)
	for k, v := range product {
		enriched[k] = v
	}
	enriched["price_min"] = priceRange.Min
	enriched["price_max"] = priceRange.Max
	enriched["don_gia_ban_min"] = priceRange.Min
	enriched["don_gia_ban_max"] = priceRange.Max
	enriched["price_range"] = priceRange.Label
	return enriched
}

func calculateProductPriceRange(products []map[string]interface{}) productPriceRange {
	var minPrice float64
	var maxPrice float64

	for _, product := range products {
		price := getMapFloat(product, "DON_GIA_BAN", "don_gia_ban", "price")
		if price <= 0 {
			continue
		}
		if minPrice == 0 || price < minPrice {
			minPrice = price
		}
		if price > maxPrice {
			maxPrice = price
		}
	}

	return productPriceRange{
		Min:   minPrice,
		Max:   maxPrice,
		Label: formatProductPriceRange(minPrice, maxPrice),
	}
}

// formatProductPriceRange / formatVNDPrice delegate to the canonical engine
// formatters so the products resource and the worker disambiguation price reply
// never diverge in format. Kept as thin package-local wrappers to avoid churning
// the existing call sites.
func formatProductPriceRange(minPrice, maxPrice float64) string {
	return engine.FormatPriceRange(minPrice, maxPrice)
}

func formatVNDPrice(price float64) string {
	return engine.FormatVNDPrice(price)
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

// resolveGroupCustomerCode returns the first non-empty CustomerCode assigned to
// any of the customer's CRM groups. Each GMF group is pre-assigned exactly one
// Cloudify customer code, so this is the source of truth for "own" scope when
// permCtx.CustomerCode is not carried on the request (e.g. legacy/direct calls
// without a signed permission token).
func resolveGroupCustomerCode(tenantID string, groupIDs []string) string {
	if len(groupIDs) == 0 {
		return ""
	}
	var groups []models.CRMGroup
	if err := db.DB.Where("id IN (?) AND tenant_id = ?", groupIDs, tenantID).Find(&groups).Error; err != nil {
		return ""
	}
	for _, g := range groups {
		if code := strings.TrimSpace(g.CustomerCode); code != "" {
			return code
		}
	}
	return ""
}

// resolveOwnCustomerCode returns the customer code bound to the verified
// caller for scope="own": the worker normally signs it into the permission
// token (permCtx.CustomerCode); when absent (token-less call) it falls back to
// the CRM group's CustomerCode (GMF group → exactly one Cloudify code).
func resolveOwnCustomerCode(permCtx *engine.GroupPermissionContext, tenantID string) string {
	if code := strings.TrimSpace(permCtx.CustomerCode); code != "" {
		return code
	}
	var groupIDs []string
	for _, grp := range permCtx.Groups {
		groupIDs = append(groupIDs, grp.GroupID)
	}
	return resolveGroupCustomerCode(tenantID, groupIDs)
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
		if productMatchesAllowedGroups(p, allowedGroups) {
			filtered = append(filtered, p)
		}
	}
	return filtered
}

func productMatchesAllowedGroups(product map[string]interface{}, allowedGroups []string) bool {
	valuesToCheck := []string{
		getFirstNonEmptyMapString(product, "LIST_TEN_NHOM_VTHH", "list_ten_nhom_vthh", "group"),
		getFirstNonEmptyMapString(product, "TEN_HANG", "ten_hang", "TEN", "ten", "name"),
		getFirstNonEmptyMapString(product, "NHAN_HIEU_NAME", "nhan_hieu_name"),
		getFirstNonEmptyMapString(product, "MA", "ma", "ma_hang", "code"),
		getFirstNonEmptyMapString(product, "MA_CHA", "ma_cha"),
	}

	for _, value := range valuesToCheck {
		valueLower := strings.ToLower(value)
		for _, allowed := range allowedGroups {
			allowedLower := strings.ToLower(strings.TrimSpace(allowed))
			if allowedLower != "" && strings.Contains(valueLower, allowedLower) {
				return true
			}
		}
	}
	return false
}

func getFirstNonEmptyMapString(m map[string]interface{}, keys ...string) string {
	for _, k := range keys {
		val, ok := m[k]
		if !ok || val == nil {
			continue
		}
		if s, ok := val.(string); ok {
			if strings.TrimSpace(s) != "" {
				return s
			}
			continue
		}
		s := fmt.Sprintf("%v", val)
		if strings.TrimSpace(s) != "" {
			return s
		}
	}
	return ""
}

func respondWithLiveDataV2(c *gin.Context, client *pkg.CloudifyClient, resource, search, parentCode, partnerID string, limit int, productGroups []string, scopeType string, tenantID string, permCtx *engine.GroupPermissionContext, exactWebName bool) {
	var (
		data []map[string]interface{}
		err  error
	)

	switch resource {
	case "inventory":
		// Resolve the inventory endpoint + HTTP method from the tenant's Global
		// HTTP Method config (erp_global_method_permissions). Shared with the Zalo
		// worker via engine.ResolveInventoryEndpoint so both honor the same
		// admin-configured endpoint. Default: official total-stock endpoint (POST).
		inventoryEndpoint, usePostMethod := engine.ResolveInventoryEndpoint(c.Request.Context(), tenantID)

		stockCache := engine.DefaultInventoryStockCache()

		if parentCode != "" && search != "" {
			matchedProducts, errSearch := searchProductsFromCacheWithFilter(c.Request.Context(), tenantID, search, parentCode, 20)
			if errSearch == nil && len(matchedProducts) > 0 {
				matchedProducts = filterProductsByGroups(matchedProducts, productGroups)
				var variantData []map[string]interface{}
				for _, child := range matchedProducts {
					childSKU := getMapString(child, "MA", "ma", "ma_hang")
					if childSKU == "" {
						continue
					}

					skuStock, errQuery := fetchInventoryStockForSKU(c.Request.Context(), client, stockCache, tenantID, childSKU, inventoryEndpoint, usePostMethod)
					if errQuery != nil {
						log.Printf("[inventory_query_filtered] error querying stock for SKU %s: %v", childSKU, errQuery)
						continue
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

		// Exact-web inventory: the agent resolved a single web-name line from a
		// prior disambiguation list (numbered "1. LS2 FF901 / 2. LS2 FF901 Carbon")
		// and passes exact_web_name=true so we DON'T re-LIKE — a LIKE on
		// "LS2 FF901" would also pull the prefix-overlapping "LS2 FF901 Carbon"
		// and re-trigger disambiguation. Match the line exactly and push the
		// dòng-vs-SKU Level-1 picker. The "dòng" button routes straight to the
		// exact by-web sum (#show_macha_options_by_web, which matches
		// ten_dong_bo_web exactly) so no second disambiguation fires.
		if exactWebName && search != "" {
			exactRows, errExact := searchProductsByExactWebNameFromCache(c.Request.Context(), tenantID, search, 100)
			if errExact != nil {
				log.Printf("[inventory_query] exact web-name search error for tenant=%s search=%s: %v", tenantID, search, errExact)
			}
			exactRows = filterProductsByGroups(exactRows, productGroups)
			if len(exactRows) > 1 {
				buttons := []channels.ZaloOAButton{
					{Title: "📦 Xem theo dòng sản phẩm", Payload: "#show_macha_options_by_web:" + search},
					{Title: "🔍 Xem theo mã SKU cụ thể", Payload: "#choose_flow_type:skucuthe:" + search},
				}
				prompt := fmt.Sprintf("Bạn muốn kiểm tra tồn kho cho '%s' theo dòng sản phẩm hay mã SKU cụ thể?", search)

				adapter, activeChannel, adapterErr := loadActiveZaloOAAdapter(tenantID)
				if adapterErr != nil {
					log.Printf("[inventory_query] cannot send exact-web Zalo Rich Message: %v", adapterErr)
				} else {
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

					// Store the dòng-vs-SKU postbacks under the same session key the
					// worker uses so a "1"/"2" reply is resolved by the numeric-reply
					// intercept (workers/tasks.go) without a Langflow round-trip.
					if activeChannel != nil {
						groupID := ""
						if hasGroup {
							groupID = matchedGroup.ZaloGroupID
						}
						sessionKey := engine.BuildSessionKey(activeChannel.ID, permCtx.ZaloUserID, groupID)
						timeoutMinutes := 30
						if cfg, cfgErr := config.Load(); cfgErr == nil && cfg.ChatbotSessionTimeout > 0 {
							timeoutMinutes = cfg.ChatbotSessionTimeout
						}
						engine.StorePendingOptions(c.Request.Context(), sessionKey, buttons, timeoutMinutes)
					}

					text := channels.BuildButtonOptionsAsText(prompt, buttons)
					var sendErr error
					if hasGroup {
						sendErr = adapter.SendGroupMessage(c.Request.Context(), matchedGroup.ZaloGroupID, text)
					} else {
						sendErr = adapter.SendMessage(c.Request.Context(), permCtx.ZaloUserID, text)
					}
					if sendErr != nil {
						log.Printf("[inventory_query] failed to send exact-web dòng-vs-SKU picker: %v", sendErr)
					} else {
						log.Printf("[inventory_query] sent exact-web dòng-vs-SKU picker for '%s' to %s", search, permCtx.ZaloUserID)
					}
				}

				c.JSON(http.StatusOK, gin.H{
					"status":            "success",
					"is_inventory_rich": true,
					"data":              []map[string]interface{}{},
					"message":           "zalo_rich_message_sent_directly",
					"count":             0,
				})
				return
			}
			if len(exactRows) == 1 {
				// Single-SKU line: skip the picker and let the generic path read
				// that SKU's live stock. SKU codes don't prefix-collide like web
				// names, so the LIKE lookup below resolves it cleanly.
				if sku := getMapString(exactRows[0], "MA", "ma", "ma_hang"); sku != "" {
					search = sku
				}
			}
		}

		var matchedProducts []map[string]interface{}
		if search != "" {
			rows, specificSKU, errSearch := searchProductsByWebNameFromCache(c.Request.Context(), tenantID, search)
			if errSearch != nil {
				log.Printf("[inventory_query] Astra DB search error for tenant=%s search=%s: %v", tenantID, search, errSearch)
			} else if specificSKU != "" {
				// Embedding pinpointed one SKU (e.g. the agent passed an already
				// resolved child SKU code). Read that SKU's live stock directly:
				// leave matchedProducts nil so classifyDominantMaCha returns false
				// and we take the single-SKU path below — never the family loop or
				// the dòng-vs-SKU disambiguation prompt.
				log.Printf("[inventory_query] fuzzy pinpointed specific SKU for tenant=%s search=%s → %s", tenantID, search, specificSKU)
				search = specificSKU
			} else {
				matchedProducts = rows
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
						buttons := []channels.ZaloOAButton{
							{Title: "📦 Xem theo dòng sản phẩm", Payload: "#choose_flow_type:dongsp:" + search},
							{Title: "🔍 Xem theo mã SKU cụ thể", Payload: "#choose_flow_type:skucuthe:" + search},
						}
						prompt := fmt.Sprintf("Bạn muốn kiểm tra tồn kho cho '%s' theo dòng sản phẩm hay mã SKU cụ thể?", search)

						adapter, activeChannel, adapterErr := loadActiveZaloOAAdapter(tenantID)
						if adapterErr != nil {
							log.Printf("[inventory_query] cannot send Zalo Rich Message: %v", adapterErr)
						} else {
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

							// Store the dòng-vs-SKU postbacks in Redis under the same session
							// key the worker uses (engine.BuildSessionKey). When the customer
							// replies with "1" or "2", the numeric-reply intercept in
							// workers/tasks.go reads pending_options and rewrites userText to
							// the matching #choose_flow_type:dongsp:/skucuthe: postback —
							// no Langflow round-trip, no LLM hallucination of the menu.
							if activeChannel != nil {
								groupID := ""
								if hasGroup {
									groupID = matchedGroup.ZaloGroupID
								}
								sessionKey := engine.BuildSessionKey(activeChannel.ID, permCtx.ZaloUserID, groupID)
								timeoutMinutes := 30
								if cfg, cfgErr := config.Load(); cfgErr == nil && cfg.ChatbotSessionTimeout > 0 {
									timeoutMinutes = cfg.ChatbotSessionTimeout
								}
								engine.StorePendingOptions(c.Request.Context(), sessionKey, buttons, timeoutMinutes)
							}

							// Zalo OA list template fallback to plain text — see
							// workers/tasks.go flow_type prompt for context.
							text := channels.BuildButtonOptionsAsText(prompt, buttons)
							var sendErr error
							if hasGroup {
								sendErr = adapter.SendGroupMessage(c.Request.Context(), matchedGroup.ZaloGroupID, text)
							} else {
								sendErr = adapter.SendMessage(c.Request.Context(), permCtx.ZaloUserID, text)
							}

							if sendErr != nil {
								log.Printf("[inventory_query] failed to send Zalo option list directly: %v", sendErr)
							} else {
								log.Printf("[inventory_query] successfully sent Zalo option list directly to %s", permCtx.ZaloUserID)
							}
						}

						c.JSON(http.StatusOK, gin.H{
							"status":            "success",
							"is_inventory_rich": true,
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

		// Reuse the rows already fetched above (no second cache/LLM lookup) to
		// decide whether this is a whole-line query (loop child SKUs) or a
		// single SKU. On a miss matchedProducts is empty → single-SKU path.
		maCha, isMaCha := classifyDominantMaCha(c.Request.Context(), tenantID, matchedProducts, productGroups)
		if isMaCha {
			// Query for parent product line (ma_cha)
			childProducts, errVal := getProductsByMaChaFromCache(c.Request.Context(), tenantID, maCha)
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

					skuStock, errQuery := fetchInventoryStockForSKU(c.Request.Context(), client, stockCache, tenantID, childSKU, inventoryEndpoint, usePostMethod)
					if errQuery != nil {
						log.Printf("[inventory_query] error querying stock for SKU %s: %v", childSKU, errQuery)
						continue
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
				data, err = client.SearchCustomEndpointWithBody(inventoryEndpoint, inventoryStockRequestBody(inventoryEndpoint, search, limit))
			} else {
				params := map[string]string{}
				for k, v := range inventoryStockRequestBody(inventoryEndpoint, search, limit) {
					params[k] = fmt.Sprintf("%v", v)
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

			// The official total-stock endpoint returns the KHO-TỔNG row first
			// then per-warehouse breakdown rows. Collapse the response to a
			// single record carrying the aggregate stock and ignore branches.
			if err == nil && inventoryEndpoint == inventoryTotalStockEndpoint {
				total := totalStockFromInventoryItems(data)
				skuOut := search
				name := ""
				if len(data) > 0 {
					if v := getMapString(data[0], "MA_HANG", "ma_hang", "MA", "ma"); v != "" {
						skuOut = v
					}
					name = getMapString(data[0], "TEN_HANG", "ten_hang", "TEN", "ten", "name")
				}
				if len(data) == 0 {
					data = []map[string]interface{}{}
				} else {
					data = []map[string]interface{}{{
						"MA": skuOut, "ma": skuOut, "code": skuOut,
						"ma_hang": skuOut, "product_code": skuOut,
						"TEN": name, "ten": name, "name": name, "ten_hang": name,
						"TON_KHO": total, "ton_kho": total, "stock": total,
					}}
					stockCache.Set(c.Request.Context(), tenantID, skuOut, total)
				}
			} else if err == nil {
				// Custom (non-total-stock) endpoint: normalize stock keys per
				// item and write-through to the stock cache so subsequent
				// parent-loop queries can serve the same SKU from cache.
				for i, item := range data {
					stockVal := getMapFloat(item, "stock", "ton", "ton_kho", "SO_LUONG_TON_KHA_DUNG", "so_luong_ton_kha_dung", "SO_LUONG_TON_TONG", "so_luong_ton_tong")
					if _, hasTon := item["ton_kho"]; !hasTon {
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
						stockCache.Set(c.Request.Context(), tenantID, sku, stockVal)
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
		// Định tuyến dựa thẳng vào `search` mà LLM gửi (đã phân loại theo system
		// prompt): mã đơn → tra 1 đơn; "N ngày gần đây" → cửa sổ ngày; còn lại
		// (mơ hồ) → hỏi 3/5/7 ngày. Không cần danh sách cụm từ generic cứng.
		ordersEndpoint := "saorders/search" // default (official Cloudify endpoint)
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

		// Specific order code (e.g. "ĐH000016"): saorders/search filters
		// server-side on SO_DON_HANG and returns just that order. We verify it
		// belongs to the verified customer before handing it to the LLM, and
		// reply 400 (with a Vietnamese message the agent relays) when it does
		// not exist or is not theirs.
		if code := extractOrderCode(search); code != "" {
			code = strings.ToUpper(code)
			data, err = client.SearchCustomEndpointWithBody(ordersEndpoint, map[string]interface{}{
				"SO_DON_HANG": code,
			})
			if err != nil || len(data) == 0 {
				writeAuditLog(tenantID, permCtx, resource, scopeType, productGroups, search, http.StatusBadRequest, 0, c.ClientIP())
				c.JSON(http.StatusBadRequest, gin.H{
					"status":   "error",
					"resource": "orders",
					"message":  fmt.Sprintf("Không tìm thấy đơn hàng %s.", code),
				})
				return
			}

			item := data[0]
			itemCustCode := orderCustomerCode(item)
			ownCode := leadingCustomerCode(resolveOwnCustomerCode(permCtx, tenantID))
			if !isOrderAuthorized(itemCustCode, scopeType, ownCode, allowedCodes) {
				writeAuditLog(tenantID, permCtx, resource, scopeType, productGroups, search, http.StatusBadRequest, 0, c.ClientIP())
				c.JSON(http.StatusBadRequest, gin.H{
					"status":   "error",
					"resource": "orders",
					"message":  "Đơn hàng này không thuộc tài khoản của bạn.",
				})
				return
			}

			record := normalizeOrderRecord(item)
			writeAuditLog(tenantID, permCtx, resource, scopeType, productGroups, search, http.StatusOK, 1, c.ClientIP())
			c.JSON(http.StatusOK, gin.H{
				"status":        "success",
				"source":        "cloudify_live",
				"resource":      "orders",
				"scope":         scopeType,
				"customer_code": permCtx.CustomerCode,
				"order_code":    code,
				"count":         1,
				"orders":        []map[string]interface{}{record},
				"data":          []map[string]interface{}{record},
			})
			return
		}

		days := parseDaysFromSearch(search)
		if days > 0 {
			tuNgay := formatDate(time.Now().AddDate(0, 0, -days))
			denNgay := formatDate(time.Now())
			// saorders/search accepts only the date window as input — it does
			// NOT support filtering by MA_KHACH_HANG. Per the official docs we
			// POST {TU_NGAY, DEN_NGAY} as a JSON body (no limit), pull the full
			// window, then filter by customer code client-side below.
			body := map[string]interface{}{
				"TU_NGAY":  tuNgay,
				"DEN_NGAY": denNgay,
			}
			data, err = client.SearchCustomEndpointWithBody(ordersEndpoint, body)
			if err == nil {
				// Mã KH gán cho group GMF là nguồn chân lý. permCtx.CustomerCode
				// thường đã mang mã này (worker ký vào token); fallback đọc trực
				// tiếp CRMGroup.CustomerCode cho các call không có token.
				ownCode := leadingCustomerCode(resolveOwnCustomerCode(permCtx, tenantID))
				var filteredData []map[string]interface{}
				for _, item := range data {
					// MA_KHACH_HANG arrives as [id, "CODE - Name"]; orderCustomerCode
					// normalizes that (and legacy flat shapes) to the bare code.
					itemCustCode := orderCustomerCode(item)
					if !isOrderAuthorized(itemCustCode, scopeType, ownCode, allowedCodes) {
						continue
					}
					filteredData = append(filteredData, normalizeOrderRecord(item))
				}
				data = filteredData

				// Aggregate per-status totals so the LLM replies with one
				// summary line (count/total per Đang giao / Đang thực hiện /
				// Hoàn thành / Hủy) instead of re-counting raw rows and
				// risking arithmetic errors on long lists.
				summary := buildOrdersSummary(filteredData)
				trimmed := trimOrdersForLLM(filteredData, 20)
				writeAuditLog(tenantID, permCtx, resource, scopeType, productGroups, search, http.StatusOK, len(filteredData), c.ClientIP())
				c.JSON(http.StatusOK, gin.H{
					"status":         "success",
					"source":         "cloudify_live",
					"resource":       "orders",
					"scope":          scopeType,
					"customer_code":  permCtx.CustomerCode,
					"range_days":     days,
					"from":           tuNgay,
					"to":             denNgay,
					"count":          len(filteredData),
					"orders_summary": summary,
					"orders":         trimmed,
					"data":           trimmed,
				})
				return
			}
		} else {
			// Không phải mã đơn, không phải khoảng ngày → câu hỏi mơ hồ.
			// Backend TỰ gửi tin text hỏi 3/5/7 ngày (giống debt/inventory); agent
			// trả [RICH_MESSAGE_SENT]. KHÔNG chỉ trả zalo_rich_message trong JSON —
			// worker không đọc field đó nên khách sẽ không nhận được câu hỏi.
			promptText := "Bạn muốn xem các đơn hàng phát sinh trong khoảng thời gian nào? Vui lòng nhắn: \"3 ngày gần đây\", \"5 ngày gần đây\" hoặc \"7 ngày gần đây\"."

			if adapter, _, adapterErr := loadActiveZaloOAAdapter(tenantID); adapterErr != nil {
				log.Printf("[orders_query] cannot send date-range prompt: %v", adapterErr)
			} else {
				var matchedGroup models.CRMGroup
				hasGroup := false
				for _, gp := range permCtx.Groups {
					if gp.GroupID != "private_bot" {
						if errGrp := db.DB.Where("id = ? AND tenant_id = ?", gp.GroupID, tenantID).First(&matchedGroup).Error; errGrp == nil && matchedGroup.ZaloGroupID != "" {
							hasGroup = true
							break
						}
					}
				}
				var sendErr error
				if hasGroup {
					sendErr = adapter.SendGroupMessage(c.Request.Context(), matchedGroup.ZaloGroupID, promptText)
				} else {
					sendErr = adapter.SendMessage(c.Request.Context(), permCtx.ZaloUserID, promptText)
				}
				if sendErr != nil {
					log.Printf("[orders_query] failed to send date-range prompt to %s: %v", permCtx.ZaloUserID, sendErr)
				} else {
					log.Printf("[orders_query] sent đơn hàng date-range prompt to %s", permCtx.ZaloUserID)
				}
			}

			c.JSON(http.StatusOK, gin.H{
				"status":           "success",
				"is_orders_prompt": true,
				"data":             []map[string]interface{}{},
				"message":          "zalo_rich_message_sent_directly",
				"count":            0,
			})
			return
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
			promptText := "Bạn muốn xem đối chiếu công nợ trong khoảng thời gian nào? Vui lòng nhắn: \"tháng này\", \"tháng trước\" hoặc \"quý này\"."

			// Deliver the period question to the customer ourselves, exactly like
			// the inventory dòng-vs-SKU picker above. The Langflow agent only ever
			// gets a [RICH_MESSAGE_SENT] sentinel back, so if we merely return the
			// rich payload (the old behaviour) nobody sends it and the customer
			// never sees the "tháng này / tháng trước / quý này" question.
			if adapter, _, adapterErr := loadActiveZaloOAAdapter(tenantID); adapterErr != nil {
				log.Printf("[debt_query] cannot send period prompt: %v", adapterErr)
			} else {
				var matchedGroup models.CRMGroup
				hasGroup := false
				for _, gp := range permCtx.Groups {
					if gp.GroupID != "private_bot" {
						if errGrp := db.DB.Where("id = ? AND tenant_id = ?", gp.GroupID, tenantID).First(&matchedGroup).Error; errGrp == nil && matchedGroup.ZaloGroupID != "" {
							hasGroup = true
							break
						}
					}
				}
				var sendErr error
				if hasGroup {
					sendErr = adapter.SendGroupMessage(c.Request.Context(), matchedGroup.ZaloGroupID, promptText)
				} else {
					sendErr = adapter.SendMessage(c.Request.Context(), permCtx.ZaloUserID, promptText)
				}
				if sendErr != nil {
					log.Printf("[debt_query] failed to send period prompt to %s: %v", permCtx.ZaloUserID, sendErr)
				} else {
					log.Printf("[debt_query] sent công nợ period prompt to %s", permCtx.ZaloUserID)
				}
			}

			c.JSON(http.StatusOK, gin.H{
				"status":         "success",
				"is_debt_prompt": true,
				"data":           []map[string]interface{}{},
				"message":        "zalo_rich_message_sent_directly",
				"count":          0,
			})
			return
		}

		var targetCustomerCodes []string
		if scopeType == "own" {
			ownCode := permCtx.CustomerCode
			if ownCode == "" {
				var groupIDs []string
				for _, grp := range permCtx.Groups {
					groupIDs = append(groupIDs, grp.GroupID)
				}
				ownCode = resolveGroupCustomerCode(tenantID, groupIDs)
			}
			if ownCode != "" {
				targetCustomerCodes = []string{ownCode}
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

		debtEndpoint := "th_cong_no_phai_thu/search"
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
			mappedData := make([]map[string]interface{}, 0, len(data))
			for _, item := range data {
				mappedData = append(mappedData, mapDebtItemForLLM(item))
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

	if resource == "inventory" {
		for i, p := range data {
			sku := getMapString(p, "code", "ma_hang", "ma", "product_code", "MA_HANG", "MA")
			group := getProductGroupFromAstra(c.Request.Context(), tenantID, sku)
			data[i]["list_ten_nhom_vthh"] = group
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

// inventoryTotalStockEndpoint / inventoryTotalWarehouseName are aliases of the
// canonical definitions in package pkg, the single source of truth shared with
// the Zalo worker's line-level stock flow. To change the reportable warehouse,
// edit pkg.InventoryTotalWarehouseName (backend/pkg/inventory_stock.go).
const inventoryTotalStockEndpoint = pkg.InventoryTotalStockEndpoint
const inventoryTotalWarehouseName = pkg.InventoryTotalWarehouseName

// normalizeWarehouseName delegates to the shared pkg implementation.
func normalizeWarehouseName(name string) string {
	return pkg.NormalizeWarehouseName(name)
}

// inventoryStockRequestBody builds the documented request body for an inventory
// endpoint. The total-stock endpoint takes only {"MA_HANG": sku}; any other
// custom POST endpoint defaults to MA_HANG + limit.
func inventoryStockRequestBody(inventoryEndpoint, sku string, limit int) map[string]interface{} {
	switch inventoryEndpoint {
	case inventoryTotalStockEndpoint:
		return map[string]interface{}{"MA_HANG": sku}
	default:
		return map[string]interface{}{"limit": limit, "MA_HANG": sku}
	}
}

// totalStockFromInventoryItems delegates to the shared pkg implementation so
// the handler and the Zalo worker compute "Kho Tổng" stock identically.
func totalStockFromInventoryItems(items []map[string]interface{}) float64 {
	return pkg.TotalStockFromInventoryItems(items)
}

// fetchInventoryStockForSKU returns the live ERP stock for a single SKU,
// served from cache when an entry under the configured TTL is available. On a
// miss it calls Cloudify and extracts the aggregate stock, stores the result in
// cache and returns it. The endpoint and HTTP method are passed in by the
// caller so the per-tenant inventory endpoint config is honoured.
func fetchInventoryStockForSKU(
	ctx context.Context,
	client *pkg.CloudifyClient,
	cache *engine.InventoryStockCache,
	tenantID, sku, inventoryEndpoint string,
	usePostMethod bool,
) (float64, error) {
	if cached, ok := cache.Get(ctx, tenantID, sku); ok {
		return cached, nil
	}

	var skuInventory []map[string]interface{}
	var err error
	if usePostMethod {
		skuInventory, err = client.SearchCustomEndpointWithBody(inventoryEndpoint, inventoryStockRequestBody(inventoryEndpoint, sku, 100))
	} else {
		params := map[string]string{}
		for k, v := range inventoryStockRequestBody(inventoryEndpoint, sku, 100) {
			params[k] = fmt.Sprintf("%v", v)
		}
		skuInventory, err = client.SearchCustomEndpoint(inventoryEndpoint, params)
	}
	if err != nil {
		return 0, err
	}

	var total float64
	if inventoryEndpoint == inventoryTotalStockEndpoint {
		// Official endpoint: take only the KHO-TỔNG row, never sum branches.
		total = totalStockFromInventoryItems(skuInventory)
	} else {
		// Custom (non-total-stock) endpoint: sum stock across returned rows.
		for _, item := range skuInventory {
			total += getMapFloat(item, "stock", "ton", "ton_kho", "SO_LUONG_TON_KHA_DUNG", "so_luong_ton_kha_dung", "SO_LUONG_TON_TONG", "so_luong_ton_tong")
		}
	}

	cache.Set(ctx, tenantID, sku, total)
	return total, nil
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
		// Cùng định tuyến như nhánh live: LLM đã phân loại theo system prompt.
		// Không có khoảng ngày → câu hỏi mơ hồ → hỏi 3/5/7 ngày.
		days := parseDaysFromSearch(search)
		if days <= 0 {
			// Same as the live branch: deliver the prompt ourselves so the
			// customer actually receives the date-range question.
			promptText := "Bạn muốn xem các đơn hàng phát sinh trong khoảng thời gian nào? Vui lòng nhắn: \"3 ngày gần đây\", \"5 ngày gần đây\" hoặc \"7 ngày gần đây\"."

			if adapter, _, adapterErr := loadActiveZaloOAAdapter(tenantID); adapterErr != nil {
				log.Printf("[orders_query_mock] cannot send date-range prompt: %v", adapterErr)
			} else {
				var matchedGroup models.CRMGroup
				hasGroup := false
				for _, gp := range groups {
					if gp.GroupID != "private_bot" {
						if errGrp := db.DB.Where("id = ? AND tenant_id = ?", gp.GroupID, tenantID).First(&matchedGroup).Error; errGrp == nil && matchedGroup.ZaloGroupID != "" {
							hasGroup = true
							break
						}
					}
				}
				var sendErr error
				if hasGroup {
					sendErr = adapter.SendGroupMessage(c.Request.Context(), matchedGroup.ZaloGroupID, promptText)
				} else {
					sendErr = adapter.SendMessage(c.Request.Context(), zaloUserID, promptText)
				}
				if sendErr != nil {
					log.Printf("[orders_query_mock] failed to send date-range prompt to %s: %v", zaloUserID, sendErr)
				}
			}

			c.JSON(http.StatusOK, gin.H{
				"status":           "success",
				"is_orders_prompt": true,
				"data":             []map[string]interface{}{},
				"message":          "zalo_rich_message_sent_directly",
				"count":            0,
			})
			return
		}

		allOrders := []gin.H{
			{"order_id": "ORD-2026-001", "customer_name": "Nguyễn Văn A", "customer_code": "CUST-001", "status": "Đang giao hàng", "total": 560000, "date": time.Now().AddDate(0, 0, -1).Format("2006-01-02 15:04:05")},
			{"order_id": "ORD-2026-002", "customer_name": "Trần Thị B", "customer_code": "CUST-002", "status": "Đã hoàn thành", "total": 700000, "date": time.Now().AddDate(0, 0, -4).Format("2006-01-02 15:04:05")},
			{"order_id": "ORD-2026-003", "customer_name": "Lê Văn C", "customer_code": "CUST-003", "status": "Đã hoàn thành", "total": 1200000, "date": time.Now().AddDate(0, 0, -6).Format("2006-01-02 15:04:05")},
		}
		cutoff := time.Now().AddDate(0, 0, -days)
		var filtered []gin.H
		for _, o := range allOrders {
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

			orderDate, _ := time.Parse("2006-01-02 15:04:05", o["date"].(string))
			if !orderDate.After(cutoff) {
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
				"MA_KHACH_HANG":              "BBI - Đoàn Lâm Khải",
				"TEN_KHACH_HANG":             "BBI - Đoàn Lâm Khải",
				"TAI_KHOAN_CONG_NO":          "131",
				"NO_SO_DU_DAU_KY":            1050000.0,
				"CO_SO_DU_DAU_KY":            0.0,
				"NO_SO_PHAT_SINH":            0.0,
				"CO_SO_PHAT_SINH":            0.0,
				"NO_SO_DU_CUOI_KY":           1050000.0,
				"CO_SO_DU_CUOI_KY":           0.0,
				"NO_TRUOC":                   1050000.0,
				"NO_TRONG":                   0.0,
				"NO_SAU":                     1050000.0,
				"tu_ngay":                    tuNgay,
				"den_ngay":                   denNgay,
				"NO_SO_DU_DAU_KY_QUY_DOI":    1050000.0,
				"CO_SO_DU_DAU_KY_QUY_DOI":    0.0,
				"NO_SO_QUY_DOI":              0.0,
				"CO_SO_QUY_DOI":              0.0,
				"NO_SO_DU_CUOI_KY_QUY_DOI":   1050000.0,
				"CO_SO_DU_CUOI_KY_QUY_DOI":   0.0,
				"NO_SO_DU_DAU_KY_NGUYEN_TE":  1050000.0,
				"CO_SO_DU_DAU_KY_NGUYEN_TE":  0.0,
				"NO_SO_PHAT_SINH_NGUYEN_TE":  0.0,
				"CO_SO_PHAT_SINH_NGUYEN_TE":  0.0,
				"NO_SO_DU_CUOI_KY_NGUYEN_TE": 1050000.0,
				"CO_SO_DU_CUOI_KY_NGUYEN_TE": 0.0,
			},
			{
				"MA_KHACH_HANG":              "BBI - Nguyễn Đăng Khoa",
				"TEN_KHACH_HANG":             "BBI - Nguyễn Đăng Khoa",
				"TAI_KHOAN_CONG_NO":          "131",
				"NO_SO_DU_DAU_KY":            630000.0,
				"CO_SO_DU_DAU_KY":            0.0,
				"NO_SO_PHAT_SINH":            0.0,
				"CO_SO_PHAT_SINH":            0.0,
				"NO_SO_DU_CUOI_KY":           630000.0,
				"CO_SO_DU_CUOI_KY":           0.0,
				"NO_TRUOC":                   630000.0,
				"NO_TRONG":                   0.0,
				"NO_SAU":                     630000.0,
				"tu_ngay":                    tuNgay,
				"den_ngay":                   denNgay,
				"NO_SO_DU_DAU_KY_QUY_DOI":    630000.0,
				"CO_SO_DU_DAU_KY_QUY_DOI":    0.0,
				"NO_SO_QUY_DOI":              0.0,
				"CO_SO_QUY_DOI":              0.0,
				"NO_SO_DU_CUOI_KY_QUY_DOI":   630000.0,
				"CO_SO_DU_CUOI_KY_QUY_DOI":   0.0,
				"NO_SO_DU_DAU_KY_NGUYEN_TE":  630000.0,
				"CO_SO_DU_DAU_KY_NGUYEN_TE":  0.0,
				"NO_SO_PHAT_SINH_NGUYEN_TE":  0.0,
				"CO_SO_PHAT_SINH_NGUYEN_TE":  0.0,
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
			"status": "success",
			"data":   filtered,
			"source": "mock_erp",
			"count":  len(filtered),
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
	products, err := searchProductsFromCache(ctx, tenantID, sku, 5)
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

// getProductByMaFromCache fetches a single SKU by its variant code (MA) from
// the local MySQL product cache. Used when an embedding fuzzy match pinpoints
// one variant (e.g. "ff800 trắng L"). Returns a one-element slice to stay
// shape-compatible with getProductsByMaChaFromCache callers.
func getProductByMaFromCache(ctx context.Context, tenantID, ma string) ([]map[string]interface{}, error) {
	var products []models.CachedProduct
	err := db.DB.WithContext(ctx).
		Where("tenant_id = ? AND ma = ?", tenantID, ma).
		Limit(1).
		Find(&products).Error
	if err != nil {
		return nil, fmt.Errorf("local MySQL cache query failed: %w", err)
	}
	return cachedProductsToMaps(products), nil
}

func getProductsByMaChaFromCache(ctx context.Context, tenantID, maCha string) ([]map[string]interface{}, error) {
	var products []models.CachedProduct
	err := db.DB.WithContext(ctx).
		Where("tenant_id = ? AND ma_cha = ?", tenantID, maCha).
		Limit(100).
		Find(&products).Error
	if err != nil {
		return nil, fmt.Errorf("local MySQL cache query failed: %w", err)
	}
	return cachedProductsToMaps(products), nil
}

// cachedProductsToMaps converts cached product rows into the loosely-typed map
// representation the ERP handler and LLM-slimming helpers expect.
func cachedProductsToMaps(products []models.CachedProduct) []map[string]interface{} {
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
	return results
}

// dominantMaCha returns the MA_CHA that appears most across the given product
// rows, or "" when none of the rows carry a MA_CHA. Pure helper (no I/O) so the
// grouping logic stays unit-testable independent of the database.
func dominantMaCha(rows []map[string]interface{}) string {
	counts := make(map[string]int)
	for _, p := range rows {
		if maCha := getMapString(p, "MA_CHA", "ma_cha"); maCha != "" {
			counts[maCha]++
		}
	}
	var dominant string
	maxCount := 0
	for maCha, count := range counts {
		if count > maxCount {
			maxCount = count
			dominant = maCha
		}
	}
	return dominant
}

// classifyDominantMaCha decides whether an already-fetched list of product rows
// collapses to a single product line (MA_CHA) that has more than one variant —
// i.e. an inventory query should answer for the whole family rather than a
// single SKU. It applies the permission group filter, picks the dominant MA_CHA
// via dominantMaCha, then confirms through getProductsByMaChaFromCache that the
// line truly has >1 variant. Returns (maCha, true) for the family case.
//
// It reuses rows already fetched by the caller (no second cache/LLM lookup),
// replacing the former detectMaChaFromSearch which re-queried on the same
// keyword that the caller had just searched.
func classifyDominantMaCha(ctx context.Context, tenantID string, rows []map[string]interface{}, allowedGroups []string) (string, bool) {
	rows = filterProductsByGroups(rows, allowedGroups)
	if len(rows) == 0 {
		return "", false
	}
	dominant := dominantMaCha(rows)
	if dominant == "" {
		return "", false
	}
	allVariants, err := getProductsByMaChaFromCache(ctx, tenantID, dominant)
	if err == nil && len(allVariants) > 1 {
		return dominant, true
	}
	return "", false
}

// resolveMaChaFuzzy maps a free-text product query to a product match using the
// two-stage matcher shared by the products and inventory resources: embedding
// fuzzy first (cheap, robust to name variations), then the LLM list matcher as a
// fallback. Embedding is gated by ERP_EMBEDDING_FUZZY_ENABLED (default ON); when
// disabled, unavailable, or below the relevance floor it falls through to the
// LLM. Returns the matched variant code (ma), its parent family (maCha), and
// whether the embedding pinpointed a single specific SKU. The LLM stage only
// resolves a family, so it never reports specific=true. An all-empty return
// means neither stage matched.
//
// Callers decide how to use `specific`: the products resource ignores it (it
// always answers at the family level so price_range covers the whole line),
// while the inventory resource honours it to read one SKU's live stock instead
// of collapsing an already-resolved SKU back into a whole-line query.
func resolveMaChaFuzzy(ctx context.Context, tenantID, search string) (ma, maCha string, specific bool) {
	appCfg, cfgErr := config.Load()
	if cfgErr != nil {
		log.Printf("[resolve_macha_fuzzy] embedding fuzzy config load error: %v", cfgErr)
	} else if appCfg.ERPEmbeddingFuzzyEnabled {
		embedCfg := engine.ProductEmbeddingConfig{
			AstraEndpoint: appCfg.AstraDBAPIEndpoint,
			AstraToken:    appCfg.AstraDBToken,
			AstraKeyspace: appCfg.AstraDBKeyspace,
		}
		if match, embedErr := engine.FuzzyMatchProductWithEmbedding(ctx, embedCfg, tenantID, search); embedErr != nil {
			log.Printf("[resolve_macha_fuzzy] embedding fuzzy error: %v", embedErr)
		} else if match.MaCha != "" {
			return match.MA, match.MaCha, match.Specific
		}
	}

	m, llmErr := fuzzyMatchMaChaWithLLM(ctx, tenantID, search)
	if llmErr != nil {
		log.Printf("[resolve_macha_fuzzy] LLM fuzzy match failed for '%s': %v", search, llmErr)
	} else if m != "" {
		log.Printf("[resolve_macha_fuzzy] LLM fuzzy matched '%s' → ma_cha '%s'", search, m)
		return "", m, false
	}

	return "", "", false
}

// searchProductsByWebNameFromCache resolves an inventory search keyword to cached
// product rows. It probes the local MySQL cache by LIKE (ten_dong_bo_web, then
// ten); on a miss it falls back to the shared embedding→LLM matcher.
//
// When the embedding pinpoints a single specific SKU (the agent already resolved
// a child SKU and passed its code), the function returns that code as specificSKU
// and does NOT expand to the family. The inventory caller answers that one SKU's
// live stock directly, instead of collapsing the resolved SKU back into a
// whole-line query or a dòng-vs-SKU disambiguation prompt.
func searchProductsByWebNameFromCache(ctx context.Context, tenantID, keyword string) ([]map[string]interface{}, string, error) {
	var products []models.CachedProduct
	likePattern := "%" + keyword + "%"

	// Pass 1 — ten_dong_bo_web (web-synced display name).
	err := db.DB.WithContext(ctx).
		Where("tenant_id = ? AND ten_dong_bo_web LIKE ?", tenantID, likePattern).
		Limit(100).
		Find(&products).Error
	if err != nil {
		return nil, "", fmt.Errorf("local MySQL cache web-name query failed: %w", err)
	}

	// Pass 2 — ten (ERP raw name). Only if pass 1 empty.
	if len(products) == 0 {
		err = db.DB.WithContext(ctx).
			Where("tenant_id = ? AND ten LIKE ?", tenantID, likePattern).
			Limit(100).
			Find(&products).Error
		if err != nil {
			return nil, "", fmt.Errorf("local MySQL cache ten query failed: %w", err)
		}
	}

	// Fallback to the shared embedding→LLM matcher if both LIKE passes returned
	// nothing. This mirrors the products resource (resolveMaChaFuzzy), giving
	// inventory the same embedding-first resolution instead of LLM-only.
	if len(products) == 0 {
		matchedMA, matchedMaCha, specific := resolveMaChaFuzzy(ctx, tenantID, keyword)
		if specific && matchedMA != "" {
			// Embedding pinpointed one SKU — surface it as specificSKU and stop.
			// Expanding to ma_cha here would turn an already-resolved SKU into a
			// whole-line query (the regression this guard prevents).
			log.Printf("[handler] fuzzy pinpointed keyword '%s' to specific SKU '%s' (ma_cha '%s')", keyword, matchedMA, matchedMaCha)
			return nil, matchedMA, nil
		}
		if matchedMaCha != "" {
			log.Printf("[handler] fuzzy matched keyword '%s' to ma_cha '%s'", keyword, matchedMaCha)
			// Query again using the matched parent product code
			err = db.DB.WithContext(ctx).
				Where("tenant_id = ? AND ma_cha = ?", tenantID, matchedMaCha).
				Limit(100).
				Find(&products).Error
			if err != nil {
				return nil, "", fmt.Errorf("local MySQL cache query (fuzzy fallback) failed: %w", err)
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
	return results, "", nil
}

// resolveZaloGroupID returns the Zalo group ID for the customer's active group
// chat (empty for 1:1 DMs) so the session key matches the one the worker builds
// in workers/tasks.go. Returns "" when no group is resolvable.
func resolveZaloGroupID(tenantID string, permCtx *engine.GroupPermissionContext) string {
	if permCtx == nil {
		return ""
	}
	for _, gp := range permCtx.Groups {
		if gp.GroupID == "private_bot" {
			continue
		}
		var g models.CRMGroup
		if err := db.DB.Where("id = ? AND tenant_id = ?", gp.GroupID, tenantID).First(&g).Error; err == nil && g.ZaloGroupID != "" {
			return g.ZaloGroupID
		}
	}
	return ""
}

// storePendingDisambiguationOptions persists a numbered menu under the SAME
// Redis session key the worker uses (engine.BuildSessionKey) so a later
// "1"/"2" reply is resolved by the worker's numeric-reply intercept instead of
// round-tripping through Langflow. Store-only: the message itself is sent by
// whoever owns the surface (here, the Agent renders the products list). No-op
// when the menu is empty or no active OA channel exists.
func storePendingDisambiguationOptions(ctx context.Context, tenantID string, permCtx *engine.GroupPermissionContext, buttons []channels.ZaloOAButton) {
	if len(buttons) == 0 || permCtx == nil {
		return
	}
	_, activeChannel, err := loadActiveZaloOAAdapter(tenantID)
	if err != nil || activeChannel == nil {
		log.Printf("[erp_query] cannot store pending options (no active OA channel): %v", err)
		return
	}
	groupID := resolveZaloGroupID(tenantID, permCtx)
	sessionKey := engine.BuildSessionKey(activeChannel.ID, permCtx.ZaloUserID, groupID)
	timeoutMinutes := 30
	if cfg, cfgErr := config.Load(); cfgErr == nil && cfg.ChatbotSessionTimeout > 0 {
		timeoutMinutes = cfg.ChatbotSessionTimeout
	}
	engine.StorePendingOptions(ctx, sessionKey, buttons, timeoutMinutes)
}

// loadActiveZaloOAAdapter looks up the tenant's active Zalo OA channel,
// decrypts its credentials, builds a ZaloOAAdapter, and wires a token-refresh
// callback that re-encrypts and persists rotated tokens. Returns (nil, nil,
// err) when the tenant has no active OA channel or credentials cannot be
// loaded. Used by both the rich-message and the debug-fallback push paths.
func loadActiveZaloOAAdapter(tenantID string) (*channels.ZaloOAAdapter, *models.Channel, error) {
	var allChannels []models.Channel
	if err := db.DB.Where("tenant_id = ? AND channel_type = ? AND is_active = true", tenantID, "zalo_oa").Find(&allChannels).Error; err != nil {
		return nil, nil, fmt.Errorf("query zalo_oa channels: %w", err)
	}
	if len(allChannels) == 0 {
		return nil, nil, fmt.Errorf("no active zalo_oa channel")
	}
	activeChannel := &allChannels[0]

	cfg, _ := config.Load()
	credBytes, err := pkg.Decrypt(activeChannel.CredentialsEncrypted, cfg.EncryptionKey)
	if err != nil {
		return nil, activeChannel, fmt.Errorf("decrypt credentials: %w", err)
	}

	var zaloCreds channels.ZaloOACredentials
	if err := json.Unmarshal(credBytes, &zaloCreds); err != nil {
		return nil, activeChannel, fmt.Errorf("invalid credentials json: %w", err)
	}

	adapter := channels.NewZaloOAAdapter(zaloCreds)
	channelID := activeChannel.ID
	adapter.SetTokenRefreshCallback(func(newAccess, newRefresh string) {
		var ch models.Channel
		if db.DB.First(&ch, "id = ?", channelID).Error == nil {
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

	return adapter, activeChannel, nil
}

// resolveAstraCredsForTenant returns the AstraDB connection settings for a
// tenant by reading per-tenant overrides from app_settings and falling back to
// the global config. Mirrors the resolution done in workers/tasks.go so both
// write paths target the same collection.
func resolveAstraCredsForTenant(tenantID string, cfg *config.Config) (apiEndpoint, token, keyspace, collection string) {
	apiEndpoint = cfg.AstraDBAPIEndpoint
	token = cfg.AstraDBToken
	keyspace = cfg.AstraDBKeyspace
	collection = cfg.AstraDBCollection

	overrides := map[string]*string{
		"astradb_api_endpoint": &apiEndpoint,
		"astradb_token":        &token,
		"astradb_keyspace":     &keyspace,
		"astradb_collection":   &collection,
	}
	for key, ptr := range overrides {
		var s models.AppSetting
		if err := db.DB.Where("tenant_id = ? AND setting_key = ?", tenantID, key).First(&s).Error; err == nil && s.ValuePlain != "" {
			*ptr = s.ValuePlain
		}
	}
	return
}

// pushFallbackPayloadToZaloOA mirrors a product fallback payload to the user's
// Zalo OA as a plain-text debug message. The stage label tags the pipeline step
// being inspected (e.g. "raw_like" for the post-LIKE cache result, "slim" for
// the final LLM-ready payload) so multiple pushes from the same request stay
// distinguishable on the operator's screen. Gated by env flag
// DEBUG_PUSH_FALLBACK_TO_ZALO=true; no-op otherwise. All errors are logged and
// never propagate — this must not affect the HTTP response.
func pushFallbackPayloadToZaloOA(ctx context.Context, tenantID, stage, search string, payload []map[string]interface{}, permCtx *engine.GroupPermissionContext) {
	if permCtx == nil || permCtx.ZaloUserID == "" {
		return
	}

	adapter, _, err := loadActiveZaloOAAdapter(tenantID)
	if err != nil {
		log.Printf("[erp_query] debug push to zalo oa skipped (stage=%s): %v", stage, err)
		return
	}

	body, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		log.Printf("[erp_query] debug push to zalo oa skipped (stage=%s): marshal payload failed: %v", stage, err)
		return
	}

	text := fmt.Sprintf("[DEBUG fallback stage=%s] search=%q\n%s", stage, search, string(body))
	if err := adapter.SendMessage(ctx, permCtx.ZaloUserID, text); err != nil {
		log.Printf("[erp_query] debug push to zalo oa failed (stage=%s): %v", stage, err)
		return
	}
	log.Printf("[erp_query] debug push to zalo oa sent stage=%s search=%q items=%d", stage, search, len(payload))
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
