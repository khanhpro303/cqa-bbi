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

	// Enforce permitted resources & scope.
	// product_variants is a finer-grained read against the same cache as
	// products, so it inherits the products permission grant — tenants do
	// not need to configure a second resource entry.
	permResource := req.Resource
	if permResource == "product_variants" {
		permResource = "products"
	}
	allowed, scopeType, productGroups := permCtx.IsResourceAllowed(permResource)
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
			cachedData, err := searchProductsByExactWebNameFromAstraDB(c.Request.Context(), tenantID, search, req.Limit)
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
				return getProductsByMaChaFromAstraDB(ctx, tenantID, maCha)
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
		//   - B (searchProductWebGroupsFromAstraDB) probes via LIKE web/ten
		//   - >1 group → disambiguation list, return
		//   - 1 group  → use B's ma_cha directly, no re-LIKE, no LLM
		//   - 0 group  → LLM fuzzy (name-aware, see fuzzyMatchMaChaWithLLM)
		// Empty search → list all up to req.Limit.
		var cachedData []map[string]interface{}
		search := strings.TrimSpace(req.Search)

		if search != "" {
			webGroups, err := searchProductWebGroupsFromAstraDB(c.Request.Context(), tenantID, req.Search, productGroups)
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
						"count":        g.Count,
						"is_fallback":  g.IsFallback,
					})
				}

				if os.Getenv("DEBUG_PUSH_FALLBACK_TO_ZALO") == "true" {
					go func(search string, payload []map[string]interface{}) {
						ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
						defer cancel()
						pushFallbackPayloadToZaloOA(ctx, tenantID, "raw_like_groups", search, payload, permCtx)
					}(req.Search, groupPayloads)
				}

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
				for _, pc := range webGroups[0].ParentCodes {
					rows, fetchErr := getProductsByMaChaFromAstraDB(c.Request.Context(), tenantID, pc)
					if fetchErr != nil {
						log.Printf("[erp_query] fetch by ma_cha=%s failed: %v", pc, fetchErr)
						continue
					}
					cachedData = append(cachedData, rows...)
				}
			} else {
				// B proved LIKE returns nothing. Try embedding fuzzy first
				// (cheaper + robust against name variations); fall back to the
				// LLM-list matcher when disabled, unavailable, or below the
				// similarity threshold.
				//
				// A pinpoint SKU match (e.g. "ff800 trắng L") resolves to a
				// single MA; a vague family query (e.g. "storm 3") resolves to a
				// ma_cha and returns all variants so price_range covers the family.
				var matchedMA string
				var matchedMaCha string
				if os.Getenv("ERP_EMBEDDING_FUZZY_ENABLED") == "true" {
					appCfg, cfgErr := config.Load()
					if cfgErr != nil {
						log.Printf("[erp_query] embedding fuzzy config load error: %v", cfgErr)
					} else {
						embedCfg := engine.ProductEmbeddingConfig{
							AstraEndpoint: appCfg.AstraDBAPIEndpoint,
							AstraToken:    appCfg.AstraDBToken,
							AstraKeyspace: appCfg.AstraDBKeyspace,
						}
						if match, embedErr := engine.FuzzyMatchProductWithEmbedding(c.Request.Context(), embedCfg, tenantID, req.Search); embedErr != nil {
							log.Printf("[erp_query] embedding fuzzy error: %v", embedErr)
						} else if match.MaCha != "" {
							matchedMaCha = match.MaCha
							if match.Specific {
								matchedMA = match.MA
							}
						}
					}
				}
				if matchedMA == "" && matchedMaCha == "" {
					m, llmErr := fuzzyMatchMaChaWithLLM(c.Request.Context(), tenantID, req.Search)
					if llmErr != nil {
						log.Printf("[erp_query] LLM fuzzy match failed for '%s': %v", req.Search, llmErr)
					} else if m != "" {
						log.Printf("[erp_query] LLM fuzzy matched '%s' → ma_cha '%s'", req.Search, m)
						matchedMaCha = m
					}
				}
				if matchedMA != "" {
					// Pinpoint SKU query → return just the matched variant.
					rows, fetchErr := getProductByMaFromAstraDB(c.Request.Context(), tenantID, matchedMA)
					if fetchErr != nil {
						log.Printf("[erp_query] fetch by ma=%s failed: %v", matchedMA, fetchErr)
					} else {
						cachedData = rows
					}
				} else if matchedMaCha != "" {
					rows, fetchErr := getProductsByMaChaFromAstraDB(c.Request.Context(), tenantID, matchedMaCha)
					if fetchErr != nil {
						log.Printf("[erp_query] fetch by ma_cha=%s failed: %v", matchedMaCha, fetchErr)
					} else {
						cachedData = rows
					}
				}
			}
		} else {
			rows, err := searchProductsFromAstraDB(c.Request.Context(), tenantID, "", req.Limit)
			if err != nil {
				log.Printf("[erp_query] product cache search error: %v", err)
				c.JSON(http.StatusInternalServerError, gin.H{
					"error":   "product_cache_error",
					"message": fmt.Sprintf("Không thể lấy dữ liệu sản phẩm từ cache: %s", err.Error()),
				})
				writeAuditLog(tenantID, permCtx, req.Resource, scopeType, productGroups, req.Search, http.StatusInternalServerError, 0, c.ClientIP())
				return
			}
			cachedData = rows
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
			return getProductsByMaChaFromAstraDB(ctx, tenantID, maCha)
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

		variants, err := searchVariantsByAttributes(c.Request.Context(), tenantID, parentCode, req.Color, req.Size, req.Brand, req.Limit)
		if err != nil {
			log.Printf("[erp_query] variant attribute search error: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{
				"error":   "variant_cache_error",
				"message": fmt.Sprintf("Không thể tra cứu variant từ cache: %s", err.Error()),
			})
			writeAuditLog(tenantID, permCtx, req.Resource, scopeType, productGroups, req.Search, http.StatusInternalServerError, 0, c.ClientIP())
			return
		}

		filtered := filterProductsByGroups(variants, productGroups)
		slim := slimVariantsForLLM(filtered)

		response := gin.H{
			"status":      "success",
			"data":        slim,
			"source":      "astradb_cache_variants",
			"resource":    req.Resource,
			"parent_code": parentCode,
			"count":       len(slim),
		}

		// Zero-result fallback. Two passes:
		//  (1) Bilingual fuzzy match — the cache may store "Gloss Black"
		//      while the customer typed "đen bóng". Resolve color/size/brand
		//      against the parent's actual stored values and retry once.
		//  (2) If still zero, surface available_* lists so the LLM agent can
		//      ask the customer to pick a real combination.
		if len(slim) == 0 && (strings.TrimSpace(req.Color) != "" || strings.TrimSpace(req.Size) != "" || strings.TrimSpace(req.Brand) != "") {
			allVariants, allErr := getProductsByMaChaFromAstraDB(c.Request.Context(), tenantID, parentCode)
			if allErr != nil {
				log.Printf("[erp_query] variant fallback lookup error: %v", allErr)
			} else {
				allowed := filterProductsByGroups(allVariants, productGroups)
				availColors, availSizes, availBrands := collectAvailableAttributes(allowed)

				matchedColor, matchedSize, matchedBrand, matchErr := fuzzyMatchAttributesWithLLM(
					c.Request.Context(), tenantID,
					req.Color, req.Size, req.Brand,
					availColors, availSizes, availBrands,
				)
				if matchErr != nil {
					log.Printf("[erp_query] bilingual attribute match failed: %v", matchErr)
				}

				// Only retry if the LLM moved at least one filter to a value
				// different from what we already tried (otherwise we'd loop).
				movedColor := matchedColor != "" && !strings.EqualFold(matchedColor, strings.TrimSpace(req.Color))
				movedSize := matchedSize != "" && !strings.EqualFold(matchedSize, strings.TrimSpace(req.Size))
				movedBrand := matchedBrand != "" && !strings.EqualFold(matchedBrand, strings.TrimSpace(req.Brand))

				if movedColor || movedSize || movedBrand {
					retryColor := matchedColor
					if retryColor == "" {
						retryColor = req.Color
					}
					retrySize := matchedSize
					if retrySize == "" {
						retrySize = req.Size
					}
					retryBrand := matchedBrand
					if retryBrand == "" {
						retryBrand = req.Brand
					}

					retryVariants, retryErr := searchVariantsByAttributes(c.Request.Context(), tenantID, parentCode, retryColor, retrySize, retryBrand, req.Limit)
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
								parentCode, req.Color, retryColor, req.Size, retrySize, req.Brand, retryBrand)
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

		writeAuditLog(tenantID, permCtx, req.Resource, scopeType, productGroups, parentCode, http.StatusOK, len(slim), c.ClientIP())
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

// searchProductsByExactWebNameFromAstraDB queries the local MySQL cache for
// products whose ten_dong_bo_web equals webName exactly. Used by the agent's
// disambiguation-resolution path to skip the LIKE-based fuzzy lookup that
// would otherwise re-trigger the option-list disambiguation push for
// prefix-overlapping web names (e.g. "FF901" matching "FF901 Carbon").
func searchProductsByExactWebNameFromAstraDB(ctx context.Context, tenantID, webName string, limit int) ([]map[string]interface{}, error) {
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

func searchProductWebGroupsFromAstraDB(ctx context.Context, tenantID, search string, allowedGroups []string) ([]engine.WebGroupMatch, error) {
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

func formatProductPriceRange(minPrice, maxPrice float64) string {
	if minPrice <= 0 || maxPrice <= 0 {
		return "Liên hệ"
	}
	if minPrice == maxPrice {
		return formatVNDPrice(minPrice)
	}
	return fmt.Sprintf("%s - %s", formatVNDPrice(minPrice), formatVNDPrice(maxPrice))
}

func formatVNDPrice(price float64) string {
	value := strconv.FormatInt(int64(price+0.5), 10)
	var parts []string
	for len(value) > 3 {
		parts = append([]string{value[len(value)-3:]}, parts...)
		value = value[:len(value)-3]
	}
	parts = append([]string{value}, parts...)
	return strings.Join(parts, ".") + "đ"
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

func respondWithLiveDataV2(c *gin.Context, client *pkg.CloudifyClient, resource, search, parentCode, partnerID string, limit int, productGroups []string, scopeType string, tenantID string, permCtx *engine.GroupPermissionContext) {
	var (
		data []map[string]interface{}
		err  error
	)

	switch resource {
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

		stockCache := engine.DefaultInventoryStockCache()

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

		var matchedProducts []map[string]interface{}
		if search != "" {
			rows, errSearch := searchProductsByWebNameAstraDBNonVectorized(c.Request.Context(), tenantID, search)
			if errSearch != nil {
				log.Printf("[inventory_query] Astra DB search error for tenant=%s search=%s: %v", tenantID, search, errSearch)
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

						adapter, _, adapterErr := loadActiveZaloOAAdapter(tenantID)
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
				// Legacy receipt-search: normalize stock keys per item and
				// write-through to the stock cache so subsequent parent-loop
				// queries can serve the same SKU from cache.
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
		if isGenericOrderSearch(search) {
			promptMsg := gin.H{
				"recipient": gin.H{
					"user_id": permCtx.ZaloUserID,
				},
				"message": gin.H{
					"text": "Bạn muốn xem các đơn hàng phát sinh trong khoảng thời gian nào? Vui lòng nhắn: \"3 ngày gần đây\", \"5 ngày gần đây\" hoặc \"7 ngày gần đây\".",
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
					"text": "Bạn muốn xem đối chiếu công nợ trong khoảng thời gian nào? Vui lòng nhắn: \"tháng này\", \"tháng trước\" hoặc \"quý này\".",
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

// inventoryTotalStockEndpoint is the official Cloudify endpoint that returns a
// product's stock. Each data item carries product fields plus a nested
// TON_KHO_CHI_TIET array of per-warehouse rows (TEN_KHO/SO_LUONG_TON). We read
// stock only from the "Kho Tổng" warehouse row, not the SO_LUONG_TON_TONG
// aggregate. The documented request body is {"MA_HANG": <sku>}.
const inventoryTotalStockEndpoint = "danhmucvattuhanghoa/lay_ton_kho_san_pham"

// inventoryTotalWarehouseName is the warehouse whose SO_LUONG_TON is treated as
// the reportable stock. Matching is case- and space-insensitive.
const inventoryTotalWarehouseName = "Kho Tổng"

// normalizeWarehouseName upper-cases and strips spaces so warehouse names match
// regardless of casing or incidental spacing (e.g. "1. KHO-TONG").
func normalizeWarehouseName(name string) string {
	return strings.ReplaceAll(strings.ToUpper(strings.TrimSpace(name)), " ", "")
}

// inventoryStockRequestBody builds the documented request body for an inventory
// endpoint. The total-stock endpoint takes only {"MA_HANG": sku}; the legacy
// receipt-search endpoint takes keyword + limit; any other custom POST endpoint
// defaults to MA_HANG + limit.
func inventoryStockRequestBody(inventoryEndpoint, sku string, limit int) map[string]interface{} {
	switch inventoryEndpoint {
	case inventoryTotalStockEndpoint:
		return map[string]interface{}{"MA_HANG": sku}
	case "inventory_receipt/search":
		return map[string]interface{}{"limit": limit, "keyword": sku}
	default:
		return map[string]interface{}{"limit": limit, "MA_HANG": sku}
	}
}

// totalStockFromInventoryItems sums SO_LUONG_TON across the "Kho Tổng"
// warehouse rows in a lay_ton_kho_san_pham response. Warehouse rows live in the
// nested TON_KHO_CHI_TIET array; older/flat responses expose the warehouse on
// the item itself. The SO_LUONG_TON_TONG aggregate and every other warehouse
// are ignored. Returns 0 when no matching warehouse row is found.
func totalStockFromInventoryItems(items []map[string]interface{}) float64 {
	target := normalizeWarehouseName(inventoryTotalWarehouseName)
	var total float64

	addIfMatch := func(row map[string]interface{}) {
		if normalizeWarehouseName(getMapString(row, "TEN_KHO", "ten_kho")) == target {
			total += getMapFloat(row, "SO_LUONG_TON", "so_luong_ton")
		}
	}

	for _, item := range items {
		if details, ok := item["TON_KHO_CHI_TIET"].([]interface{}); ok {
			for _, d := range details {
				if row, ok := d.(map[string]interface{}); ok {
					addIfMatch(row)
				}
			}
			continue
		}
		// Flat shape: the item itself is a warehouse row.
		addIfMatch(item)
	}
	return total
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
		// Legacy receipt-search: sum stock across returned receipt rows.
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
		if isGenericOrderSearch(search) {
			promptMsg := gin.H{
				"recipient": gin.H{
					"user_id": zaloUserID,
				},
				"message": gin.H{
					"text": "Bạn muốn xem các đơn hàng phát sinh trong khoảng thời gian nào? Vui lòng nhắn: \"3 ngày gần đây\", \"5 ngày gần đây\" hoặc \"7 ngày gần đây\".",
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

// getProductByMaFromAstraDB fetches a single SKU by its variant code (MA) from
// the local MySQL product cache. Used when an embedding fuzzy match pinpoints
// one variant (e.g. "ff800 trắng L"). Returns a one-element slice to stay
// shape-compatible with getProductsByMaChaFromAstraDB callers.
func getProductByMaFromAstraDB(ctx context.Context, tenantID, ma string) ([]map[string]interface{}, error) {
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

func getProductsByMaChaFromAstraDB(ctx context.Context, tenantID, maCha string) ([]map[string]interface{}, error) {
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
// via dominantMaCha, then confirms through getProductsByMaChaFromAstraDB that the
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
	allVariants, err := getProductsByMaChaFromAstraDB(ctx, tenantID, dominant)
	if err == nil && len(allVariants) > 1 {
		return dominant, true
	}
	return "", false
}

func searchProductsByWebNameAstraDBNonVectorized(ctx context.Context, tenantID, keyword string) ([]map[string]interface{}, error) {
	var products []models.CachedProduct
	likePattern := "%" + keyword + "%"

	// Pass 1 — ten_dong_bo_web (web-synced display name).
	err := db.DB.WithContext(ctx).
		Where("tenant_id = ? AND ten_dong_bo_web LIKE ?", tenantID, likePattern).
		Limit(100).
		Find(&products).Error
	if err != nil {
		return nil, fmt.Errorf("local MySQL cache web-name query failed: %w", err)
	}

	// Pass 2 — ten (ERP raw name). Only if pass 1 empty.
	if len(products) == 0 {
		err = db.DB.WithContext(ctx).
			Where("tenant_id = ? AND ten LIKE ?", tenantID, likePattern).
			Limit(100).
			Find(&products).Error
		if err != nil {
			return nil, fmt.Errorf("local MySQL cache ten query failed: %w", err)
		}
	}

	// Fallback to LLM fuzzy matching if both LIKE passes returned no results
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
