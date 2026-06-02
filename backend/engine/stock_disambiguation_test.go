package engine

import (
	"strings"
	"testing"
)

// TestBuildStockPickPendingButtons_OrderAndPrefix verifies the pending buttons
// keep the SAME order as the numbered list shown to the customer and that each
// carries the #stockpick_web:<intent>: postback, so the worker numeric intercept
// can resolve "1"/"2" by index and replay the captured intent verbatim.
func TestBuildStockPickPendingButtons_OrderAndPrefix(t *testing.T) {
	// Arrange
	webNames := []string{"LS2 FF901", "LS2 FF901 Carbon"}

	// Act — a PRICE question bakes the price intent into every postback.
	buttons := BuildStockPickPendingButtons(webNames, StockPickIntentPrice)

	// Assert
	if len(buttons) != 2 {
		t.Fatalf("got %d buttons; want 2", len(buttons))
	}
	if buttons[0].Payload != "#stockpick_web:price:LS2 FF901" {
		t.Errorf("button[0] payload = %q; want #stockpick_web:price:LS2 FF901", buttons[0].Payload)
	}
	if buttons[1].Payload != "#stockpick_web:price:LS2 FF901 Carbon" {
		t.Errorf("button[1] payload = %q; want #stockpick_web:price:LS2 FF901 Carbon", buttons[1].Payload)
	}
	if buttons[0].Title != "LS2 FF901" {
		t.Errorf("button[0] title = %q; want LS2 FF901", buttons[0].Title)
	}
}

// TestBuildStockPickPendingButtons_IntentDefaultsStock verifies that anything
// that is not an explicit price ask folds to the conservative stock token, so a
// wrong/empty intent never produces a confident price answer.
func TestBuildStockPickPendingButtons_IntentDefaultsStock(t *testing.T) {
	cases := []string{"", "STOCK", "tồn", "garbage"}
	for _, intent := range cases {
		buttons := BuildStockPickPendingButtons([]string{"LS2 FF901"}, intent)
		if buttons[0].Payload != "#stockpick_web:stock:LS2 FF901" {
			t.Errorf("intent %q → payload %q; want #stockpick_web:stock:LS2 FF901", intent, buttons[0].Payload)
		}
	}
}

func TestBuildStockPickPendingButtons_SkipsEmpty(t *testing.T) {
	buttons := BuildStockPickPendingButtons([]string{"LS2 FF901", "", "Bulldog TORII"}, StockPickIntentStock)
	if len(buttons) != 2 {
		t.Fatalf("got %d buttons; want 2 (empty skipped)", len(buttons))
	}
	if buttons[1].Payload != "#stockpick_web:stock:Bulldog TORII" {
		t.Errorf("button[1] payload = %q; want #stockpick_web:stock:Bulldog TORII", buttons[1].Payload)
	}
}

// TestParseStockPickWeb covers the reader side: the current intent-bearing form,
// the legacy intent-less form still resident in Redis across a deploy, a web
// name that itself contains ':', and a non-matching payload.
func TestParseStockPickWeb(t *testing.T) {
	cases := []struct {
		name       string
		payload    string
		wantIntent string
		wantWeb    string
	}{
		{"price form", "#stockpick_web:price:LS2 FF901", StockPickIntentPrice, "LS2 FF901"},
		{"stock form", "#stockpick_web:stock:LS2 FF901 Carbon", StockPickIntentStock, "LS2 FF901 Carbon"},
		{"legacy no intent defaults stock", "#stockpick_web:LS2 FF901", StockPickIntentStock, "LS2 FF901"},
		{"web name with colon under price", "#stockpick_web:price:LS2 FF901: Special", StockPickIntentPrice, "LS2 FF901: Special"},
		{"legacy web name with colon defaults stock", "#stockpick_web:LS2 FF901: Special", StockPickIntentStock, "LS2 FF901: Special"},
		{"not a stockpick payload", "#show_macha_options_by_web:LS2 FF901", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotIntent, gotWeb := ParseStockPickWeb(tc.payload)
			if gotIntent != tc.wantIntent || gotWeb != tc.wantWeb {
				t.Errorf("ParseStockPickWeb(%q) = (%q, %q); want (%q, %q)", tc.payload, gotIntent, gotWeb, tc.wantIntent, tc.wantWeb)
			}
		})
	}
}

// TestBuildParseStockPickRoundTrip pins that what the handler bakes in is exactly
// what the worker reads back — the two sides must never drift.
func TestBuildParseStockPickRoundTrip(t *testing.T) {
	for _, intent := range []string{StockPickIntentPrice, StockPickIntentStock} {
		for _, web := range []string{"LS2 FF901", "LS2 FF901 Carbon", "Bulldog TORII"} {
			buttons := BuildStockPickPendingButtons([]string{web}, intent)
			gotIntent, gotWeb := ParseStockPickWeb(buttons[0].Payload)
			if gotIntent != intent || gotWeb != web {
				t.Errorf("round-trip(%q,%q) = (%q,%q)", intent, web, gotIntent, gotWeb)
			}
		}
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
	// Arrange — the `products` web-groups disambiguation list the Agent shows
	// (a STOCK question, so the stock intent is baked in).
	webNames := []string{"LS2 FF901", "LS2 FF901 Carbon"}
	pending := BuildStockPickPendingButtons(webNames, StockPickIntentStock)
	postbacks := make([]string, len(pending))
	for i, b := range pending {
		postbacks[i] = b.Payload
	}

	// Act 1 — customer types "1" (picks the shorter "LS2 FF901" line).
	picked, matched, inRange := ResolveNumericSelection("1", postbacks)
	if !matched || !inRange {
		t.Fatalf("numeric '1' not resolved: matched=%v inRange=%v", matched, inRange)
	}
	if picked != "#stockpick_web:stock:LS2 FF901" {
		t.Fatalf("picked = %q; want #stockpick_web:stock:LS2 FF901", picked)
	}

	// Act 2 — worker parses the intent + web name and builds the exact-web picker.
	intent, webName := ParseStockPickWeb(picked)
	if intent != StockPickIntentStock {
		t.Fatalf("parsed intent = %q; want stock", intent)
	}
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
