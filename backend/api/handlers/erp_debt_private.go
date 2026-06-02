package handlers

import (
	"context"
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
