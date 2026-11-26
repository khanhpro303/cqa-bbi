package workers

import (
	"context"
	"time"

	"github.com/vietbui/chat-quality-agent/engine"
)

// PersonalZaloGroupNeedsReply uses the same editable classifier as OA.
// Explicit calls and session keywords are handled by the personal channel.
func PersonalZaloGroupNeedsReply(ctx context.Context, tenantID, input string) bool {
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	content, err := classifyWithTaskPrompt(ctx, tenantID, engine.PromptGroupClassifier, input, false)
	if err != nil {
		return false
	}
	switch parseGroupIntent(content) {
	case "IN_SCOPE", "HANDOVER", "OUT_OF_SCOPE":
		return true
	default:
		return false
	}
}
