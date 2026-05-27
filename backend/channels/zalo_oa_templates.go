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

// BuildV3ListTemplatePayload returns the JSON body for a Zalo OA V3 "list"
// template message intended for /v3.0/oa/message/cs (1:1 customer-support
// chat). The prompt becomes the single list element title and the buttons
// appear below. Pass the returned string to ZaloOAAdapter.SendMessage which
// parses it back into the request body.
//
// Zalo deprecated the V2 buttons-only template (error -240); list/buttons must
// now be wrapped under template_type="list" with an elements array.
func BuildV3ListTemplatePayload(userID, prompt string, buttons []ZaloOAButton) (string, error) {
	btns := make([]map[string]interface{}, 0, len(buttons))
	for _, b := range buttons {
		btns = append(btns, map[string]interface{}{
			"title":   b.Title,
			"type":    "oa.query.hide",
			"payload": b.Payload,
		})
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
					"elements": []map[string]interface{}{
						{"title": prompt},
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
