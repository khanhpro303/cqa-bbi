package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/vietbui/chat-quality-agent/db/models"
	"github.com/vietbui/chat-quality-agent/engine"
	"github.com/vietbui/chat-quality-agent/orderactions"
)

// A small helmet catalog with the cases the customer asked about: "FF901"
// fits two lines (FF901 and FF901 Carbon), colours are stored in English,
// some variants are out of stock, and the visor has a single SKU.
var cartFixtureProducts = []models.CachedProduct{
	{MA: "F901-BK-L", TEN: "LS2 FF901 Black L", TEN_DONG_BO_WEB: "LS2 FF901", THUOC_TINH_1: "Black", THUOC_TINH_2: "L", DVT: "Cái", DON_GIA_BAN: 3990000},
	{MA: "F901-BK-M", TEN: "LS2 FF901 Black M", TEN_DONG_BO_WEB: "LS2 FF901", THUOC_TINH_1: "Black", THUOC_TINH_2: "M", DVT: "Cái", DON_GIA_BAN: 3990000},
	{MA: "F901-WH-L", TEN: "LS2 FF901 White L", TEN_DONG_BO_WEB: "LS2 FF901", THUOC_TINH_1: "White", THUOC_TINH_2: "L", DVT: "Cái", DON_GIA_BAN: 3990000},
	{MA: "C901-CB-L", TEN: "LS2 FF901 Carbon L", TEN_DONG_BO_WEB: "LS2 FF901 Carbon", THUOC_TINH_1: "Carbon Gloss", THUOC_TINH_2: "L", DVT: "Cái", DON_GIA_BAN: 7490000},
	{MA: "C901-CB-M", TEN: "LS2 FF901 Carbon M", TEN_DONG_BO_WEB: "LS2 FF901 Carbon", THUOC_TINH_1: "Carbon Gloss", THUOC_TINH_2: "M", DVT: "Cái", DON_GIA_BAN: 7490000},
	{MA: "S800-BK-L", TEN: "LS2 FF800 Storm Gloss Black L", TEN_DONG_BO_WEB: "LS2 FF800 Storm", THUOC_TINH_1: "Gloss Black", THUOC_TINH_2: "L", DVT: "Cái", DON_GIA_BAN: 2490000},
	{MA: "S800-RD-L", TEN: "LS2 FF800 Storm Red L", TEN_DONG_BO_WEB: "LS2 FF800 Storm", THUOC_TINH_1: "Red", THUOC_TINH_2: "L", DVT: "Cái", DON_GIA_BAN: 2490000},
	{MA: "KINH800", TEN: "Kính FF800 khói", TEN_DONG_BO_WEB: "Kính FF800", DVT: "Cái", DON_GIA_BAN: 350000},
	{MA: "NOPRICE1", TEN: "Hàng chưa giá", TEN_DONG_BO_WEB: "Hàng chưa giá", DVT: "Cái"},
}

var cartFixtureStock = map[string]float64{
	"F901-BK-L": 3, "F901-BK-M": 0, "F901-WH-L": 2, "C901-CB-L": 1, "C901-CB-M": 4,
	"S800-BK-L": 5, "S800-RD-L": 0, "KINH800": 9,
}

func fixtureCatalog() (cartCatalog, *int) {
	llmCalls := 0
	lines := groupCartLines(cartFixtureProducts)
	return cartCatalog{
		bySKU: func(_ context.Context, sku string) (models.CachedProduct, bool) {
			for _, p := range cartFixtureProducts {
				if strings.EqualFold(p.MA, sku) {
					return p, true
				}
			}
			return models.CachedProduct{}, false
		},
		search: func(_ context.Context, text string) ([]cartProductLine, bool, error) {
			var found []cartProductLine
			for _, l := range lines {
				if strings.Contains(orderactions.Normalize(l.Name), orderactions.Normalize(text)) {
					found = append(found, l)
				}
			}
			if len(found) == 0 && strings.Contains(orderactions.Normalize(text), "visor") {
				for _, l := range lines {
					if l.Name == "Kính FF800" {
						return []cartProductLine{l}, true, nil
					}
				}
			}
			return found, false, nil
		},
		line: func(_ context.Context, name string) (cartProductLine, bool) {
			for _, l := range lines {
				if l.Name == name {
					return l, true
				}
			}
			return cartProductLine{}, false
		},
		stock: func(context.Context) (map[string]float64, bool) { return cartFixtureStock, true },
		attributes: func(_ context.Context, color, size string, colors, sizes []string) (string, string) {
			llmCalls++
			mapped := ""
			if orderactions.Normalize(color) == "den" {
				for _, c := range colors {
					if strings.Contains(c, "Black") {
						mapped = c
					}
				}
			}
			return mapped, ""
		},
	}, &llmCalls
}

func newFixtureResolver() (*cartResolver, *int) {
	catalog, calls := fixtureCatalog()
	return &cartResolver{catalog: catalog, allowed: func(models.CachedProduct) bool { return true }}, calls
}

func qty(n int) *int { return &n }

// The conversation from the customer's example: several products in one
// message, one of them ambiguous, then answers and corrections.
func TestCartConversation(t *testing.T) {
	r, llmCalls := newFixtureResolver()
	ctx := context.Background()
	var state orderactions.CartState

	// "lên đơn: FF800 Storm đen L 1 cái, kính FF800 1 cái, FF901 1 cái"
	out := []cartOutcome{
		r.apply(ctx, &state, cartItemArgs{Product: "FF800 Storm", Color: "đen", Size: "L", Quantity: qty(1)}),
		r.apply(ctx, &state, cartItemArgs{Product: "kính FF800", Quantity: qty(1)}),
		r.apply(ctx, &state, cartItemArgs{Product: "FF901", Quantity: qty(1)}),
	}
	if !strings.Contains(out[0].change, "LS2 FF800 Storm - Gloss Black - L: 1 cái") || *llmCalls != 1 {
		t.Fatalf("Storm should resolve through the colour matcher: %+v calls=%d", out[0], *llmCalls)
	}
	if !strings.Contains(out[1].change, "Kính FF800") {
		t.Fatalf("visor: %+v", out[1])
	}
	if out[2].pending != "p1" || len(state.Pending) != 1 {
		t.Fatalf("FF901 must wait for a choice: %+v", out[2])
	}
	p := state.Pending[0]
	if p.Need != orderactions.NeedLine || len(p.Options) != 2 || p.Options[0].Line != "LS2 FF901" || p.Options[1].Line != "LS2 FF901 Carbon" ||
		p.Options[0].Stock == nil || *p.Options[0].Stock != 5 || p.Options[1].PriceRange != "7.490.000đ" || p.Quantity != 1 {
		t.Fatalf("line question: %+v", p)
	}
	if state.Stage() != "needs_choice" {
		t.Fatal(state.Stage())
	}

	// "cái carbon" → the agent passes option 2; colour and size are still open.
	got := r.apply(ctx, &state, cartItemArgs{PendingID: "p1", Option: 2})
	if got.pending != "p1" {
		t.Fatalf("Carbon needs a size: %+v", got)
	}
	p = state.Pending[0]
	if p.Need != orderactions.NeedVariant || p.Line != "LS2 FF901 Carbon" || len(p.Options) != 2 || strings.Join(p.Sizes, ",") != "M,L" || p.Options[0].SKU != "C901-CB-M" || strings.Join(p.Colors, ",") != "Carbon Gloss" {
		t.Fatalf("variant question: %+v", p)
	}

	// "size M"
	got = r.apply(ctx, &state, cartItemArgs{PendingID: "p1", Size: "size M"})
	if !strings.Contains(got.change, "LS2 FF901 Carbon - Carbon Gloss - M: 1 cái") || len(state.Pending) != 0 || len(state.Lines) != 3 {
		t.Fatalf("answered: %+v %+v", got, state)
	}

	// "à FF800 Storm lấy 2 cái nhé", "bỏ kính đi"
	got = r.apply(ctx, &state, cartItemArgs{Product: "FF800 Storm", Op: "set", Quantity: qty(2)})
	if !strings.Contains(got.change, "thành 2 cái") || state.Lines[0].Quantity != 2 {
		t.Fatalf("correction: %+v", got)
	}
	got = r.apply(ctx, &state, cartItemArgs{Product: "kính", Op: "remove"})
	if !strings.HasPrefix(got.change, "Đã bỏ Kính FF800") || len(state.Lines) != 2 {
		t.Fatalf("remove: %+v", got)
	}
	// "thêm 1 cái Storm nữa" adds to the existing line, no new question.
	got = r.apply(ctx, &state, cartItemArgs{Product: "Storm", Quantity: qty(1)})
	if state.Lines[0].Quantity != 3 || len(state.Pending) != 0 {
		t.Fatalf("add to existing: %+v", got)
	}
	if state.Stage() != "ready" {
		t.Fatalf("stage %s", state.Stage())
	}
}

func TestCartVariantQuestions(t *testing.T) {
	r, _ := newFixtureResolver()
	ctx := context.Background()
	var state orderactions.CartState

	// Out-of-stock variants are not offered.
	got := r.apply(ctx, &state, cartItemArgs{Product: "LS2 FF901", Quantity: qty(1)})
	p := state.Pending[0]
	if got.pending == "" || p.Need != orderactions.NeedVariant || len(p.Options) != 2 || p.Options[0].SKU != "F901-BK-L" || p.Options[1].SKU != "F901-WH-L" {
		t.Fatalf("exact line name skips the line question and hides sold-out variants: %+v", p)
	}
	// A colour the line does not have is reported, and dropped from the
	// question so the next answer replaces it.
	r.apply(ctx, &state, cartItemArgs{PendingID: got.pending, Color: "xanh"})
	p = state.Pending[0]
	if !strings.Contains(p.Note, "không có màu xanh") || p.Color != "" {
		t.Fatalf("unknown colour: %+v", p)
	}
	// Answering by option number with a new quantity.
	r.apply(ctx, &state, cartItemArgs{PendingID: got.pending, Option: 2, Quantity: qty(2)})
	if len(state.Pending) != 0 || state.Lines[0].SKU != "F901-WH-L" || state.Lines[0].Quantity != 2 {
		t.Fatalf("option pick: %+v", state)
	}

	// More than the main warehouse holds: kept, flagged, checkout blocked.
	r.apply(ctx, &state, cartItemArgs{Product: "LS2 FF901 Carbon", Size: "L", Quantity: qty(3)})
	if state.Lines[1].SKU != "C901-CB-L" || !state.Lines[1].ShortStock() || state.Stage() != "stock_short" {
		t.Fatalf("short stock: %+v", state.Lines)
	}
}

func TestCartProblems(t *testing.T) {
	r, _ := newFixtureResolver()
	ctx := context.Background()
	var state orderactions.CartState
	for _, c := range []struct {
		item cartItemArgs
		want string
	}{
		{cartItemArgs{Product: "XYZ999"}, "Không tìm thấy sản phẩm khớp"},
		{cartItemArgs{SKU: "NOPE"}, "Không tìm thấy mã sản phẩm NOPE"},
		{cartItemArgs{Product: "Hàng chưa giá"}, "chưa có giá bán"},
		{cartItemArgs{Product: "kính", Op: "remove"}, "Không thấy \"kính\" trong giỏ hàng"},
		{cartItemArgs{Product: "FF800", Op: "set"}, "op=set cần quantity"},
		{cartItemArgs{Product: "FF800", Quantity: qty(0)}, "không hợp lệ"},
		{cartItemArgs{PendingID: "p9", Option: 1}, "Không còn câu hỏi p9"},
		{cartItemArgs{Product: "FF800", Op: "replace"}, "không hợp lệ"},
	} {
		got := r.apply(ctx, &state, c.item)
		if !strings.Contains(got.problem, c.want) {
			t.Errorf("%+v: want problem %q, got %+v", c.item, c.want, got)
		}
	}
	if len(state.Lines) != 0 || len(state.Pending) != 0 {
		t.Fatalf("problems must not change the cart: %+v", state)
	}

	// Two cart lines match a correction: ask which.
	r.apply(ctx, &state, cartItemArgs{SKU: "S800-BK-L", Quantity: qty(1)})
	r.apply(ctx, &state, cartItemArgs{SKU: "KINH800", Quantity: qty(1)})
	got := r.apply(ctx, &state, cartItemArgs{Product: "FF800", Op: "set", Quantity: qty(3)})
	if !strings.Contains(got.problem, "nhiều dòng khớp") {
		t.Fatalf("ambiguous correction: %+v", got)
	}

	// A guessed (fuzzy) match is confirmed with the customer first.
	got = r.apply(ctx, &state, cartItemArgs{Product: "visor 800", Quantity: qty(1)})
	if got.pending == "" || !strings.Contains(state.Pending[0].Note, "gần nhất") || len(state.Pending[0].Options) != 1 {
		t.Fatalf("fuzzy match must ask: %+v %+v", got, state.Pending)
	}
	// Dropping a question.
	got = r.apply(ctx, &state, cartItemArgs{PendingID: state.Pending[0].ID, Op: "remove"})
	if !strings.Contains(got.change, "Đã bỏ yêu cầu") || len(state.Pending) != 0 {
		t.Fatalf("drop pending: %+v", got)
	}
}

func TestCartScope(t *testing.T) {
	catalog, _ := fixtureCatalog()
	onlyVisors := func(p models.CachedProduct) bool { return strings.HasPrefix(p.MA, "KINH") }
	r := &cartResolver{catalog: scopedCatalog(catalog, onlyVisors), allowed: onlyVisors}
	var state orderactions.CartState
	if got := r.apply(context.Background(), &state, cartItemArgs{Product: "FF901"}); !strings.Contains(got.problem, "Không tìm thấy") {
		t.Errorf("out-of-scope lines are invisible: %+v", got)
	}
	if got := r.apply(context.Background(), &state, cartItemArgs{SKU: "S800-BK-L"}); !strings.Contains(got.problem, "không nằm trong danh mục") {
		t.Errorf("out-of-scope SKU: %+v", got)
	}
}

func TestListedOrderTools(t *testing.T) {
	names := map[string]bool{}
	for _, tool := range listedOrderTools() {
		names[tool.Name] = true
	}
	for _, want := range []string{"order_cart_update", "order_cart_view", "order_cart_checkout", "order_cart_clear", "draft_order_update", "draft_order_cancel"} {
		if !names[want] {
			t.Errorf("missing tool %s", want)
		}
	}
	if names["draft_order_create"] {
		t.Error("creating goes through the cart; draft_order_create is no longer advertised")
	}
	if _, ok := findOrderTool("draft_order_create"); !ok {
		t.Error("draft_order_create must still answer for flows with an old tool list")
	}
}

func TestBuildCartDraft(t *testing.T) {
	setupDraftFixture(t)
	permissions := draftPermissions("public",
		engine.ResourcePermission{Resource: resourceOrderCreate, IsEnabled: true, ScopeType: "own"},
	)
	cart := &models.OrderCart{ID: "cart-1", WorkKey: "zalo_session:channel-1:zalo-1", Note: "giao chiều"}
	state := orderactions.CartState{Lines: []orderactions.CartLine{
		{SKU: "SP462694", Name: "LS2 FF818 Black XL", Line: "LS2 FF818", Color: "Black", Size: "XL", Unit: "Cái", Quantity: 2, Price: 1},
		{SKU: "KINH01", Name: "Kính", Line: "Kính FF818", Unit: "Cái", Quantity: 1, Price: 1},
	}}
	deps := cartDeps{freshStock: func(context.Context) (map[string]float64, bool) {
		return map[string]float64{"SP462694": 5, "KINH01": 5}, true
	}}
	action, err := buildCartDraft(context.Background(), permissions, "own", cart, state, deps)
	if err != nil {
		t.Fatal(err)
	}
	if action.CartID != "cart-1" || action.Kind != orderactions.KindCreate || action.CustomerCode != "KH125" || !strings.HasPrefix(action.OrderCode, "CQA-") {
		t.Errorf("draft: %+v", action)
	}
	for _, want := range []string{
		"📝 XÁC NHẬN ĐƠN HÀNG", "Khách: KH125 - Shop A",
		"1. LS2 FF818 - Black - XL (SP462694)\n   2 cái × 4.490.000đ = 8.980.000đ",
		"2. Kính FF818 (KINH01)\n   1 cái × 150.000đ = 150.000đ",
		"Tổng: 2 sản phẩm, 3 món. Tạm tính 9.130.000đ",
		"Ghi chú: giao chiều", "Trả lời 1", "\"LS2 FF818 lấy 2 cái\"", "\"bỏ Kính FF818\"", "30 phút",
	} {
		if !strings.Contains(action.Summary, want) {
			t.Errorf("summary misses %q:\n%s", want, action.Summary)
		}
	}
	var body struct {
		Lines []orderLine `json:"DON_DAT_HANG_CHI_TIET"`
	}
	if err := json.Unmarshal([]byte(action.Payload), &body); err != nil || len(body.Lines) != 2 || body.Lines[0].Price != 4490000 {
		t.Errorf("prices come from the cache, not the cart: %s", action.Payload)
	}

	// Refusals: open questions, empty cart, not enough stock.
	refused := func(state orderactions.CartState, deps cartDeps, want string) {
		t.Helper()
		_, err := buildCartDraft(context.Background(), permissions, "own", cart, state, deps)
		var refusal *orderDraftError
		if !errors.As(err, &refusal) || !strings.Contains(refusal.message, want) {
			t.Errorf("want refusal %q, got %v", want, err)
		}
	}
	refused(orderactions.CartState{Lines: state.Lines, Pending: []orderactions.CartPending{{ID: "p1", Request: "FF901"}}}, deps, "Còn sản phẩm chờ")
	refused(orderactions.CartState{}, deps, "Giỏ hàng đang trống")
	refused(state, cartDeps{freshStock: func(context.Context) (map[string]float64, bool) {
		return map[string]float64{"SP462694": 1, "KINH01": 5}, true
	}}, "Kho tổng không đủ: LS2 FF818 Black XL (SKU SP462694) đặt 2, kho tổng còn 1")
}
