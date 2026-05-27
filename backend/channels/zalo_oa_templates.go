package channels

import (
	"encoding/json"
	"fmt"
	"strings"
)

// ZaloOAButton is a postback button used in a Zalo OA V3 "list" template
// message. All buttons use type "oa.query.hide" with the string Payload echoed
// back to the webhook on click (e.g. "#show_product_variants:SP123").
//
// Subtitle is optional helper text rendered under the button's card title. If
// empty, defaultListCardSubtitle is used so Zalo does not reject the element
// (api error -201: "subtitle is empty").
type ZaloOAButton struct {
	Title    string
	Payload  string
	Subtitle string
}

// defaultListCardSubtitle is the helper text rendered under each option card
// when the caller does not provide ZaloOAButton.Subtitle. Required because
// Zalo V3 rejects elements with empty subtitle.
const defaultListCardSubtitle = "Bấm để chọn tùy chọn này."

// BuildV3ListTemplatePayload returns the JSON body for a Zalo OA V3 "list"
// template message intended for /v3.0/oa/message/cs (1:1 customer-support
// chat). Pass the returned string to ZaloOAAdapter.SendMessage which parses
// it back into the request body.
//
// Layout per Zalo V3 spec:
//   - message.text holds the header sentence (the prompt) shown above the list.
//   - Each ZaloOAButton becomes its own element (a "card") with its own
//     embedded button. Element title = button title; subtitle = optional helper
//     text; the embedded button carries the postback payload.
//
// Buttons must NOT be placed at the payload top-level — Zalo answers -233
// ("message type is invalid or not support") when the structure does not
// match. Each element also requires non-empty title and subtitle, otherwise
// Zalo returns -201.
func BuildV3ListTemplatePayload(userID, prompt string, buttons []ZaloOAButton) (string, error) {
	return BuildV3ListTemplatePayloadWithImage(userID, prompt, "", buttons)
}

// BuildV3ListTemplatePayloadWithImage is like BuildV3ListTemplatePayload but
// also embeds an image_url on every element so each card renders the same
// banner image. imageURL must be a publicly reachable HTTPS URL that Zalo's
// servers can fetch; pass "" to omit the field (image_url is optional in
// Zalo V3, but when present must be non-empty).
func BuildV3ListTemplatePayloadWithImage(userID, prompt, imageURL string, buttons []ZaloOAButton) (string, error) {
	trimmedPrompt := strings.TrimSpace(prompt)
	trimmedImage := strings.TrimSpace(imageURL)

	elements := make([]map[string]interface{}, 0, len(buttons))
	for _, b := range buttons {
		subtitle := strings.TrimSpace(b.Subtitle)
		if subtitle == "" {
			subtitle = defaultListCardSubtitle
		}
		el := map[string]interface{}{
			"title":    b.Title,
			"subtitle": subtitle,
			"buttons": []map[string]interface{}{
				{
					"title":   b.Title,
					"type":    "oa.query.hide",
					"payload": b.Payload,
				},
			},
		}
		if trimmedImage != "" {
			el["image_url"] = trimmedImage
		}
		elements = append(elements, el)
	}

	// Empty button list — emit a single info card using the prompt as title so
	// the template stays renderable. Rare in practice (callers always supply
	// at least one option), but avoids producing a "list" payload with zero
	// elements which Zalo also rejects.
	if len(elements) == 0 {
		title := trimmedPrompt
		if title == "" {
			title = defaultListCardSubtitle
		}
		el := map[string]interface{}{
			"title":    title,
			"subtitle": defaultListCardSubtitle,
		}
		if trimmedImage != "" {
			el["image_url"] = trimmedImage
		}
		elements = append(elements, el)
	}

	payload := map[string]interface{}{
		"recipient": map[string]interface{}{
			"user_id": userID,
		},
		"message": map[string]interface{}{
			"text": trimmedPrompt,
			"attachment": map[string]interface{}{
				"type": "template",
				"payload": map[string]interface{}{
					"template_type": "list",
					"elements":      elements,
				},
			},
		},
	}

	out, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal v3 list template: %w", err)
	}
	return string(out), nil
}

// BuildButtonOptionsAsText renders the prompt + button labels as a plain text
// message. Used as a fallback for Zalo OA group chats (/v3.0/oa/group/message)
// which only support text/file/image/sticker in V3 — template+buttons are not
// allowed there. The button titles become a numbered list so users can still
// see the options, even though they have to type the postback manually.
func BuildButtonOptionsAsText(prompt string, buttons []ZaloOAButton) string {
	if len(buttons) == 0 {
		return prompt
	}
	var sb strings.Builder
	sb.WriteString(prompt)
	for i, b := range buttons {
		fmt.Fprintf(&sb, "\n%d. %s", i+1, b.Title)
	}
	return sb.String()
}
