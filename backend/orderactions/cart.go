package orderactions

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/db/models"
	"github.com/vietbui/chat-quality-agent/engine"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Cart statuses.
const (
	CartOpen      = "open"
	CartOrdered   = "ordered"
	CartCancelled = "cancelled"
	CartExpired   = "expired"
)

// CartTTL is how long an untouched cart stays open. Every change extends it.
const CartTTL = 24 * time.Hour

// CartDraftTTL is how long the final confirmation of a cart waits for "1".
const CartDraftTTL = 30 * time.Minute

// Cart operations.
const (
	OpAdd    = "add"
	OpSet    = "set"
	OpRemove = "remove"
)

// Why a cart item still waits for the customer.
const (
	NeedLine    = "line"    // the name matches several product lines (FF901 / FF901 Carbon)
	NeedVariant = "variant" // the line is known, colour or size is missing or unknown
)

// CartLine is one resolved product in the cart. Price and stock come from the
// product cache and Cloudify, never from the model.
type CartLine struct {
	SKU      string   `json:"sku"`
	Name     string   `json:"name"`
	Line     string   `json:"line,omitempty"`
	Color    string   `json:"color,omitempty"`
	Size     string   `json:"size,omitempty"`
	Unit     string   `json:"unit,omitempty"`
	Quantity int      `json:"quantity"`
	Price    float64  `json:"price"`
	Stock    *float64 `json:"stock,omitempty"`
}

// ShortStock reports whether the main warehouse holds less than the quantity.
func (l CartLine) ShortStock() bool {
	return l.Stock != nil && float64(l.Quantity) > *l.Stock
}

// Label is the line as the customer reads it: name, colour, size.
func (l CartLine) Label() string {
	parts := []string{firstText(l.Line, l.Name)}
	for _, attr := range []string{l.Color, l.Size} {
		if attr != "" && !strings.Contains(" "+Normalize(parts[0])+" ", " "+Normalize(attr)+" ") {
			parts = append(parts, attr)
		}
	}
	return strings.Join(parts, " - ")
}

// CartOption is one numbered answer to a pending question.
type CartOption struct {
	Option     int      `json:"option"`
	Line       string   `json:"line,omitempty"`
	SKU        string   `json:"sku,omitempty"`
	Color      string   `json:"color,omitempty"`
	Size       string   `json:"size,omitempty"`
	Price      float64  `json:"price,omitempty"`
	PriceRange string   `json:"price_range,omitempty"`
	Stock      *float64 `json:"stock,omitempty"`
}

// CartPending is a product the customer asked for that still needs their
// choice. It keeps what is already known, so the answer ("cái Carbon",
// "màu đen size L", "số 2") only adds the missing part.
type CartPending struct {
	ID       string       `json:"pending_id"`
	Request  string       `json:"request"`
	Op       string       `json:"op"`
	Quantity int          `json:"quantity"`
	Line     string       `json:"line,omitempty"`
	Color    string       `json:"color,omitempty"`
	Size     string       `json:"size,omitempty"`
	Need     string       `json:"need"`
	Note     string       `json:"note,omitempty"`
	Colors   []string     `json:"colors,omitempty"`
	Sizes    []string     `json:"sizes,omitempty"`
	Options  []CartOption `json:"options"`
}

// CartState is the JSON body of a cart.
type CartState struct {
	Lines       []CartLine    `json:"lines"`
	Pending     []CartPending `json:"pending"`
	NextPending int           `json:"next_pending"`
}

// DecodeCartState reads a stored state; an empty value is an empty cart.
func DecodeCartState(raw string) CartState {
	var state CartState
	if strings.TrimSpace(raw) != "" {
		_ = json.Unmarshal([]byte(raw), &state)
	}
	return state
}

func (s CartState) encode() string {
	if s.Lines == nil {
		s.Lines = []CartLine{}
	}
	if s.Pending == nil {
		s.Pending = []CartPending{}
	}
	data, _ := json.Marshal(s)
	return string(data)
}

// Apply adds, sets or removes a resolved line. It reports what changed in
// words for the agent.
func (s *CartState) Apply(line CartLine, op string, quantity int) string {
	index := -1
	for i, existing := range s.Lines {
		if strings.EqualFold(existing.SKU, line.SKU) {
			index = i
			break
		}
	}
	switch {
	case op == OpRemove || (op == OpSet && quantity <= 0):
		if index < 0 {
			return ""
		}
		removed := s.Lines[index]
		s.Lines = append(s.Lines[:index], s.Lines[index+1:]...)
		return "Đã bỏ " + removed.Label()
	case index >= 0:
		current := s.Lines[index]
		if op == OpAdd {
			current.Quantity += quantity
		} else {
			current.Quantity = quantity
		}
		if line.Stock != nil {
			current.Stock = line.Stock
		}
		if line.Price > 0 {
			current.Price = line.Price
		}
		s.Lines[index] = current
		return fmt.Sprintf("Đã đổi %s thành %d %s", current.Label(), current.Quantity, unitText(current.Unit))
	default:
		line.Quantity = quantity
		s.Lines = append(s.Lines, line)
		return fmt.Sprintf("Đã thêm %s: %d %s", line.Label(), quantity, unitText(line.Unit))
	}
}

// ChangeQuantity sets, adds to or removes the line at index.
func (s *CartState) ChangeQuantity(index int, op string, quantity int) string {
	if index < 0 || index >= len(s.Lines) {
		return ""
	}
	return s.Apply(s.Lines[index], op, quantity)
}

// FindLines returns the indexes of cart lines that match what the customer
// said: an exact SKU, or all words of the text (accents ignored) found in
// the line's name, colour and size, narrowed by colour and size when given.
func (s CartState) FindLines(text, color, size string) []int {
	text = Normalize(text)
	color = Normalize(color)
	size = Normalize(size)
	var found []int
	for i, line := range s.Lines {
		if text != "" && strings.EqualFold(text, Normalize(line.SKU)) {
			return []int{i}
		}
		haystack := Normalize(strings.Join([]string{line.SKU, line.Line, line.Name, line.Color, line.Size}, " "))
		if text != "" && !containsWords(haystack, text) {
			continue
		}
		if color != "" && !strings.Contains(Normalize(line.Color+" "+line.Name), color) {
			continue
		}
		if size != "" && Normalize(line.Size) != size {
			continue
		}
		if text == "" && color == "" && size == "" {
			continue
		}
		found = append(found, i)
	}
	return found
}

func containsWords(haystack, needle string) bool {
	for _, word := range strings.Fields(needle) {
		if !strings.Contains(haystack, word) {
			return false
		}
	}
	return true
}

// FindPending returns the pending question with id.
func (s CartState) FindPending(id string) (int, bool) {
	for i, p := range s.Pending {
		if strings.EqualFold(p.ID, strings.TrimSpace(id)) {
			return i, true
		}
	}
	return -1, false
}

// PutPending stores a question, keeping its id when it replaces an older one.
func (s *CartState) PutPending(p CartPending) string {
	if index, ok := s.FindPending(p.ID); ok && p.ID != "" {
		s.Pending[index] = p
		return p.ID
	}
	s.NextPending++
	p.ID = fmt.Sprintf("p%d", s.NextPending)
	s.Pending = append(s.Pending, p)
	return p.ID
}

// DropPending removes a question.
func (s *CartState) DropPending(id string) {
	if index, ok := s.FindPending(id); ok {
		s.Pending = append(s.Pending[:index], s.Pending[index+1:]...)
	}
}

// Totals returns the number of pieces and the amount at ERP prices.
func (s CartState) Totals() (int, float64) {
	pieces, amount := 0, 0.0
	for _, line := range s.Lines {
		pieces += line.Quantity
		amount += float64(line.Quantity) * line.Price
	}
	return pieces, amount
}

// Stage tells the agent what the cart needs next.
func (s CartState) Stage() string {
	switch {
	case len(s.Pending) > 0:
		return "needs_choice"
	case len(s.Lines) == 0:
		return "empty"
	}
	for _, line := range s.Lines {
		if line.ShortStock() {
			return "stock_short"
		}
	}
	return "ready"
}

// View is the cart as the agent sees it in tool results.
func View(cart *models.OrderCart, state CartState) map[string]any {
	lines := make([]map[string]any, 0, len(state.Lines))
	for i, line := range state.Lines {
		row := map[string]any{
			"n": i + 1, "sku": line.SKU, "name": line.Label(), "quantity": line.Quantity,
			"unit": line.Unit, "price": line.Price, "amount": float64(line.Quantity) * line.Price,
		}
		if line.Stock != nil {
			row["stock"] = *line.Stock
		}
		if line.ShortStock() {
			row["warning"] = fmt.Sprintf("Kho tổng chỉ còn %s", formatStock(*line.Stock))
		}
		lines = append(lines, row)
	}
	pieces, amount := state.Totals()
	view := map[string]any{
		"stage":          state.Stage(),
		"lines":          lines,
		"pending":        nonNilPending(state.Pending),
		"total_quantity": pieces,
		"total_amount":   amount,
		"total_text":     FormatVND(amount),
	}
	if cart != nil {
		if cart.CustomerCode != "" {
			view["customer_code"] = cart.CustomerCode
		}
		if cart.Note != "" {
			view["note"] = cart.Note
		}
		view["updated_at"] = cart.UpdatedAt.In(vietnam).Format("15:04 02/01/2006")
	}
	return view
}

func nonNilPending(p []CartPending) []CartPending {
	if p == nil {
		return []CartPending{}
	}
	return p
}

var vietnam = time.FixedZone("ICT", 7*3600)

// RenderCart writes the cart as plain text for the agent's system prompt.
func RenderCart(cart *models.OrderCart, state CartState) string {
	if cart == nil {
		return "(chưa có giỏ hàng)"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Cập nhật lần cuối: %s. Trạng thái: %s.\n", cart.UpdatedAt.In(vietnam).Format("15:04 02/01/2006"), state.Stage())
	if cart.CustomerCode != "" {
		fmt.Fprintf(&b, "Khách hàng: %s\n", customerText(cart.CustomerCode, cart.CustomerName))
	}
	if len(state.Lines) == 0 {
		b.WriteString("Chưa có sản phẩm nào.\n")
	}
	for i, line := range state.Lines {
		fmt.Fprintf(&b, "%d. %s (SKU %s): %d %s × %s", i+1, line.Label(), line.SKU, line.Quantity, unitText(line.Unit), FormatVND(line.Price))
		if line.ShortStock() {
			fmt.Fprintf(&b, " ⚠ kho tổng chỉ còn %s", formatStock(*line.Stock))
		}
		b.WriteString("\n")
	}
	for _, p := range state.Pending {
		fmt.Fprintf(&b, "Chờ khách chọn [%s] <khach_go>%s</khach_go> × %d (%s):", p.ID, engine.UntrustedText(p.Request), p.Quantity, p.Need)
		for _, o := range p.Options {
			fmt.Fprintf(&b, " %d) %s;", o.Option, optionText(o))
		}
		b.WriteString("\n")
	}
	if cart.Note != "" {
		fmt.Fprintf(&b, "Ghi chú của khách: <khach_go>%s</khach_go>\n", engine.UntrustedText(cart.Note))
	}
	return strings.TrimSpace(b.String())
}

func optionText(o CartOption) string {
	parts := []string{}
	for _, v := range []string{o.Line, o.Color, o.Size} {
		if v != "" {
			parts = append(parts, v)
		}
	}
	if o.SKU != "" {
		parts = append(parts, "SKU "+o.SKU)
	}
	return strings.Join(parts, " ")
}

func formatStock(v float64) string {
	if v == float64(int64(v)) {
		return fmt.Sprintf("%d", int64(v))
	}
	return fmt.Sprintf("%g", v)
}

func unitText(unit string) string {
	if strings.TrimSpace(unit) == "" {
		return "cái"
	}
	return strings.ToLower(strings.TrimSpace(unit))
}

func firstText(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func customerText(code, name string) string {
	if name == "" {
		return code
	}
	return code + " - " + name
}

// Normalize lowercases text and strips Vietnamese accents and punctuation,
// so "Đen bóng" matches "den bong".
func Normalize(text string) string { return normalize(text) }

// OpenCart returns the chat's open cart, if any.
func OpenCart(ctx context.Context, tenantID, workKey string) (*models.OrderCart, CartState, bool) {
	if workKey == "" || db.DB == nil {
		return nil, CartState{}, false
	}
	var cart models.OrderCart
	err := db.DB.WithContext(ctx).
		Where("tenant_id = ? AND open_key = ? AND status = ? AND expires_at > ?", tenantID, workKey, CartOpen, time.Now()).
		First(&cart).Error
	if err != nil {
		return nil, CartState{}, false
	}
	return &cart, DecodeCartState(cart.State), true
}

// ErrNoCart means the chat has no open cart.
var ErrNoCart = errors.New("no open cart")

// UpdateCart runs fn on the chat's open cart under a row lock, creating the
// cart from seed when there is none (create=true). When fn reports a change,
// the cart's waiting confirmation is withdrawn, because it no longer shows
// what the customer will get, and the cart's lifetime is extended.
func UpdateCart(ctx context.Context, seed models.OrderCart, create bool, fn func(cart *models.OrderCart, state *CartState) (bool, error)) (*models.OrderCart, CartState, error) {
	var cart models.OrderCart
	var state CartState
	now := time.Now()
	err := db.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("tenant_id = ? AND open_key = ? AND status = ?", seed.TenantID, seed.WorkKey, CartOpen).
			First(&cart).Error
		switch {
		case err == nil && !cart.ExpiresAt.After(now):
			if err := tx.Model(&models.OrderCart{}).Where("id = ?", cart.ID).
				Updates(map[string]any{"status": CartExpired, "open_key": nil}).Error; err != nil {
				return err
			}
			cart = models.OrderCart{}
		case errors.Is(err, gorm.ErrRecordNotFound):
			cart = models.OrderCart{}
		case err != nil:
			return err
		}
		isNew := cart.ID == ""
		if isNew {
			if !create {
				return ErrNoCart
			}
			// Built in memory and stored only once something is in it, so a
			// request that matched nothing leaves no empty cart behind.
			cart = seed
			cart.ID = uuid.NewString()
			key := seed.WorkKey
			cart.OpenKey = &key
			cart.Status = CartOpen
			cart.CreatedAt, cart.UpdatedAt = now, now
		}
		state = DecodeCartState(cart.State)
		changed, err := fn(&cart, &state)
		if err != nil {
			return err
		}
		if !changed {
			if isNew {
				cart.State = state.encode()
			}
			return nil
		}
		cart.State = state.encode()
		cart.ExpiresAt = now.Add(CartTTL)
		cart.UpdatedAt = now
		if seed.ZaloUserID != "" {
			cart.ZaloUserID = seed.ZaloUserID
		}
		if isNew {
			return tx.Create(&cart).Error
		}
		if err := tx.Model(&models.OrderCart{}).Where("id = ?", cart.ID).Updates(map[string]any{
			"state": cart.State, "expires_at": cart.ExpiresAt, "zalo_user_id": cart.ZaloUserID,
			"customer_code": cart.CustomerCode, "customer_name": cart.CustomerName, "note": cart.Note,
		}).Error; err != nil {
			return err
		}
		return withdrawCartDrafts(tx, cart.ID)
	})
	if err != nil {
		return nil, CartState{}, err
	}
	// A numbered menu from another flow would capture the "1"/"2" the
	// customer types to answer the cart's own question.
	if len(state.Pending) > 0 && db.RedisClient != nil {
		db.RedisClient.Del(ctx, seed.WorkKey+engine.PendingOptionsSuffix)
	}
	return &cart, state, nil
}

func withdrawCartDrafts(tx *gorm.DB, cartID string) error {
	return tx.Model(&models.OrderAction{}).
		Where("cart_id = ? AND status = ?", cartID, StatusPending).
		Update("status", StatusSuperseded).Error
}

// CancelCart closes the chat's open cart without ordering and withdraws its
// waiting confirmation.
func CancelCart(ctx context.Context, tenantID, workKey string) (bool, error) {
	cancelled := false
	err := db.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var cart models.OrderCart
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("tenant_id = ? AND open_key = ? AND status = ?", tenantID, workKey, CartOpen).
			First(&cart).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil
			}
			return err
		}
		if err := tx.Model(&models.OrderCart{}).Where("id = ?", cart.ID).
			Updates(map[string]any{"status": CartCancelled, "open_key": nil}).Error; err != nil {
			return err
		}
		cancelled = true
		return withdrawCartDrafts(tx, cart.ID)
	})
	return cancelled, err
}

// SetCartDraft records the confirmation draft last sent for the cart.
func SetCartDraft(ctx context.Context, cartID, draftID string) {
	db.DB.WithContext(ctx).Model(&models.OrderCart{}).Where("id = ?", cartID).Update("draft_id", draftID)
}

// closeOrderedCart marks the cart as ordered once its draft reached Cloudify.
func closeOrderedCart(ctx context.Context, cartID, code string) {
	if cartID == "" {
		return
	}
	db.DB.WithContext(ctx).Model(&models.OrderCart{}).
		Where("id = ? AND status = ?", cartID, CartOpen).
		Updates(map[string]any{"status": CartOrdered, "open_key": nil, "order_code": code})
}

// payloadTotals reads the line count, pieces and amount of a stored create
// body.
func payloadTotals(payload string) (int, float64, float64) {
	var body struct {
		Lines []struct {
			Quantity float64 `json:"SO_LUONG"`
			Price    float64 `json:"DON_GIA"`
		} `json:"DON_DAT_HANG_CHI_TIET"`
	}
	if json.Unmarshal([]byte(payload), &body) != nil {
		return 0, 0, 0
	}
	pieces, amount := 0.0, 0.0
	for _, line := range body.Lines {
		pieces += line.Quantity
		amount += line.Quantity * line.Price
	}
	return len(body.Lines), pieces, amount
}

// Permission resources of the chatbot order tools. They are off unless an
// admin enables them for a CRM group or the private bot.
const (
	ResourceOrderCreate = "order_create"
	ResourceOrderUpdate = "order_update"
	ResourceOrderCancel = "order_cancel"
)

// WriteScope reports whether a write resource is enabled and with which
// scope. Customers (public bot) are always limited to their own codes. Unlike
// the read resources, a private bot with no explicit configuration gets no
// write access.
func WriteScope(permissions *engine.GroupPermissionContext, resource string) (bool, string) {
	if permissions == nil {
		return false, ""
	}
	if permissions.AgentType == "private" {
		for _, group := range permissions.Groups {
			if group.GroupID != "private_bot" {
				continue
			}
			for _, res := range group.Resources {
				if res.Resource == resource && res.IsEnabled {
					scope := res.ScopeType
					if scope == "" {
						scope = "all"
					}
					return true, scope
				}
			}
		}
		return false, ""
	}
	allowed, _, _ := permissions.IsResourceAllowed(resource)
	return allowed, "own"
}
