package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/db/models"
	"github.com/vietbui/chat-quality-agent/engine"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

const draftTenant = "fixture-draft-tenant"

// setupDraftFixture answers the product and customer cache lookups a draft
// makes, without a database.
func setupDraftFixture(t *testing.T) {
	t.Helper()
	database, err := gorm.Open(mysql.New(mysql.Config{
		DSN:                       "fixture@tcp(127.0.0.1:3306)/fixture",
		SkipInitializeWithVersion: true,
	}), &gorm.Config{DryRun: true, DisableAutomaticPing: true, SkipDefaultTransaction: true})
	if err != nil {
		t.Fatal(err)
	}
	saved := db.DB
	db.DB = database
	t.Cleanup(func() { db.DB = saved })
	products := map[string]models.CachedProduct{
		"SP462694": {MA: "SP462694", TEN: "LS2 FF818 Black XL", DVT: "Cái", DON_GIA_BAN: 4490000, LIST_TEN_NHOM_VTHH: "Mũ", NHAN_HIEU_NAME: "LS2"},
		"KINH01":   {MA: "KINH01", TEN: "Kính", DVT: "Cái", DON_GIA_BAN: 150000, LIST_TEN_NHOM_VTHH: "Kính", NHAN_HIEU_NAME: "LS2"},
		"NOPRICE":  {MA: "NOPRICE", TEN: "Hàng chưa giá", DVT: "Cái", LIST_TEN_NHOM_VTHH: "Mũ", NHAN_HIEU_NAME: "LS2"},
	}
	err = database.Callback().Query().After("gorm:query").Register("fixture:drafts", func(q *gorm.DB) {
		vars := q.Statement.Vars
		switch result := q.Statement.Dest.(type) {
		case *models.CachedProduct:
			if len(vars) >= 2 {
				if p, ok := products[vars[1].(string)]; ok {
					*result = p
					q.RowsAffected = 1
				}
			}
		case *[]models.CachedCustomer:
			if len(vars) >= 2 && vars[1] == "KH125" {
				*result = []models.CachedCustomer{{MA: "KH125 - Shop A", HO_VA_TEN: "Shop A"}}
			}
		}
	})
	if err != nil {
		t.Fatal(err)
	}
}

func draftPermissions(agentType string, resources ...engine.ResourcePermission) *engine.GroupPermissionContext {
	group := "crm-group"
	if agentType == "private" {
		group = "private_bot"
	}
	return &engine.GroupPermissionContext{
		TenantID: draftTenant, AgentType: agentType, ZaloUserID: "zalo-1", ChannelID: "channel-1",
		CustomerCode: "KH125", CustomerCodes: []string{"KH125"},
		Groups: []engine.GroupPermission{{GroupID: group, Resources: resources}},
	}
}

func createTool() erpMCPTool {
	tool, _ := findOrderTool("draft_order_create")
	return tool
}

func TestOrderDraftRequiresExplicitPermission(t *testing.T) {
	setupDraftFixture(t)
	args := orderDraftArgs{CustomerCode: "KH125", Items: []orderDraftItem{{SKU: "SP462694", Quantity: 1}}}
	// A private bot without any private_bot configuration reads everything,
	// but must not write orders.
	for _, permissions := range []*engine.GroupPermissionContext{
		draftPermissions("private"),
		draftPermissions("public", engine.ResourcePermission{Resource: "orders", IsEnabled: true}),
	} {
		_, err := buildOrderDraft(context.Background(), permissions, createTool(), args, orderDraftDeps{})
		var refusal *orderDraftError
		if !errors.As(err, &refusal) || !strings.Contains(refusal.message, "chưa được cấp quyền") {
			t.Errorf("%s: expected permission refusal, got %v", permissions.AgentType, err)
		}
	}
}

func TestCustomerCreateDraft(t *testing.T) {
	setupDraftFixture(t)
	permissions := draftPermissions("public",
		engine.ResourcePermission{Resource: resourceOrderCreate, IsEnabled: true, ScopeType: "own"},
		engine.ResourcePermission{Resource: "products", IsEnabled: true, ScopeType: "own", ProductGroups: []string{"mũ"}, AllBrands: true},
	)
	action, err := buildOrderDraft(context.Background(), permissions, createTool(), orderDraftArgs{
		Items: []orderDraftItem{{SKU: "SP462694", Quantity: 2}}, Note: "giao chiều",
	}, orderDraftDeps{})
	if err != nil {
		t.Fatal(err)
	}
	if action.Kind != "create" || action.CustomerCode != "KH125" || action.CustomerName != "Shop A" ||
		action.WorkKey != engine.BuildSessionKey("channel-1", "zalo-1", "") {
		t.Errorf("unexpected draft: %+v", action)
	}
	var body struct {
		Customer string      `json:"MA_KHACH_HANG"`
		Note     string      `json:"GHI_CHU"`
		Lines    []orderLine `json:"DON_DAT_HANG_CHI_TIET"`
	}
	if err := json.Unmarshal([]byte(action.Payload), &body); err != nil {
		t.Fatal(err)
	}
	if body.Customer != "KH125" || len(body.Lines) != 1 || body.Lines[0].Price != 4490000 || body.Lines[0].Quantity != 2 ||
		!strings.HasPrefix(body.Note, "[CQA:") || !strings.HasSuffix(body.Note, "giao chiều") {
		t.Errorf("unexpected body: %s", action.Payload)
	}
	for _, want := range []string{"TẠO ĐƠN", "KH125 - Shop A", "2 Cái × 4.490.000đ = 8.980.000đ", "Tạm tính: 8.980.000đ", "Trả lời 1"} {
		if !strings.Contains(action.Summary, want) {
			t.Errorf("summary misses %q:\n%s", want, action.Summary)
		}
	}
}

func TestCustomerCreateDraftRefusals(t *testing.T) {
	setupDraftFixture(t)
	permissions := draftPermissions("public",
		engine.ResourcePermission{Resource: resourceOrderCreate, IsEnabled: true, ScopeType: "own"},
		engine.ResourcePermission{Resource: "products", IsEnabled: true, ScopeType: "own", ProductGroups: []string{"mũ"}, AllBrands: true},
	)
	for name, args := range map[string]orderDraftArgs{
		"other customer":  {CustomerCode: "KH999", Items: []orderDraftItem{{SKU: "SP462694", Quantity: 1}}},
		"unknown sku":     {Items: []orderDraftItem{{SKU: "NOPE", Quantity: 1}}},
		"outside catalog": {Items: []orderDraftItem{{SKU: "KINH01", Quantity: 1}}},
		"no price":        {Items: []orderDraftItem{{SKU: "NOPRICE", Quantity: 1}}},
		"zero quantity":   {Items: []orderDraftItem{{SKU: "SP462694", Quantity: 0}}},
		"duplicate sku":   {Items: []orderDraftItem{{SKU: "SP462694", Quantity: 1}, {SKU: "sp462694", Quantity: 1}}},
		"no items":        {},
	} {
		_, err := buildOrderDraft(context.Background(), permissions, createTool(), args, orderDraftDeps{})
		var refusal *orderDraftError
		if !errors.As(err, &refusal) {
			t.Errorf("%s: expected refusal, got %v", name, err)
		}
	}
}

func TestStaffCreateDraftNeedsCustomer(t *testing.T) {
	setupDraftFixture(t)
	permissions := draftPermissions("private", engine.ResourcePermission{Resource: resourceOrderCreate, IsEnabled: true, ScopeType: "all"})
	args := orderDraftArgs{Items: []orderDraftItem{{SKU: "KINH01", Quantity: 3}}}
	if _, err := buildOrderDraft(context.Background(), permissions, createTool(), args, orderDraftDeps{}); err == nil {
		t.Fatal("expected a refusal without customer_code")
	}
	args.CustomerCode = "KH125"
	action, err := buildOrderDraft(context.Background(), permissions, createTool(), args, orderDraftDeps{})
	if err != nil || action.CustomerCode != "KH125" {
		t.Fatalf("staff draft: %v %+v", err, action)
	}
}
