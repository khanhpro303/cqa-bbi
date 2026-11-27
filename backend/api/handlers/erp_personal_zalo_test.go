package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/vietbui/chat-quality-agent/engine"
)

func TestERPMCPPersonalZaloReadsAndRejectsWrites(t *testing.T) {
	_, calls := setupWarehouseFixture(t)
	router := gin.New()
	router.POST("/mcp/erp/:tenantId", ERPMCP)
	for _, tc := range []struct {
		name, agentType, group, scope, tool, args, want string
		wantError                                       bool
	}{
		{"private stock", "private", "private_bot", "all", "inventory_by_sku", `{"sku":"SP457996"}`, `"ton_kho":32`, false},
		{"public stock", "public", "crm-group", "own", "inventory_by_sku", `{"sku":"SP457996"}`, `"in_stock":true`, false},
		{"private restricted debt", "private", "private_bot", "own", "lookup_debt", `{"search":"công nợ tháng này"}`, "forbidden_scope", true},
		{"private restricted customer", "private", "private_bot", "assigned", "find_customer", `{"search":"customer"}`, "forbidden_scope", true},
		{"private create", "private", "private_bot", "all", "draft_order_create", `{}`, "forbidden_scope", true},
		{"public cart", "public", "crm-group", "own", "order_cart_update", `{}`, "forbidden_scope", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			grant := engine.GroupPermissionContext{
				TenantID: stockFixtureTenant, AgentType: tc.agentType, PersonalZalo: true,
				ChannelID: "personal-channel", ZaloUserID: "123", CustomerCode: "CUST-A",
				Groups: []engine.GroupPermission{{GroupID: tc.group, Resources: []engine.ResourcePermission{
					{Resource: "inventory", IsEnabled: true, ScopeType: tc.scope, AllBrands: true},
					{Resource: "debt", IsEnabled: true, ScopeType: tc.scope},
					{Resource: "customers", IsEnabled: true, ScopeType: tc.scope},
					{Resource: resourceOrderCreate, IsEnabled: true, ScopeType: tc.scope},
				}}},
			}
			token, err := engine.SignPermissionToken(grant, stockFixtureSigningKey)
			if err != nil {
				t.Fatal(err)
			}
			body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"` + tc.tool + `","arguments":` + tc.args + `}}`
			req := httptest.NewRequest(http.MethodPost, "/mcp/erp/"+stockFixtureTenant, strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-Permission-Token", token)
			if tc.agentType == "private" {
				req.Header.Set("X-Agent-Token", "fixture-agent-token")
			} else {
				req.Header.Set("X-Agent-Token", "fixture-public-token")
			}
			before := calls.Load()
			res := httptest.NewRecorder()
			router.ServeHTTP(res, req)
			var response struct {
				Result struct {
					IsError bool
					Content []struct{ Text string }
				}
			}
			if res.Code != http.StatusOK || json.Unmarshal(res.Body.Bytes(), &response) != nil ||
				len(response.Result.Content) != 1 || response.Result.IsError != tc.wantError ||
				!strings.Contains(response.Result.Content[0].Text, tc.want) {
				t.Fatalf("got %d %s; want %q (error=%v)", res.Code, res.Body, tc.want, tc.wantError)
			}
			if tc.agentType == "public" && strings.Contains(response.Result.Content[0].Text, `"ton_kho"`) {
				t.Fatalf("public response disclosed stock quantity: %s", response.Result.Content[0].Text)
			}
			if tc.wantError && calls.Load() != before {
				t.Fatal("refused ERP operation reached Cloudify")
			}
		})
	}
}

func TestPersonalZaloERPUsesPinnedChat(t *testing.T) {
	// The no-channel input exercises the legacy fallback boundary. A signed
	// personal grant never searches the tenant for an unrelated OA channel.
	grant := engine.GroupPermissionContext{PersonalZalo: true, ZaloUserID: "123"}
	if adapter, channel, err := loadZaloOAAdapterFor("tenant", &grant); adapter != nil || channel != nil || err == nil {
		t.Fatalf("personal grant must not load an OA: adapter=%v channel=%v error=%v", adapter, channel, err)
	}
	grant.ChannelID = "personal-channel"
	if key, err := erpCustomerStateKey("tenant", &grant); err == nil || key != "" {
		t.Fatalf("personal ERP follow-up requires a signed session, got %q err=%v", key, err)
	}
	grant.PersonalZaloSessionID = "personal-session"
	key, err := erpCustomerStateKey("tenant", &grant)
	if err != nil || key != "personal-zalo-erp-state:personal-session" {
		t.Fatalf("ERP follow-up state must use the originating chat, got %q err=%v", key, err)
	}
}
