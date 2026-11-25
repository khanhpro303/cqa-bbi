package handlers

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/engine"
	"github.com/vietbui/chat-quality-agent/pkg"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

// TestLeadingCustomerCode covers stripping the "<code> - <name>" label that
// Cloudify ships in MA_KHACH_HANG / customer codes down to the bare code.
func TestLeadingCustomerCode(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"S052 - Phượt 4P", "S052"},
		{"  S052 - Phượt 4P ", "S052"},
		{"EG05", "EG05"},
		{"", ""},
		{"BBI001 - Khách lẻ", "BBI001"},
	}
	for _, tc := range tests {
		if got := leadingCustomerCode(tc.in); got != tc.want {
			t.Errorf("leadingCustomerCode(%q) = %q; want %q", tc.in, got, tc.want)
		}
	}
}

// TestOrderCustomerCode covers the official Cloudify saorders/search shape
// where MA_KHACH_HANG is an array [id, "CODE - Name"], plus the legacy flat
// string / alias keys older payloads use.
func TestOrderCustomerCode(t *testing.T) {
	tests := []struct {
		name string
		item map[string]interface{}
		want string
	}{
		{
			name: "official array shape",
			item: map[string]interface{}{
				"MA_KHACH_HANG": []interface{}{float64(2273), "S052 - Phượt 4P"},
			},
			want: "S052",
		},
		{
			name: "array with only id",
			item: map[string]interface{}{
				"MA_KHACH_HANG": []interface{}{float64(2273)},
			},
			want: "2273",
		},
		{
			name: "flat string with label",
			item: map[string]interface{}{
				"MA_KHACH_HANG": "EG05 - EGO Store",
			},
			want: "EG05",
		},
		{
			name: "flat string bare code",
			item: map[string]interface{}{
				"MA_KHACH_HANG": "EG05",
			},
			want: "EG05",
		},
		{
			name: "legacy MA_KH alias",
			item: map[string]interface{}{
				"MA_KH": "BBI001",
			},
			want: "BBI001",
		},
		{
			name: "no customer field",
			item: map[string]interface{}{"foo": "bar"},
			want: "",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := orderCustomerCode(tc.item); got != tc.want {
				t.Errorf("orderCustomerCode() = %q; want %q", got, tc.want)
			}
		})
	}
}

// TestComputeOrderTotal verifies the total is derived from line items
// (Σ SO_LUONG × DON_GIA) minus the invoice-level discount, because the
// saorders/search payload carries no precomputed total field.
func TestComputeOrderTotal(t *testing.T) {
	tests := []struct {
		name string
		item map[string]interface{}
		want float64
	}{
		{
			name: "official warranty sample (zero price)",
			item: map[string]interface{}{
				"GIAM_GIA_HOA_DON": float64(0),
				"DON_DAT_HANG_CHI_TIET": []interface{}{
					map[string]interface{}{"SO_LUONG": float64(1), "DON_GIA": float64(0)},
				},
			},
			want: 0,
		},
		{
			name: "two lines minus discount",
			item: map[string]interface{}{
				"GIAM_GIA_HOA_DON": float64(20000),
				"DON_DAT_HANG_CHI_TIET": []interface{}{
					map[string]interface{}{"SO_LUONG": float64(2), "DON_GIA": float64(100000)},
					map[string]interface{}{"SO_LUONG": float64(1), "DON_GIA": float64(50000)},
				},
			},
			want: 230000,
		},
		{
			name: "discount larger than lines clamps to zero",
			item: map[string]interface{}{
				"GIAM_GIA_HOA_DON": float64(999999),
				"DON_DAT_HANG_CHI_TIET": []interface{}{
					map[string]interface{}{"SO_LUONG": float64(1), "DON_GIA": float64(1000)},
				},
			},
			want: 0,
		},
		{
			name: "missing line items",
			item: map[string]interface{}{},
			want: 0,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := computeOrderTotal(tc.item); got != tc.want {
				t.Errorf("computeOrderTotal() = %v; want %v", got, tc.want)
			}
		})
	}
}

// TestBuildOrdersSummaryRealShape feeds normalized records (as the orders
// handler builds them from saorders/search) and checks the per-status
// aggregation and the value derived from line items.
func TestBuildOrdersSummaryRealShape(t *testing.T) {
	records := []map[string]interface{}{
		{
			"status": "4",
			"total":  float64(230000),
			"don_dat_hang_chi_tiet": []interface{}{
				map[string]interface{}{"SO_LUONG": float64(3)},
			},
		},
		{
			"status": "2",
			"total":  float64(50000),
			"don_dat_hang_chi_tiet": []interface{}{
				map[string]interface{}{"SO_LUONG": float64(1)},
			},
		},
	}

	summary := buildOrdersSummary(records)
	if summary.TotalOrders != 2 {
		t.Errorf("TotalOrders = %d; want 2", summary.TotalOrders)
	}
	if summary.TotalValue != 280000 {
		t.Errorf("TotalValue = %v; want 280000", summary.TotalValue)
	}
	if summary.TotalQuantity != 4 {
		t.Errorf("TotalQuantity = %v; want 4", summary.TotalQuantity)
	}
	if len(summary.ByStatus) != 2 {
		t.Fatalf("ByStatus len = %d; want 2", len(summary.ByStatus))
	}
	// Canonical order: "4" (Chờ giao) before "2" (Hoàn thành).
	if summary.ByStatus[0].Status != "4" || summary.ByStatus[0].StatusName != "Chờ giao" {
		t.Errorf("ByStatus[0] = %+v; want status 4 Chờ giao", summary.ByStatus[0])
	}
	if summary.ByStatus[1].Status != "2" || summary.ByStatus[1].StatusName != "Hoàn thành" {
		t.Errorf("ByStatus[1] = %+v; want status 2 Hoàn thành", summary.ByStatus[1])
	}
}

// TestExtractOrderCode covers routing a customer message into the
// single-order lookup branch: the "ĐH"/"DH" + digits pattern must match
// across diacritic/case variants and when embedded in a sentence, while
// generic phrases, date-window replies and bare prefixes must not.
func TestExtractOrderCode(t *testing.T) {
	tests := []struct {
		name   string
		search string
		want   string
	}{
		{"canonical with diacritic", "ĐH000016", "ĐH000016"},
		{"plain D prefix", "DH000016", "DH000016"},
		{"web order", "đơn DHWS900001 sao rồi?", "DHWS900001"},
		{"not embedded in a SKU", "SP-DHWS900001_A", ""},
		{"lowercase diacritic", "đh9", "đh9"},
		{"lowercase plain", "dh12", "dh12"},
		{"embedded in a sentence", "đơn ĐH000016 sao rồi?", "ĐH000016"},
		{"generic order phrase", "đơn hàng", ""},
		{"date window reply", "7 ngày gần đây", ""},
		{"empty", "", ""},
		{"prefix without digits", "ĐH", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := extractOrderCode(tc.search); got != tc.want {
				t.Errorf("extractOrderCode(%q) = %q; want %q", tc.search, got, tc.want)
			}
		})
	}
}

// TestIsOrderAuthorized covers the per-scope ownership check used by both the
// single-order branch and the date-window filter loop. Unknown/empty scopes
// must deny by default.
func TestIsOrderAuthorized(t *testing.T) {
	allowed := []string{"S052 - Phượt 4P", "EG05"}
	tests := []struct {
		name         string
		itemCustCode string
		scopeType    string
		ownCodes     []string
		allowedCodes []string
		want         bool
	}{
		{"own match", "S052", "own", []string{"S052"}, nil, true},
		{"own match case-insensitive", "s052", "own", []string{"S052"}, nil, true},
		{"own mismatch", "EG05", "own", []string{"S052"}, nil, false},
		{"own match second of multi", "EG05", "own", []string{"S052", "EG05"}, nil, true},
		{"own no codes denies", "S052", "own", nil, nil, false},
		{"assigned in list (labelled)", "S052", "assigned", nil, allowed, true},
		{"assigned in list (bare)", "EG05", "assigned", nil, allowed, true},
		{"assigned not in list", "X999", "assigned", nil, allowed, false},
		{"all always visible", "ANY", "all", nil, nil, true},
		{"empty scope denies", "S052", "", []string{"S052"}, nil, false},
		{"unknown scope denies", "S052", "weird", []string{"S052"}, allowed, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := isOrderAuthorized(tc.itemCustCode, tc.scopeType, tc.ownCodes, tc.allowedCodes); got != tc.want {
				t.Errorf("isOrderAuthorized(%q, %q, %v, %v) = %v; want %v",
					tc.itemCustCode, tc.scopeType, tc.ownCodes, tc.allowedCodes, got, tc.want)
			}
		})
	}
}

// TestNormalizeOrderRecord verifies the single-order/date-window record reads
// the order code from SO_DON_HANG and surfaces the Vietnamese status label.
func TestNormalizeOrderRecord(t *testing.T) {
	item := map[string]interface{}{
		"SO_DON_HANG":   "ĐH000016",
		"MA_KHACH_HANG": []interface{}{float64(2273), "S052 - Phượt 4P"},
		"TRANG_THAI":    "4",
		"DON_DAT_HANG_CHI_TIET": []interface{}{
			map[string]interface{}{"SO_LUONG": float64(2), "DON_GIA": float64(100000)},
		},
		"GIAM_GIA_HOA_DON": float64(0),
	}
	rec := normalizeOrderRecord(item)
	if rec["order_id"] != "ĐH000016" {
		t.Errorf("order_id = %v; want ĐH000016", rec["order_id"])
	}
	if rec["customer_code"] != "S052" {
		t.Errorf("customer_code = %v; want S052", rec["customer_code"])
	}
	if rec["status_name"] != "Chờ giao" {
		t.Errorf("status_name = %v; want Chờ giao", rec["status_name"])
	}
	if rec["total"] != float64(200000) {
		t.Errorf("total = %v; want 200000", rec["total"])
	}
}

func TestOrderCreationRangeUsesVietnamDate(t *testing.T) {
	now := time.Date(2026, 11, 24, 18, 30, 0, 0, time.UTC)
	for _, tc := range []struct {
		days int
		from string
	}{{0, "25/11/2026"}, {3, "22/11/2026"}, {5, "20/11/2026"}, {7, "18/11/2026"}} {
		from, to := orderCreationRange(now, tc.days)
		if from != tc.from || to != "25/11/2026" {
			t.Errorf("days=%d: got %s to %s, want %s to 25/11/2026", tc.days, from, to, tc.from)
		}
	}
}

func TestParseOrderRecentDays(t *testing.T) {
	for _, tc := range []struct {
		search string
		want   int
	}{
		{"đơn hàng", 0},
		{"Mình có đơn hàng nào sắp giao không em", 0},
		{"đơn hàng hôm nay", 0},
		{"đơn hàng 3 ngày gần đây", 3},
		{"5 ngay", 5},
		{"đơn hàng 7 ngày qua", 7},
		{"đơn hàng 1 tuần qua", 7},
		{"đơn hàng 13 ngày gần đây", 0},
		{"đơn nào giao trong 3 ngày tới", 0},
		{"đơn nào giao trong 5 ngày nữa", 0},
		{"đơn nào giao trong 1 tuần sau", 0},
	} {
		if got := parseDaysFromSearch(tc.search); got != tc.want {
			t.Errorf("parseDaysFromSearch(%q) = %d, want %d", tc.search, got, tc.want)
		}
	}
}

func TestLiveCustomerOrderLookup(t *testing.T) {
	database, err := gorm.Open(mysql.New(mysql.Config{
		DSN: "fixture@tcp(127.0.0.1:3306)/fixture", SkipInitializeWithVersion: true,
	}), &gorm.Config{DryRun: true, DisableAutomaticPing: true, SkipDefaultTransaction: true})
	if err != nil {
		t.Fatal(err)
	}
	oldDB, oldTransport := db.DB, http.DefaultTransport
	client := &pkg.CloudifyClient{BaseURL: "https://orders-fixture.test", Login: "fixture"}
	db.DB = database
	t.Cleanup(func() {
		db.DB, http.DefaultTransport = oldDB, oldTransport
		client.InvalidateSession()
		pool, _ := database.DB()
		_ = pool.Close()
	})

	today := pkg.ToVN(time.Now())
	date := today.Format("02/01/2006")
	for _, tc := range []struct {
		name        string
		search      string
		wantFrom    string
		wantCode    string
		wantStatus  int
		zero        bool
		appointment string
		denied      bool
	}{
		{name: "default today", search: "đơn hàng", wantFrom: date, wantStatus: http.StatusOK, appointment: "blank"},
		{name: "delivery question", search: "Mình có đơn hàng nào sắp giao không em", wantFrom: date, wantStatus: http.StatusOK, appointment: "date"},
		{name: "appointment unavailable", search: "đơn hàng", wantFrom: date, wantStatus: http.StatusOK, appointment: "upstream error"},
		{name: "conflicting appointment dates", search: "đơn hàng", wantFrom: date, wantStatus: http.StatusOK, appointment: "conflict"},
		{name: "recent follow-up", search: "5 ngày gần đây", wantFrom: today.AddDate(0, 0, -5).Format("02/01/2006"), wantStatus: http.StatusOK},
		{name: "zero after scope filtering", search: "đơn hàng hôm nay", wantFrom: date, wantStatus: http.StatusOK, zero: true},
		{name: "web order", search: "đơn DHWS900001", wantCode: "DHWS900001", wantStatus: http.StatusOK, appointment: "date"},
		{name: "other customer", search: "DHWS900002", wantCode: "DHWS900002", wantStatus: http.StatusBadRequest},
		{name: "mismatch returned by ERP", search: "DHWS900003", wantCode: "DHWS900003", wantStatus: http.StatusBadRequest},
		{name: "ERP authentication still denied after retry", search: "đơn hàng", wantFrom: date, wantStatus: http.StatusBadGateway, denied: true},
		{name: "single order with ERP offline", search: "DHWS900001", wantCode: "DHWS900001", wantStatus: http.StatusBadGateway},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			appointmentCalls := 0
			http.DefaultTransport = warehouseFixtureTransport(func(req *http.Request) (*http.Response, error) {
				body := `{"result":{"uid":1,"session_id":"fixture"}}`
				if req.URL.Path == "/web/dataset/call_kw/account.ex.don.dat.hang/search_read" {
					appointmentCalls++
					raw, err := io.ReadAll(req.Body)
					if err != nil {
						t.Fatal(err)
					}
					if !strings.Contains(string(raw), `["SO_DON_HANG","in",["DHWS900001"]]`) ||
						!strings.Contains(string(raw), `["KHACH_HANG_ID.MA","in",["TEST001"]]`) ||
						req.Header.Get("Cookie") == "" {
						t.Fatalf("unscoped Cloudify appointment lookup: %s", raw)
					}
					base := `{"SO_DON_HANG":"DHWS900001","KHACH_HANG_ID":[123,"TEST001 - Own"],"NGAY_GIAO_HANG":`
					own := base + `"2026-11-27"}`
					switch tc.appointment {
					case "blank":
						own = base + `false}`
					case "upstream error":
						return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"error":{"code":200}}`))}, nil
					case "conflict":
						own += "," + base + `"2026-11-28"}`
					}
					body = `{"result":[` + own + `,{"SO_DON_HANG":"DHWS900001","KHACH_HANG_ID":[456,"TEST999 - Other"],"NGAY_GIAO_HANG":"2026-12-15"}]}`
				} else if req.URL.Path != "/web/session/authenticate" {
					calls++
					var input map[string]string
					if err := json.NewDecoder(req.Body).Decode(&input); err != nil {
						t.Fatal(err)
					}
					if req.URL.Path != "/rest_api/private/saorders/search" || req.Method != http.MethodPost ||
						input["SO_DON_HANG"] != tc.wantCode || input["TU_NGAY"] != tc.wantFrom ||
						(tc.wantCode == "" && input["DEN_NGAY"] != date) {
						t.Fatalf("unexpected ERP order request: %s %s %v", req.Method, req.URL.Path, input)
					}
					if tc.wantStatus == http.StatusBadGateway {
						status := http.StatusServiceUnavailable
						if tc.denied {
							status = http.StatusForbidden
						}
						return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(`{}`))}, nil
					}
					body = `{"data":[{"SO_DON_HANG":"DHWS900002","MA_KHACH_HANG":"TEST999 - Other"},` +
						`{"SO_DON_HANG":"DHWS900001","MA_KHACH_HANG":[123,"TEST001 - Own"],"TRANG_THAI":"5"}]}`
					if tc.zero {
						body = `{"data":[{"SO_DON_HANG":"DHWS900002","MA_KHACH_HANG":"TEST999 - Other"}]}`
					}
				}
				return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body))}, nil
			})
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/erp/query", nil)
			perm := &engine.GroupPermissionContext{AgentType: "public", CustomerCode: "TEST001"}
			respondWithLiveDataV2(c, client, "orders", tc.search, "", "", 10, nil, "own", "fixture", perm, false, "", "", "")
			wantCalls := 1
			if tc.denied {
				wantCalls = 2
			}
			if calls != wantCalls || rec.Code != tc.wantStatus {
				t.Fatalf("got %d lookups, HTTP %d: %s", calls, rec.Code, rec.Body)
			}
			wantAppointments := 0
			if tc.appointment != "" {
				wantAppointments = 1
			}
			if appointmentCalls != wantAppointments {
				t.Fatalf("got %d appointment queries, want %d", appointmentCalls, wantAppointments)
			}
			if strings.Contains(rec.Body.String(), "TEST999") || strings.Contains(rec.Body.String(), "15/12/2026") ||
				strings.Contains(rec.Body.String(), "is_orders_prompt") {
				t.Fatalf("unexpected customer or prompt: %s", rec.Body)
			}
			if rec.Code != http.StatusOK {
				return
			}
			hasDate := strings.Contains(rec.Body.String(), `"scheduled_delivery_date":`)
			if hasDate != (tc.appointment == "date") ||
				(hasDate && !strings.Contains(rec.Body.String(), `"scheduled_delivery_date":"27/11/2026"`)) {
				t.Fatalf("unexpected appointment: %s", rec.Body)
			}
			var result struct {
				Count     int            `json:"count"`
				OrderCode string         `json:"order_code"`
				RangeDays []int          `json:"suggested_range_days"`
				DateBasis string         `json:"date_basis"`
				Summary   *OrdersSummary `json:"orders_summary"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if tc.wantCode != "" {
				if result.Count != 1 || result.OrderCode != tc.wantCode || !strings.Contains(rec.Body.String(), "Đang giao") {
					t.Fatalf("expected matching order and status: %s", rec.Body)
				}
				return
			}
			wantCount := 1
			if tc.zero {
				wantCount = 0
			}
			if result.Count != wantCount || result.Summary == nil || result.Summary.TotalOrders != wantCount || result.DateBasis != "order_created" {
				t.Fatalf("unexpected summary: %s", rec.Body)
			}
			if (len(result.RangeDays) == 3) != (tc.wantFrom == date) {
				t.Fatalf("unexpected follow-up: %s", rec.Body)
			}
			if !tc.zero && (len(result.Summary.ByStatus) != 1 || result.Summary.ByStatus[0].StatusName != "Đang giao") {
				t.Fatalf("unexpected order status: %s", rec.Body)
			}
		})
	}
}
