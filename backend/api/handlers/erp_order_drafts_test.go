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
		"SP003055": {MA: "SP003055", TEN: "Nút mắt cáo", DVT: "Cái", DON_GIA_BAN: 100, LIST_TEN_NHOM_VTHH: "Vật tư", NHAN_HIEU_NAME: "Bulldog"},
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

// probeOrderRow is the saorders/search shape seen on the live ERP.
const probeOrderRow = `{"id":25458,"MA_HOA_DON":"CQA-TEST","MA_KHACH_HANG":[2773,"Test01"],"TEN_KHACH_HANG":"Bảo Test Dealer",
"TRANG_THAI":"%s","GHI_CHU":false,"GIAM_GIA_HOA_DON":0,"NGUOI_BAN":[false,false],
"DON_DAT_HANG_CHI_TIET":[{"MA_HANG":[24018,"SP003055"],"TEN_HANG":"Nút mắt cáo","DVT":[41,"Cái"],"SO_LUONG":1,"DON_GIA":100,"GHI_CHU":"[CQA-TEST]","CHIET_KHAU":0}]}`

func orderDeps(t *testing.T, status string) orderDraftDeps {
	return orderDraftDeps{findOrder: func(_ context.Context, code string) (map[string]any, error) {
		if code != "CQA-TEST" {
			return nil, nil
		}
		var row map[string]any
		if err := json.Unmarshal([]byte(strings.Replace(probeOrderRow, "%s", status, 1)), &row); err != nil {
			t.Fatal(err)
		}
		return row, nil
	}}
}

func customerWithOrderRights() *engine.GroupPermissionContext {
	permissions := draftPermissions("public",
		engine.ResourcePermission{Resource: resourceOrderUpdate, IsEnabled: true, ScopeType: "own"},
		engine.ResourcePermission{Resource: resourceOrderCancel, IsEnabled: true, ScopeType: "own"},
	)
	permissions.CustomerCode, permissions.CustomerCodes = "Test01", []string{"Test01"}
	return permissions
}

type sentOrder struct {
	Status string      `json:"TRANG_THAI"`
	Code   string      `json:"MA_HOA_DON"`
	Lines  []orderLine `json:"DON_DAT_HANG_CHI_TIET"`
}

func TestCancelDraftSendsFullOrder(t *testing.T) {
	setupDraftFixture(t)
	tool, _ := findOrderTool("draft_order_cancel")
	action, err := buildOrderDraft(context.Background(), customerWithOrderRights(), tool, orderDraftArgs{OrderCode: "cqa-test", Reason: "đặt nhầm"}, orderDeps(t, "0"))
	if err != nil {
		t.Fatal(err)
	}
	var body sentOrder
	_ = json.Unmarshal([]byte(action.Payload), &body)
	// A partial {"TRANG_THAI"} update fails on Cloudify, and the line list is
	// replaced, so the cancel must resend every line unchanged.
	if action.OrderID != 25458 || action.OrderCode != "CQA-TEST" || body.Status != "3" || body.Code != "CQA-TEST" ||
		len(body.Lines) != 1 || body.Lines[0].SKU != "SP003055" || body.Lines[0].Unit != "Cái" || body.Lines[0].Quantity != 1 || body.Lines[0].Note != "[CQA-TEST]" {
		t.Errorf("unexpected cancel draft %+v: %s", action, action.Payload)
	}
	if !strings.Contains(action.Summary, "HUỶ ĐƠN CQA-TEST") || !strings.Contains(action.Summary, "Lý do: đặt nhầm") {
		t.Errorf("unexpected summary: %s", action.Summary)
	}
}

func TestUpdateDraftMergesIntoFullLineList(t *testing.T) {
	setupDraftFixture(t)
	tool, _ := findOrderTool("draft_order_update")
	permissions := customerWithOrderRights()
	action, err := buildOrderDraft(context.Background(), permissions, tool, orderDraftArgs{
		OrderCode: "CQA-TEST",
		Items:     []orderDraftItem{{SKU: "SP462694", Quantity: 1}},
	}, orderDeps(t, "1"))
	if err != nil {
		t.Fatal(err)
	}
	var body sentOrder
	_ = json.Unmarshal([]byte(action.Payload), &body)
	if len(body.Lines) != 2 || body.Lines[0].SKU != "SP003055" || body.Lines[1].SKU != "SP462694" || body.Lines[1].Price != 4490000 || body.Status != "1" {
		t.Errorf("expected the existing line kept and the new one added: %s", action.Payload)
	}

	if _, err := buildOrderDraft(context.Background(), permissions, tool, orderDraftArgs{
		OrderCode: "CQA-TEST", Items: []orderDraftItem{{SKU: "SP003055", Quantity: 0}},
	}, orderDeps(t, "1")); err == nil {
		t.Error("removing the last line must be refused")
	}

	for status, allowed := range map[string]bool{"0": true, "1": true, "2": false, "3": false, "4": false} {
		_, err := buildOrderDraft(context.Background(), permissions, tool, orderDraftArgs{OrderCode: "CQA-TEST", Note: "giao sáng"}, orderDeps(t, status))
		if (err == nil) != allowed {
			t.Errorf("customer edit of a status %s order: err=%v, allowed=%v", status, err, allowed)
		}
	}
	if _, err := buildOrderDraft(context.Background(), permissions, tool, orderDraftArgs{OrderCode: "CQA-TEST", Status: "2"}, orderDeps(t, "1")); err == nil {
		t.Error("customers must not change the status")
	}
}
