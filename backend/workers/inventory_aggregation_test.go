package workers

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/db/models"
	"github.com/vietbui/chat-quality-agent/engine"
	"github.com/vietbui/chat-quality-agent/pkg"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

type stockTestTransport func(*http.Request) (*http.Response, error)

func (transport stockTestTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return transport(req)
}

func TestParentInventoryIncludesSiblingWebNames(t *testing.T) {
	database, err := gorm.Open(mysql.New(mysql.Config{
		DSN:                       "fixture@tcp(127.0.0.1:3306)/fixture",
		SkipInitializeWithVersion: true,
	}), &gorm.Config{DryRun: true, DisableAutomaticPing: true})
	if err != nil {
		t.Fatal(err)
	}
	pool, err := database.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pool.Close() })
	originalDB := db.DB
	db.DB = database
	t.Cleanup(func() { db.DB = originalDB })

	// Simulate the catalog returning a second eligible SKU with no web name.
	err = database.Callback().Query().After("gorm:query").Register("fixture:inventory", func(query *gorm.DB) {
		switch result := query.Statement.Dest.(type) {
		case *[]models.CachedProduct:
			*result = []models.CachedProduct{{MA: "fixture-visible"}}
			if vars := query.Statement.Vars; len(vars) >= 2 && vars[1] == "fixture-parent_DK" {
				// The line's "_DK" copy parent holds stock of its own.
				*result = []models.CachedProduct{{MA: "fixture-copy", MA_CHA: "fixture-parent_DK"}}
			}
			sql := query.Statement.SQL.String()
			if !strings.Contains(sql, "tenant_id = ? AND ma_cha = ?") {
				t.Errorf("catalog lookup lacks tenant or parent scope: %s", sql)
			}
			if !strings.Contains(sql, "ten_dong_bo_web = ?") && !strings.Contains(sql, "LIMIT") && (*result)[0].MA != "fixture-copy" {
				*result = append(*result, models.CachedProduct{MA: "fixture-sibling"})
			}
		case *int64:
			if vars := query.Statement.Vars; len(vars) >= 2 && vars[1] == "fixture-parent_DK" {
				*result = 1
				query.RowsAffected = 1 // gorm's Count keeps the value only for one row
			}
		case *models.AppSetting:
			if strings.Contains(query.Statement.SQL.String(), "setting_key = ?") &&
				len(query.Statement.Vars) >= 2 && query.Statement.Vars[1] == "erp_api_url" {
				result.ValuePlain = "https://inventory-aggregation.test"
			}
		}
	})
	if err != nil {
		t.Fatal(err)
	}

	originalTransport := http.DefaultTransport
	failSibling := false
	http.DefaultTransport = stockTestTransport(func(req *http.Request) (*http.Response, error) {
		status, body := http.StatusOK, `{"result":{"uid":1,"session_id":"fixture"}}`
		if req.URL.Path != "/web/session/authenticate" {
			if req.Method != http.MethodPost || req.URL.Path != "/rest_api/private/tong_hop_ton_kho/search" {
				t.Errorf("unexpected stock request: %s %s", req.Method, req.URL.Path)
			}
			values := req.URL.Query()
			if values.Get("DS_KHO") != "1.KHO-TONG" || values.Get("CHI_NHANH") != "BBI NỘI BỘ" ||
				values.Get("TU_NGAY") == "" || values.Get("TU_NGAY") != values.Get("DEN_NGAY") {
				t.Errorf("unexpected warehouse parameters: %v", values)
			}
			body = `[{"MA_HANG":"fixture-visible","SO_LUONG_CUOI_KY":5},{"MA_HANG":"fixture-sibling","SO_LUONG_CUOI_KY":376},{"MA_HANG":"fixture-copy","SO_LUONG_CUOI_KY":7}]`
			if failSibling {
				status, body = http.StatusServiceUnavailable, "fixture inventory unavailable"
			}
		}
		return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body))}, nil
	})
	t.Cleanup(func() {
		http.DefaultTransport = originalTransport
		(&pkg.CloudifyClient{BaseURL: "https://inventory-aggregation.test"}).InvalidateSession()
	})

	permissions := &engine.GroupPermissionContext{AgentType: "private"}
	for webName, want := range map[string]float64{"Bulldog DOGO": 5, "": 381} {
		got, _, err := sumInventoryByMaChaAndWebName(context.Background(), "fixture-tenant", permissions, "fixture-parent", webName)
		if err != nil || got != want {
			t.Errorf("stock for web filter %q: got %g, %v; want %g", webName, got, err, want)
		}
	}
	// A parent-code pick (no web name) also counts its "_DK" copy's stock,
	// once: the plain sum above must not add it (the by-web loop sums the
	// copy parent itself).
	if got, _, err := sumInventoryByParentWithDKCopy(context.Background(), "fixture-tenant", permissions, "fixture-parent"); err != nil || got != 388 {
		t.Errorf("parent + _DK copy stock: got %g, %v; want 388", got, err)
	}
	failSibling = true
	got, _, err := sumInventoryByMaChaAndWebName(context.Background(), "fixture-tenant", permissions, "fixture-parent", "")
	if err == nil || got != 0 {
		t.Errorf("incomplete parent stock: got %g, %v; want a lookup error", got, err)
	}
}
