package engine

import (
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
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
