package workers

import (
	"context"
	"encoding/json"
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
			sql := query.Statement.SQL.String()
			if !strings.Contains(sql, "tenant_id = ? AND ma_cha = ?") {
				t.Errorf("catalog lookup lacks tenant or parent scope: %s", sql)
			}
			if !strings.Contains(sql, "ten_dong_bo_web = ?") && !strings.Contains(sql, "LIMIT") {
				*result = append(*result, models.CachedProduct{MA: "fixture-sibling"})
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
			var payload struct {
				SKU string `json:"MA_HANG"`
			}
			if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
				return nil, err
			}
			switch payload.SKU {
			case "fixture-visible":
				body = `[{"TEN_KHO":"KHO-TONG","SO_LUONG_TON":5}]`
			case "fixture-sibling":
				body = `[{"TEN_KHO":"KHO-TONG","SO_LUONG_TON":376}]`
				if failSibling {
					status, body = http.StatusServiceUnavailable, "fixture inventory unavailable"
				}
			default:
				status, body = http.StatusBadRequest, "unknown fixture SKU"
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
	failSibling = true
	got, _, err := sumInventoryByMaChaAndWebName(context.Background(), "fixture-tenant", permissions, "fixture-parent", "")
	if err == nil || got != 0 {
		t.Errorf("incomplete parent stock: got %g, %v; want a lookup error", got, err)
	}
}
