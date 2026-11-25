package workers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
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
			*result = []models.CachedProduct{{MA: "fixture-visible", LIST_TEN_NHOM_VTHH: "helmets"}}
			switch query.Statement.Vars[1] {
			case "fixture-parent_DK":
				// The line's "_DK" copy parent holds stock of its own.
				*result = []models.CachedProduct{{MA: "fixture-copy", MA_CHA: "fixture-parent_DK", LIST_TEN_NHOM_VTHH: "visors"}}
			case "fixture-zero-parent":
				*result = []models.CachedProduct{{MA: "fixture-zero", LIST_TEN_NHOM_VTHH: "helmets"}}
			case "fixture-missing", "fixture-missing_DK":
				*result = nil
			}
			sql := query.Statement.SQL.String()
			if !strings.Contains(sql, "tenant_id = ? AND ma_cha = ?") {
				t.Errorf("catalog lookup lacks tenant or parent scope: %s", sql)
			}
			if !strings.Contains(sql, "ten_dong_bo_web = ?") && !strings.Contains(sql, "LIMIT") && query.Statement.Vars[1] == "fixture-parent" {
				*result = append(*result, models.CachedProduct{MA: "fixture-sibling", LIST_TEN_NHOM_VTHH: "helmets"})
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
	var reports atomic.Int32
	http.DefaultTransport = stockTestTransport(func(req *http.Request) (*http.Response, error) {
		status, body := http.StatusOK, `{"result":{"uid":1,"session_id":"fixture"}}`
		if req.URL.Path != "/web/session/authenticate" {
			reports.Add(1)
			if req.Method != http.MethodPost || req.URL.Path != "/rest_api/private/danhmucvattuhanghoa/lay_ton_kho_san_pham" {
				t.Errorf("unexpected stock request: %s %s", req.Method, req.URL.Path)
			}
			var input struct {
				SKU string `json:"MA_HANG"`
			}
			if err := json.NewDecoder(req.Body).Decode(&input); err != nil || input.SKU == "" {
				t.Errorf("missing SKU in inventory request: %v", err)
			}
			qty, found := map[string]int{"fixture-visible": 5, "fixture-sibling": 376, "fixture-copy": 7, "fixture-zero": 0}[input.SKU]
			if !found {
				t.Errorf("unexpected warehouse SKU %q", input.SKU)
			}
			body = fmt.Sprintf(`{"code":200,"data":[{"MA_HANG":%q,"TON_KHO_CHI_TIET":[{"TEN_KHO":"KHO-TONG","SO_LUONG_TON":999,"SO_LUONG_TON_KHA_DUNG":%d}]}]}`, input.SKU, qty)
			if failSibling && input.SKU == "fixture-sibling" {
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
	// Within one web line the parent's "_DK" copy rows count too (5 + 7);
	// with no web name the plain sum stays on the parent alone.
	for webName, want := range map[string]float64{"Bulldog DOGO": 12, "": 381} {
		got, _, err := sumInventoryByMaChaAndWebName(context.Background(), "fixture-tenant", permissions, "fixture-parent", webName)
		if err != nil || got != want {
			t.Errorf("stock for web filter %q: got %g, %v; want %g", webName, got, err, want)
		}
	}
	// A parent-code pick (no web name) also counts its "_DK" copy's stock,
	// once: the plain sum above must not add it (the by-web loop sums the
	// copy parent itself).
	reports.Store(0)
	if got, _, err := sumInventoryByParentWithDKCopy(context.Background(), "fixture-tenant", permissions, "fixture-parent"); err != nil || got != 388 {
		t.Errorf("parent + _DK copy stock: got %g, %v; want 388", got, err)
	}
	if got := reports.Load(); got != 3 {
		t.Errorf("parent + copy queried %d SKUs; want 3", got)
	}
	stockScope := func(productGroup string) *engine.GroupPermissionContext {
		return &engine.GroupPermissionContext{AgentType: "public", Groups: []engine.GroupPermission{{Resources: []engine.ResourcePermission{
			{Resource: "inventory", IsEnabled: true, ScopeType: "own", ProductGroups: []string{productGroup}},
		}}}}
	}
	ctx := context.Background()
	reports.Store(0)
	excluded := stockScope("boots")
	if _, err := sumInventoryByMaCha(ctx, "fixture-tenant", excluded, "fixture-parent"); !errors.Is(err, errNoVisibleStock) {
		t.Errorf("legacy parent button returned %v for an excluded line", err)
	}
	if _, _, err := sumInventoryByMaChaAndWebName(ctx, "fixture-tenant", excluded, "fixture-parent", "Bulldog DOGO"); !errors.Is(err, errNoVisibleStock) {
		t.Errorf("legacy web-name button returned %v for an excluded line", err)
	}
	if _, _, err := sumInventoryByParentWithDKCopy(ctx, "fixture-tenant", excluded, "fixture-parent"); !errors.Is(err, errNoVisibleStock) {
		t.Errorf("legacy parent with _DK returned %v for an excluded line", err)
	}
	if _, _, err := sumInventoryByParentWithDKCopy(ctx, "fixture-tenant", permissions, "fixture-missing"); !errors.Is(err, errNoVisibleStock) {
		t.Errorf("legacy parent with _DK returned %v for a missing line", err)
	}
	if got := reports.Load(); got != 0 {
		t.Errorf("out-of-scope and missing SKUs triggered %d ERP requests", got)
	}
	if got, _, err := sumInventoryByParentWithDKCopy(ctx, "fixture-tenant", stockScope("visors"), "fixture-parent"); err != nil || got != 7 {
		t.Errorf("visible _DK only: got %g, %v; want 7", got, err)
	}
	if got, _, err := sumInventoryByParentWithDKCopy(ctx, "fixture-tenant", stockScope("helmets"), "fixture-parent"); err != nil || got != 381 {
		t.Errorf("visible parent only: got %g, %v; want 381", got, err)
	}
	if got, err := sumInventoryByMaCha(ctx, "fixture-tenant", stockScope("helmets"), "fixture-zero-parent"); err != nil || got != 0 {
		t.Errorf("confirmed zero: got %g, %v; want valid zero", got, err)
	}
	failSibling = true
	got, _, err := sumInventoryByMaChaAndWebName(context.Background(), "fixture-tenant", permissions, "fixture-parent", "")
	if err == nil || got != 0 {
		t.Errorf("incomplete parent stock: got %g, %v; want a lookup error", got, err)
	}
}
