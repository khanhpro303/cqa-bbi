package channels

import (
	"encoding/json"
	"fmt"
	"strings"
)

// ZaloOAButton is a postback button used in a Zalo OA V3 "list" template
// message. All buttons use type "oa.query.hide" with the string Payload echoed
// back to the webhook on click (e.g. "#show_product_variants:SP123").
type ZaloOAButton struct {
	Title   string
	Payload string
}

// defaultListElementSubtitle is the helper text rendered under the prompt when
// the caller does not embed an explicit subtitle in the prompt (via a newline
// separator). Zalo V3 list templates reject elements with an empty subtitle
// (api error -201: "subtitle is empty"), so this field must always be set.
const defaultListElementSubtitle = "Vui lòng chọn một tùy chọn bên dưới."

// BuildV3ListTemplatePayload returns the JSON body for a Zalo OA V3 "list"
// template message intended for /v3.0/oa/message/cs (1:1 customer-support
// chat). The prompt becomes the single list element title (the first line if
// the prompt contains "\n"; the remainder is used as the subtitle) and the
// buttons appear below. Pass the returned string to ZaloOAAdapter.SendMessage
// which parses it back into the request body.
//
// Zalo deprecated the V2 buttons-only template (error -240); list/buttons must
// now be wrapped under template_type="list" with an elements array.
// Zalo V3 also rejects elements without a non-empty subtitle (error -201), so
// when the prompt has no embedded subtitle this function falls back to a
// generic helper sentence.
func BuildV3ListTemplatePayload(userID, prompt string, buttons []ZaloOAButton) (string, error) {
	btns := make([]map[string]interface{}, 0, len(buttons))
	for _, b := range buttons {
		btns = append(btns, map[string]interface{}{
			"title":   b.Title,
			"type":    "oa.query.hide",
			"payload": b.Payload,
		})
	}

	title, subtitle := splitPromptTitleSubtitle(prompt)

	payload := map[string]interface{}{
		"recipient": map[string]interface{}{
			"user_id": userID,
		},
		"message": map[string]interface{}{
			"attachment": map[string]interface{}{
				"type": "template",
				"payload": map[string]interface{}{
					"template_type": "list",
					"elements": []map[string]interface{}{
						{"title": title, "subtitle": subtitle},
					},
					"buttons": btns,
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

// splitPromptTitleSubtitle derives a (title, subtitle) pair from a single
// prompt string. If the prompt contains a newline, the first line becomes the
// title and the rest becomes the subtitle. Otherwise the whole prompt is the
// title and a generic helper sentence is used as the subtitle so Zalo does not
// reject the element.
func splitPromptTitleSubtitle(prompt string) (string, string) {
	trimmed := strings.TrimSpace(prompt)
	if idx := strings.IndexAny(trimmed, "\n"); idx >= 0 {
		title := strings.TrimSpace(trimmed[:idx])
		subtitle := strings.TrimSpace(trimmed[idx+1:])
		if title == "" {
			title = trimmed
		}
		if subtitle == "" {
			subtitle = defaultListElementSubtitle
		}
		return title, subtitle
	}
	if trimmed == "" {
		return defaultListElementSubtitle, defaultListElementSubtitle
	}
	return trimmed, defaultListElementSubtitle
}

// BuildV3ListTemplatePayloadWithImage is like BuildV3ListTemplatePayload but
// also embeds an image_url on the single element. Zalo V3 list templates
// reject elements without a non-empty image_url (api error -201: "image_url
// is empty"), so callers that hit strict validation must use this variant
// and pass a publicly reachable HTTPS URL that Zalo's servers can fetch.
// Note: existing callers of BuildV3ListTemplatePayload have the same latent
// validation risk and should migrate to this function as they hit -201.
func BuildV3ListTemplatePayloadWithImage(userID, prompt, imageURL string, buttons []ZaloOAButton) (string, error) {
	btns := make([]map[string]interface{}, 0, len(buttons))
	for _, b := range buttons {
		btns = append(btns, map[string]interface{}{
			"title":   b.Title,
			"type":    "oa.query.hide",
			"payload": b.Payload,
		})
	}

	title, subtitle := splitPromptTitleSubtitle(prompt)
	element := map[string]interface{}{
		"title":    title,
		"subtitle": subtitle,
	}
	if strings.TrimSpace(imageURL) != "" {
		element["image_url"] = imageURL
	}

	payload := map[string]interface{}{
		"recipient": map[string]interface{}{
			"user_id": userID,
		},
		"message": map[string]interface{}{
			"attachment": map[string]interface{}{
				"type": "template",
				"payload": map[string]interface{}{
					"template_type": "list",
					"elements":      []map[string]interface{}{element},
					"buttons":       btns,
				},
			},
		},
	}

	out, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal v3 list template with image: %w", err)
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
