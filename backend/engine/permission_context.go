package engine

import (
	"fmt"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/db/models"
)

// ---------------------------------------------------------------------------
// Permission Context — resolved per-group ERP permissions for a customer
// ---------------------------------------------------------------------------

// GroupPermissionContext holds the full resolved permissions for a customer
// across all their CRM groups. This is encoded into a signed JWT and passed
// through Langflow tweaks to the ERP Gateway for server-side enforcement.
type GroupPermissionContext struct {
	TenantID     string            `json:"tenant_id"`
	ZaloUserID   string            `json:"zalo_user_id"`
	CustomerCode string            `json:"customer_code"`
	AgentType    string            `json:"agent_type"` // "public" | "private"
	Groups       []GroupPermission `json:"groups"`
}

// GroupPermission holds the permissions for a single CRM group.
type GroupPermission struct {
	GroupID   string               `json:"group_id"`
	GroupName string               `json:"group_name"`
	Resources []ResourcePermission `json:"resources"`
}

// ResourcePermission holds the access config for a single ERP resource within a group.
type ResourcePermission struct {
	Resource      string   `json:"resource"`        // products|inventory|orders|customers|debt
	IsEnabled     bool     `json:"is_enabled"`
	ScopeType     string   `json:"scope_type"`      // all|own|assigned
	ProductGroups []string `json:"product_groups"`   // filtered VTHH groups
	Brands        []string `json:"brands"`           // filtered brand (nhãn hiệu) names, lowercase, exact-match
	AllBrands     bool     `json:"all_brands"`       // true = no brand filter for this resource
}

// resourcePermissionFromEndpoint converts a stored ERPEndpoint row into the
// runtime ResourcePermission, parsing the comma-separated ProductGroups and
// Brands filters identically (trim + lowercase). Centralizing this keeps the
// three resolution branches below from drifting apart.
func resourcePermissionFromEndpoint(ep models.ERPEndpoint) ResourcePermission {
	return ResourcePermission{
		Resource:      ep.Resource,
		IsEnabled:     ep.IsEnabled,
		ScopeType:     ep.ScopeType,
		ProductGroups: splitCSVLower(ep.ProductGroups),
		Brands:        splitCSVLower(ep.Brands),
		AllBrands:     ep.AllBrands,
	}
}

// splitCSVLower splits a comma-separated string into trimmed, lowercased,
// non-empty tokens.
func splitCSVLower(csv string) []string {
	if csv == "" {
		return nil
	}
	var out []string
	for _, t := range strings.Split(csv, ",") {
		if v := strings.TrimSpace(t); v != "" {
			out = append(out, strings.ToLower(v))
		}
	}
	return out
}

// permissionClaims wraps GroupPermissionContext as JWT claims.
type permissionClaims struct {
	jwt.RegisteredClaims
	Permissions GroupPermissionContext `json:"permissions"`
}

// ---------------------------------------------------------------------------
// ResolvePermissions — queries DB for customer's group permissions
// ---------------------------------------------------------------------------

// ResolvePermissions loads all CRM groups a customer belongs to and their
// ERPEndpoint configs. For private agents, returns an empty groups list
// (private = full access, enforced at gateway level).
// ResolvePermissionsWithGroup loads CRM group permissions. If a specific groupID is provided,
// it resolves permissions exclusively for that group. Otherwise, it resolves based on agentType.
func ResolvePermissionsWithGroup(tenantID, zaloUserID, customerCode, agentType, groupID string) GroupPermissionContext {
	ctx := GroupPermissionContext{
		TenantID:     tenantID,
		ZaloUserID:   zaloUserID,
		CustomerCode: customerCode,
		AgentType:    agentType,
	}

	// 1. If a specific group context is provided, resolve permissions only for this group.
	if groupID != "" {
		var groups []models.CRMGroup
		if err := db.DB.Where("id = ? AND tenant_id = ?", groupID, tenantID).Find(&groups).Error; err != nil || len(groups) == 0 {
			return ctx
		}

		var endpoints []models.ERPEndpoint
		db.DB.Where("tenant_id = ? AND group_id = ?", tenantID, groupID).Find(&endpoints)

		var resources []ResourcePermission
		for _, ep := range endpoints {
			resources = append(resources, resourcePermissionFromEndpoint(ep))
		}

		ctx.Groups = []GroupPermission{
			{
				GroupID:   groups[0].ID,
				GroupName: groups[0].Name,
				Resources: resources,
			},
		}
		return ctx
	}

	// 2. Private agents load their permissions configured for the "private_bot" group
	if agentType == "private" {
		var endpoints []models.ERPEndpoint
		db.DB.Where("tenant_id = ? AND group_id = ?", tenantID, "private_bot").Find(&endpoints)

		var resources []ResourcePermission
		for _, ep := range endpoints {
			resources = append(resources, resourcePermissionFromEndpoint(ep))
		}

		ctx.Groups = []GroupPermission{
			{
				GroupID:   "private_bot",
				GroupName: "Private Chatbot",
				Resources: resources,
			},
		}
		return ctx
	}

	if zaloUserID == "" {
		return ctx
	}

	// 3. Fallback to normal customer (public agent) resolution
	var customer models.ZaloCustomer
	if err := db.DB.Where("tenant_id = ? AND zalo_user_id = ? AND status = ?",
		tenantID, zaloUserID, "approved").First(&customer).Error; err != nil {
		return ctx
	}

	var groupLinks []models.CRMGroupCustomer
	if err := db.DB.Where("zalo_customer_id = ?", customer.ID).Find(&groupLinks).Error; err != nil || len(groupLinks) == 0 {
		return ctx
	}

	groupIDs := make([]string, len(groupLinks))
	for i, link := range groupLinks {
		groupIDs[i] = link.GroupID
	}

	var groups []models.CRMGroup
	db.DB.Where("id IN (?)", groupIDs).Find(&groups)
	groupNameMap := make(map[string]string)
	for _, g := range groups {
		groupNameMap[g.ID] = g.Name
	}

	var endpoints []models.ERPEndpoint
	db.DB.Where("tenant_id = ? AND group_id IN (?)", tenantID, groupIDs).Find(&endpoints)

	groupEndpointMap := make(map[string][]ResourcePermission)
	for _, ep := range endpoints {
		groupEndpointMap[ep.GroupID] = append(groupEndpointMap[ep.GroupID], resourcePermissionFromEndpoint(ep))
	}

	for _, gid := range groupIDs {
		gp := GroupPermission{
			GroupID:   gid,
			GroupName: groupNameMap[gid],
			Resources: groupEndpointMap[gid],
		}
		ctx.Groups = append(ctx.Groups, gp)
	}

	return ctx
}

// ResolvePermissions loads all CRM groups a customer belongs to and their
// ERPEndpoint configs. For private agents, returns an empty groups list
// (private = full access, enforced at gateway level).
func ResolvePermissions(tenantID, zaloUserID, customerCode, agentType string) GroupPermissionContext {
	return ResolvePermissionsWithGroup(tenantID, zaloUserID, customerCode, agentType, "")
}

// ---------------------------------------------------------------------------
// SignPermissionToken — creates HMAC-SHA256 signed JWT from permissions
// ---------------------------------------------------------------------------

// SignPermissionToken encodes the permission context into a signed JWT token.
// The token is valid for 5 minutes (covers the Langflow execution window).
func SignPermissionToken(permCtx GroupPermissionContext, signingKey string) (string, error) {
	if signingKey == "" {
		return "", fmt.Errorf("signing key is empty")
	}

	now := time.Now()
	claims := permissionClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(5 * time.Minute)),
			Issuer:    "cqa-gateway",
		},
		Permissions: permCtx,
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(signingKey))
}

// ---------------------------------------------------------------------------
// VerifyPermissionToken — verifies and decodes the signed JWT
// ---------------------------------------------------------------------------

// VerifyPermissionToken validates the JWT signature and expiry, then returns
// the decoded GroupPermissionContext. Returns error if tampered or expired.
func VerifyPermissionToken(tokenString, signingKey string) (*GroupPermissionContext, error) {
	if tokenString == "" {
		return nil, fmt.Errorf("empty permission token")
	}

	token, err := jwt.ParseWithClaims(tokenString, &permissionClaims{}, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return []byte(signingKey), nil
	})

	if err != nil {
		return nil, fmt.Errorf("invalid permission token: %w", err)
	}

	claims, ok := token.Claims.(*permissionClaims)
	if !ok || !token.Valid {
		return nil, fmt.Errorf("invalid permission token claims")
	}

	return &claims.Permissions, nil
}

// ---------------------------------------------------------------------------
// Helper: check if a resource is permitted for a given permission context
// ---------------------------------------------------------------------------

// IsResourceAllowed checks if a specific resource is enabled in any of the
// customer's groups. Returns the merged allowed product groups and the most
// permissive scope type across all groups that enable this resource.
func (ctx *GroupPermissionContext) IsResourceAllowed(resource string) (allowed bool, scopeType string, productGroups []string) {
	if ctx.AgentType == "private" {
		// Evaluate the "private_bot" group permissions if configured.
		// If private_bot is not configured yet (empty resources), fallback to full access.
		var hasAnyPrivateConfig = false
		for _, group := range ctx.Groups {
			if group.GroupID == "private_bot" && len(group.Resources) > 0 {
				hasAnyPrivateConfig = true
			}
		}

		if !hasAnyPrivateConfig {
			return true, "all", nil
		}

		for _, group := range ctx.Groups {
			if group.GroupID == "private_bot" {
				for _, res := range group.Resources {
					if res.Resource == resource {
						if res.IsEnabled {
							allowed = true
							scopeType = res.ScopeType
							productGroups = res.ProductGroups
							return
						}
					}
				}
			}
		}
		return false, "own", nil
	}

	// Per-group evaluation: each group's scope applies to that group's data
	// We return the union of all enabled groups' product filters
	// and the most permissive scope
	scopePriority := map[string]int{"all": 3, "assigned": 2, "own": 1}
	bestScope := ""
	bestPriority := 0

	seen := make(map[string]bool)

	for _, group := range ctx.Groups {
		for _, res := range group.Resources {
			if res.Resource == resource && res.IsEnabled {
				allowed = true

				// Merge product groups (union)
				for _, pg := range res.ProductGroups {
					if !seen[pg] {
						seen[pg] = true
						productGroups = append(productGroups, pg)
					}
				}

				// Keep most permissive scope
				priority := scopePriority[res.ScopeType]
				if priority > bestPriority {
					bestPriority = priority
					bestScope = res.ScopeType
				}
			}
		}
	}

	if bestScope == "" {
		bestScope = "own" // default to most restrictive
	}
	scopeType = bestScope

	return
}

// ResolveBrandFilter returns the brand (nhãn hiệu) boundary for a resource,
// parallel to the ProductGroups boundary in IsResourceAllowed but kept as a
// separate method so existing IsResourceAllowed callers are untouched.
//
// Returned semantics (safe by construction — an empty/"all" result never blocks):
//   - allBrands == true  → no brand restriction (the caller must NOT filter).
//   - allBrands == false && len(brands) > 0 → keep only products whose
//     lowercased nhan_hieu_name exactly equals one of brands.
//   - allBrands == false && len(brands) == 0 → coerced to allBrands=true
//     (no restriction) so a misconfigured/empty filter or a legacy JWT without
//     these fields can never silently hide every product.
func (ctx *GroupPermissionContext) ResolveBrandFilter(resource string) (brands []string, allBrands bool) {
	defer func() {
		if !allBrands && len(brands) == 0 {
			allBrands = true // empty list is never a restriction
		}
	}()

	if ctx.AgentType == "private" {
		hasAnyPrivateConfig := false
		for _, group := range ctx.Groups {
			if group.GroupID == "private_bot" && len(group.Resources) > 0 {
				hasAnyPrivateConfig = true
			}
		}
		if !hasAnyPrivateConfig {
			return nil, true // private_bot unconfigured → all brands
		}
		for _, group := range ctx.Groups {
			if group.GroupID != "private_bot" {
				continue
			}
			for _, res := range group.Resources {
				if res.Resource == resource && res.IsEnabled {
					return res.Brands, res.AllBrands
				}
			}
		}
		return nil, true
	}

	// Public/per-group: union brands across enabled groups; "all brands" in any
	// enabled group wins (most permissive), matching the scope-union semantics.
	seen := make(map[string]bool)
	anyEnabled := false
	for _, group := range ctx.Groups {
		for _, res := range group.Resources {
			if res.Resource != resource || !res.IsEnabled {
				continue
			}
			anyEnabled = true
			if res.AllBrands {
				return nil, true
			}
			for _, b := range res.Brands {
				if !seen[b] {
					seen[b] = true
					brands = append(brands, b)
				}
			}
		}
	}
	if !anyEnabled {
		return nil, true
	}
	return brands, false
}

// GetGroupSpecificPermission returns the permission for a specific resource
// within a specific group. Used when group context is known.
func (ctx *GroupPermissionContext) GetGroupSpecificPermission(groupID, resource string) *ResourcePermission {
	for _, group := range ctx.Groups {
		if group.GroupID == groupID {
			for _, res := range group.Resources {
				if res.Resource == resource {
					return &res
				}
			}
		}
	}
	return nil
}
