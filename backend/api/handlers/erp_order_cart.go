package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/vietbui/chat-quality-agent/config"
	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/db/models"
	"github.com/vietbui/chat-quality-agent/engine"
	"github.com/vietbui/chat-quality-agent/orderactions"
	"github.com/vietbui/chat-quality-agent/pkg"
)

// The cart tools let the agent take a whole order in a chat: the customer
// lists products in their own words, the backend resolves each one against
// the catalog and keeps the list across turns, asks back through the agent
// when a name fits several products (FF901 / FF901 Carbon) or misses colour
// and size, accepts corrections at any point, and finally sends one complete
// confirmation. The order still reaches Cloudify only when the customer
// answers "1" to that confirmation (see orderactions.Commit).

const (
	cartMaxItemsPerCall  = 30
	cartMaxLineOptions   = 6
	cartMaxVariantOption = 12
	cartCatalogRowLimit  = 2000
)

var cartItemSchema = map[string]any{
	"type":     "array",
	"maxItems": cartMaxItemsPerCall,
	"items": map[string]any{
		"type": "object",
		"properties": map[string]any{
			"product":    map[string]any{"type": "string", "description": "Tên hoặc mã sản phẩm đúng như người dùng gõ, ví dụ \"FF800 Storm\", \"kính FF800\"."},
			"sku":        map[string]any{"type": "string", "description": "Mã SKU chính xác nếu đã biết (từ giỏ hàng hoặc options). Không tự đoán."},
			"line":       map[string]any{"type": "string", "description": "Tên dòng sản phẩm chính xác, lấy từ options khi người dùng đã chọn dòng."},
			"color":      map[string]any{"type": "string", "description": "Màu người dùng nói, nếu có."},
			"size":       map[string]any{"type": "string", "description": "Size người dùng nói, nếu có."},
			"quantity":   map[string]any{"type": "integer", "description": "Số lượng. op=add: số lượng thêm (mặc định 1). op=set: số lượng mới, 0 để bỏ."},
			"op":         map[string]any{"type": "string", "enum": []string{orderactions.OpAdd, orderactions.OpSet, orderactions.OpRemove}, "description": "add (mặc định) thêm vào giỏ; set đặt lại số lượng; remove bỏ khỏi giỏ."},
			"pending_id": map[string]any{"type": "string", "description": "Mã câu hỏi đang chờ (p1, p2...) khi người dùng trả lời câu hỏi chọn sản phẩm."},
			"option":     map[string]any{"type": "integer", "description": "Số thứ tự lựa chọn người dùng chọn trong options của pending_id."},
		},
		"additionalProperties": false,
	},
}

const cartRichMessageNote = " Khi kết quả có message=zalo_rich_message_sent_directly, chỉ trả lời đúng [RICH_MESSAGE_SENT]. Không tự báo đơn đã được tạo."

var erpMCPCartTools = []erpMCPTool{
	{
		Name:        "order_cart_update",
		Description: "Ghi giỏ hàng của cuộc chat khi người dùng đặt hàng hoặc sửa đơn đang lên: thêm sản phẩm, đổi số lượng, bỏ sản phẩm, trả lời câu hỏi chọn sản phẩm. Gửi mọi sản phẩm của một tin nhắn trong một lần gọi. Kết quả trả về cả giỏ: stage (needs_choice, stock_short, ready, empty), lines, pending (câu cần hỏi người dùng, có options đánh số), changes, problems. Giá do hệ thống lấy từ ERP.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"items":         cartItemSchema,
				"customer_code": map[string]any{"type": "string", "description": "Mã khách hàng (nhân viên đặt hộ, hoặc khách có nhiều mã). Khách một mã thì bỏ trống."},
				"note":          map[string]any{"type": "string", "description": "Ghi chú cho đơn (địa chỉ, thời gian giao...)."},
			},
			"additionalProperties": false,
		},
		resource: resourceOrderCreate,
	},
	{
		Name:        "order_cart_view",
		Description: "Xem giỏ hàng đang lên của cuộc chat (sản phẩm, số lượng, câu hỏi đang chờ, tạm tính).",
		InputSchema: map[string]any{"type": "object", "properties": map[string]any{}, "additionalProperties": false},
		resource:    resourceOrderCreate,
	},
	{
		Name:        "order_cart_checkout",
		Description: "Gửi bản xác nhận cuối cùng của giỏ hàng (đủ danh sách và tạm tính) khi người dùng muốn chốt đơn. Chỉ gọi khi stage là ready. Hệ thống tự gửi tin xác nhận và chỉ ghi đơn lên ERP khi chính người dùng trả lời 1." + cartRichMessageNote,
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"customer_code": map[string]any{"type": "string", "description": "Mã khách hàng nếu chưa có trong giỏ (nhân viên đặt hộ)."},
				"note":          map[string]any{"type": "string", "description": "Ghi chú cho đơn, nếu người dùng vừa nói thêm."},
			},
			"additionalProperties": false,
		},
		resource: resourceOrderCreate,
	},
	{
		Name:        "order_cart_clear",
		Description: "Huỷ giỏ hàng đang lên khi người dùng nói không đặt nữa.",
		InputSchema: map[string]any{"type": "object", "properties": map[string]any{}, "additionalProperties": false},
		resource:    resourceOrderCreate,
	},
}

func findCartTool(name string) (erpMCPTool, bool) {
	for _, tool := range erpMCPCartTools {
		if tool.Name == name {
			return tool, true
		}
	}
	return erpMCPTool{}, false
}

// listedOrderTools are the order tools advertised to the agent. Creating an
// order goes through the cart; draft_order_create still answers for flows
// whose tool list was loaded before the cart existed.
func listedOrderTools() []erpMCPTool {
	tools := make([]erpMCPTool, 0, len(erpMCPCartTools)+len(erpMCPOrderTools))
	tools = append(tools, erpMCPCartTools...)
	for _, tool := range erpMCPOrderTools {
		if tool.Name != "draft_order_create" {
			tools = append(tools, tool)
		}
	}
	return tools
}

type cartItemArgs struct {
	Product   string `json:"product"`
	SKU       string `json:"sku"`
	Line      string `json:"line"`
	Color     string `json:"color"`
	Size      string `json:"size"`
	Quantity  *int   `json:"quantity"`
	Op        string `json:"op"`
	PendingID string `json:"pending_id"`
	Option    int    `json:"option"`
}

type cartArgs struct {
	Items        []cartItemArgs `json:"items"`
	CustomerCode string         `json:"customer_code"`
	Note         *string        `json:"note"`
}

// cartProductLine is a product line (one web name) and its variants, already
// limited to the caller's catalog scope.
type cartProductLine struct {
	Name     string
	Variants []models.CachedProduct
}

// cartCatalog is the product data a cart needs, swappable in tests.
type cartCatalog struct {
	bySKU func(ctx context.Context, sku string) (models.CachedProduct, bool)
	// search returns the lines whose name matches text; fuzzy is true when
	// no name matched and the closest line was guessed.
	search func(ctx context.Context, text string) (lines []cartProductLine, fuzzy bool, err error)
	line   func(ctx context.Context, name string) (cartProductLine, bool)
	// stock returns main-warehouse stock by SKU; ok=false when unknown.
	stock func(ctx context.Context) (map[string]float64, bool)
	// attributes maps the customer's colour/size words onto stored values.
	attributes func(ctx context.Context, color, size string, colors, sizes []string) (string, string)
}

type cartResolver struct {
	catalog    cartCatalog
	allowed    func(p models.CachedProduct) bool
	stockOnce  sync.Once
	stockMap   map[string]float64
	stockKnown bool
}

func (r *cartResolver) stockOf(ctx context.Context, sku string) *float64 {
	r.stockOnce.Do(func() {
		if r.catalog.stock != nil {
			r.stockMap, r.stockKnown = r.catalog.stock(ctx)
		}
	})
	if !r.stockKnown {
		return nil
	}
	v := r.stockMap[sku]
	return &v
}

type cartOutcome struct {
	change  string
	problem string
	pending string
}

// apply runs one item of an order_cart_update call against the cart.
func (r *cartResolver) apply(ctx context.Context, state *orderactions.CartState, item cartItemArgs) cartOutcome {
	op := strings.ToLower(strings.TrimSpace(item.Op))
	if op == "" {
		op = orderactions.OpAdd
	}
	if op != orderactions.OpAdd && op != orderactions.OpSet && op != orderactions.OpRemove {
		return cartOutcome{problem: fmt.Sprintf("op %q không hợp lệ.", item.Op)}
	}
	quantity := 1
	if item.Quantity != nil {
		quantity = *item.Quantity
	} else if op == orderactions.OpSet {
		return cartOutcome{problem: "op=set cần quantity."}
	}
	if quantity < 0 || quantity > orderDraftMaxQuantity || (op == orderactions.OpAdd && quantity == 0) {
		return cartOutcome{problem: fmt.Sprintf("Số lượng %d không hợp lệ.", quantity)}
	}
	label := firstNonEmpty(item.Product, item.SKU, item.Line, item.PendingID)

	// An answer to a question the cart is waiting on.
	if id := strings.TrimSpace(item.PendingID); id != "" {
		index, ok := state.FindPending(id)
		if !ok {
			return cartOutcome{problem: fmt.Sprintf("Không còn câu hỏi %s. Xem lại pending trong giỏ.", id)}
		}
		p := state.Pending[index]
		if op == orderactions.OpRemove {
			state.DropPending(id)
			return cartOutcome{change: fmt.Sprintf("Đã bỏ yêu cầu \"%s\"", p.Request)}
		}
		spec := cartSpec{pendingID: p.ID, request: p.Request, op: p.Op, quantity: p.Quantity, line: p.Line, color: p.Color, size: p.Size}
		if item.Quantity != nil && quantity > 0 {
			spec.quantity = quantity
		}
		if item.Option > 0 {
			if item.Option > len(p.Options) {
				return cartOutcome{problem: fmt.Sprintf("Câu hỏi %s không có lựa chọn số %d.", id, item.Option)}
			}
			chosen := p.Options[item.Option-1]
			if chosen.SKU != "" {
				spec.sku = chosen.SKU
			} else {
				spec.line = chosen.Line
			}
		}
		if v := strings.TrimSpace(item.Line); v != "" {
			spec.line = v
		}
		if v := strings.TrimSpace(item.Color); v != "" {
			spec.color = v
		}
		if v := strings.TrimSpace(item.Size); v != "" {
			spec.size = v
		}
		if v := strings.TrimSpace(item.SKU); v != "" {
			spec.sku = v
		}
		if v := strings.TrimSpace(item.Product); v != "" && spec.line == "" && spec.sku == "" {
			spec.request = v
		}
		return r.resolveInto(ctx, state, spec)
	}

	// A change to a line already in the cart: "lấy 2 cái thôi", "bỏ kính".
	if sku := strings.TrimSpace(item.SKU); sku != "" && op != orderactions.OpAdd {
		for i, line := range state.Lines {
			if strings.EqualFold(line.SKU, sku) {
				return cartOutcome{change: state.ChangeQuantity(i, op, quantity)}
			}
		}
		if op == orderactions.OpRemove {
			return cartOutcome{problem: fmt.Sprintf("Mã %s không có trong giỏ hàng.", sku)}
		}
	}
	if strings.TrimSpace(item.SKU) == "" && strings.TrimSpace(item.Line) == "" {
		matches := state.FindLines(item.Product, item.Color, item.Size)
		switch {
		case len(matches) == 1:
			return cartOutcome{change: state.ChangeQuantity(matches[0], op, quantity)}
		case len(matches) > 1 && op != orderactions.OpAdd:
			names := make([]string, 0, len(matches))
			for _, i := range matches {
				names = append(names, fmt.Sprintf("%s (SKU %s)", state.Lines[i].Label(), state.Lines[i].SKU))
			}
			return cartOutcome{problem: fmt.Sprintf("Trong giỏ có nhiều dòng khớp \"%s\": %s. Hỏi người dùng dòng nào rồi gọi lại với sku.", label, strings.Join(names, "; "))}
		case op == orderactions.OpRemove:
			return cartOutcome{problem: fmt.Sprintf("Không thấy \"%s\" trong giỏ hàng.", label)}
		}
	}
	if op == orderactions.OpSet && quantity == 0 {
		return cartOutcome{problem: fmt.Sprintf("Không thấy \"%s\" trong giỏ hàng.", label)}
	}
	return r.resolveInto(ctx, state, cartSpec{
		request: strings.TrimSpace(item.Product), op: op, quantity: quantity,
		sku: strings.TrimSpace(item.SKU), line: strings.TrimSpace(item.Line),
		color: strings.TrimSpace(item.Color), size: strings.TrimSpace(item.Size),
	})
}

type cartSpec struct {
	pendingID string
	request   string
	op        string
	quantity  int
	sku       string
	line      string
	color     string
	size      string
}

type cartResolution struct {
	line    *orderactions.CartLine
	pending *orderactions.CartPending
	problem string
}

func (r *cartResolver) resolveInto(ctx context.Context, state *orderactions.CartState, spec cartSpec) cartOutcome {
	res := r.resolve(ctx, spec)
	switch {
	case res.line != nil:
		if spec.pendingID != "" {
			state.DropPending(spec.pendingID)
		}
		op := spec.op
		if op == orderactions.OpRemove {
			op = orderactions.OpAdd
		}
		return cartOutcome{change: state.Apply(*res.line, op, spec.quantity)}
	case res.pending != nil:
		p := *res.pending
		p.ID = spec.pendingID
		p.Request = firstNonEmpty(spec.request, spec.line, spec.sku)
		p.Op = spec.op
		p.Quantity = spec.quantity
		return cartOutcome{pending: state.PutPending(p)}
	}
	return cartOutcome{problem: res.problem}
}

func (r *cartResolver) resolve(ctx context.Context, spec cartSpec) cartResolution {
	if spec.sku != "" {
		product, ok := r.catalog.bySKU(ctx, spec.sku)
		if !ok {
			return cartResolution{problem: fmt.Sprintf("Không tìm thấy mã sản phẩm %s.", spec.sku)}
		}
		if !r.allowed(product) {
			return cartResolution{problem: fmt.Sprintf("Mã %s không nằm trong danh mục được đặt.", spec.sku)}
		}
		return r.resolved(ctx, product)
	}
	var line cartProductLine
	switch {
	case spec.line != "":
		found, ok := r.catalog.line(ctx, spec.line)
		if !ok {
			return cartResolution{problem: fmt.Sprintf("Không tìm thấy dòng sản phẩm \"%s\".", spec.line)}
		}
		line = found
	case spec.request == "":
		return cartResolution{problem: "Thiếu tên hoặc mã sản phẩm."}
	default:
		lines, fuzzy, err := r.catalog.search(ctx, spec.request)
		if err != nil {
			log.Printf("[order_cart] product search %q failed: %v", spec.request, err)
			return cartResolution{problem: "Chưa tra được sản phẩm lúc này. Vui lòng thử lại sau."}
		}
		if len(lines) == 0 {
			return cartResolution{problem: fmt.Sprintf("Không tìm thấy sản phẩm khớp \"%s\". Hỏi người dùng mã hoặc tên đầy đủ hơn.", spec.request)}
		}
		exact := -1
		for i, l := range lines {
			if orderactions.Normalize(l.Name) == orderactions.Normalize(spec.request) {
				exact = i
				break
			}
		}
		switch {
		case exact >= 0:
			line = lines[exact]
		case len(lines) == 1 && !fuzzy:
			line = lines[0]
		default:
			return cartResolution{pending: r.linePending(ctx, lines, spec, fuzzy)}
		}
	}
	return r.pickVariant(ctx, line, spec)
}

// linePending asks which product line the customer means.
func (r *cartResolver) linePending(ctx context.Context, lines []cartProductLine, spec cartSpec, fuzzy bool) *orderactions.CartPending {
	sort.SliceStable(lines, func(i, j int) bool {
		if len(lines[i].Name) != len(lines[j].Name) {
			return len(lines[i].Name) < len(lines[j].Name)
		}
		return lines[i].Name < lines[j].Name
	})
	p := &orderactions.CartPending{Need: orderactions.NeedLine, Color: spec.color, Size: spec.size}
	switch {
	case fuzzy:
		p.Note = fmt.Sprintf("Không có tên khớp đúng \"%s\"; đây là sản phẩm gần nhất, hỏi người dùng có đúng không.", spec.request)
	case len(lines) > cartMaxLineOptions:
		p.Note = fmt.Sprintf("Có %d dòng khớp \"%s\", mới liệt kê %d dòng đầu; nếu không có, hỏi người dùng tên đầy đủ hơn.", len(lines), spec.request, cartMaxLineOptions)
	default:
		p.Note = fmt.Sprintf("\"%s\" khớp nhiều dòng sản phẩm, hỏi người dùng chọn dòng nào.", spec.request)
	}
	for i, l := range lines {
		if i >= cartMaxLineOptions {
			break
		}
		option := orderactions.CartOption{Option: i + 1, Line: l.Name, PriceRange: variantPriceRange(l.Variants)}
		var total float64
		known := true
		for _, v := range l.Variants {
			stock := r.stockOf(ctx, v.MA)
			if stock == nil {
				known = false
				break
			}
			total += *stock
		}
		if known {
			option.Stock = &total
		}
		p.Options = append(p.Options, option)
	}
	return p
}

func variantPriceRange(variants []models.CachedProduct) string {
	low, high := 0.0, 0.0
	for _, v := range variants {
		if v.DON_GIA_BAN <= 0 {
			continue
		}
		if low == 0 || v.DON_GIA_BAN < low {
			low = v.DON_GIA_BAN
		}
		if v.DON_GIA_BAN > high {
			high = v.DON_GIA_BAN
		}
	}
	switch {
	case high == 0:
		return ""
	case low == high:
		return orderactions.FormatVND(low)
	}
	return orderactions.FormatVND(low) + " - " + orderactions.FormatVND(high)
}

// pickVariant picks the SKU of a known line from the customer's colour and
// size, or asks for what is missing.
func (r *cartResolver) pickVariant(ctx context.Context, line cartProductLine, spec cartSpec) cartResolution {
	variants := line.Variants
	if len(variants) == 0 {
		return cartResolution{problem: fmt.Sprintf("Dòng \"%s\" không có mã nào được đặt.", line.Name)}
	}
	if len(variants) == 1 && spec.color == "" && spec.size == "" {
		return r.resolved(ctx, variants[0])
	}
	if len(variants) == 1 {
		// The sole SKU is added only when the asked colour/size fits it, or
		// when the line has no colour/size at all: hiding "_DK" copies can
		// leave one visible SKU of a different colour.
		if colors, sizes := variantAttributes(variants); len(colors) == 0 && len(sizes) == 0 {
			return r.resolved(ctx, variants[0])
		}
	}
	color, size := spec.color, spec.size
	matched := filterCartVariants(variants, color, size)
	if len(matched) == 0 && (color != "" || size != "") && r.catalog.attributes != nil {
		colors, sizes := variantAttributes(variants)
		mappedColor, mappedSize := r.catalog.attributes(ctx, color, size, colors, sizes)
		if mappedColor != "" {
			color = mappedColor
		}
		if mappedSize != "" {
			size = mappedSize
		}
		matched = filterCartVariants(variants, color, size)
	}
	if len(matched) == 1 {
		return r.resolved(ctx, matched[0])
	}

	p := &orderactions.CartPending{Need: orderactions.NeedVariant, Line: line.Name}
	base := matched
	switch {
	case spec.color == "" && spec.size == "":
		base = variants
		p.Note = fmt.Sprintf("Dòng \"%s\": hỏi người dùng chọn màu/size.", line.Name)
	case len(matched) == 0:
		base = variants
		var asked []string
		if spec.color != "" {
			asked = append(asked, "màu "+spec.color)
		}
		if spec.size != "" {
			asked = append(asked, "size "+spec.size)
		}
		p.Note = fmt.Sprintf("Dòng \"%s\" không có %s. Báo người dùng và hỏi chọn lại.", line.Name, strings.Join(asked, ", "))
	default:
		p.Color, p.Size = spec.color, spec.size
		p.Note = fmt.Sprintf("Dòng \"%s\": hỏi người dùng chọn màu/size.", line.Name)
	}

	type candidate struct {
		product models.CachedProduct
		stock   *float64
	}
	candidates := make([]candidate, 0, len(base))
	inStock := 0
	for _, v := range base {
		stock := r.stockOf(ctx, v.MA)
		if stock != nil && *stock > 0 {
			inStock++
		}
		candidates = append(candidates, candidate{v, stock})
	}
	if inStock > 0 {
		kept := candidates[:0]
		for _, c := range candidates {
			if c.stock != nil && *c.stock > 0 {
				kept = append(kept, c)
			}
		}
		candidates = kept
	} else if candidates[0].stock != nil {
		p.Note = fmt.Sprintf("Dòng \"%s\" hiện hết hàng ở kho tổng%s. Báo người dùng.", line.Name, attrSuffix(spec.color, spec.size))
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		a, b := candidates[i].product, candidates[j].product
		if a.THUOC_TINH_1 != b.THUOC_TINH_1 {
			return a.THUOC_TINH_1 < b.THUOC_TINH_1
		}
		return sizeRank(a.THUOC_TINH_2) < sizeRank(b.THUOC_TINH_2)
	})
	colorSet, sizeSet := map[string]bool{}, map[string]bool{}
	for _, c := range candidates {
		if v := strings.TrimSpace(c.product.THUOC_TINH_1); v != "" && !colorSet[v] {
			colorSet[v] = true
			p.Colors = append(p.Colors, v)
		}
		if v := strings.TrimSpace(c.product.THUOC_TINH_2); v != "" && !sizeSet[v] {
			sizeSet[v] = true
			p.Sizes = append(p.Sizes, v)
		}
	}
	for i, c := range candidates {
		if i >= cartMaxVariantOption {
			p.Note += fmt.Sprintf(" Còn %d lựa chọn khác: hỏi màu và size trước (colors, sizes).", len(candidates)-cartMaxVariantOption)
			break
		}
		p.Options = append(p.Options, orderactions.CartOption{
			Option: i + 1, SKU: c.product.MA, Color: strings.TrimSpace(c.product.THUOC_TINH_1),
			Size: strings.TrimSpace(c.product.THUOC_TINH_2), Price: c.product.DON_GIA_BAN, Stock: c.stock,
		})
	}
	return cartResolution{pending: p}
}

func attrSuffix(color, size string) string {
	var parts []string
	if color != "" {
		parts = append(parts, "màu "+color)
	}
	if size != "" {
		parts = append(parts, "size "+size)
	}
	if len(parts) == 0 {
		return ""
	}
	return " (" + strings.Join(parts, ", ") + ")"
}

var sizeOrder = map[string]int{"XXS": 1, "XS": 2, "S": 3, "M": 4, "L": 5, "XL": 6, "XXL": 7, "2XL": 7, "XXXL": 8, "3XL": 8}

func sizeRank(size string) string {
	if n, ok := sizeOrder[strings.ToUpper(strings.TrimSpace(size))]; ok {
		return fmt.Sprintf("0%02d", n)
	}
	return "1" + strings.ToUpper(size)
}

// filterCartVariants keeps variants whose colour contains the colour words
// and whose size equals the size (accents and case ignored).
func filterCartVariants(variants []models.CachedProduct, color, size string) []models.CachedProduct {
	color = orderactions.Normalize(color)
	size = orderactions.Normalize(normalizeSizeFilter(size))
	if color == "" && size == "" {
		return nil
	}
	var out []models.CachedProduct
	for _, v := range variants {
		if color != "" {
			stored := orderactions.Normalize(v.THUOC_TINH_1)
			if stored != color && !strings.Contains(stored, color) {
				continue
			}
		}
		if size != "" && orderactions.Normalize(v.THUOC_TINH_2) != size {
			continue
		}
		out = append(out, v)
	}
	return out
}

func variantAttributes(variants []models.CachedProduct) ([]string, []string) {
	colorSet, sizeSet := map[string]struct{}{}, map[string]struct{}{}
	for _, v := range variants {
		if c := strings.TrimSpace(v.THUOC_TINH_1); c != "" {
			colorSet[c] = struct{}{}
		}
		if s := strings.TrimSpace(v.THUOC_TINH_2); s != "" {
			sizeSet[s] = struct{}{}
		}
	}
	return sortedStringKeys(colorSet), sortedStringKeys(sizeSet)
}

func (r *cartResolver) resolved(ctx context.Context, p models.CachedProduct) cartResolution {
	if p.DON_GIA_BAN <= 0 {
		return cartResolution{problem: fmt.Sprintf("Mã %s chưa có giá bán trên ERP nên chưa đặt qua chatbot được.", p.MA)}
	}
	return cartResolution{line: &orderactions.CartLine{
		SKU:   p.MA,
		Name:  strings.TrimSpace(p.TEN),
		Line:  strings.TrimSpace(p.TEN_DONG_BO_WEB),
		Color: strings.TrimSpace(p.THUOC_TINH_1),
		Size:  strings.TrimSpace(p.THUOC_TINH_2),
		Unit:  strings.TrimSpace(p.DVT),
		Price: p.DON_GIA_BAN,
		Stock: r.stockOf(ctx, p.MA),
	}}
}

// ---------------------------------------------------------------------------
// Catalog backed by the product cache, the fuzzy matchers and Cloudify stock.

func liveCartCatalog(tenantID string, stockClient *pkg.CloudifyClient) cartCatalog {
	return cartCatalog{
		bySKU: func(ctx context.Context, sku string) (models.CachedProduct, bool) {
			var product models.CachedProduct
			if err := db.DB.WithContext(ctx).Where("tenant_id = ? AND ma = ?", tenantID, sku).First(&product).Error; err != nil || product.MA == "" {
				return models.CachedProduct{}, false
			}
			return product, true
		},
		search: func(ctx context.Context, text string) ([]cartProductLine, bool, error) {
			text = strings.TrimSpace(text)
			if text == "" {
				return nil, false, nil
			}
			pattern := "%" + text + "%"
			for _, column := range []string{"ten_dong_bo_web", "ten", "ma"} {
				var rows []models.CachedProduct
				if err := db.DB.WithContext(ctx).Scopes(engine.ExcludeDKCopies).Where("tenant_id = ? AND "+column+" LIKE ?", tenantID, pattern).
					Limit(cartCatalogRowLimit).Find(&rows).Error; err != nil {
					return nil, false, err
				}
				if lines := groupCartLines(rows); len(lines) > 0 {
					return lines, false, nil
				}
			}
			_, maCha, _ := resolveMaChaFuzzy(ctx, tenantID, text, engine.ExcludeDKCopies)
			maCha = engine.RealParentOf(maCha)
			if maCha == "" {
				return nil, false, nil
			}
			var rows []models.CachedProduct
			if err := db.DB.WithContext(ctx).Scopes(engine.ExcludeDKCopies).Where("tenant_id = ? AND ma_cha = ?", tenantID, maCha).
				Limit(cartCatalogRowLimit).Find(&rows).Error; err != nil {
				return nil, false, err
			}
			return groupCartLines(rows), true, nil
		},
		line: func(ctx context.Context, name string) (cartProductLine, bool) {
			for _, column := range []string{"ten_dong_bo_web", "ma_cha"} {
				var rows []models.CachedProduct
				if err := db.DB.WithContext(ctx).Scopes(engine.ExcludeDKCopies).Where("tenant_id = ? AND "+column+" = ?", tenantID, strings.TrimSpace(name)).
					Limit(cartCatalogRowLimit).Find(&rows).Error; err == nil {
					if lines := groupCartLines(rows); len(lines) > 0 {
						return lines[0], true
					}
				}
			}
			return cartProductLine{}, false
		},
		stock: func(ctx context.Context) (map[string]float64, bool) {
			if stockClient == nil {
				return nil, false
			}
			return cartStock.get(ctx, tenantID, stockClient)
		},
		attributes: func(ctx context.Context, color, size string, colors, sizes []string) (string, string) {
			c, s, _, err := fuzzyMatchAttributesWithLLM(ctx, tenantID, color, size, "", colors, sizes, nil)
			if err != nil {
				log.Printf("[order_cart] attribute match failed: %v", err)
			}
			return c, s
		},
	}
}

// groupCartLines buckets cache rows by web name (or parent code when a row
// has none). ERP "_DK" copy rows are left out: unpriced, colour/size
// "false", never orderable. Lookups by exact SKU (bySKU) still see them.
func groupCartLines(rows []models.CachedProduct) []cartProductLine {
	index := map[string]int{}
	var lines []cartProductLine
	for _, row := range rows {
		if engine.IsDKCopy(row.MA, row.MA_CHA) {
			continue
		}
		name := strings.TrimSpace(row.TEN_DONG_BO_WEB)
		if name == "" {
			name = strings.TrimSpace(row.MA_CHA)
		}
		if name == "" {
			name = strings.TrimSpace(row.MA)
		}
		i, ok := index[name]
		if !ok {
			i = len(lines)
			index[name] = i
			lines = append(lines, cartProductLine{Name: name})
		}
		lines[i].Variants = append(lines[i].Variants, row)
	}
	return lines
}

// scopeLines drops variants outside the caller's catalog, and lines left
// empty.
func scopeLines(lines []cartProductLine, allowed func(models.CachedProduct) bool) []cartProductLine {
	out := lines[:0]
	for _, line := range lines {
		kept := make([]models.CachedProduct, 0, len(line.Variants))
		for _, v := range line.Variants {
			if allowed(v) {
				kept = append(kept, v)
			}
		}
		if len(kept) > 0 {
			line.Variants = kept
			out = append(out, line)
		}
	}
	return out
}

func scopedCatalog(c cartCatalog, allowed func(models.CachedProduct) bool) cartCatalog {
	search, line := c.search, c.line
	c.search = func(ctx context.Context, text string) ([]cartProductLine, bool, error) {
		lines, fuzzy, err := search(ctx, text)
		return scopeLines(lines, allowed), fuzzy, err
	}
	c.line = func(ctx context.Context, name string) (cartProductLine, bool) {
		found, ok := line(ctx, name)
		if !ok {
			return found, false
		}
		scoped := scopeLines([]cartProductLine{found}, allowed)
		if len(scoped) == 0 {
			return cartProductLine{}, false
		}
		return scoped[0], true
	}
	return c
}

func cartProductAllowed(permissions *engine.GroupPermissionContext) func(models.CachedProduct) bool {
	groups, brands, allBrands, filtered := productScope(permissions)
	return func(p models.CachedProduct) bool {
		return !filtered || engine.WarehouseProductAllowed(p.LIST_TEN_NHOM_VTHH, p.NHAN_HIEU_NAME, groups, brands, allBrands)
	}
}

// cartStockCache keeps the main-warehouse report for a short time: a chat
// order calls the cart tools several times within a minute, and the report
// is one large Cloudify request.
type cartStockCache struct {
	mu      sync.Mutex
	entries map[string]cartStockEntry
}

type cartStockEntry struct {
	totals  map[string]float64
	fetched time.Time
	failed  bool
}

const (
	cartStockTTL = 2 * time.Minute
	// cartStockRetryAfter keeps a failing report from stalling every cart
	// call: stock is shown as unknown for a minute, then tried again.
	cartStockRetryAfter = time.Minute
)

var cartStock = &cartStockCache{entries: map[string]cartStockEntry{}}

func (c *cartStockCache) get(ctx context.Context, tenantID string, client *pkg.CloudifyClient) (map[string]float64, bool) {
	c.mu.Lock()
	entry, ok := c.entries[tenantID]
	c.mu.Unlock()
	switch {
	case ok && entry.failed && time.Since(entry.fetched) < cartStockRetryAfter:
		return nil, false
	case ok && !entry.failed && time.Since(entry.fetched) < cartStockTTL:
		return entry.totals, true
	}
	totals, err := engine.MainWarehouseStock(ctx, tenantID, client)
	if err != nil {
		log.Printf("[order_cart] main warehouse stock unavailable: %v", err)
		c.mu.Lock()
		c.entries[tenantID] = cartStockEntry{fetched: time.Now(), failed: true}
		c.mu.Unlock()
		return nil, false
	}
	c.mu.Lock()
	c.entries[tenantID] = cartStockEntry{totals: totals, fetched: time.Now()}
	c.mu.Unlock()
	return totals, true
}

func (c *cartStockCache) forget(tenantID string) {
	c.mu.Lock()
	delete(c.entries, tenantID)
	c.mu.Unlock()
}

// ---------------------------------------------------------------------------
// Tool handler.

// cartDeps are the outside calls the cart tools make, swappable in tests.
type cartDeps struct {
	catalog cartCatalog
	notify  func(ctx context.Context, permissions *engine.GroupPermissionContext, text string) error
	// freshStock re-reads stock before the final confirmation.
	freshStock func(ctx context.Context) (map[string]float64, bool)
}

func handleCartTool(ctx context.Context, tenantID, agentType, permissionToken string, tool erpMCPTool, rawArgs map[string]json.RawMessage) gin.H {
	result := func(body any, failed bool) gin.H {
		data, _ := json.Marshal(body)
		return erpMCPResult(data, failed)
	}
	if permissionToken == "" {
		return result(gin.H{"error": "forbidden_token"}, true)
	}
	cfg, _ := config.Load()
	permissions, err := engine.VerifyPermissionToken(permissionToken, cfg.EncryptionKey)
	if err != nil {
		return result(gin.H{"error": "forbidden_token", "message": "Token phân quyền không hợp lệ hoặc đã hết hạn."}, true)
	}
	if permissions.TenantID != tenantID || permissions.AgentType != agentType {
		return result(gin.H{"error": "forbidden_token"}, true)
	}
	if !isERPActive(tenantID, agentType) {
		return result(gin.H{"error": "erp_inactive", "message": "Tích hợp ERP đang tắt."}, true)
	}
	var args cartArgs
	if data, err := json.Marshal(rawArgs); err != nil || json.Unmarshal(data, &args) != nil {
		return result(gin.H{"error": "invalid_arguments", "message": "Tham số không hợp lệ."}, true)
	}

	var client *pkg.CloudifyClient
	if erpURL, erpDB, erpLogin, erpPassword, err := loadCloudifyCredentials(tenantID, cfg); err == nil && erpURL != "" && erpLogin != "" && erpPassword != "" {
		client = &pkg.CloudifyClient{BaseURL: erpURL, DB: erpDB, Login: erpLogin, Password: erpPassword}
	}
	deps := cartDeps{
		catalog: liveCartCatalog(tenantID, client),
		notify: func(ctx context.Context, permissions *engine.GroupPermissionContext, text string) error {
			return sendPrivateOrdersPrompt(ctx, tenantID, permissions, text)
		},
		freshStock: func(ctx context.Context) (map[string]float64, bool) {
			if client == nil {
				return nil, false
			}
			cartStock.forget(tenantID)
			return cartStock.get(ctx, tenantID, client)
		},
	}
	body, failed := runCartTool(ctx, permissions, tool, args, deps)
	if failed {
		writeAuditLog(tenantID, permissions, tool.resource, "", nil, tool.Name, 400, 0, "")
	} else {
		writeAuditLog(tenantID, permissions, tool.resource, "", nil, tool.Name, 200, 1, "")
	}
	return result(body, failed)
}

// runCartTool runs a cart tool for a verified caller and returns the tool
// result body.
func runCartTool(ctx context.Context, permissions *engine.GroupPermissionContext, tool erpMCPTool, args cartArgs, deps cartDeps) (gin.H, bool) {
	allowed, scope := orderWriteScope(permissions, resourceOrderCreate)
	if !allowed {
		return gin.H{"status": "error", "message": "Tài khoản này chưa được cấp quyền đặt hàng qua chatbot. Vui lòng liên hệ nhân viên."}, true
	}
	if permissions.ChannelID == "" || permissions.ZaloUserID == "" {
		return gin.H{"status": "error", "message": "Không xác định được cuộc trò chuyện để lên đơn."}, true
	}
	workKey := engine.BuildSessionKey(permissions.ChannelID, permissions.ZaloUserID, permissions.ZaloGroupID)
	seed := models.OrderCart{
		TenantID: permissions.TenantID, WorkKey: workKey, AgentType: permissions.AgentType,
		ChannelID: permissions.ChannelID, ZaloUserID: permissions.ZaloUserID, ZaloGroupID: permissions.ZaloGroupID,
	}
	fail := func(err error) (gin.H, bool) {
		var refusal *orderDraftError
		if errors.As(err, &refusal) {
			return gin.H{"status": "error", "message": refusal.message}, true
		}
		if errors.Is(err, orderactions.ErrNoCart) {
			return gin.H{"status": "error", "message": "Chưa có giỏ hàng nào đang lên.", "cart": orderactions.ViewForAgent(nil, orderactions.CartState{}, permissions.AgentType)}, true
		}
		log.Printf("[order_cart] %s failed for %s: %v", tool.Name, permissions.ZaloUserID, err)
		return gin.H{"error": "cart_unavailable", "message": "Chưa thể cập nhật giỏ hàng lúc này. Vui lòng thử lại sau."}, true
	}

	switch tool.Name {
	case "order_cart_view":
		cart, state, ok := orderactions.OpenCart(ctx, permissions.TenantID, workKey)
		if !ok {
			return gin.H{"status": "success", "message": "Chưa có giỏ hàng nào đang lên.", "cart": orderactions.ViewForAgent(nil, orderactions.CartState{}, permissions.AgentType)}, false
		}
		return gin.H{"status": "success", "cart": orderactions.ViewForAgent(cart, state, permissions.AgentType)}, false

	case "order_cart_clear":
		cancelled, err := orderactions.CancelCart(ctx, permissions.TenantID, workKey)
		if err != nil {
			return fail(err)
		}
		if !cancelled {
			return gin.H{"status": "success", "message": "Không có giỏ hàng nào đang lên."}, false
		}
		return gin.H{"status": "success", "message": "Đã huỷ giỏ hàng. Chưa có đơn nào được tạo."}, false

	case "order_cart_update":
		if len(args.Items) > cartMaxItemsPerCall {
			return gin.H{"status": "error", "message": fmt.Sprintf("Tối đa %d sản phẩm mỗi lần.", cartMaxItemsPerCall)}, true
		}
		allowedProduct := cartProductAllowed(permissions)
		resolver := &cartResolver{catalog: scopedCatalog(deps.catalog, allowedProduct), allowed: allowedProduct}
		var changes, problems []string
		var asked []string
		cart, state, err := orderactions.UpdateCart(ctx, seed, true, func(cart *models.OrderCart, state *orderactions.CartState) (bool, error) {
			changed := false
			if code := strings.TrimSpace(args.CustomerCode); code != "" {
				resolvedCode, name, err := resolveDraftCustomer(permissions, scope, code)
				if err != nil {
					return false, err
				}
				if cart.CustomerCode != resolvedCode {
					cart.CustomerCode, cart.CustomerName = resolvedCode, name
					changes = append(changes, "Khách hàng: "+customerText(resolvedCode, name))
					changed = true
				}
			}
			if args.Note != nil && strings.TrimSpace(*args.Note) != cart.Note {
				cart.Note = truncateRunes(strings.TrimSpace(*args.Note), 900)
				changes = append(changes, "Đã ghi chú: "+cart.Note)
				changed = true
			}
			for _, item := range args.Items {
				outcome := resolver.apply(ctx, state, item)
				switch {
				case outcome.change != "":
					changes = append(changes, outcome.change)
					changed = true
				case outcome.pending != "":
					asked = append(asked, outcome.pending)
					changed = true
				case outcome.problem != "":
					problems = append(problems, outcome.problem)
				}
			}
			if len(state.Lines) > orderDraftMaxLines {
				return false, refuse("Mỗi đơn tối đa %d dòng sản phẩm.", orderDraftMaxLines)
			}
			return changed, nil
		})
		if err != nil {
			return fail(err)
		}
		body := gin.H{"status": "success", "cart": orderactions.ViewForAgent(cart, state, permissions.AgentType)}
		if len(changes) > 0 {
			body["changes"] = changes
		}
		if len(asked) > 0 {
			body["new_questions"] = asked
		}
		if len(problems) > 0 {
			body["problems"] = problems
		}
		return body, false

	case "order_cart_checkout":
		if code := strings.TrimSpace(args.CustomerCode); code != "" || args.Note != nil {
			_, _, err := orderactions.UpdateCart(ctx, seed, false, func(cart *models.OrderCart, _ *orderactions.CartState) (bool, error) {
				changed := false
				if code != "" {
					resolvedCode, name, err := resolveDraftCustomer(permissions, scope, code)
					if err != nil {
						return false, err
					}
					changed = changed || cart.CustomerCode != resolvedCode
					cart.CustomerCode, cart.CustomerName = resolvedCode, name
				}
				if args.Note != nil && strings.TrimSpace(*args.Note) != "" && strings.TrimSpace(*args.Note) != cart.Note {
					cart.Note = truncateRunes(strings.TrimSpace(*args.Note), 900)
					changed = true
				}
				return changed, nil
			})
			if err != nil {
				return fail(err)
			}
		}
		cart, state, ok := orderactions.OpenCart(ctx, permissions.TenantID, workKey)
		if !ok {
			return fail(orderactions.ErrNoCart)
		}
		action, err := buildCartDraft(ctx, permissions, scope, cart, state, deps)
		if err != nil {
			if refusal := (*orderDraftError)(nil); errors.As(err, &refusal) {
				return gin.H{"status": "error", "message": refusal.message, "cart": orderactions.ViewForAgent(cart, state, permissions.AgentType)}, true
			}
			return fail(err)
		}
		if err := orderactions.Save(ctx, action); err != nil {
			return fail(err)
		}
		orderactions.SetCartDraft(ctx, cart.ID, action.ID)
		if err := deps.notify(ctx, permissions, action.Summary); err != nil {
			log.Printf("[order_cart] confirmation push failed for %s (worker will resend): %v", permissions.ZaloUserID, err)
		}
		log.Printf("[order_cart] cart %s → draft %s for user=%s customer=%s lines=%d", cart.ID, action.ID, action.ZaloUserID, action.CustomerCode, len(state.Lines))
		return gin.H{
			"status":   "success",
			"draft_id": action.ID,
			"message":  "zalo_rich_message_sent_directly",
			"note":     "Đã gửi bản xác nhận đơn cho người dùng. Chờ họ trả lời 1 để lên đơn, hoặc nhắn điều cần sửa.",
		}, false
	}
	return gin.H{"error": "unknown_tool"}, true
}

// buildCartDraft turns the cart into the final create draft: prices are read
// again from the cache, stock again from Cloudify, and the summary lists every
// line so the customer confirms exactly what will be written.
func buildCartDraft(ctx context.Context, permissions *engine.GroupPermissionContext, scope string, cart *models.OrderCart, state orderactions.CartState, deps cartDeps) (*models.OrderAction, error) {
	if len(state.Pending) > 0 {
		var asks []string
		for _, p := range state.Pending {
			asks = append(asks, fmt.Sprintf("%s \"%s\"", p.ID, p.Request))
		}
		return nil, refuse("Còn sản phẩm chờ người dùng chọn: %s. Hỏi xong rồi mới chốt đơn.", strings.Join(asks, ", "))
	}
	if len(state.Lines) == 0 {
		return nil, refuse("Giỏ hàng đang trống.")
	}
	items := make([]orderDraftItem, 0, len(state.Lines))
	for _, line := range state.Lines {
		items = append(items, orderDraftItem{SKU: line.SKU, Quantity: line.Quantity})
	}
	action := &models.OrderAction{
		ID:          uuid.NewString(),
		Kind:        orderactions.KindCreate,
		TenantID:    permissions.TenantID,
		AgentType:   permissions.AgentType,
		WorkKey:     cart.WorkKey,
		ChannelID:   permissions.ChannelID,
		ZaloUserID:  permissions.ZaloUserID,
		ZaloGroupID: permissions.ZaloGroupID,
		CartID:      cart.ID,
		ExpiresAt:   time.Now().Add(orderactions.CartDraftTTL),
	}
	body, err := buildCreateDraft(permissions, scope, orderDraftArgs{CustomerCode: cart.CustomerCode, Items: items, Note: cart.Note}, action)
	if err != nil {
		return nil, err
	}
	lines, _ := body["DON_DAT_HANG_CHI_TIET"].([]orderLine)

	if deps.freshStock != nil {
		if stock, ok := deps.freshStock(ctx); ok {
			var short []string
			for _, line := range lines {
				if available := stock[line.SKU]; line.Quantity > available {
					if permissions.AgentType != "private" {
						if available <= 0 {
							short = append(short, fmt.Sprintf("%s (SKU %s) hiện hết hàng ở kho tổng", line.Name, line.SKU))
						} else {
							short = append(short, fmt.Sprintf("%s (SKU %s) đặt %s nhưng kho tổng không đủ số lượng", line.Name, line.SKU, formatQuantity(line.Quantity)))
						}
					} else {
						short = append(short, fmt.Sprintf("%s (SKU %s) đặt %s, kho tổng còn %s", line.Name, line.SKU, formatQuantity(line.Quantity), formatQuantity(available)))
					}
				}
			}
			if len(short) > 0 {
				return nil, refuse("Kho tổng không đủ: %s. Hỏi người dùng giảm số lượng hoặc bỏ sản phẩm.", strings.Join(short, "; "))
			}
		}
	}

	labels := map[string]string{}
	for _, line := range state.Lines {
		labels[strings.ToUpper(line.SKU)] = line.Label()
	}
	var b strings.Builder
	b.WriteString("📝 XÁC NHẬN ĐƠN HÀNG\n")
	fmt.Fprintf(&b, "Khách: %s\n\n", customerText(action.CustomerCode, action.CustomerName))
	var total, pieces float64
	for i, line := range lines {
		label := labels[strings.ToUpper(line.SKU)]
		if label == "" {
			label = line.Name
		}
		amount := line.Quantity * line.Price
		total += amount
		pieces += line.Quantity
		fmt.Fprintf(&b, "%d. %s (%s)\n   %s %s × %s = %s\n", i+1, label, line.SKU, formatQuantity(line.Quantity), strings.ToLower(firstNonEmpty(line.Unit, "cái")), orderactions.FormatVND(line.Price), orderactions.FormatVND(amount))
	}
	fmt.Fprintf(&b, "\nTổng: %d sản phẩm, %s món. Tạm tính %s (giá bán ERP, chưa gồm chiết khấu).\n", len(lines), formatQuantity(pieces), orderactions.FormatVND(total))
	if cart.Note != "" {
		fmt.Fprintf(&b, "Ghi chú: %s\n", cart.Note)
	}
	b.WriteString("\n👉 Trả lời 1 (hoặc \"chốt\") để lên đơn.\n")
	fmt.Fprintf(&b, "✏️ Cần sửa thì cứ nhắn, ví dụ \"%s lấy 2 cái\" hoặc \"bỏ %s\". Bản xác nhận giữ trong %d phút.", shortName(labels, lines, 0), shortName(labels, lines, len(lines)-1), int(orderactions.CartDraftTTL.Minutes()))
	action.Summary = b.String()

	payload, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	action.Payload = string(payload)
	return action, nil
}

// shortName is a line's product name without colour and size, for the
// correction example in the confirmation.
func shortName(labels map[string]string, lines []orderLine, index int) string {
	if index < 0 || index >= len(lines) {
		return "sản phẩm"
	}
	label := labels[strings.ToUpper(lines[index].SKU)]
	if i := strings.Index(label, " - "); i > 0 {
		label = label[:i]
	}
	if label == "" {
		label = lines[index].SKU
	}
	return label
}

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		return string(r[:n])
	}
	return s
}
