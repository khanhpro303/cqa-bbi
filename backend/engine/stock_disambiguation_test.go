package engine

import (
	"strings"
	"testing"
)

// TestBuildStockPickPendingButtons_OrderAndPrefix verifies the pending buttons
// keep the SAME order as the numbered list shown to the customer and that each
// carries the #stockpick_web: postback, so the worker numeric intercept can
// resolve "1"/"2" by index.
func TestBuildStockPickPendingButtons_OrderAndPrefix(t *testing.T) {
	// Arrange
	webNames := []string{"LS2 FF901", "LS2 FF901 Carbon"}

	// Act
	buttons := BuildStockPickPendingButtons(webNames)

	// Assert
	if len(buttons) != 2 {
		t.Fatalf("got %d buttons; want 2", len(buttons))
	}
	if buttons[0].Payload != "#stockpick_web:LS2 FF901" {
		t.Errorf("button[0] payload = %q; want #stockpick_web:LS2 FF901", buttons[0].Payload)
	}
	if buttons[1].Payload != "#stockpick_web:LS2 FF901 Carbon" {
		t.Errorf("button[1] payload = %q; want #stockpick_web:LS2 FF901 Carbon", buttons[1].Payload)
	}
	if buttons[0].Title != "LS2 FF901" {
		t.Errorf("button[0] title = %q; want LS2 FF901", buttons[0].Title)
	}
}

func TestBuildStockPickPendingButtons_SkipsEmpty(t *testing.T) {
	buttons := BuildStockPickPendingButtons([]string{"LS2 FF901", "", "Bulldog TORII"})
	if len(buttons) != 2 {
		t.Fatalf("got %d buttons; want 2 (empty skipped)", len(buttons))
	}
	if buttons[1].Payload != StockPickWebPrefix+"Bulldog TORII" {
		t.Errorf("button[1] payload = %q; want %sBulldog TORII", buttons[1].Payload, StockPickWebPrefix)
	}
}

// TestStockPickChain_NoPrefixCollisionLoop is the regression test for the
// 2026-06-01 "FF901 còn tồn bao nhiêu" loop. Reproduces the full deterministic
// pick chain and proves the customer's "1" lands on the EXACT-web stock sum,
// never on a LIKE re-disambiguation. Before the fix, picking "LS2 FF901" went
// back through the Agent → inventory(Branch-2, LIKE) → #choose_flow_type:dongsp
// → a LIKE web-name search that re-collided "LS2 FF901" with the prefix-overlap
// "LS2 FF901 Carbon" and re-emitted the line list.
func TestStockPickChain_NoPrefixCollisionLoop(t *testing.T) {
	// Arrange — the `products` web-groups disambiguation list the Agent shows.
	webNames := []string{"LS2 FF901", "LS2 FF901 Carbon"}
	pending := BuildStockPickPendingButtons(webNames)
	postbacks := make([]string, len(pending))
	for i, b := range pending {
		postbacks[i] = b.Payload
	}

	// Act 1 — customer types "1" (picks the shorter "LS2 FF901" line).
	picked, matched, inRange := ResolveNumericSelection("1", postbacks)
	if !matched || !inRange {
		t.Fatalf("numeric '1' not resolved: matched=%v inRange=%v", matched, inRange)
	}
	if picked != "#stockpick_web:LS2 FF901" {
		t.Fatalf("picked = %q; want #stockpick_web:LS2 FF901", picked)
	}

	// Act 2 — worker strips the prefix and builds the exact-web stock picker.
	webName := strings.TrimPrefix(picked, StockPickWebPrefix)
	_, buttons := BuildExactWebStockPicker(webName)

	// Assert — the "dòng" button sums by EXACT web name (collision-free path),
	// NOT via #choose_flow_type:dongsp (the LIKE path that caused the loop).
	dongPayload := buttons[0].Payload
	if dongPayload != "#show_macha_options_by_web:LS2 FF901" {
		t.Errorf("dòng button = %q; want #show_macha_options_by_web:LS2 FF901", dongPayload)
	}
	if strings.Contains(dongPayload, "#choose_flow_type:dongsp:") {
		t.Errorf("dòng button still routes through the LIKE dongsp path: %q", dongPayload)
	}
	if got := strings.TrimPrefix(dongPayload, "#show_macha_options_by_web:"); got != "LS2 FF901" {
		t.Errorf("exact-web target = %q; want exactly LS2 FF901 (no Carbon overlap)", got)
	}

	// The SKU button asks for color/size on the same exact line.
	if buttons[1].Payload != "#choose_flow_type:skucuthe:LS2 FF901" {
		t.Errorf("SKU button = %q; want #choose_flow_type:skucuthe:LS2 FF901", buttons[1].Payload)
	}
}
