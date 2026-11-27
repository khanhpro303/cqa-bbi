package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/vietbui/chat-quality-agent/cloudifyorders"
	"github.com/vietbui/chat-quality-agent/config"
	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/db/models"
	"github.com/vietbui/chat-quality-agent/engine"
	"github.com/vietbui/chat-quality-agent/orderactions"
	"github.com/vietbui/chat-quality-agent/pkg"
)

// Permission resources for the order write tools (see orderactions.WriteScope).
const (
	resourceOrderCreate = orderactions.ResourceOrderCreate
	resourceOrderUpdate = orderactions.ResourceOrderUpdate
	resourceOrderCancel = orderactions.ResourceOrderCancel
)

const (
	orderDraftMaxLines    = 30
	orderDraftMaxQuantity = 100000
)

// orderDraftItemsSchema is the JSON schema of the items argument.
var orderDraftItemsSchema = map[string]any{
	"type":     "array",
	"minItems": 1,
	"maxItems": orderDraftMaxLines,
	"items": map[string]any{
		"type": "object",
		"properties": map[string]any{
			"sku":      map[string]any{"type": "string", "description": "Mã SKU chính xác (MA), đã tra bằng search_product_variants."},
			"quantity": map[string]any{"type": "integer", "description": "Số lượng. Khi sửa đơn: số lượng mới của dòng, 0 để bỏ dòng."},
		},
		"required":             []string{"sku", "quantity"},
		"additionalProperties": false,
	},
}

const orderDraftDescriptionSuffix = " Tool chỉ tạo BẢN NHÁP: hệ thống tự gửi tóm tắt cho người dùng và chỉ ghi lên ERP khi chính họ trả lời 1 để xác nhận. Khi kết quả có message=zalo_rich_message_sent_directly, chỉ trả lời đúng [RICH_MESSAGE_SENT]. Không tự báo đơn đã tạo/sửa/huỷ."

var erpMCPOrderTools = []erpMCPTool{
	{
		Name:        "draft_order_create",
		Description: "Tạo nháp đơn đặt hàng mới khi người dùng muốn đặt hàng. Trước đó phải xác định đúng SKU (màu, size) bằng search_product_variants. customer_code chỉ cần cho nhân viên đặt hộ khách; khách hàng tự đặt thì bỏ trống. Giá do hệ thống lấy từ ERP." + orderDraftDescriptionSuffix,
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"customer_code": map[string]any{"type": "string", "description": "Mã khách hàng (nhân viên đặt hộ). Khách tự đặt thì bỏ trống."},
				"items":         orderDraftItemsSchema,
				"note":          map[string]any{"type": "string", "description": "Ghi chú của người dùng cho đơn (địa chỉ, thời gian giao...)."},
			},
			"required":             []string{"items"},
			"additionalProperties": false,
		},
		resource: resourceOrderCreate,
	},
	{
		Name:        "draft_order_update",
		Description: "Tạo nháp sửa một đơn đặt hàng đã có (đổi số lượng, thêm hoặc bỏ dòng, sửa ghi chú; nhân viên có thể đổi trạng thái). items chỉ liệt kê các dòng cần đổi, quantity là số lượng mới (0 để bỏ dòng)." + orderDraftDescriptionSuffix,
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"order_code": map[string]any{"type": "string", "description": "Mã đơn, ví dụ ĐH000016."},
				"items":      orderDraftItemsSchema,
				"note":       map[string]any{"type": "string", "description": "Ghi chú mới (thay ghi chú cũ)."},
				"status":     map[string]any{"type": "string", "enum": []string{"0", "1", "2", "4"}, "description": "Chỉ nhân viên: 0 Chưa thực hiện, 1 Đang thực hiện, 2 Hoàn thành, 4 Đang giao. Huỷ đơn thì dùng draft_order_cancel."},
			},
			"required":             []string{"order_code"},
			"additionalProperties": false,
		},
		resource: resourceOrderUpdate,
	},
	{
		Name:        "draft_order_cancel",
		Description: "Tạo nháp huỷ một đơn đặt hàng (chuyển trạng thái sang Hủy, không xoá đơn)." + orderDraftDescriptionSuffix,
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"order_code": map[string]any{"type": "string", "description": "Mã đơn, ví dụ ĐH000016."},
				"reason":     map[string]any{"type": "string", "description": "Lý do huỷ, nếu người dùng có nói."},
			},
			"required":             []string{"order_code"},
			"additionalProperties": false,
		},
		resource: resourceOrderCancel,
	},
}

func findOrderTool(name string) (erpMCPTool, bool) {
	for _, tool := range erpMCPOrderTools {
		if tool.Name == name {
			return tool, true
		}
	}
	return erpMCPTool{}, false
}

type orderDraftItem struct {
	SKU      string `json:"sku"`
	Quantity int    `json:"quantity"`
}

type orderDraftArgs struct {
	CustomerCode string           `json:"customer_code"`
	OrderCode    string           `json:"order_code"`
	Items        []orderDraftItem `json:"items"`
	Note         string           `json:"note"`
	Reason       string           `json:"reason"`
	Status       string           `json:"status"`
}

// orderDraftError is a refusal the agent should relay to the user as is.
type orderDraftError struct{ message string }

func (e *orderDraftError) Error() string { return e.message }

func refuse(format string, args ...any) error {
	return &orderDraftError{message: fmt.Sprintf(format, args...)}
}

// orderLine is one sales-order line in the Cloudify create/update body.
type orderLine struct {
	SKU      string  `json:"MA_HANG"`
	Name     string  `json:"TEN_HANG"`
	Unit     string  `json:"DVT"`
	Quantity float64 `json:"SO_LUONG"`
	Price    float64 `json:"DON_GIA"`
	// Note is the line note. Cloudify drops the order-level GHI_CHU on
	// create, so the draft tag and the customer's note live on the first line.
	Note     string  `json:"GHI_CHU,omitempty"`
	Discount float64 `json:"CHIET_KHAU,omitempty"`
}

// orderDraftDeps are the outside calls a draft needs, swappable in tests.
type orderDraftDeps struct {
	findOrder func(ctx context.Context, code string) (map[string]any, error)
	notify    func(ctx context.Context, permissions *engine.GroupPermissionContext, text string) error
}

// handleOrderDraftTool answers a tools/call for one of the order draft tools.
func handleOrderDraftTool(ctx context.Context, tenantID, agentType, permissionToken string, tool erpMCPTool, rawArgs map[string]json.RawMessage) gin.H {
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
	if permissions.PersonalZalo {
		return result(gin.H{"error": "forbidden_scope", "message": "Kênh Zalo cá nhân hỗ trợ tra cứu ERP. Vui lòng dùng Zalo OA để tạo, sửa hoặc hủy đơn."}, true)
	}
	if !isERPActive(tenantID, agentType) {
		return result(gin.H{"error": "erp_inactive", "message": "Tích hợp ERP đang tắt."}, true)
	}

	var args orderDraftArgs
	if data, err := json.Marshal(rawArgs); err != nil || json.Unmarshal(data, &args) != nil {
		return result(gin.H{"error": "invalid_arguments", "message": "Tham số không hợp lệ."}, true)
	}

	erpURL, erpDB, erpLogin, erpPassword, err := loadCloudifyCredentials(tenantID, cfg)
	if err != nil || erpURL == "" || erpLogin == "" || erpPassword == "" {
		return result(gin.H{"error": "erp_credentials_missing"}, true)
	}
	client := &pkg.CloudifyClient{BaseURL: erpURL, DB: erpDB, Login: erpLogin, Password: erpPassword}
	deps := orderDraftDeps{
		findOrder: func(ctx context.Context, code string) (map[string]any, error) {
			rows, err := client.SearchCustomEndpointWithBody("saorders/search", map[string]any{"SO_DON_HANG": code})
			if err != nil {
				return nil, err
			}
			for _, row := range rows {
				if strings.EqualFold(getMapString(row, "SO_DON_HANG", "MA_HOA_DON", "name"), code) {
					return row, nil
				}
			}
			return nil, nil
		},
		notify: func(ctx context.Context, permissions *engine.GroupPermissionContext, text string) error {
			return sendPrivateOrdersPrompt(ctx, tenantID, permissions, text)
		},
	}

	action, err := buildOrderDraft(ctx, permissions, tool, args, deps)
	if err != nil {
		var refusal *orderDraftError
		if errors.As(err, &refusal) {
			writeAuditLog(tenantID, permissions, tool.resource, "", nil, args.OrderCode, 400, 0, "")
			return result(gin.H{"status": "error", "message": refusal.message}, true)
		}
		log.Printf("[order_draft] %s failed for %s: %v", tool.Name, permissions.ZaloUserID, err)
		return result(gin.H{"error": "erp_data_unavailable", "message": "Chưa thể tạo đơn nháp lúc này. Vui lòng thử lại sau."}, true)
	}
	if err := orderactions.Save(ctx, action); err != nil {
		log.Printf("[order_draft] save failed: %v", err)
		return result(gin.H{"error": "draft_unavailable", "message": "Chưa thể lưu đơn nháp. Vui lòng thử lại sau."}, true)
	}
	if err := deps.notify(ctx, permissions, action.Summary); err != nil {
		log.Printf("[order_draft] summary push failed for %s (worker will resend): %v", permissions.ZaloUserID, err)
	}
	writeAuditLog(tenantID, permissions, tool.resource, "", nil, action.OrderCode, 200, 1, "")
	log.Printf("[order_draft] stored %s draft %s for user=%s customer=%s order=%s", action.Kind, action.ID, action.ZaloUserID, action.CustomerCode, action.OrderCode)
	return result(gin.H{
		"status":   "success",
		"draft_id": action.ID,
		"message":  "zalo_rich_message_sent_directly",
		"note":     "Đã gửi tóm tắt đơn nháp cho người dùng. Chờ họ trả lời 1 (xác nhận) hoặc 2 (huỷ).",
	}, false)
}

// orderWriteScope reports whether a write resource is enabled and with which
// scope.
func orderWriteScope(permissions *engine.GroupPermissionContext, resource string) (bool, string) {
	return orderactions.WriteScope(permissions, resource)
}

func buildOrderDraft(ctx context.Context, permissions *engine.GroupPermissionContext, tool erpMCPTool, args orderDraftArgs, deps orderDraftDeps) (*models.OrderAction, error) {
	allowed, scope := orderWriteScope(permissions, tool.resource)
	if !allowed {
		return nil, refuse("Tài khoản này chưa được cấp quyền %s qua chatbot. Vui lòng liên hệ nhân viên.", orderToolLabel(tool.resource))
	}
	if permissions.ChannelID == "" || permissions.ZaloUserID == "" {
		return nil, refuse("Không xác định được cuộc trò chuyện để gửi xác nhận đơn.")
	}
	action := &models.OrderAction{
		ID:          uuid.NewString(),
		TenantID:    permissions.TenantID,
		AgentType:   permissions.AgentType,
		WorkKey:     engine.BuildSessionKey(permissions.ChannelID, permissions.ZaloUserID, permissions.ZaloGroupID),
		ChannelID:   permissions.ChannelID,
		ZaloUserID:  permissions.ZaloUserID,
		ZaloGroupID: permissions.ZaloGroupID,
		ExpiresAt:   time.Now().Add(orderactions.DraftTTL),
	}
	var body map[string]any
	var err error
	switch tool.Name {
	case "draft_order_create":
		action.Kind = orderactions.KindCreate
		body, err = buildCreateDraft(permissions, scope, args, action)
	case "draft_order_update":
		action.Kind = orderactions.KindUpdate
		body, err = buildUpdateDraft(ctx, permissions, scope, args, action, deps)
	case "draft_order_cancel":
		action.Kind = orderactions.KindCancel
		body, err = buildCancelDraft(ctx, permissions, scope, args, action, deps)
	default:
		return nil, fmt.Errorf("unknown order tool %s", tool.Name)
	}
	if err != nil {
		return nil, err
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	action.Payload = string(payload)
	return action, nil
}

func orderToolLabel(resource string) string {
	switch resource {
	case resourceOrderCreate:
		return "tạo đơn"
	case resourceOrderUpdate:
		return "sửa đơn"
	case resourceOrderCancel:
		return "huỷ đơn"
	}
	return resource
}

// draftTag marks the order note so staff can trace an order to its draft.
func draftTag(id string) string { return "[CQA:" + id[:8] + "]" }

// draftOrderCode is the MA_HOA_DON a chatbot order is created with.
func draftOrderCode(id string) string {
	return "CQA-" + time.Now().In(time.FixedZone("ICT", 7*3600)).Format("060102") + "-" + strings.ToUpper(id[:6])
}

func buildCreateDraft(permissions *engine.GroupPermissionContext, scope string, args orderDraftArgs, action *models.OrderAction) (map[string]any, error) {
	code, name, err := resolveDraftCustomer(permissions, scope, args.CustomerCode)
	if err != nil {
		return nil, err
	}
	lines, err := resolveDraftLines(permissions, args.Items, false)
	if err != nil {
		return nil, err
	}
	action.CustomerCode, action.CustomerName = code, name
	note := strings.TrimSpace(draftTag(action.ID) + " " + strings.TrimSpace(args.Note))
	lines[0].Note = note
	// Cloudify keeps MA_HOA_DON exactly as sent, so give each draft its own
	// code: it is the order number staff see, and it lets anyone find the
	// order when a create timed out with an unknown outcome.
	action.OrderCode = draftOrderCode(action.ID)
	body := map[string]any{
		"TINH_TRANG":            orderactions.OrderStatusInProgress,
		"MA_HOA_DON":            action.OrderCode,
		"MA_KHACH_HANG":         code,
		"TEN_KHACH_HANG":        name,
		"GHI_CHU":               note,
		"DON_DAT_HANG_CHI_TIET": lines,
	}
	var b strings.Builder
	fmt.Fprintf(&b, "📝 Xác nhận TẠO ĐƠN cho %s:\n", customerText(code, name))
	writeLines(&b, lines)
	if n := strings.TrimSpace(args.Note); n != "" {
		fmt.Fprintf(&b, "Ghi chú: %s\n", n)
	}
	b.WriteString(confirmFooter)
	action.Summary = b.String()
	return body, nil
}

const confirmFooter = "\nTrả lời 1 để xác nhận, 2 để huỷ. Đơn nháp hết hạn sau 10 phút."

func customerText(code, name string) string {
	if name == "" {
		return code
	}
	return code + " - " + name
}

func writeLines(b *strings.Builder, lines []orderLine) {
	var total float64
	for i, line := range lines {
		amount := line.Quantity * line.Price
		total += amount
		fmt.Fprintf(b, "%d. %s %s: %s %s × %s = %s\n", i+1, line.SKU, line.Name, formatQuantity(line.Quantity), line.Unit, orderactions.FormatVND(line.Price), orderactions.FormatVND(amount))
	}
	fmt.Fprintf(b, "Tạm tính: %s (giá bán ERP, chưa gồm chiết khấu)\n", orderactions.FormatVND(total))
}

func formatQuantity(q float64) string {
	if q == float64(int64(q)) {
		return fmt.Sprintf("%d", int64(q))
	}
	return fmt.Sprintf("%g", q)
}

// resolveDraftCustomer decides whose order this is. Customers order for their
// own code(s) only; staff name the customer, checked against their scope.
func resolveDraftCustomer(permissions *engine.GroupPermissionContext, scope, requested string) (string, string, error) {
	requested = leadingCustomerCode(requested)
	tenantID := permissions.TenantID
	if permissions.AgentType != "private" {
		own := ownCustomerCodesLeading(permissions, tenantID)
		switch {
		case len(own) == 0:
			return "", "", refuse("Tài khoản Zalo này chưa được gắn mã khách hàng nên chưa đặt hàng được. Vui lòng liên hệ nhân viên.")
		case requested != "":
			if !codesContain(own, requested) {
				return "", "", refuse("Mã khách hàng %s không thuộc tài khoản của bạn.", requested)
			}
		case len(own) == 1:
			requested = own[0]
		default:
			return "", "", refuse("Bạn đang có nhiều mã khách hàng (%s). Bạn muốn đặt cho mã nào?", strings.Join(own, ", "))
		}
		return requested, cachedCustomerName(tenantID, requested), nil
	}
	if requested == "" {
		return "", "", refuse("Nhân viên đặt hộ cần cho biết mã khách hàng.")
	}
	name, found := cachedCustomer(tenantID, requested)
	if !found {
		return "", "", refuse("Không tìm thấy khách hàng có mã %s.", requested)
	}
	var allowedCodes []string
	if scope == "assigned" {
		var groupIDs []string
		for _, group := range permissions.Groups {
			groupIDs = append(groupIDs, group.GroupID)
		}
		allowedCodes, _ = resolveGroupCustomerCodes(tenantID, groupIDs)
	}
	if !isOrderAuthorized(requested, scope, ownCustomerCodesLeading(permissions, tenantID), allowedCodes) {
		return "", "", refuse("Bạn không có quyền đặt đơn cho khách hàng %s.", requested)
	}
	return requested, name, nil
}

// cachedCustomer looks a customer up by exact code. The cache stores MA either
// bare ("KH125") or labelled ("KH125 - Name").
func cachedCustomer(tenantID, code string) (string, bool) {
	var rows []models.CachedCustomer
	db.DB.Where("tenant_id = ? AND (ma = ? OR ma LIKE ?)", tenantID, code, code+" - %").Limit(5).Find(&rows)
	for _, row := range rows {
		if strings.EqualFold(leadingCustomerCode(row.MA), code) {
			return strings.TrimSpace(row.HO_VA_TEN), true
		}
	}
	return "", false
}

func cachedCustomerName(tenantID, code string) string {
	name, _ := cachedCustomer(tenantID, code)
	return name
}

// resolveDraftLines turns the agent's SKU list into priced order lines from
// the product cache. Prices never come from the model. allowZero lets an
// update set a line to 0 (remove it).
func resolveDraftLines(permissions *engine.GroupPermissionContext, items []orderDraftItem, allowZero bool) ([]orderLine, error) {
	if len(items) == 0 {
		return nil, refuse("Đơn cần ít nhất một sản phẩm.")
	}
	if len(items) > orderDraftMaxLines {
		return nil, refuse("Mỗi đơn tối đa %d dòng sản phẩm.", orderDraftMaxLines)
	}
	groups, brands, allBrands, filtered := productScope(permissions)
	seen := make(map[string]bool, len(items))
	lines := make([]orderLine, 0, len(items))
	for _, item := range items {
		sku := strings.TrimSpace(item.SKU)
		if sku == "" {
			return nil, refuse("Thiếu mã SKU.")
		}
		if item.Quantity < 0 || item.Quantity > orderDraftMaxQuantity || (item.Quantity == 0 && !allowZero) {
			return nil, refuse("Số lượng của %s không hợp lệ.", sku)
		}
		key := strings.ToUpper(sku)
		if seen[key] {
			return nil, refuse("Mã %s bị lặp, vui lòng gộp số lượng.", sku)
		}
		seen[key] = true
		var product models.CachedProduct
		if err := db.DB.Where("tenant_id = ? AND ma = ?", permissions.TenantID, sku).First(&product).Error; err != nil || product.MA == "" {
			return nil, refuse("Không tìm thấy mã sản phẩm %s. Vui lòng kiểm tra lại mã SKU (màu, size).", sku)
		}
		if filtered && len(filterWarehouseProducts(cachedProductsToMaps([]models.CachedProduct{product}), groups, brands, allBrands)) == 0 {
			return nil, refuse("Mã %s không nằm trong danh mục bạn được đặt.", sku)
		}
		if item.Quantity > 0 && product.DON_GIA_BAN <= 0 {
			return nil, refuse("Mã %s chưa có giá bán trên ERP nên chưa đặt qua chatbot được.", sku)
		}
		lines = append(lines, orderLine{
			SKU:      product.MA,
			Name:     strings.TrimSpace(product.TEN),
			Unit:     strings.TrimSpace(product.DVT),
			Quantity: float64(item.Quantity),
			Price:    product.DON_GIA_BAN,
		})
	}
	return lines, nil
}

// productScope returns the catalog boundary of the caller: the product groups
// and brands their "products" grant allows. Staff without a products filter
// are unrestricted.
func productScope(permissions *engine.GroupPermissionContext) ([]string, []string, bool, bool) {
	allowed, _, groups := permissions.IsResourceAllowed("products")
	if !allowed {
		return nil, nil, true, false
	}
	brands, allBrands := permissions.ResolveBrandFilter("products")
	if len(groups) == 0 && allBrands {
		return nil, nil, true, false
	}
	return groups, brands, allBrands, true
}

// loadDraftOrder fetches the live order and checks the caller may change it.
func loadDraftOrder(ctx context.Context, permissions *engine.GroupPermissionContext, scope, rawCode string, deps orderDraftDeps) (map[string]any, string, error) {
	code := strings.ToUpper(extractOrderCode(rawCode))
	if code == "" {
		code = strings.ToUpper(strings.TrimSpace(rawCode))
	}
	if code == "" {
		return nil, "", refuse("Cần mã đơn hàng, ví dụ ĐH000016.")
	}
	row, err := deps.findOrder(ctx, code)
	if err != nil {
		return nil, "", err
	}
	if row == nil {
		return nil, "", refuse("Không tìm thấy đơn hàng %s.", code)
	}
	customer := orderCustomerCode(row)
	var allowedCodes []string
	if scope == "assigned" {
		var groupIDs []string
		for _, group := range permissions.Groups {
			groupIDs = append(groupIDs, group.GroupID)
		}
		allowedCodes, _ = resolveGroupCustomerCodes(permissions.TenantID, groupIDs)
	}
	if !isOrderAuthorized(customer, scope, ownCustomerCodesLeading(permissions, permissions.TenantID), allowedCodes) {
		return nil, "", refuse("Đơn hàng %s không thuộc tài khoản của bạn.", code)
	}
	if id, ok := orderRowID(row); !ok || id <= 0 {
		return nil, "", fmt.Errorf("order %s has no numeric id", code)
	}
	status := cloudifyorders.NormalizeStatus(getMapString(row, "TRANG_THAI"))
	switch {
	case status == orderactions.OrderStatusCancelled:
		return nil, "", refuse("Đơn %s đã huỷ nên không sửa được nữa.", code)
	// Customers may change an order only before it is completed or on the way.
	case permissions.AgentType != "private" && status != orderactions.OrderStatusNotStarted && status != orderactions.OrderStatusInProgress && status != "":
		return nil, "", refuse("Đơn %s đang ở trạng thái %s nên không sửa qua chatbot được. Vui lòng liên hệ nhân viên.", code, cloudifyorders.StatusName(status))
	}
	return row, code, nil
}

func orderRowID(row map[string]any) (int64, bool) {
	switch v := row["id"].(type) {
	case float64:
		return int64(v), v == float64(int64(v))
	case int64:
		return v, true
	case int:
		return int64(v), true
	case json.Number:
		n, err := v.Int64()
		return n, err == nil
	}
	return 0, false
}

// orderBodyFromRow rebuilds a full update body from a saorders/search row:
// many2one fields come back as [id, "label"], the write API takes the code.
func orderBodyFromRow(row map[string]any) (map[string]any, []orderLine) {
	body := map[string]any{
		"TINH_TRANG":       firstNonEmpty(getMapString(row, "TINH_TRANG"), orderactions.OrderStatusInProgress),
		"TRANG_THAI":       cloudifyorders.NormalizeStatus(getMapString(row, "TRANG_THAI")),
		"MA_HOA_DON":       getMapString(row, "MA_HOA_DON", "SO_DON_HANG"),
		"MA_KHACH_HANG":    orderCustomerCode(row),
		"TEN_KHACH_HANG":   getMapString(row, "TEN_KHACH_HANG"),
		"GHI_CHU":          getMapString(row, "GHI_CHU"),
		"GIAM_GIA_HOA_DON": getMapFloat(row, "GIAM_GIA_HOA_DON"),
	}
	var lines []orderLine
	if raw, ok := row["DON_DAT_HANG_CHI_TIET"].([]any); ok {
		for _, item := range raw {
			m, ok := item.(map[string]any)
			if !ok {
				continue
			}
			lines = append(lines, orderLine{
				SKU:      many2oneCode(m["MA_HANG"]),
				Name:     getMapString(m, "TEN_HANG"),
				Unit:     many2oneLabel(m["DVT"]),
				Quantity: getMapFloat(m, "SO_LUONG"),
				Price:    getMapFloat(m, "DON_GIA"),
				Note:     getMapString(m, "GHI_CHU"),
				Discount: getMapFloat(m, "CHIET_KHAU"),
			})
		}
	}
	body["DON_DAT_HANG_CHI_TIET"] = lines
	return body, lines
}

// many2oneCode reads [id, "CODE"] or [id, "CODE - Name"] (or a plain string)
// and returns the code.
func many2oneCode(v any) string { return leadingCustomerCode(many2oneLabel(v)) }

func many2oneLabel(v any) string {
	switch t := v.(type) {
	case []any:
		if len(t) >= 2 {
			if s, ok := t[1].(string); ok {
				return strings.TrimSpace(s)
			}
		}
	case string:
		return strings.TrimSpace(t)
	}
	return ""
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func buildUpdateDraft(ctx context.Context, permissions *engine.GroupPermissionContext, scope string, args orderDraftArgs, action *models.OrderAction, deps orderDraftDeps) (map[string]any, error) {
	if len(args.Items) == 0 && strings.TrimSpace(args.Note) == "" && args.Status == "" {
		return nil, refuse("Bạn muốn sửa gì trong đơn (số lượng, sản phẩm, ghi chú)?")
	}
	if args.Status != "" && permissions.AgentType != "private" {
		return nil, refuse("Chỉ nhân viên mới đổi được trạng thái đơn.")
	}
	switch args.Status {
	case "", orderactions.OrderStatusNotStarted, orderactions.OrderStatusInProgress, orderactions.OrderStatusCompleted, orderactions.OrderStatusDelivering:
	default:
		return nil, refuse("Trạng thái %s không hợp lệ.", args.Status)
	}
	row, code, err := loadDraftOrder(ctx, permissions, scope, args.OrderCode, deps)
	if err != nil {
		return nil, err
	}
	body, current := orderBodyFromRow(row)
	updated := append([]orderLine(nil), current...)
	if len(args.Items) > 0 {
		changes, err := resolveDraftLines(permissions, args.Items, true)
		if err != nil {
			return nil, err
		}
		for _, change := range changes {
			index := -1
			for i, line := range updated {
				if strings.EqualFold(line.SKU, change.SKU) {
					index = i
					break
				}
			}
			switch {
			case index >= 0 && change.Quantity == 0:
				updated = append(updated[:index], updated[index+1:]...)
			case index >= 0:
				updated[index].Quantity = change.Quantity
			case change.Quantity > 0:
				updated = append(updated, change)
			}
		}
		if len(updated) == 0 {
			return nil, refuse("Đơn phải còn ít nhất một sản phẩm. Muốn huỷ đơn thì dùng yêu cầu huỷ đơn.")
		}
	}
	body["DON_DAT_HANG_CHI_TIET"] = updated
	if note := strings.TrimSpace(args.Note); note != "" {
		body["GHI_CHU"] = note
	}
	if args.Status != "" {
		body["TRANG_THAI"] = args.Status
	}
	id, _ := orderRowID(row)
	action.OrderID, action.OrderCode = id, code
	action.CustomerCode, action.CustomerName = orderCustomerCode(row), getMapString(row, "TEN_KHACH_HANG")

	var b strings.Builder
	fmt.Fprintf(&b, "📝 Xác nhận SỬA ĐƠN %s (%s):\n", code, customerText(action.CustomerCode, action.CustomerName))
	if len(args.Items) > 0 {
		b.WriteString("Sau khi sửa:\n")
		writeLines(&b, updated)
	}
	if note := strings.TrimSpace(args.Note); note != "" {
		fmt.Fprintf(&b, "Ghi chú mới: %s\n", note)
	}
	if args.Status != "" {
		fmt.Fprintf(&b, "Trạng thái: %s → %s\n", cloudifyorders.StatusName(getMapString(row, "TRANG_THAI")), cloudifyorders.StatusName(args.Status))
	}
	b.WriteString(confirmFooter)
	action.Summary = b.String()
	return body, nil
}

func buildCancelDraft(ctx context.Context, permissions *engine.GroupPermissionContext, scope string, args orderDraftArgs, action *models.OrderAction, deps orderDraftDeps) (map[string]any, error) {
	row, code, err := loadDraftOrder(ctx, permissions, scope, args.OrderCode, deps)
	if err != nil {
		return nil, err
	}
	body, lines := orderBodyFromRow(row)
	body["TRANG_THAI"] = orderactions.OrderStatusCancelled
	if reason := strings.TrimSpace(args.Reason); reason != "" {
		body["GHI_CHU"] = strings.TrimSpace(getMapString(row, "GHI_CHU") + " | Huỷ qua chatbot: " + reason)
	}
	id, _ := orderRowID(row)
	action.OrderID, action.OrderCode = id, code
	action.CustomerCode, action.CustomerName = orderCustomerCode(row), getMapString(row, "TEN_KHACH_HANG")

	var b strings.Builder
	fmt.Fprintf(&b, "📝 Xác nhận HUỶ ĐƠN %s (%s):\n", code, customerText(action.CustomerCode, action.CustomerName))
	writeLines(&b, lines)
	if reason := strings.TrimSpace(args.Reason); reason != "" {
		fmt.Fprintf(&b, "Lý do: %s\n", reason)
	}
	b.WriteString("Đơn sẽ chuyển sang trạng thái Hủy (không xoá).\n")
	b.WriteString(confirmFooter)
	action.Summary = b.String()
	return body, nil
}
