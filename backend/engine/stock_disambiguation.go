package engine

import (
	"fmt"
	"strings"

	"github.com/vietbui/chat-quality-agent/channels"
)

// StockPickWebPrefix is the postback that routes a numeric disambiguation pick
// on the `products` web-groups list straight to the exact-web stock picker in
// the worker, WITHOUT re-entering Langflow. The `products` disambiguation list
// is rendered by the Agent, so a "1"/"2" reply used to round-trip through the
// LLM — which repeatedly dropped `exact_web_name=true` and the
// `[RICH_MESSAGE_SENT]` sentinel, producing a re-disambiguation loop ("LS2
// FF901" LIKE-colliding with "LS2 FF901 Carbon") plus duplicate prose. Storing
// these postbacks as pending_options lets the worker numeric-reply intercept
// resolve the pick deterministically. See workers/tasks.go #stockpick_web.
//
// The postback carries the intent the Agent classified for the ORIGINAL question
// (price vs stock), captured verbatim at list-build time:
//
//	#stockpick_web:<intent>:<web_name>   e.g. #stockpick_web:price:LS2 FF901
//
// The intent is a fixed token immediately after the prefix; the web name is the
// remainder (split once, so a web name may itself contain ':'). This makes the
// Agent the single source of truth for intent — the worker no longer keyword-
// classifies the question. See ParseStockPickWeb for the reader side.
const StockPickWebPrefix = "#stockpick_web:"

// Stock-pick intent tokens baked into the #stockpick_web postback. "stock" is
// the conservative default for ambiguous questions so a wrong guess never shows
// a confident-but-wrong price.
const (
	StockPickIntentPrice = "price"
	StockPickIntentStock = "stock"
)

const showMaChaByWebPrefix = "#show_macha_options_by_web:"
const chooseSkuByWebPrefix = "#choose_flow_type:skucuthe:"

// NormalizeStockPickIntent folds an arbitrary intent string to one of the two
// valid tokens, defaulting to StockPickIntentStock for anything that is not an
// explicit price ask. Callers (the ERP handler reading req.Intent, the postback
// parser) route everything non-price through the stock picker.
func NormalizeStockPickIntent(intent string) string {
	if strings.EqualFold(strings.TrimSpace(intent), StockPickIntentPrice) {
		return StockPickIntentPrice
	}
	return StockPickIntentStock
}

// BuildStockPickPendingButtons turns the ordered product-line web names from a
// `products` disambiguation list into pending-option buttons, baking the
// Agent-classified intent into each postback. Order MUST match the numbered list
// the Agent shows the customer, because the worker resolves a bare-number reply
// against this slice by index. Empty web names are skipped so a blank entry can
// never become an unresolvable postback.
func BuildStockPickPendingButtons(webNames []string, intent string) []channels.ZaloOAButton {
	intentTok := NormalizeStockPickIntent(intent)
	buttons := make([]channels.ZaloOAButton, 0, len(webNames))
	for _, w := range webNames {
		if w == "" {
			continue
		}
		buttons = append(buttons, channels.ZaloOAButton{
			Title:   w,
			Payload: StockPickWebPrefix + intentTok + ":" + w,
		})
	}
	return buttons
}

// ParseStockPickWeb splits a #stockpick_web postback into its intent token and
// web name. It accepts both the current intent-bearing form
// ("#stockpick_web:price:LS2 FF901") and the legacy intent-less form
// ("#stockpick_web:LS2 FF901") still resident in Redis pending_options across a
// deploy — the legacy form defaults to StockPickIntentStock (the historical
// "ambiguous → stock" behaviour). The web name is the remainder after the intent
// token, so a web name containing ':' survives. Returns ("", "") for a payload
// without the prefix.
func ParseStockPickWeb(payload string) (intent, webName string) {
	if !strings.HasPrefix(payload, StockPickWebPrefix) {
		return "", ""
	}
	rest := strings.TrimPrefix(payload, StockPickWebPrefix)
	if head, tail, found := strings.Cut(rest, ":"); found {
		switch head {
		case StockPickIntentPrice, StockPickIntentStock:
			return head, tail
		}
	}
	// Legacy intent-less postback (or a web name that happens to contain ':'
	// with no leading intent token): treat the whole remainder as the web name
	// and default to the conservative stock intent.
	return StockPickIntentStock, rest
}

// BuildExactWebStockPicker builds the dòng-vs-SKU picker for one EXACT web name
// (mirrors the inventory Branch-0 picker in erp.go). The "dòng" button sums
// stock by the EXACT web name (#show_macha_options_by_web), so "LS2 FF901"
// never re-LIKE-collides with the longer "LS2 FF901 Carbon"; the "SKU" button
// asks the customer for color/size. Returns the prompt and the two buttons in
// the order they are presented.
func BuildExactWebStockPicker(webName string) (string, []channels.ZaloOAButton) {
	prompt := fmt.Sprintf("Bạn muốn kiểm tra tồn kho cho '%s' theo dòng sản phẩm hay mã SKU cụ thể?", webName)
	buttons := []channels.ZaloOAButton{
		{Title: "📦 Xem theo dòng sản phẩm", Payload: showMaChaByWebPrefix + webName},
		{Title: "🔍 Xem theo mã SKU cụ thể", Payload: chooseSkuByWebPrefix + webName},
	}
	return prompt, buttons
}
