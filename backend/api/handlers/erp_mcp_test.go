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
	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/db/models"
	"github.com/vietbui/chat-quality-agent/engine"
	"github.com/vietbui/chat-quality-agent/pkg"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

const stockFixtureTenant = "fixture-warehouse-tenant"
const stockFixtureSigningKey = "01234567890123456789012345678901"

type warehouseFixtureTransport func(*http.Request) (*http.Response, error)

func (f warehouseFixtureTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func setupWarehouseFixture(t *testing.T) (*pkg.CloudifyClient, *int) {
	t.Helper()
	t.Setenv("JWT_SECRET", stockFixtureSigningKey)
	t.Setenv("ENCRYPTION_KEY", stockFixtureSigningKey)
	t.Setenv("DB_PASSWORD", "fixture")
	database, err := gorm.Open(mysql.New(mysql.Config{
		DSN:                       "fixture@tcp(127.0.0.1:3306)/fixture",
		SkipInitializeWithVersion: true,
	}), &gorm.Config{DryRun: true, DisableAutomaticPing: true, SkipDefaultTransaction: true})
	if err != nil {
		t.Fatal(err)
	}
	saved := db.DB
	db.DB = database
	t.Cleanup(func() {
		db.DB = saved
		pool, _ := database.DB()
		_ = pool.Close()
	})

	products := []models.CachedProduct{
		{MA: "SP457996", MA_CHA: "SP457204", TEN: "Bulldog DOGO", LIST_TEN_NHOM_VTHH: "Mũ", NHAN_HIEU_NAME: "Bulldog"},
		{MA: "SP458003", MA_CHA: "SP457204", LIST_TEN_NHOM_VTHH: "Mũ", NHAN_HIEU_NAME: "Bulldog"},
		{MA: "SP457220_DK", MA_CHA: "SP457212_DK", LIST_TEN_NHOM_VTHH: "Mũ", NHAN_HIEU_NAME: "Bulldog"},
		{MA: "OTHER", MA_CHA: "SP457212_DK", TEN: "Mũ DOGO", LIST_TEN_NHOM_VTHH: "Kính", NHAN_HIEU_NAME: "Bulldog"},
	}
	err = database.Callback().Query().After("gorm:query").Register("fixture:warehouse", func(q *gorm.DB) {
		switch result := q.Statement.Dest.(type) {
		case *[]models.CachedProduct:
			sql, vars := q.Statement.SQL.String(), q.Statement.Vars
			switch {
			case strings.Contains(sql, "ma_cha IN") && len(vars) == 3 && vars[0] == stockFixtureTenant && vars[1] == stockFixtureTenant && vars[2] == "Bulldog DOGO":
				*result = products
			case len(vars) == 2 && vars[0] == stockFixtureTenant && vars[1] == "SP457996":
				*result = products[:1]
			case strings.Contains(sql, "ma_cha IN") && len(vars) == 3 && vars[0] == stockFixtureTenant && vars[1] == "SP457204" && vars[2] == "SP457204_DK":
				// Parent stock reads the parent and its "_DK" copy parent.
				*result = products[:2]
			default:
				t.Errorf("unexpected catalog query: %s %v", sql, vars)
			}
		case *int64:
			// isParentProductCode: SP457204 is the parent of two SKUs.
			if vars := q.Statement.Vars; strings.Contains(q.Statement.SQL.String(), "ma_cha IN") && len(vars) == 3 && vars[1] == "SP457204" && vars[2] == "SP457204_DK" {
				*result = 2
				q.RowsAffected = 1 // gorm's Count keeps the scanned value only when one row came back
			}
		case *models.AppSetting:
			key := ""
			if len(q.Statement.Vars) >= 2 {
				key, _ = q.Statement.Vars[1].(string)
			}
			switch key {
			case "ai_agent_erp_token_private":
				result.ValuePlain = "fixture-agent-token"
			case "ai_agent_erp_token_public":
				result.ValuePlain = "fixture-public-token"
			case "erp_private_active":
				result.ValuePlain = "true"
			case "erp_api_url":
				result.ValuePlain = "https://warehouse-mcp.test"
			case "erp_api_username":
				result.ValuePlain = "fixture-user"
			case "erp_branch_name":
				result.ValuePlain = "Fixture branch"
			}
			if strings.Contains(q.Statement.SQL.String(), "setting_key = 'erp_integration_active'") {
				result.ValuePlain = "true"
			}
			if strings.Contains(q.Statement.SQL.String(), "setting_key = 'erp_api_password'") {
				result.ValuePlain = os.Getenv("DB_PASSWORD")
			}
		}
	})
	if err != nil {
		t.Fatal(err)
	}

	calls := 0
	previous := http.DefaultTransport
	client := &pkg.CloudifyClient{BaseURL: "https://warehouse-mcp.test", Login: "fixture-user", Password: os.Getenv("DB_PASSWORD")}
	http.DefaultTransport = warehouseFixtureTransport(func(req *http.Request) (*http.Response, error) {
		body := `{"result":{"uid":1,"session_id":"fixture"}}`
		if req.URL.Path != "/web/session/authenticate" {
			calls++
			query := req.URL.Query()
			if req.URL.Path != "/rest_api/private/tong_hop_ton_kho/search" ||
				req.Method != http.MethodPost || query.Get("DS_KHO") != engine.MainWarehouseCode ||
				query.Get("CHI_NHANH") != "Fixture branch" || query.Get("TU_NGAY") != query.Get("DEN_NGAY") ||
				query.Get("TU_NGAY") == "" || query.Get("BAO_GOM_SO_LIEU_CHI_NHANH_PHU_THUOC") != "true" {
				t.Errorf("unexpected warehouse request: %s %s", req.Method, req.URL.Path)
			}
			body = `[{"MA_HANG":"SP457996","SO_LUONG_CUOI_KY":30},{"MA_HANG":"SP457996","SO_LUONG_CUOI_KY":"2"},{"MA_HANG":"SP458003","SO_LUONG_CUOI_KY":4},{"MA_HANG":"SP457220_DK","SO_LUONG_CUOI_KY":1},{"MA_HANG":"OTHER","SO_LUONG_CUOI_KY":100}]`
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body))}, nil
	})
	t.Cleanup(func() {
		http.DefaultTransport = previous
		client.InvalidateSession()
	})
	return client, &calls
}

func TestERPMCPWarehouseAndPermission(t *testing.T) {
	_, calls := setupWarehouseFixture(t)
	router := gin.New()
	router.POST("/mcp/erp/:tenantId", ERPMCP)
	signed := func(tenant string, allowed bool) string {
		token, err := engine.SignPermissionToken(engine.GroupPermissionContext{
			TenantID: tenant, AgentType: "private", ZaloUserID: "fixture-zalo",
			Groups: []engine.GroupPermission{{GroupID: "private_bot", Resources: []engine.ResourcePermission{
				{Resource: "inventory", IsEnabled: allowed, ScopeType: "all", ProductGroups: []string{"mũ"}, Brands: []string{"bulldog"}},
			}}},
		}, stockFixtureSigningKey)
		if err != nil {
			t.Fatal(err)
		}
		return token
	}
	request := func(permission, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/mcp/erp/"+stockFixtureTenant, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Agent-Token", "fixture-agent-token")
		req.Header.Set("X-Permission-Token", permission)
		res := httptest.NewRecorder()
		router.ServeHTTP(res, req)
		return res
	}
	const sku = `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"inventory_by_sku","arguments":{"sku":"SP457996"}}}`
	const line = `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"inventory_by_line","arguments":{"line_name":"Bulldog DOGO"}}}`
	// A parent code passed to inventory_by_sku sums the whole line (FF818 bug:
	// the agent passed parent_codes[0] and got one variant's stock).
	const parent = `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"inventory_by_sku","arguments":{"search":"SP457204"}}}`
	for _, tc := range []struct {
		permission string
		input      string
		want       string
		fails      bool
	}{
		{"", line, "forbidden_token", true},
		{signed("other-tenant", true), line, "forbidden_token", true},
		{signed(stockFixtureTenant, false), sku, "forbidden_scope", true},
		{signed(stockFixtureTenant, true), sku, `"ton_kho":32`, false},
		{signed(stockFixtureTenant, true), line, `"ton_kho":37`, false},
		{signed(stockFixtureTenant, true), parent, `"ton_kho":36`, false},
	} {
		res := request(tc.permission, tc.input)
		var data struct {
			Result struct {
				IsError bool `json:"isError"`
				Content []struct{ Text string }
			}
		}
		if res.Code != 200 || json.Unmarshal(res.Body.Bytes(), &data) != nil || len(data.Result.Content) != 1 ||
			!strings.Contains(data.Result.Content[0].Text, tc.want) || data.Result.IsError != tc.fails {
			t.Errorf("expected result %q (error=%t), got %d %s", tc.want, tc.fails, res.Code, res.Body.String())
		}
	}
	if *calls != 3 {
		t.Errorf("report requests = %d, want 3", *calls)
	}
	discovery := request("", `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	if discovery.Code != http.StatusOK || !strings.Contains(discovery.Body.String(), "inventory_by_line") ||
		strings.Contains(discovery.Body.String(), "permission_token") {
		t.Errorf("unexpected tool list: %d %s", discovery.Code, discovery.Body.String())
	}
}
