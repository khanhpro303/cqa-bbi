package engine

import (
	"fmt"

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
const StockPickWebPrefix = "#stockpick_web:"

const showMaChaByWebPrefix = "#show_macha_options_by_web:"
const chooseSkuByWebPrefix = "#choose_flow_type:skucuthe:"

// BuildStockPickPendingButtons turns the ordered product-line web names from a
// `products` disambiguation list into pending-option buttons. Order MUST match
// the numbered list the Agent shows the customer, because the worker resolves a
// bare-number reply against this slice by index. Empty web names are skipped so
// a blank entry can never become an unresolvable postback.
func BuildStockPickPendingButtons(webNames []string) []channels.ZaloOAButton {
	buttons := make([]channels.ZaloOAButton, 0, len(webNames))
	for _, w := range webNames {
		if w == "" {
			continue
		}
		buttons = append(buttons, channels.ZaloOAButton{
			Title:   w,
			Payload: StockPickWebPrefix + w,
		})
	}
	return buttons
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
