package engine

import (
	"os"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/pkg"
)

func TestSignAndVerifyPermissionToken(t *testing.T) {
	signingKey := "my-secret-test-key-12345"

	permCtx := GroupPermissionContext{
		TenantID:     "tenant-abc",
		ZaloUserID:   "zalo-123",
		CustomerCode: "CUST-001",
		AgentType:    "public",
		Groups: []GroupPermission{
			{
				GroupID:   "group-1",
				GroupName: "Nhóm Hàng Bò Mỹ",
				Resources: []ResourcePermission{
					{
						Resource:      "products",
						IsEnabled:     true,
						ScopeType:     "own",
						ProductGroups: []string{"bò mỹ", "bò úc"},
					},
				},
			},
		},
	}

	// 1. Sign token
	token, err := SignPermissionToken(permCtx, signingKey)
	if err != nil {
		t.Fatalf("failed to sign token: %v", err)
	}

	if token == "" {
		t.Fatalf("expected non-empty token")
	}

	// 2. Verify token
	decoded, err := VerifyPermissionToken(token, signingKey)
	if err != nil {
		t.Fatalf("failed to verify token: %v", err)
	}

	if decoded.TenantID != permCtx.TenantID || decoded.ZaloUserID != permCtx.ZaloUserID || decoded.CustomerCode != permCtx.CustomerCode {
		t.Errorf("decoded token fields do not match")
	}

	if len(decoded.Groups) != 1 || decoded.Groups[0].GroupID != "group-1" {
		t.Errorf("decoded groups do not match")
	}

	// 3. Verify with wrong key
	_, err = VerifyPermissionToken(token, "wrong-key")
	if err == nil {
		t.Errorf("expected verification failure with wrong key")
	}
}

func TestVerifyExpiredPermissionToken(t *testing.T) {
	signingKey := "my-secret-test-key-12345"

	// Sign a token that's already expired
	claims := permissionClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			IssuedAt:  jwt.NewNumericDate(time.Now().Add(-10 * time.Minute)),
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(-5 * time.Minute)),
			Issuer:    "cqa-gateway",
		},
		Permissions: GroupPermissionContext{
			TenantID:   "tenant-abc",
			ZaloUserID: "zalo-123",
			AgentType:  "public",
		},
	}

	tokenObj := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	token, err := tokenObj.SignedString([]byte(signingKey))
	if err != nil {
		t.Fatalf("failed to sign expired token: %v", err)
	}

	_, err = VerifyPermissionToken(token, signingKey)
	if err == nil {
		t.Errorf("expected verification failure for expired token")
	}
}

func TestIsResourceAllowed(t *testing.T) {
	// A. Whitelisted Internal Staff (private agent) -> full access when not configured
	privateCtx := GroupPermissionContext{
		AgentType: "private",
	}

	allowed, scope, products := privateCtx.IsResourceAllowed("orders")
	if !allowed || scope != "all" || len(products) != 0 {
		t.Errorf("private agent should have full access when not configured")
	}

	// A2. Whitelisted Internal Staff (private agent) -> restricted access when configured
	privateConfigCtx := GroupPermissionContext{
		AgentType: "private",
		Groups: []GroupPermission{
			{
				GroupID: "private_bot",
				Resources: []ResourcePermission{
					{
						Resource:      "products",
						IsEnabled:     true,
						ScopeType:     "assigned",
						ProductGroups: []string{"bò úc"},
					},
					{
						Resource:  "orders",
						IsEnabled: false,
						ScopeType: "all",
					},
				},
			},
		},
	}

	allowed, scope, products = privateConfigCtx.IsResourceAllowed("products")
	if !allowed || scope != "assigned" || len(products) != 1 || products[0] != "bò úc" {
		t.Errorf("private agent products should be restricted to bò úc, scope assigned")
	}

	allowed, _, _ = privateConfigCtx.IsResourceAllowed("orders")
	if allowed {
		t.Errorf("private agent orders should be disabled")
	}

	// B. Customer (public agent) with single group permission
	publicCtx := GroupPermissionContext{
		AgentType: "public",
		Groups: []GroupPermission{
			{
				GroupID: "group-1",
				Resources: []ResourcePermission{
					{
						Resource:      "products",
						IsEnabled:     true,
						ScopeType:     "own",
						ProductGroups: []string{"bò mỹ"},
					},
					{
						Resource:  "orders",
						IsEnabled: false,
						ScopeType: "all",
					},
				},
			},
		},
	}

	// products is allowed, own scope, bò mỹ filter
	allowed, scope, products = publicCtx.IsResourceAllowed("products")
	if !allowed || scope != "own" || len(products) != 1 || products[0] != "bò mỹ" {
		t.Errorf("invalid permission for products: allowed=%v, scope=%s, products=%v", allowed, scope, products)
	}

	// orders is disabled (IsEnabled is false)
	allowed, _, _ = publicCtx.IsResourceAllowed("orders")
	if allowed {
		t.Errorf("orders should be disabled")
	}

	// C. Customer with multiple groups (merge check)
	multiCtx := GroupPermissionContext{
		AgentType: "public",
		Groups: []GroupPermission{
			{
				GroupID: "group-1",
				Resources: []ResourcePermission{
					{
						Resource:      "products",
						IsEnabled:     true,
						ScopeType:     "own",
						ProductGroups: []string{"bò mỹ"},
					},
				},
			},
			{
				GroupID: "group-2",
				Resources: []ResourcePermission{
					{
						Resource:      "products",
						IsEnabled:     true,
						ScopeType:     "assigned",
						ProductGroups: []string{"bò úc", "bò mỹ"},
					},
				},
			},
		},
	}

	// products merge: best scope is "assigned" (higher priority than "own"), product groups union is ["bò mỹ", "bò úc"]
	allowed, scope, products = multiCtx.IsResourceAllowed("products")
	if !allowed || scope != "assigned" {
		t.Errorf("expected scope assigned, got %s", scope)
	}

	if len(products) != 2 {
		t.Errorf("expected 2 product groups, got %d", len(products))
	}
}

func TestResolvePermissionsWithGroup(t *testing.T) {
	dsn := os.Getenv("TEST_DB_DSN")
	if dsn == "" {
		dsn = "cqa:cqa_password@tcp(127.0.0.1:3306)/cqa?charset=utf8mb4&parseTime=True&loc=UTC"
	}

	if err := db.Connect(dsn, false); err != nil {
		t.Skip("Skipping TestResolvePermissionsWithGroup: database not available")
		return
	}
	defer db.Close()

	tenantID := "testperm-" + pkg.NewUUID()[:8]
	groupID := "grp-" + pkg.NewUUID()[:8]

	// 1. Seed tenant and group
	db.DB.Exec(`INSERT INTO tenants (id, name, slug, settings, created_at, updated_at) VALUES (?, ?, ?, '{}', NOW(), NOW())`,
		tenantID, "Test Tenant", "test-"+tenantID)
	defer db.DB.Exec("DELETE FROM tenants WHERE id = ?", tenantID)

	db.DB.Exec(`INSERT INTO crm_groups (id, tenant_id, name, description, customer_code, created_at, updated_at) VALUES (?, ?, ?, ?, ?, NOW(), NOW())`,
		groupID, tenantID, "GMF Test Group", "Description", "CUST-GMF")
	defer db.DB.Exec("DELETE FROM crm_groups WHERE tenant_id = ?", tenantID)

	// 2. Seed ERPEndpoint for this group
	db.DB.Exec(`INSERT INTO erp_endpoints (id, tenant_id, group_id, resource, is_enabled, scope_type, product_groups, created_at, updated_at) VALUES (?, ?, ?, 'products', true, 'own', 'bò mỹ', NOW(), NOW())`,
		pkg.NewUUID(), tenantID, groupID, groupID)
	defer db.DB.Exec("DELETE FROM erp_endpoints WHERE tenant_id = ?", tenantID)

	// 3. Resolve permissions passing the groupID context
	ctx := ResolvePermissionsWithGroup(tenantID, "some-zalo-user", "CUST-GMF", "public", groupID)

	if ctx.TenantID != tenantID {
		t.Errorf("expected tenant ID %s, got %s", tenantID, ctx.TenantID)
	}

	if len(ctx.Groups) != 1 {
		t.Fatalf("expected exactly 1 group in context, got %d", len(ctx.Groups))
	}

	if ctx.Groups[0].GroupID != groupID {
		t.Errorf("expected group ID %s, got %s", groupID, ctx.Groups[0].GroupID)
	}

	if len(ctx.Groups[0].Resources) != 1 || ctx.Groups[0].Resources[0].Resource != "products" {
		t.Errorf("expected products resource permission")
	}

	if ctx.Groups[0].Resources[0].ScopeType != "own" || len(ctx.Groups[0].Resources[0].ProductGroups) != 1 || ctx.Groups[0].Resources[0].ProductGroups[0] != "bò mỹ" {
		t.Errorf("unexpected product resource details: %+v", ctx.Groups[0].Resources[0])
	}
}
