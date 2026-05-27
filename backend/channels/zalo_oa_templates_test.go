package channels

import (
	"encoding/json"
	"strings"
	"testing"
)

// decodeListPayload is a small helper that drills into the nested map shape
// produced by BuildV3ListTemplate* so individual tests stay readable.
func decodeListPayload(t *testing.T, body string) (text string, elements []map[string]interface{}) {
	t.Helper()

	var decoded map[string]interface{}
	if err := json.Unmarshal([]byte(body), &decoded); err != nil {
		t.Fatalf("payload is not valid JSON: %v\nraw=%s", err, body)
	}

	message, _ := decoded["message"].(map[string]interface{})
	text, _ = message["text"].(string)

	attachment, _ := message["attachment"].(map[string]interface{})
	if attachment["type"] != "template" {
		t.Errorf("attachment.type = %v; want template", attachment["type"])
	}

	payload, _ := attachment["payload"].(map[string]interface{})
	if payload["template_type"] != "list" {
		t.Errorf("payload.template_type = %v; want list", payload["template_type"])
	}
	if _, present := payload["buttons"]; present {
		t.Errorf("payload.buttons must NOT exist at top level (Zalo -233); buttons belong inside each element")
	}

	rawElements, _ := payload["elements"].([]interface{})
	for _, el := range rawElements {
		m, _ := el.(map[string]interface{})
		elements = append(elements, m)
	}
	return text, elements
}

func TestBuildV3ListTemplatePayload_OneElementPerButton(t *testing.T) {
	got, err := BuildV3ListTemplatePayload("USER123", "Pick a product line", []ZaloOAButton{
		{Title: "SP100", Payload: "#show_product_variants:SP100"},
		{Title: "SP200", Payload: "#show_product_variants:SP200"},
	})
	if err != nil {
		t.Fatalf("BuildV3ListTemplatePayload returned error: %v", err)
	}

	var decoded map[string]interface{}
	if err := json.Unmarshal([]byte(got), &decoded); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	recipient, _ := decoded["recipient"].(map[string]interface{})
	if recipient["user_id"] != "USER123" {
		t.Errorf("recipient.user_id = %v; want USER123", recipient["user_id"])
	}

	text, elements := decodeListPayload(t, got)
	if text != "Pick a product line" {
		t.Errorf("message.text = %q; want prompt", text)
	}
	if len(elements) != 2 {
		t.Fatalf("expected 2 elements (one per button); got %d", len(elements))
	}

	for i, want := range []struct {
		title, payload string
	}{
		{"SP100", "#show_product_variants:SP100"},
		{"SP200", "#show_product_variants:SP200"},
	} {
		el := elements[i]
		if el["title"] != want.title {
			t.Errorf("element[%d].title = %v; want %q", i, el["title"], want.title)
		}
		if strings.TrimSpace(el["subtitle"].(string)) == "" {
			t.Errorf("element[%d].subtitle must be non-empty (Zalo -201)", i)
		}
		btns, _ := el["buttons"].([]interface{})
		if len(btns) != 1 {
			t.Fatalf("element[%d] expected 1 embedded button; got %d", i, len(btns))
		}
		btn, _ := btns[0].(map[string]interface{})
		if btn["type"] != "oa.query.hide" {
			t.Errorf("element[%d].buttons[0].type = %v; want oa.query.hide", i, btn["type"])
		}
		if btn["payload"] != want.payload {
			t.Errorf("element[%d].buttons[0].payload = %v; want %q", i, btn["payload"], want.payload)
		}
	}
}

func TestBuildV3ListTemplatePayload_NoButtonsFallsBackToInfoCard(t *testing.T) {
	got, err := BuildV3ListTemplatePayload("U1", "hello", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	text, elements := decodeListPayload(t, got)
	if text != "hello" {
		t.Errorf("message.text = %q; want %q", text, "hello")
	}
	if len(elements) != 1 {
		t.Fatalf("expected single info element; got %d", len(elements))
	}
	if elements[0]["title"] != "hello" {
		t.Errorf("info card title = %v; want prompt", elements[0]["title"])
	}
	if strings.TrimSpace(elements[0]["subtitle"].(string)) == "" {
		t.Errorf("info card subtitle must be non-empty")
	}
	if _, present := elements[0]["buttons"]; present {
		t.Errorf("info card should have no embedded buttons; got %v", elements[0]["buttons"])
	}
}

func TestBuildV3ListTemplatePayload_UsesProvidedSubtitle(t *testing.T) {
	got, err := BuildV3ListTemplatePayload("U1", "Prompt", []ZaloOAButton{
		{Title: "Option A", Payload: "#a", Subtitle: "Detailed helper for A"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	_, elements := decodeListPayload(t, got)
	if elements[0]["subtitle"] != "Detailed helper for A" {
		t.Errorf("subtitle = %v; want caller-provided value", elements[0]["subtitle"])
	}
}

func TestBuildV3ListTemplatePayloadWithImage_EmbedsImageInEachElement(t *testing.T) {
	got, err := BuildV3ListTemplatePayloadWithImage(
		"U1",
		"Header",
		"https://example.com/banner.png",
		[]ZaloOAButton{
			{Title: "A", Payload: "#a"},
			{Title: "B", Payload: "#b"},
		},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	_, elements := decodeListPayload(t, got)
	if len(elements) != 2 {
		t.Fatalf("expected 2 elements; got %d", len(elements))
	}
	for i, el := range elements {
		if el["image_url"] != "https://example.com/banner.png" {
			t.Errorf("element[%d].image_url = %v; want banner URL", i, el["image_url"])
		}
	}
}

func TestBuildV3ListTemplatePayloadWithImage_OmitsEmptyImageURL(t *testing.T) {
	got, err := BuildV3ListTemplatePayloadWithImage("U1", "Hello", "   ", []ZaloOAButton{
		{Title: "A", Payload: "#a"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	_, elements := decodeListPayload(t, got)
	if _, present := elements[0]["image_url"]; present {
		t.Errorf("image_url should be omitted when blank; got %v", elements[0]["image_url"])
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
