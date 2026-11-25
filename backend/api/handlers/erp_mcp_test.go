package handlers

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
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

func setupWarehouseFixture(t *testing.T) (*pkg.CloudifyClient, *atomic.Int32) {
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
	zeroStock := models.CachedProduct{MA: "ZERO-STOCK", MA_CHA: "ZERO-LINE", LIST_TEN_NHOM_VTHH: "Mũ", NHAN_HIEU_NAME: "Bulldog"}
	missingStock := models.CachedProduct{MA: "MISSING-STOCK", MA_CHA: "INCOMPLETE-LINE", LIST_TEN_NHOM_VTHH: "Mũ", NHAN_HIEU_NAME: "Bulldog"}
	err = database.Callback().Query().After("gorm:query").Register("fixture:warehouse", func(q *gorm.DB) {
		switch result := q.Statement.Dest.(type) {
		case *[]models.CachedProduct:
			sql, vars := q.Statement.SQL.String(), q.Statement.Vars
			switch {
			case strings.Contains(sql, "ma_cha IN") && len(vars) == 3 && vars[0] == stockFixtureTenant && vars[1] == stockFixtureTenant && vars[2] == "Bulldog DOGO":
				*result = products
			case strings.Contains(sql, "ma_cha IN") && len(vars) == 3 && vars[0] == stockFixtureTenant && vars[1] == stockFixtureTenant && vars[2] == "Dòng thiếu dữ liệu":
				*result = []models.CachedProduct{products[0], missingStock}
			case len(vars) == 2 && vars[0] == stockFixtureTenant && vars[1] == "SP457996":
				*result = products[:1]
			case len(vars) == 2 && vars[0] == stockFixtureTenant && vars[1] == zeroStock.MA:
				*result = []models.CachedProduct{zeroStock}
			case len(vars) == 2 && vars[0] == stockFixtureTenant && vars[1] == missingStock.MA:
				*result = []models.CachedProduct{missingStock}
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

	var calls atomic.Int32
	warehouseRows := map[string]string{
		"SP457996":      `"SO_LUONG_TON":90,"SO_LUONG_TON_KHA_DUNG":"32"`,
		"SP458003":      `"SO_LUONG_TON":9,"SO_LUONG_TON_KHA_DUNG":4`,
		"SP457220_DK":   `"SO_LUONG_TON":3,"SO_LUONG_TON_KHA_DUNG":1`,
		zeroStock.MA:    `"SO_LUONG_TON":15,"SO_LUONG_TON_KHA_DUNG":0`,
		missingStock.MA: `"SO_LUONG_TON":15`,
	}
	previous := http.DefaultTransport
	client := &pkg.CloudifyClient{BaseURL: "https://warehouse-mcp.test", Login: "fixture-user", Password: os.Getenv("DB_PASSWORD")}
	http.DefaultTransport = warehouseFixtureTransport(func(req *http.Request) (*http.Response, error) {
		body := `{"result":{"uid":1,"session_id":"fixture"}}`
		if req.URL.Path != "/web/session/authenticate" {
			calls.Add(1)
			if req.URL.Path != "/rest_api/private/danhmucvattuhanghoa/lay_ton_kho_san_pham" || req.Method != http.MethodPost || req.Body == nil {
				return nil, fmt.Errorf("unexpected warehouse request: %s %s", req.Method, req.URL.Path)
			}
			defer req.Body.Close()
			var input map[string]string
			if err := json.NewDecoder(req.Body).Decode(&input); err != nil {
				return nil, fmt.Errorf("decode warehouse request: %w", err)
			}
			sku := input["MA_HANG"]
			row, ok := warehouseRows[sku]
			if !ok || len(input) != 1 {
				t.Errorf("unexpected warehouse payload: %v", input)
				return nil, fmt.Errorf("unrecognized warehouse request for SKU %q", sku)
			}
			body = fmt.Sprintf(`[{"MA_HANG":%q,"SO_LUONG_TON_TONG":999,"TON_KHO_CHI_TIET":[{"TEN_KHO":"KHO-TONG",%s},{"TEN_KHO":"KHO-KHAC","SO_LUONG_TON_KHA_DUNG":500}]}]`, sku, row)
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
	const zero = `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"inventory_by_sku","arguments":{"sku":"ZERO-STOCK"}}}`
	const missing = `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"inventory_by_sku","arguments":{"sku":"MISSING-STOCK"}}}`
	const incompleteLine = `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"inventory_by_line","arguments":{"line_name":"Dòng thiếu dữ liệu"}}}`
	// A parent code passed to inventory_by_sku sums the whole line (FF818 bug:
	// the agent passed parent_codes[0] and got one variant's stock).
	const parent = `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"inventory_by_sku","arguments":{"search":"SP457204"}}}`
	for _, tc := range []struct {
		permission string
		input      string
		want       string
		fails      bool
		wantCalls  int32
	}{
		{"", line, "forbidden_token", true, 0},
		{signed("other-tenant", true), line, "forbidden_token", true, 0},
		{signed(stockFixtureTenant, false), sku, "forbidden_scope", true, 0},
		{signed(stockFixtureTenant, true), sku, `"ton_kho":32`, false, 1},
		{signed(stockFixtureTenant, true), line, `"ton_kho":37`, false, 3},
		{signed(stockFixtureTenant, true), parent, `"ton_kho":36`, false, 2},
		{signed(stockFixtureTenant, true), zero, `"ton_kho":0`, false, 1},
		{signed(stockFixtureTenant, true), missing, "erp_data_unavailable", true, 1},
		{signed(stockFixtureTenant, true), incompleteLine, "erp_data_unavailable", true, -1},
	} {
		before := calls.Load()
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
			continue
		}
		body := data.Result.Content[0].Text
		if !tc.fails && !strings.Contains(body, `"stock_metric":"available"`) {
			t.Errorf("missing available-stock metric: %s", body)
		}
		if tc.fails && strings.Contains(body, `"ton_kho"`) {
			t.Errorf("failed request returned stock: %s", body)
		}
		if got := calls.Load() - before; tc.wantCalls >= 0 && got != tc.wantCalls {
			t.Errorf("warehouse requests = %d, want %d for %s", got, tc.wantCalls, tc.input)
		}
	}
	discovery := request("", `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	if discovery.Code != http.StatusOK || !strings.Contains(discovery.Body.String(), "inventory_by_line") ||
		strings.Contains(discovery.Body.String(), "permission_token") {
		t.Errorf("unexpected tool list: %d %s", discovery.Code, discovery.Body.String())
	}
}

func TestERPMCPWarehouseScopedMiss(t *testing.T) {
	_, calls := setupWarehouseFixture(t)
	permission, err := engine.SignPermissionToken(engine.GroupPermissionContext{
		TenantID: stockFixtureTenant, AgentType: "public", ZaloUserID: "fixture-zalo",
		Groups: []engine.GroupPermission{{GroupID: "fixture-group", Resources: []engine.ResourcePermission{
			{Resource: "inventory", IsEnabled: true, ScopeType: "own", ProductGroups: []string{"giày"}},
		}}},
	}, stockFixtureSigningKey)
	if err != nil {
		t.Fatal(err)
	}
	router := gin.New()
	router.POST("/mcp/erp/:tenantId", ERPMCP)
	request := httptest.NewRequest(http.MethodPost, "/mcp/erp/"+stockFixtureTenant, strings.NewReader(
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"inventory_by_sku","arguments":{"sku":"SP457996"}}}`,
	))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Agent-Token", "fixture-public-token")
	request.Header.Set("X-Permission-Token", permission)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	var response struct {
		Result struct {
			IsError bool `json:"isError"`
			Content []struct{ Text string }
		}
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil || recorder.Code != http.StatusOK || response.Result.IsError || len(response.Result.Content) != 1 {
		t.Fatalf("expected a successful scoped miss, got %d %s", recorder.Code, recorder.Body.String())
	}
	body := response.Result.Content[0].Text
	if !strings.Contains(body, `"lookup_outcome":"no_visible_match"`) || !strings.Contains(body, `"data":[]`) ||
		!strings.Contains(body, "chưa xác nhận được tồn kho") || strings.Contains(body, "SP457996") || strings.Contains(body, "Bulldog") {
		t.Errorf("unexpected scoped-miss payload: %s", body)
	}
	if got := calls.Load(); got != 0 {
		t.Errorf("queried live stock %d times for an excluded SKU", got)
	}
}

func TestDescribeERPProductLookup(t *testing.T) {
	for _, tc := range []struct {
		name, resource, body, outcome string
	}{
		{"catalog miss", "products", `{"status":"success","data":[],"count":0,"parent_codes":[],"source":"astradb_cache"}`, "no_visible_match"},
		{"variant miss", "product_variants", `{"status":"success","data":[],"count":0,"available_colors":[],"resolved_parent":"internal-parent"}`, "no_visible_match"},
		{"stock miss", "inventory", `{"status":"success","data":[],"count":0}`, "no_visible_match"},
		{"zero stock", "inventory", `{"status":"success","data":[{"ton_kho":0}],"count":1}`, ""},
		{"variant options", "product_variants", `{"status":"success","data":[],"count":0,"available_sizes":["L"]}`, ""},
		{"rich picker", "inventory", `{"status":"success","data":[],"count":0,"is_inventory_rich":true}`, ""},
		{"other ERP resource", "orders", `{"status":"success","data":[],"count":0}`, ""},
		{"access denied", "inventory", `{"error":"forbidden_scope"}`, ""},
		{"no data field", "inventory", `{"status":"success","count":0}`, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := string(describeERPProductLookup(tc.resource, []byte(tc.body)))
			if tc.outcome == "" {
				if got != tc.body {
					t.Errorf("unexpected change: %s", got)
				}
				return
			}
			if !strings.Contains(got, `"lookup_outcome":"`+tc.outcome+`"`) || !strings.Contains(got, `"data":[]`) ||
				!strings.Contains(got, `"customer_message":`) || strings.Contains(got, "internal-parent") {
				t.Errorf("unexpected lookup result: %s", got)
			}
		})
	}
}
