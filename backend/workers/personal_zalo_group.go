package workers

import (
	"context"
	"time"

	"github.com/vietbui/chat-quality-agent/engine"
)

// PersonalZaloComplaintNeedsHandover uses the OA intent classifier when the
// turn has no recorded case and might still require a staff alert.
func PersonalZaloComplaintNeedsHandover(ctx context.Context, tenantID, message string) bool {
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	content, err := classifyWithTaskPrompt(ctx, tenantID, engine.PromptIntentClassifier, intentClassifierInput(nil, message), false)
	return err == nil && parseMessageIntent(content, message) == "HANDOVER"
}

// PersonalZaloGroupNeedsReply uses the same editable classifier as OA.
// Explicit calls and session keywords are handled by the personal channel.
func PersonalZaloGroupNeedsReply(ctx context.Context, tenantID, input string) (reply, handover bool) {
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	content, err := classifyWithTaskPrompt(ctx, tenantID, engine.PromptGroupClassifier, input, false)
	if err != nil {
		return false, false
	}
	switch parseGroupIntent(content) {
	case "HANDOVER":
		return true, true
	case "IN_SCOPE", "OUT_OF_SCOPE":
		return true, false
	default:
		return false, false
	}
}
