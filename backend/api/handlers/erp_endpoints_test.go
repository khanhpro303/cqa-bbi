package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/db/models"
	"github.com/vietbui/chat-quality-agent/pkg"
)

func TestSaveAndToggleGroupERPEndpoints(t *testing.T) {
	dsn := os.Getenv("TEST_DB_DSN")
	if dsn == "" {
		dsn = "cqa:cqa_password@tcp(127.0.0.1:3306)/cqa?charset=utf8mb4&parseTime=True&loc=UTC"
	}

	if err := db.Connect(dsn, false); err != nil {
		t.Skip("Skipping TestSaveAndToggleGroupERPEndpoints: database not available")
		return
	}
	defer db.Close()

	// Setup clean environment
	tenantID := "testten-" + pkg.NewUUID()[:8]
	groupID := "testgrp-" + pkg.NewUUID()[:8]

	// Create test tenant and group via raw inserts
	db.DB.Exec(`INSERT INTO tenants (id, name, slug, settings, created_at, updated_at) VALUES (?, ?, ?, '{}', NOW(), NOW())`,
		tenantID, "Test Tenant", "test-"+tenantID)
	defer db.DB.Exec("DELETE FROM tenants WHERE id = ?", tenantID)

	db.DB.Exec(`INSERT INTO crm_groups (id, tenant_id, name, description, customer_code, created_at, updated_at) VALUES (?, ?, ?, ?, ?, NOW(), NOW())`,
		groupID, tenantID, "GMF Test Group", "Description", "CUST-GMF")
	defer db.DB.Exec("DELETE FROM crm_groups WHERE tenant_id = ?", tenantID)
	defer db.DB.Exec("DELETE FROM erp_endpoints WHERE tenant_id = ?", tenantID)

	gin.SetMode(gin.TestMode)

	// 1. Test List Group ERP Endpoints (should return defaults)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = []gin.Param{{Key: "id", Value: groupID}}
	c.Set("tenant_id", tenantID)

	ListGroupERPEndpoints(c)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}

	var listResp struct {
		Endpoints []models.ERPEndpoint `json:"endpoints"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &listResp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	if len(listResp.Endpoints) != 5 {
		t.Fatalf("expected 5 endpoints, got %d", len(listResp.Endpoints))
	}

	// 2. Test Save Group ERP Endpoints
	saveReqBody := map[string]interface{}{
		"endpoints": []map[string]interface{}{
			{
				"resource":       "products",
				"is_enabled":     true,
				"scope_type":     "assigned",
				"product_groups": "bò mỹ,bò úc",
			},
			{
				"resource":       "inventory",
				"is_enabled":     false,
				"scope_type":     "own",
				"product_groups": "",
			},
		},
	}
	reqJSON, _ := json.Marshal(saveReqBody)

	w = httptest.NewRecorder()
	c, _ = gin.CreateTestContext(w)
	c.Params = []gin.Param{{Key: "id", Value: groupID}}
	c.Set("tenant_id", tenantID)
	c.Request, _ = http.NewRequest("PUT", "/endpoints", bytes.NewBuffer(reqJSON))
	c.Request.Header.Set("Content-Type", "application/json")

	SaveGroupERPEndpoints(c)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d (body: %s)", w.Code, w.Body.String())
	}

	// Check DB
	var dbEp models.ERPEndpoint
	err := db.DB.Where("tenant_id = ? AND group_id = ? AND resource = ?", tenantID, groupID, "products").First(&dbEp).Error
	if err != nil {
		t.Fatalf("failed to find saved endpoint in DB: %v", err)
	}
	if !dbEp.IsEnabled || dbEp.ScopeType != "assigned" || dbEp.ProductGroups != "bò mỹ,bò úc" {
		t.Fatalf("saved endpoint values do not match: %+v", dbEp)
	}

	// 3. Test Toggle Group ERP Endpoint
	toggleReqBody := map[string]interface{}{
		"resource":   "products",
		"is_enabled": false,
	}
	toggleJSON, _ := json.Marshal(toggleReqBody)

	w = httptest.NewRecorder()
	c, _ = gin.CreateTestContext(w)
	c.Params = []gin.Param{{Key: "id", Value: groupID}}
	c.Set("tenant_id", tenantID)
	c.Request, _ = http.NewRequest("POST", "/toggle", bytes.NewBuffer(toggleJSON))
	c.Request.Header.Set("Content-Type", "application/json")

	ToggleGroupERPEndpoint(c)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d (body: %s)", w.Code, w.Body.String())
	}

	// Check DB again
	err = db.DB.Where("tenant_id = ? AND group_id = ? AND resource = ?", tenantID, groupID, "products").First(&dbEp).Error
	if err != nil {
		t.Fatalf("failed to find toggled endpoint in DB: %v", err)
	}
	if dbEp.IsEnabled {
		t.Fatalf("expected products endpoint to be disabled, got enabled")
	}
}

func TestResourceRequiresProductGroups(t *testing.T) {
	cases := []struct {
		resource string
		want     bool
	}{
		{"products", true},
		{"inventory", true},
		{"orders", false},
		{"customers", false},
		{"debt", false},
		{"unknown", false},
	}
	for _, tc := range cases {
		if got := resourceRequiresProductGroups(tc.resource); got != tc.want {
			t.Errorf("resourceRequiresProductGroups(%q) = %v, want %v", tc.resource, got, tc.want)
		}
	}
}

// saveGroupERPEndpoints validation rejects an enabled product/inventory
// endpoint with an empty VTHH group filter BEFORE any DB access, so this runs
// without a database connection.
func TestSaveGroupERPEndpointsBlocksEmptyProductGroups(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cases := []struct {
		name       string
		resource   string
		isEnabled  bool
		groups     string
		wantStatus int
	}{
		{"enabled products empty groups -> 400", "products", true, "", http.StatusBadRequest},
		{"enabled inventory empty groups -> 400", "inventory", true, "   ", http.StatusBadRequest},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body, _ := json.Marshal(map[string]interface{}{
				"endpoints": []map[string]interface{}{
					{
						"resource":       tc.resource,
						"is_enabled":     tc.isEnabled,
						"scope_type":     "own",
						"product_groups": tc.groups,
					},
				},
			})

			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Params = []gin.Param{{Key: "id", Value: "grp-test"}}
			c.Set("tenant_id", "ten-test")
			c.Request, _ = http.NewRequest("PUT", "/endpoints", bytes.NewBuffer(body))
			c.Request.Header.Set("Content-Type", "application/json")

			SaveGroupERPEndpoints(c)

			if w.Code != tc.wantStatus {
				t.Fatalf("expected status %d, got %d (body: %s)", tc.wantStatus, w.Code, w.Body.String())
			}
			var resp struct {
				Error    string `json:"error"`
				Resource string `json:"resource"`
			}
			_ = json.Unmarshal(w.Body.Bytes(), &resp)
			if resp.Error != "product_groups_required" {
				t.Fatalf("expected error product_groups_required, got %q", resp.Error)
			}
			if resp.Resource != tc.resource {
				t.Fatalf("expected resource %q, got %q", tc.resource, resp.Resource)
			}
		})
	}
}

// SaveERPSettings validation rejects an enabled private-bot product/inventory
// endpoint with an empty group filter before any persistence.
func TestSaveERPSettingsBlocksEmptyProductGroups(t *testing.T) {
	gin.SetMode(gin.TestMode)

	body, _ := json.Marshal(map[string]interface{}{
		"public_active":  "false",
		"private_active": "true",
		"private_endpoints": []map[string]interface{}{
			{
				"resource":       "products",
				"is_enabled":     true,
				"scope_type":     "all",
				"product_groups": "",
			},
		},
	})

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = []gin.Param{{Key: "tenantId", Value: "ten-test"}}
	c.Request, _ = http.NewRequest("PUT", "/settings/erp", bytes.NewBuffer(body))
	c.Request.Header.Set("Content-Type", "application/json")

	SaveERPSettings(c)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d (body: %s)", w.Code, w.Body.String())
	}
	var resp struct {
		Error    string `json:"error"`
		Resource string `json:"resource"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Error != "product_groups_required" || resp.Resource != "products" {
		t.Fatalf("expected product_groups_required for products, got %+v", resp)
	}
}
