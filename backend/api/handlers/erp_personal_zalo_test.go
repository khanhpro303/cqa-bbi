package handlers

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/db/models"
	"github.com/vietbui/chat-quality-agent/engine"
	"gorm.io/gorm"
)

func TestPersonalZaloERPActivation(t *testing.T) {
	for _, tc := range []struct {
		name, role, private, global, url string
		want                             bool
	}{
		{"private switch off", "private", "false", "true", "configured", false},
		{"private switch on", "private", "true", "false", "", true},
		{"public group governed", "public", "false", "true", "", true},
		{"public disabled globally", "public", "true", "false", "configured", false},
		{"private legacy global", "private", "", "true", "", true},
		{"legacy url", "public", "", "", "configured", true},
		{"missing settings", "public", "", "", "", false},
		{"unknown role", "other", "", "true", "configured", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			settings := map[string]models.AppSetting{"erp_public_active": {ValuePlain: "false"}}
			for key, value := range map[string]string{"erp_private_active": tc.private, "erp_integration_active": tc.global, "erp_api_url": tc.url} {
				if value != "" {
					settings[key] = models.AppSetting{ValuePlain: value}
				}
			}
			if got := erpActiveForRole(settings, tc.role); got != tc.want {
				t.Errorf("ERP active = %t, want %t", got, tc.want)
			}
		})
	}
}

func TestPersonalZaloDebtReturnsOnlyRequestedCustomers(t *testing.T) {
	setupWarehouseFixture(t)
	if err := db.DB.Callback().Query().After("gorm:query").Register("fixture:debt_customer", func(q *gorm.DB) {
		if codes, ok := q.Statement.Dest.(*[]string); ok && q.Statement.Table == "cached_customers" && len(q.Statement.Vars) >= 2 && q.Statement.Vars[1] == "%S001%" {
			*codes = []string{"S001 - Fixture"}
		}
	}); err != nil {
		t.Fatal(err)
	}
	router := gin.New()
	router.POST("/erp/:tenantId", ERPQuery)
	sessionID := uuid.NewString()
	redisAddr := os.Getenv("TEST_REDIS_ADDR")
	if redisAddr != "" {
		client := redis.NewClient(&redis.Options{Addr: redisAddr})
		if err := client.Ping(t.Context()).Err(); err != nil {
			t.Fatal(err)
		}
		previous := db.RedisClient
		db.RedisClient = client
		t.Cleanup(func() {
			db.RedisClient = previous
			_ = client.Close()
		})
	}
	for _, tc := range []struct {
		name, role, search, partner, ledgerCodes string
		wantStatus                               int
		wantLedger                               bool
	}{
		{"customer and period", "private", "công nợ khách hàng S001 trong tháng trước", "", "S001 - Fixture", 200, true},
		{"unresolved customer and period", "private", "công nợ S404 quý này", "", "", 200, false},
		{"expired selection", "private", "tháng này", "", "", 200, false},
		{"partner asks period", "private", "công nợ", "123", "", 200, false},
		{"continue selected partner", "private", "tháng này", "", "S001 - Fixture", 200, true},
		{"unresolved partner", "private", "tháng này", "999", "", 502, false},
		{"matched partner", "private", "tháng này", "123", "S001 - Fixture", 200, true},
		{"verified own scope", "public", "tháng này", "", "S001 - Fixture", 200, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.name == "continue selected partner" && redisAddr == "" {
				t.Skip("TEST_REDIS_ADDR not set")
			}
			var ledgerCalled bool
			http.DefaultTransport = warehouseFixtureTransport(func(req *http.Request) (*http.Response, error) {
				var body string
				switch req.URL.Path {
				case "/web/session/authenticate":
					body = `{"result":{"uid":1,"session_id":"fixture"}}`
				case "/rest_api/private/partner/search":
					body = `[{"ID":123,"MA":"S001 - Fixture"}]`
				case "/rest_api/private/th_cong_no_phai_thu/search":
					ledgerCalled = true
					if got := req.URL.Query().Get("DS_KHACH_HANG"); got != tc.ledgerCodes {
						t.Errorf("ledger customer filter = %q, want %q", got, tc.ledgerCodes)
					}
					body = `[{"ma_kh":"s001","NO_SAU":42},{"MA_KHACH_HANG":"S001_1","NO_SAU":9999},{"NO_SAU":8888}]`
				default:
					t.Fatalf("unexpected endpoint %s", req.URL.Path)
				}
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}, nil
			})
			scope, agent, group := "all", "fixture-agent-token", "private_bot"
			if tc.role == "public" {
				scope, agent, group = "own", "fixture-public-token", "customer-group"
			}
			token, err := engine.SignPermissionToken(engine.GroupPermissionContext{
				PersonalZalo: true, TenantID: stockFixtureTenant, AgentType: tc.role,
				ChannelID: "personal", ZaloUserID: "123", CustomerCode: "S001 - Fixture", PersonalZaloSessionID: sessionID,
				Groups: []engine.GroupPermission{{GroupID: group, Resources: []engine.ResourcePermission{{Resource: "debt", IsEnabled: true, ScopeType: scope}}}},
			}, stockFixtureSigningKey)
			if err != nil {
				t.Fatal(err)
			}
			payload, err := json.Marshal(map[string]string{"resource": "debt", "search": tc.search, "partner_id": tc.partner})
			if err != nil {
				t.Fatal(err)
			}
			req := httptest.NewRequest(http.MethodPost, "/erp/"+stockFixtureTenant, strings.NewReader(string(payload)))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-Agent-Token", agent)
			req.Header.Set("X-Permission-Token", token)
			res := httptest.NewRecorder()
			router.ServeHTTP(res, req)
			if res.Code != tc.wantStatus || ledgerCalled != tc.wantLedger {
				t.Fatalf("response=%d %s ledgerCalled=%t", res.Code, res.Body, ledgerCalled)
			}
			if tc.wantLedger && (!strings.Contains(res.Body.String(), `"count":1`) || !strings.Contains(res.Body.String(), `"NO_SO_DU_CUOI_KY":42`) ||
				strings.Contains(res.Body.String(), "9999") || strings.Contains(res.Body.String(), "8888")) {
				t.Errorf("unexpected scoped ledger data: %s", res.Body)
			}
		})
	}
}

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
