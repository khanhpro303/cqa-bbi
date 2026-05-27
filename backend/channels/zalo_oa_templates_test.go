package channels

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestBuildV3ListTemplatePayload(t *testing.T) {
	got, err := BuildV3ListTemplatePayload("USER123", "Pick a product line", []ZaloOAButton{
		{Title: "SP100", Payload: "#show_product_variants:SP100"},
		{Title: "SP200", Payload: "#show_product_variants:SP200"},
	})
	if err != nil {
		t.Fatalf("BuildV3ListTemplatePayload returned error: %v", err)
	}

	var decoded map[string]interface{}
	if err := json.Unmarshal([]byte(got), &decoded); err != nil {
		t.Fatalf("payload is not valid JSON: %v\nraw=%s", err, got)
	}

	recipient, _ := decoded["recipient"].(map[string]interface{})
	if recipient["user_id"] != "USER123" {
		t.Errorf("recipient.user_id = %v; want USER123", recipient["user_id"])
	}

	message, _ := decoded["message"].(map[string]interface{})
	attachment, _ := message["attachment"].(map[string]interface{})
	if attachment["type"] != "template" {
		t.Errorf("attachment.type = %v; want template", attachment["type"])
	}

	payload, _ := attachment["payload"].(map[string]interface{})
	if payload["template_type"] != "list" {
		t.Errorf("payload.template_type = %v; want list", payload["template_type"])
	}

	elements, _ := payload["elements"].([]interface{})
	if len(elements) != 1 {
		t.Fatalf("expected 1 element; got %d", len(elements))
	}
	firstEl, _ := elements[0].(map[string]interface{})
	if firstEl["title"] != "Pick a product line" {
		t.Errorf("first element title = %v; want prompt text", firstEl["title"])
	}

	buttons, _ := payload["buttons"].([]interface{})
	if len(buttons) != 2 {
		t.Fatalf("expected 2 buttons; got %d", len(buttons))
	}
	firstBtn, _ := buttons[0].(map[string]interface{})
	if firstBtn["title"] != "SP100" {
		t.Errorf("button[0].title = %v; want SP100", firstBtn["title"])
	}
	if firstBtn["type"] != "oa.query.hide" {
		t.Errorf("button[0].type = %v; want oa.query.hide", firstBtn["type"])
	}
	if firstBtn["payload"] != "#show_product_variants:SP100" {
		t.Errorf("button[0].payload = %v; want postback string", firstBtn["payload"])
	}
}

func TestBuildV3ListTemplatePayload_NoButtons(t *testing.T) {
	got, err := BuildV3ListTemplatePayload("U1", "hello", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var decoded map[string]interface{}
	if err := json.Unmarshal([]byte(got), &decoded); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	message, _ := decoded["message"].(map[string]interface{})
	attachment, _ := message["attachment"].(map[string]interface{})
	payload, _ := attachment["payload"].(map[string]interface{})

	buttons, ok := payload["buttons"].([]interface{})
	if !ok {
		t.Fatalf("buttons field missing or wrong type: %T", payload["buttons"])
	}
	if len(buttons) != 0 {
		t.Errorf("expected empty buttons array; got %d", len(buttons))
	}
}

func TestBuildButtonOptionsAsText(t *testing.T) {
	tests := []struct {
		name        string
		prompt      string
		buttons     []ZaloOAButton
		wantContain []string
		wantExact   string
	}{
		{
			name:      "empty buttons returns prompt only",
			prompt:    "Choose:",
			buttons:   nil,
			wantExact: "Choose:",
		},
		{
			name:   "buttons render as numbered list under prompt",
			prompt: "Pick one:",
			buttons: []ZaloOAButton{
				{Title: "SP100", Payload: "#x:SP100"},
				{Title: "SP200", Payload: "#x:SP200"},
			},
			wantContain: []string{"Pick one:", "1. SP100", "2. SP200"},
		},
		{
			name:   "payload is not leaked to text output",
			prompt: "Choose:",
			buttons: []ZaloOAButton{
				{Title: "Item A", Payload: "#secret:internal_token"},
			},
			wantContain: []string{"Choose:", "1. Item A"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := BuildButtonOptionsAsText(tc.prompt, tc.buttons)
			if tc.wantExact != "" {
				if got != tc.wantExact {
					t.Errorf("got %q; want %q", got, tc.wantExact)
				}
				return
			}
			for _, want := range tc.wantContain {
				if !strings.Contains(got, want) {
					t.Errorf("output missing %q\nfull output:\n%s", want, got)
				}
			}
			if tc.name == "payload is not leaked to text output" {
				if strings.Contains(got, "internal_token") {
					t.Errorf("output leaks button payload:\n%s", got)
				}
			}
		})
	}
}
