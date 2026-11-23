package engine

import (
	"strings"
	"testing"
)

func TestTaskPromptsRegistry(t *testing.T) {
	seen := map[string]bool{}
	for _, p := range TaskPrompts() {
		if seen[p.Key] {
			t.Fatalf("duplicate key %s", p.Key)
		}
		seen[p.Key] = true
		if strings.TrimSpace(p.Instructions) == "" || p.Title == "" || p.Group == "" {
			t.Fatalf("incomplete prompt %+v", p)
		}
		if strings.Contains(p.Instructions, "{{") {
			t.Fatalf("%s: placeholders belong in the fixed format, not the editable part", p.Key)
		}
	}
}

func TestBuildTaskPromptFillsFormatOnly(t *testing.T) {
	got := BuildTaskPrompt("", PromptProductCodeMatch, map[string]string{"keyword": `"ff 818"`, "candidates": "FF818 | LS2 FF818"})
	if !strings.HasPrefix(got, "Bạn là trợ lý mapping mã sản phẩm BBI.") {
		t.Fatalf("prompt starts with %q", got[:40])
	}
	for _, want := range []string{`Khách hàng tìm kiếm: "ff 818"`, "FF818 | LS2 FF818", `trả "NONE"`} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "{{") {
		t.Fatalf("unfilled placeholder:\n%s", got)
	}
	// Tenant text is not expanded.
	if out := ComposeTaskPrompt("rule {{keyword}}", "kw: {{keyword}}", map[string]string{"keyword": "x"}); out != "rule {{keyword}}\n\nkw: x" {
		t.Fatalf("compose = %q", out)
	}
}

func TestIntentClassifierPromptKeepsJSONContract(t *testing.T) {
	got := BuildTaskPrompt("", PromptIntentClassifier, nil)
	if !strings.Contains(got, `"label": "IN_SCOPE" | "HANDOVER" | "CASUAL" | "OUT_OF_SCOPE"`) || !strings.Contains(got, `2. "HANDOVER"`) {
		t.Fatalf("classifier prompt changed:\n%s", got)
	}
}

// The worker hands a small-talk turn back to the main bot when the model
// answers the marker, so the marker must stay in the fixed, read-only part.
func TestCasualReplyPromptKeepsHandoffMarker(t *testing.T) {
	p, ok := LookupTaskPrompt(PromptCasualReply)
	if !ok {
		t.Fatal("small-talk prompt is not registered")
	}
	if !strings.Contains(p.Format, CasualReplyHandoff) || strings.Contains(p.Instructions, CasualReplyHandoff) {
		t.Fatalf("handoff marker must live in the fixed format only:\n%s\n---\n%s", p.Instructions, p.Format)
	}
	for _, want := range []string{"<nguoi_nhan>", "<luot_cuoi>", "<im_lang>", "<tin_gan_day>", "Tin mới:"} {
		if !strings.Contains(p.Format, want) {
			t.Fatalf("format does not describe %s:\n%s", want, p.Format)
		}
	}
	if !strings.Contains(p.Instructions, "<luot_cuoi>") {
		t.Fatal("default instructions should say what to do on the last small-talk reply")
	}
}
