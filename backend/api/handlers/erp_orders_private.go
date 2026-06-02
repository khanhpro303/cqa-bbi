package handlers

import (
	"context"
	"log"
	"sort"
	"strings"

	"github.com/vietbui/chat-quality-agent/config"
	"github.com/vietbui/chat-quality-agent/engine"
)

// This file isolates the PRIVATE (employee) bot orders-by-customer flow: the
// staff member names a customer (code/name), the backend resolves it against
// the local customer cache, intersects the result with the staff member's
// scope, then asks for a date window and returns the order summary. Every
// function here is only ever reached from the orders branch when
// permCtx.AgentType == "private", so the public customer bot (scope "own") is
// untouched. It mirrors the debt-by-customer flow in erp_debt_private.go and
// reuses its cache-resolve core (resolveCachedCustomerCodesFromTokens) and the
// shared accent folder (foldDebtSearch). See docs/admin/order-query-flow.md.

// ordersCustomerPromptText asks the staff member which customer to look up when
// an orders query names no concrete customer yet ("đơn hàng" / "đơn hàng của
// khách hàng"). Delivered directly via the Zalo adapter so the worker stores
// the awaiting-followup marker on the [RICH_MESSAGE_SENT] reply and the next
// turn ("S001") is not swallowed as CASUAL.
const ordersCustomerPromptText = "Anh/chị muốn xem đơn hàng của khách nào? Cho mình mã hoặc tên khách hàng nhé."

// ordersTimeRangePromptText is the date-window question reused by both the
// public ambiguous branch and the private after-customer step.
const ordersTimeRangePromptText = "Anh/chị muốn xem các đơn hàng trong khoảng thời gian nào? Vui lòng nhắn: \"3 ngày gần đây\", \"5 ngày gần đây\" hoặc \"7 ngày gần đây\"."

// ordersCustomerOutOfScopeText is the single neutral message used when the
// named customer resolves to nothing the staff member may see — whether the
// customer does not exist or is outside their assigned groups. Kept neutral so
// it never reveals whether a customer exists outside the staff member's scope.
const ordersCustomerOutOfScopeText = "Không tìm thấy đơn hàng của khách này trong phạm vi của bạn."

// ordersCustomerPickByCodeText replaces the old enumerated "which customer?"
// list. When a name/keyword matches several customers we never dump the full
// list (it was long and noisy); instead we ask the staff member for the exact
// customer code. Their next reply resolves directly against the cache + scope
// in the Pick stage, so any in-scope code advances the flow.
const ordersCustomerPickByCodeText = "Có nhiều khách khớp. Anh/chị cho mình mã khách cụ thể nhé (ví dụ: S001), hoặc nhắn \"tất cả\"."

// ordersCustomerStopwords are accent-folded words stripped before tokenizing an
// orders-by-customer query, so "đơn hàng của khách Huy thế nào rồi" reduces to
// ["Huy"]. Superset of the debt stopwords plus order-intent words.
var ordersCustomerStopwords = map[string]bool{
	"don": true, "hang": true, "donhang": true, "xem": true, "tra": true,
	"cuu": true, "check": true, "cua": true, "khach": true, "kh": true,
	"cho": true, "minh": true, "toi": true, "anh": true, "chi": true,
	"the": true, "nao": true, "roi": true, "sao": true, "ve": true,
	"cua khach": true, "tinh": true, "trang": true, "thai": true,
	"don hang": true, "la": true, "bao": true, "nhieu": true,
	// Conversational tails that must not be mistaken for a customer name.
	// "ra sao" leaked "ra" through, so "đơn hàng của khách hàng ra sao"
	// matched LIKE %ra% across many customers and dumped a long list.
	"ra": true, "nhu": true, "vay": true, "dau": true, "nhi": true,
	"chua": true, "gi": true, "a": true, "ad": true, "shop": true,
}

// allCustomersReplyWords are the accent-folded phrases the staff member can use
// to mean "no single customer — show everything in my scope" at the customer
// step, reverting to the existing scope-wide behaviour.
var allCustomersReplyWords = map[string]bool{
	"tat ca": true, "tatca": true, "toan bo": true, "toanbo": true,
	"het": true, "all": true, "moi khach": true, "tat ca khach": true,
	"tat ca cac khach": true,
}

// tokenizeOrdersCustomerQuery splits an orders-by-customer query into the
// concrete tokens (customer codes / name fragments) to match, dropping the
// order/customer stopwords. Original casing is preserved so the LIKE keeps any
// diacritics; only the stopword test is accent-folded. Pure (no DB) so it is
// unit-testable.
func tokenizeOrdersCustomerQuery(search string) []string {
	fields := strings.Fields(strings.TrimSpace(search))
	out := make([]string, 0, len(fields))
	for _, raw := range fields {
		t := strings.Trim(raw, ".,;:?!()")
		if t == "" {
			continue
		}
		if ordersCustomerStopwords[foldDebtSearch(t)] {
			continue
		}
		out = append(out, t)
	}
	return out
}

// isAllCustomersReply reports whether the staff member's reply means "show
// every customer in my scope" rather than a specific customer.
func isAllCustomersReply(search string) bool {
	f := foldDebtSearch(search)
	if f == "" {
		return false
	}
	if allCustomersReplyWords[f] {
		return true
	}
	// Also accept the phrase appearing inside a longer reply ("xem tất cả").
	for phrase := range allCustomersReplyWords {
		if strings.Contains(phrase, " ") && strings.Contains(f, phrase) {
			return true
		}
	}
	return false
}

// isPrivateOrdersCustomerQuery reports whether a private-bot orders query is the
// generic "show orders" intent with no concrete customer, order code, date
// window, or all-customers reply yet — the case where the backend should ask
// which customer to look up instead of guessing.
func isPrivateOrdersCustomerQuery(search string) bool {
	if extractOrderCode(search) != "" {
		return false // a specific order code is handled by the single-order branch
	}
	if parseDaysFromSearch(search) > 0 {
		return false // a date window is a different step
	}
	if isAllCustomersReply(search) {
		return false // scope-wide reply, not a customer prompt
	}
	return len(tokenizeOrdersCustomerQuery(search)) == 0
}

// resolveOrdersCustomerCodes resolves a private-bot orders query ("S001",
// "Huy", "S001 Huy") to bare customer codes using the local MySQL customer
// cache, reusing the debt flow's AND-narrow / OR-widen core with the
// orders-specific tokenizer. Returns nil on a full cache miss.
func resolveOrdersCustomerCodes(tenantID, search string) []string {
	return resolveCachedCustomerCodesFromTokens(tenantID, tokenizeOrdersCustomerQuery(search))
}

// scopeApprovedOrdersCodes intersects cache-resolved customer codes with the
// staff member's scope so a named-customer lookup can never widen access:
//   - all      → every resolved code (staff still sees only that customer)
//   - assigned → resolved ∩ allowedCodes (drops customers outside their groups)
//   - own      → resolved ∩ {ownCode}
//   - other    → none (deny)
//
// Returned codes are bare (label stripped) and de-duplicated. An empty result
// means the staff member may not see any of the named customers.
func scopeApprovedOrdersCodes(resolved []string, scopeType, ownCode string, allowedCodes []string) []string {
	if len(resolved) == 0 {
		return nil
	}
	switch scopeType {
	case "all":
		return dedupeBareCodes(resolved)
	case "assigned":
		return intersectBareCodes(resolved, allowedCodes)
	case "own":
		return intersectBareCodes(resolved, []string{ownCode})
	default:
		return nil
	}
}

// dedupeBareCodes strips any "CODE - Name" labels and removes duplicates,
// preserving a deterministic (sorted) order.
func dedupeBareCodes(codes []string) []string {
	set := make(map[string]bool, len(codes))
	for _, c := range codes {
		if bare := leadingCustomerCode(c); strings.TrimSpace(bare) != "" {
			set[bare] = true
		}
	}
	out := make([]string, 0, len(set))
	for c := range set {
		out = append(out, c)
	}
	sort.Strings(out)
	return out
}

// intersectBareCodes returns the bare codes present in BOTH resolved and
// allowed (case-insensitive, labels stripped on both sides), sorted and
// de-duplicated.
func intersectBareCodes(resolved, allowed []string) []string {
	allowedSet := make(map[string]bool, len(allowed))
	for _, a := range allowed {
		if bare := strings.ToLower(leadingCustomerCode(a)); bare != "" {
			allowedSet[bare] = true
		}
	}
	set := make(map[string]bool)
	for _, r := range resolved {
		bare := leadingCustomerCode(r)
		if bare != "" && allowedSet[strings.ToLower(bare)] {
			set[bare] = true
		}
	}
	out := make([]string, 0, len(set))
	for c := range set {
		out = append(out, c)
	}
	sort.Strings(out)
	return out
}

// codesContain reports whether bare code target is present in codes
// (case-insensitive, labels stripped).
func codesContain(codes []string, target string) bool {
	t := strings.ToLower(leadingCustomerCode(target))
	if t == "" {
		return false
	}
	for _, c := range codes {
		if strings.ToLower(leadingCustomerCode(c)) == t {
			return true
		}
	}
	return false
}

// orderCustomerSessionTimeout returns the configured chatbot session timeout in
// minutes (default 30), matching the TTL used for pending options / followup.
func orderCustomerSessionTimeout() int {
	if cfg, err := config.Load(); err == nil && cfg.ChatbotSessionTimeout > 0 {
		return cfg.ChatbotSessionTimeout
	}
	return 30
}

// sendPrivateOrdersPrompt delivers a plain-text prompt to the staff member via
// the active Zalo OA adapter, preferring the matched CRM group chat over the
// direct user thread (mirroring the debt/period prompt delivery).
func sendPrivateOrdersPrompt(ctx context.Context, tenantID string, permCtx *engine.GroupPermissionContext, text string) error {
	adapter, _, err := loadActiveZaloOAAdapter(tenantID)
	if err != nil {
		return err
	}
	if groupID := resolveZaloGroupID(tenantID, permCtx); groupID != "" {
		return adapter.SendGroupMessage(ctx, groupID, text)
	}
	return adapter.SendMessage(ctx, permCtx.ZaloUserID, text)
}

// storeOrdersCustomerState persists the orders-by-customer flow state under the
// worker's session key so the next free-text turn resumes it. No-op when no
// active OA channel exists.
func storeOrdersCustomerState(ctx context.Context, tenantID string, permCtx *engine.GroupPermissionContext, state engine.OrderCustomerState) {
	_, activeChannel, err := loadActiveZaloOAAdapter(tenantID)
	if err != nil || activeChannel == nil {
		log.Printf("[orders_query] cannot store order-customer state (no active OA channel): %v", err)
		return
	}
	groupID := resolveZaloGroupID(tenantID, permCtx)
	sessionKey := engine.BuildSessionKey(activeChannel.ID, permCtx.ZaloUserID, groupID)
	engine.StoreOrderCustomerState(ctx, sessionKey, state, orderCustomerSessionTimeout())
}

// takeOrdersCustomerState returns and clears any stored orders-by-customer flow
// state for this staff member's session.
func takeOrdersCustomerState(ctx context.Context, tenantID string, permCtx *engine.GroupPermissionContext) (engine.OrderCustomerState, bool) {
	_, activeChannel, err := loadActiveZaloOAAdapter(tenantID)
	if err != nil || activeChannel == nil {
		return engine.OrderCustomerState{}, false
	}
	groupID := resolveZaloGroupID(tenantID, permCtx)
	sessionKey := engine.BuildSessionKey(activeChannel.ID, permCtx.ZaloUserID, groupID)
	return engine.TakeOrderCustomerState(ctx, sessionKey)
}
