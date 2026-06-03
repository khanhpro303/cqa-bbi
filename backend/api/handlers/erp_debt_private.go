package handlers

import (
	"context"
	"log"
	"sort"
	"strings"
	"unicode"

	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/db/models"
	"github.com/vietbui/chat-quality-agent/engine"
	"golang.org/x/text/unicode/norm"
)

// This file isolates the PRIVATE (employee) bot debt-by-customer flow. Every
// function here is only ever reached from the debt branch when
// permCtx.AgentType == "private", so the public customer bot (scope "own") and
// every other resource are untouched. See docs/admin/debt-query-flow.md.

// debtCustomerPromptText is the plain-text question the backend sends when a
// private-bot debt query references a customer but no code/name has been given
// yet. Delivered directly via the Zalo adapter (like the period prompt) so the
// worker stores the awaiting-followup marker on the [RICH_MESSAGE_SENT] reply
// and the staff member's next turn ("S001") is not swallowed as CASUAL.
const debtCustomerPromptText = "Anh/chị cho mình mã hoặc tên khách hàng cần tra cứu nhé."

// debtPeriodPromptText is the period question reused by both the generic-debt
// branch (bare "công nợ") and the private after-customer step. Kept identical
// to the literal the generic branch sends so staff see one consistent question.
const debtPeriodPromptText = "Anh/chị muốn xem đối chiếu công nợ trong khoảng thời gian nào? Vui lòng nhắn: \"tháng này\", \"tháng trước\" hoặc \"quý này\"."

// debtCustomerPickByCodeText is the FALLBACK pick prompt, used only when the
// candidate codes cannot be looked up for display. The normal path enumerates
// the matched customers (renderDebtCustomerPickText) so staff can pick a real
// code (e.g. "S001_1") instead of re-typing the ambiguous prefix forever.
// Mirrors ordersCustomerPickByCodeText, including the "tất cả" escape that the
// Pick stage honours (isAllCustomersReply) to guarantee the loop can terminate.
const debtCustomerPickByCodeText = "Có nhiều khách khớp. Anh/chị cho mình mã khách cụ thể nhé (ví dụ: S001), hoặc nhắn \"tất cả\"."

// customerCacheResolveLimit caps how many customer codes a single private-bot
// debt lookup may resolve from the local cache, keeping DS_KHACH_HANG bounded.
const customerCacheResolveLimit = 20

// customerQueryStopwords are accent-folded words stripped before tokenizing a
// debt-by-customer query, so "công nợ của khách hàng EGO" reduces to ["EGO"].
var customerQueryStopwords = map[string]bool{
	"cong": true, "no": true, "cua": true, "khach": true, "hang": true,
	"tra": true, "cuu": true, "xem": true, "check": true, "doi": true,
	"chieu": true, "la": true, "bao": true, "nhieu": true, "cong no": true,
	"cong-no": true, "kh": true,
}

// foldDebtSearch lowercases a string and strips Vietnamese diacritics so phrase
// matching works on both "công nợ của khách hàng" and "cong no cua khach hang".
func foldDebtSearch(s string) string {
	var b strings.Builder
	for _, r := range norm.NFD.String(strings.ToLower(strings.TrimSpace(s))) {
		switch {
		case unicode.Is(unicode.Mn, r): // combining diacritic mark from NFD
			continue
		case r == 'đ': // đ does not decompose under NFD
			b.WriteRune('d')
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// isPrivateDebtCustomerQuery reports whether a private-bot debt query references
// a customer generically ("công nợ của khách hàng", "công nợ khách", "tra công
// nợ khách hàng"...) without yet naming a specific code/name or period. When
// true the backend asks for the code/name instead of guessing. Period queries
// ("tháng này"/"quý này") and the bare "công nợ" (handled by isGenericDebtSearch)
// are deliberately excluded.
func isPrivateDebtCustomerQuery(search string) bool {
	f := foldDebtSearch(search)
	if f == "" {
		return false
	}
	if strings.Contains(f, "thang") || strings.Contains(f, "quy") {
		return false // a period query takes precedence over the customer prompt
	}
	if !strings.Contains(f, "khach") {
		return false // must reference a customer generically
	}
	// Only prompt when nothing concrete (code/name) remains after stopwords.
	return len(tokenizeCustomerQuery(search)) == 0
}

// tokenizeCustomerQuery splits a debt-by-customer query into the concrete tokens
// (customer codes / name fragments) to match, dropping the debt/customer
// stopwords. Original casing is preserved so the LIKE keeps any diacritics; only
// the stopword test is accent-folded. Pure (no DB) so it is unit-testable.
func tokenizeCustomerQuery(search string) []string {
	fields := strings.Fields(strings.TrimSpace(search))
	out := make([]string, 0, len(fields))
	for _, raw := range fields {
		t := strings.Trim(raw, ".,;:?!()")
		if t == "" {
			continue
		}
		if customerQueryStopwords[foldDebtSearch(t)] {
			continue
		}
		out = append(out, t)
	}
	return out
}

// resolveCustomerCodesFromCache resolves a private-bot debt query ("S001",
// "Huy", "S001 Huy") to customer codes (MA) using the local MySQL customer
// cache (cached_customers, tenant-scoped). Each token matches MA or HO_VA_TEN
// with a LIKE; multi-token results are intersected (AND, narrowing) and fall
// back to the union (OR) when the intersection is empty. Returns nil on a full
// cache miss so the caller can fall back to the live ERP SearchPartners lookup.
func resolveCustomerCodesFromCache(tenantID, search string) []string {
	return resolveCachedCustomerCodesFromTokens(tenantID, tokenizeCustomerQuery(search))
}

// resolveCachedCustomerCodesFromTokens is the tokenizer-agnostic core of the
// cache resolve: it takes already-tokenized concrete fragments (codes/name
// parts) and matches them against cached_customers with the same AND-narrow /
// OR-widen strategy. Debt tokenizes with its own stopwords; the private orders
// flow supplies order-specific tokens. Returns nil on a full cache miss.
func resolveCachedCustomerCodesFromTokens(tenantID string, tokens []string) []string {
	if len(tokens) == 0 {
		return nil
	}

	// AND: intersect per-token match sets so "S001 Huy" narrows to one customer.
	var intersection map[string]bool
	for _, tok := range tokens {
		set := make(map[string]bool)
		for _, code := range queryCachedCustomerCodes(tenantID, tok) {
			set[code] = true
		}
		if intersection == nil {
			intersection = set
			continue
		}
		for code := range intersection {
			if !set[code] {
				delete(intersection, code)
			}
		}
		if len(intersection) == 0 {
			break
		}
	}
	if len(intersection) > 0 {
		return sortedCustomerCodes(intersection)
	}

	// OR fallback: union of all per-token matches.
	union := make(map[string]bool)
	for _, tok := range tokens {
		for _, code := range queryCachedCustomerCodes(tenantID, tok) {
			union[code] = true
		}
	}
	return sortedCustomerCodes(union)
}

// queryCachedCustomerCodes returns the customer codes (MA) whose code or name
// contains the token, scoped to the tenant. GORM parameterizes the LIKE so the
// token is never interpolated into SQL.
func queryCachedCustomerCodes(tenantID, token string) []string {
	token = strings.TrimSpace(token)
	if token == "" {
		return nil
	}
	like := "%" + token + "%"
	var codes []string
	db.DB.Model(&models.CachedCustomer{}).
		Where("tenant_id = ? AND (ma LIKE ? OR ho_va_ten LIKE ?)", tenantID, like, like).
		Order("ma asc").
		Limit(customerCacheResolveLimit).
		Pluck("ma", &codes)
	return codes
}

// sortedCustomerCodes flattens a code set into a deterministic, capped slice.
func sortedCustomerCodes(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for code := range set {
		if strings.TrimSpace(code) != "" {
			out = append(out, code)
		}
	}
	sort.Strings(out)
	if len(out) > customerCacheResolveLimit {
		out = out[:customerCacheResolveLimit]
	}
	return out
}

// sendDebtCustomerPrompt delivers the "mã hoặc tên khách hàng" question to the
// staff member via the active Zalo OA adapter, preferring a matched CRM group
// chat over the direct user thread — mirroring the period-prompt delivery in the
// debt branch. Only used by the private-bot debt path.
func sendDebtCustomerPrompt(ctx context.Context, tenantID string, permCtx *engine.GroupPermissionContext) error {
	adapter, _, err := loadActiveZaloOAAdapter(tenantID)
	if err != nil {
		return err
	}

	var matchedGroup models.CRMGroup
	hasGroup := false
	for _, gp := range permCtx.Groups {
		if gp.GroupID == "private_bot" {
			continue
		}
		if errGrp := db.DB.Where("id = ? AND tenant_id = ?", gp.GroupID, tenantID).First(&matchedGroup).Error; errGrp == nil && matchedGroup.ZaloGroupID != "" {
			hasGroup = true
			break
		}
	}

	if hasGroup {
		return adapter.SendGroupMessage(ctx, matchedGroup.ZaloGroupID, debtCustomerPromptText)
	}
	return adapter.SendMessage(ctx, permCtx.ZaloUserID, debtCustomerPromptText)
}

// sendPrivateDebtPrompt delivers an arbitrary plain-text prompt (pick / period)
// to the staff member via the active Zalo OA adapter, preferring the matched
// CRM group chat over the direct user thread. Mirrors sendPrivateOrdersPrompt so
// the debt pick/period steps use the same delivery path as the orders flow.
func sendPrivateDebtPrompt(ctx context.Context, tenantID string, permCtx *engine.GroupPermissionContext, text string) error {
	adapter, _, err := loadActiveZaloOAAdapter(tenantID)
	if err != nil {
		return err
	}
	if groupID := resolveZaloGroupID(tenantID, permCtx); groupID != "" {
		return adapter.SendGroupMessage(ctx, groupID, text)
	}
	return adapter.SendMessage(ctx, permCtx.ZaloUserID, text)
}

// debtCustomerCandidate is one matched customer offered in the pick prompt when
// a token resolved to several codes. Code is the bare MA (e.g. "S001_1"); Name
// is HO_VA_TEN (may be empty).
type debtCustomerCandidate struct {
	Code string
	Name string
}

// renderDebtCustomerPickText builds the multi-match pick prompt, listing each
// candidate's code + name so the staff member can choose a concrete code
// (e.g. "S001_1") or reply "tất cả". Falls back to the static prompt when no
// candidates are available (cache lookup miss), so the caller never sends an
// empty list. Pure (no DB) → unit-testable.
func renderDebtCustomerPickText(candidates []debtCustomerCandidate) string {
	if len(candidates) == 0 {
		return debtCustomerPickByCodeText
	}
	var b strings.Builder
	b.WriteString("Có nhiều khách khớp. Anh/chị chọn giúp mình mã khách cụ thể (hoặc nhắn \"tất cả\"):")
	for _, c := range candidates {
		code := strings.TrimSpace(c.Code)
		if code == "" {
			continue
		}
		if name := strings.TrimSpace(c.Name); name != "" {
			b.WriteString("\n• " + code + " — " + name)
		} else {
			b.WriteString("\n• " + code)
		}
	}
	return b.String()
}

// debtCustomerCandidates loads the display name for each resolved code from the
// tenant-scoped customer cache so the pick prompt can enumerate concrete codes.
// Codes are bare MA values (already scope-approved). Returns nil on a full miss
// so renderDebtCustomerPickText falls back to the static prompt.
func debtCustomerCandidates(tenantID string, codes []string) []debtCustomerCandidate {
	if len(codes) == 0 {
		return nil
	}
	nameByCode := make(map[string]string, len(codes))
	var rows []models.CachedCustomer
	db.DB.Model(&models.CachedCustomer{}).
		Where("tenant_id = ? AND ma IN ?", tenantID, codes).
		Limit(customerCacheResolveLimit).
		Find(&rows)
	for _, r := range rows {
		if _, seen := nameByCode[r.MA]; !seen {
			nameByCode[r.MA] = r.HO_VA_TEN
		}
	}
	out := make([]debtCustomerCandidate, 0, len(codes))
	for _, code := range codes {
		out = append(out, debtCustomerCandidate{Code: code, Name: nameByCode[code]})
	}
	return out
}

// sendDebtCustomerPickPrompt delivers the enumerated multi-match pick prompt to
// the staff member, looking up candidate names from the cache. Used by both the
// fresh-turn and StagePick multi-match branches so a token that maps to several
// codes (e.g. "S001" → S001_1, S001_2) shows the real codes instead of looping.
func sendDebtCustomerPickPrompt(ctx context.Context, tenantID string, permCtx *engine.GroupPermissionContext, codes []string) error {
	text := renderDebtCustomerPickText(debtCustomerCandidates(tenantID, codes))
	return sendPrivateDebtPrompt(ctx, tenantID, permCtx, text)
}

// resolveDebtCustomerCodes resolves a private-bot debt query ("S001", "Huy",
// "S001 Huy") to bare customer codes using the local MySQL customer cache,
// reusing the shared cache-resolve core with the debt-specific tokenizer.
// Returns nil on a full cache miss.
func resolveDebtCustomerCodes(tenantID, search string) []string {
	return resolveCachedCustomerCodesFromTokens(tenantID, tokenizeCustomerQuery(search))
}

// scopeApprovedDebtCodes intersects cache-resolved customer codes with the staff
// member's scope so a named-customer debt lookup can never widen access. Mirrors
// scopeApprovedOrdersCodes:
//   - all      → every resolved code
//   - assigned → resolved ∩ allowedCodes
//   - own      → resolved ∩ {ownCode}
//   - other    → none (deny)
//
// Returned codes are bare (label stripped) and de-duplicated. An empty result
// means the staff member may not see any of the named customers.
func scopeApprovedDebtCodes(resolved []string, scopeType, ownCode string, allowedCodes []string) []string {
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

// storeDebtCustomerState persists the debt-by-customer flow state under the
// worker's session key so the next free-text turn resumes it. No-op when no
// active OA channel exists.
func storeDebtCustomerState(ctx context.Context, tenantID string, permCtx *engine.GroupPermissionContext, state engine.DebtCustomerState) {
	_, activeChannel, err := loadActiveZaloOAAdapter(tenantID)
	if err != nil || activeChannel == nil {
		log.Printf("[debt_query] cannot store debt-customer state (no active OA channel): %v", err)
		return
	}
	groupID := resolveZaloGroupID(tenantID, permCtx)
	sessionKey := engine.BuildSessionKey(activeChannel.ID, permCtx.ZaloUserID, groupID)
	engine.StoreDebtCustomerState(ctx, sessionKey, state, orderCustomerSessionTimeout())
}

// takeDebtCustomerState returns and clears any stored debt-by-customer flow
// state for this staff member's session.
func takeDebtCustomerState(ctx context.Context, tenantID string, permCtx *engine.GroupPermissionContext) (engine.DebtCustomerState, bool) {
	_, activeChannel, err := loadActiveZaloOAAdapter(tenantID)
	if err != nil || activeChannel == nil {
		return engine.DebtCustomerState{}, false
	}
	groupID := resolveZaloGroupID(tenantID, permCtx)
	sessionKey := engine.BuildSessionKey(activeChannel.ID, permCtx.ZaloUserID, groupID)
	return engine.TakeDebtCustomerState(ctx, sessionKey)
}
