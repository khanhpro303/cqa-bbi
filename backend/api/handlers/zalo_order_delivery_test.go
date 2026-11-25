package handlers

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/vietbui/chat-quality-agent/engine"
	"github.com/vietbui/chat-quality-agent/pkg"
)

func TestLookupCurrentZaloOrderDelivery(t *testing.T) {
	_, unexpectedBackendCalls := setupWarehouseFixture(t)
	today := pkg.ToVN(time.Now())
	expectedDays := 0
	var orderLookups, appointmentLookups int
	http.DefaultTransport = warehouseFixtureTransport(func(req *http.Request) (*http.Response, error) {
		body := `{"result":{"uid":1,"session_id":"fixture"}}`
		switch req.URL.Path {
		case "/web/session/authenticate":
		case "/rest_api/private/saorders/search":
			orderLookups++
			var query map[string]string
			if err := json.NewDecoder(req.Body).Decode(&query); err != nil {
				t.Fatal(err)
			}
			wantFrom := today.AddDate(0, 0, -expectedDays).Format("02/01/2006")
			if req.Method != http.MethodPost || len(query) != 2 || query["TU_NGAY"] != wantFrom || query["DEN_NGAY"] != today.Format("02/01/2006") {
				t.Fatalf("unexpected current order lookup: %s %v", req.Method, query)
			}
			body = `{"data":[{"SO_DON_HANG":"DHWS900001","MA_KHACH_HANG":[123,"TEST001 - Own"],"TRANG_THAI":"5"},
				{"SO_DON_HANG":"DHWS900002","MA_KHACH_HANG":[123,"TEST001 - Own"],"TRANG_THAI":"4","THOI_GIAN_TAO":"2026-11-25"},
				{"SO_DON_HANG":"DHWS900003","MA_KHACH_HANG":[456,"TEST999 - Other"],"TRANG_THAI":"5"}]}`
			if expectedDays == 5 {
				body = strings.TrimSuffix(body, "]}")
				body += `,{"SO_DON_HANG":"DHWS900004","MA_KHACH_HANG":[123,"TEST001 - Own"]}]}`
			}
		case "/web/dataset/call_kw/account.ex.don.dat.hang/search_read":
			appointmentLookups++
			body = `{"result":[{"SO_DON_HANG":"DHWS900001","KHACH_HANG_ID":[123,"TEST001 - Own"],"NGAY_GIAO_HANG":"2026-11-27"},
				{"SO_DON_HANG":"DHWS900002","KHACH_HANG_ID":[123,"TEST001 - Own"],"NGAY_GIAO_HANG":false},
				{"SO_DON_HANG":"DHWS900003","KHACH_HANG_ID":[456,"TEST999 - Other"],"NGAY_GIAO_HANG":"2026-12-15"}]}`
		default:
			return nil, fmt.Errorf("unexpected upstream path %s", req.URL.Path)
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body))}, nil
	})

	sign := func(agent, scope string, isAllowed bool) string {
		t.Helper()
		token, err := engine.SignPermissionToken(engine.GroupPermissionContext{
			TenantID: stockFixtureTenant, AgentType: agent, ZaloUserID: "customer", CustomerCode: "TEST001",
			Groups: []engine.GroupPermission{{GroupID: "grp-1", Resources: []engine.ResourcePermission{
				{Resource: "orders", IsEnabled: isAllowed, ScopeType: scope},
			}}},
		}, stockFixtureSigningKey)
		if err != nil {
			t.Fatal(err)
		}
		return token
	}
	for name, fixture := range map[string]struct {
		agentToken, permissionToken string
	}{
		"missing permission token": {"fixture-public-token", ""},
		"private permission token": {"fixture-agent-token", sign("private", "all", true)},
		"wrong agent token":        {"fixture-agent-token", sign("public", "own", true)},
	} {
		t.Run(name, func(t *testing.T) {
			got, unresolved, err := LookupCurrentZaloOrderDelivery(t.Context(), stockFixtureTenant, fixture.agentToken, fixture.permissionToken, 0)
			if err == nil || got != "" || unresolved {
				t.Fatalf("unsigned or mis-scoped lookup: %q, %v", got, err)
			}
		})
	}
	for _, permission := range []string{sign("public", "own", false), sign("public", "all", true)} {
		got, unresolved, err := LookupCurrentZaloOrderDelivery(t.Context(), stockFixtureTenant, "fixture-public-token", permission, 0)
		if err != nil || got != zaloOrderDeliveryAccessDenied || unresolved {
			t.Fatalf("access denial: %q, %v", got, err)
		}
	}
	if got, unresolved, err := LookupCurrentZaloOrderDelivery(t.Context(), stockFixtureTenant, "fixture-public-token", sign("public", "own", true), 4); err == nil || got != "" || unresolved {
		t.Fatalf("invalid period: %q, %t, %v", got, unresolved, err)
	}
	if orderLookups != 0 || appointmentLookups != 0 {
		t.Fatal("denied lookups must not call Cloudify")
	}

	got, unresolved, err := LookupCurrentZaloOrderDelivery(t.Context(), stockFixtureTenant, "fixture-public-token", sign("public", "own", true), 0)
	if err != nil || unresolved {
		t.Fatalf("today: %q, %t, %v", got, unresolved, err)
	}
	for _, want := range []string{"phát sinh hôm nay", "1 đơn Chờ giao và 1 đơn Đang giao", "DHWS900001 có ngày hẹn giao 27/11/2026", "3, 5 hoặc 7 ngày"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in %q", want, got)
		}
	}
	for _, forbidden := range []string{"DHWS900002", "DHWS900003", "15/12/2026", "25/11/2026", "TEST999"} {
		if strings.Contains(got, forbidden) {
			t.Errorf("found invalid or unscoped data %q in %q", forbidden, got)
		}
	}
	for _, days := range []int{3, 5, 7} {
		expectedDays = days
		reply, unresolved, err := LookupCurrentZaloOrderDelivery(t.Context(), stockFixtureTenant, "fixture-public-token", sign("public", "own", true), days)
		period := fmt.Sprintf("%d ngày gần đây (từ %s đến %s)", days, today.AddDate(0, 0, -days).Format("02/01/2006"), today.Format("02/01/2006"))
		if err != nil || !strings.Contains(reply, period) || !strings.Contains(reply, "1 đơn Chờ giao và 1 đơn Đang giao") || strings.Contains(reply, "muốn em xem thêm") {
			t.Errorf("period %d: %q, %v", days, reply, err)
		}
		if unresolved != (days == 5) || strings.Contains(reply, "1 đơn chưa xác định được trạng thái") != (days == 5) {
			t.Errorf("period %d unexpected unresolved status: %q, %t", days, reply, unresolved)
		}
	}
	if orderLookups != 4 || appointmentLookups != 1 || unexpectedBackendCalls.Load() != 0 {
		t.Fatalf("unexpected upstream counts: orders=%d, appointments=%d, other=%d", orderLookups, appointmentLookups, unexpectedBackendCalls.Load())
	}
}

func TestRenderZaloOrderDeliveryResponseRequiresVerifiedResults(t *testing.T) {
	const scoped = `{"status":"success","resource":"orders","scope":"own","source":"cloudify_live","date_basis":"order_created","range_days":0,"from":"25/11/2026","to":"25/11/2026",`
	for _, tc := range []struct {
		name, body, want string
		status           int
		unresolved       bool
	}{
		{"verified empty", scoped + `"orders_summary":{"total_orders":0}}`, "không thấy đơn hàng nào", http.StatusOK, false},
		{"valid orders without delivery status", scoped + `"orders_summary":{"total_orders":1,"by_status":[{"status":"2","count":1}]}}`, "chưa thấy đơn nào có trạng thái Chờ giao hoặc Đang giao", http.StatusOK, false},
		{"unknown status", scoped + `"orders_summary":{"total_orders":1,"by_status":[{"status":"","count":1}]}}`, "1 đơn chưa xác định được trạng thái", http.StatusOK, true},
		{"incomplete summary", scoped + `"orders_summary":{"total_orders":1}}`, "", http.StatusOK, false},
		{"no summary", scoped + `"orders_summary":null}`, "", http.StatusOK, false},
		{"unsupported date basis", strings.Replace(scoped, "order_created", "order_delivery", 1) + `"orders_summary":{"total_orders":0}}`, "", http.StatusOK, false},
		{"unexpected period", strings.Replace(scoped, `"range_days":0`, `"range_days":5`, 1) + `"orders_summary":{"total_orders":0}}`, "", http.StatusOK, false},
		{"mismatched date bounds", strings.Replace(scoped, `"from":"25/11/2026"`, `"from":"20/11/2026"`, 1) + `"orders_summary":{"total_orders":0}}`, "", http.StatusOK, false},
		{"unauthorized", `{"error":"unauthorized"}`, "", http.StatusUnauthorized, false},
		{"provider unavailable", `{"error":"upstream_error"}`, "", http.StatusBadGateway, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, unresolved, err := renderZaloOrderDeliveryResponse(tc.status, []byte(tc.body), 0)
			if tc.want == "" {
				if err == nil || got != "" {
					t.Fatalf("unverified data returned %q, %v", got, err)
				}
				return
			}
			if err != nil || unresolved != tc.unresolved || !strings.Contains(got, tc.want) || !strings.Contains(got, "3, 5 hoặc 7 ngày") {
				t.Fatalf("verified result returned %q, %v", got, err)
			}
		})
	}
}
