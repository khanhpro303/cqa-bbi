package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/vietbui/chat-quality-agent/engine"
)

// ERPQuery used to rebuild permissions from the caller-supplied zalo_user_id
// when no signed token came with the call, so an agent token alone (stored in
// Langflow) read any customer's data.
func TestERPQueryRequiresPermissionToken(t *testing.T) {
	_, calls := setupWarehouseFixture(t)
	router := gin.New()
	router.POST("/tenants/:tenantId/erp/query", ERPQuery)
	for _, agent := range []string{"fixture-agent-token", "fixture-public-token"} {
		req := httptest.NewRequest(http.MethodPost, "/tenants/"+stockFixtureTenant+"/erp/query",
			strings.NewReader(`{"resource":"debt","search":"công nợ tháng này","zalo_user_id":"victim"}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Agent-Token", agent)
		res := httptest.NewRecorder()
		router.ServeHTTP(res, req)
		if res.Code != http.StatusForbidden || !strings.Contains(res.Body.String(), "forbidden_token") {
			t.Errorf("agent %s without permission token: got %d %s, want 403 forbidden_token", agent, res.Code, res.Body.String())
		}
	}
	if *calls != 0 {
		t.Errorf("Cloudify was called %d times without a permission token", *calls)
	}
}

// A customer-facing debt call with no customer code must not reach Cloudify:
// an empty DS_KHACH_HANG returns every customer's ledger.
func TestPublicDebtWithoutCustomerCodeIsRefused(t *testing.T) {
	_, calls := setupWarehouseFixture(t)
	router := gin.New()
	router.POST("/tenants/:tenantId/erp/query", ERPQuery)
	token, err := engine.SignPermissionToken(engine.GroupPermissionContext{
		TenantID: stockFixtureTenant, AgentType: "public", ZaloUserID: "no-code-customer",
		Groups: []engine.GroupPermission{{GroupID: "grp-1", Resources: []engine.ResourcePermission{
			{Resource: "debt", IsEnabled: true, ScopeType: "own"},
		}}},
	}, stockFixtureSigningKey)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/tenants/"+stockFixtureTenant+"/erp/query",
		strings.NewReader(`{"resource":"debt","search":"công nợ tháng này"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Agent-Token", "fixture-public-token")
	req.Header.Set("X-Permission-Token", token)
	res := httptest.NewRecorder()
	router.ServeHTTP(res, req)
	if res.Code != http.StatusForbidden || !strings.Contains(res.Body.String(), "forbidden_scope") {
		t.Errorf("got %d %s, want 403 forbidden_scope", res.Code, res.Body.String())
	}
	if *calls != 0 {
		t.Errorf("Cloudify debt report was requested %d times with no customer code", *calls)
	}
}
