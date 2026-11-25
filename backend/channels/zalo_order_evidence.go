package channels

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"

	"golang.org/x/text/unicode/norm"
)

// GMFOrderEvidence describes the original OA message and the member's reply
// as verified by Zalo's official group conversation API.
type GMFOrderEvidence struct {
	SentAtMillis  int64
	ReplyAtMillis int64
	SentText      string
	SentType      string
}

// OrderEvidenceMessages resolves both opaque message IDs to the timestamps
// needed to locate the corresponding messages in the Zalo OA chat interface.
func (z *ZaloOAAdapter) OrderEvidenceMessages(ctx context.Context, groupID, sentID, replyID, replySenderID, replyText string) (GMFOrderEvidence, error) {
	const pageSize, maxPages = 20, 3
	wantReply := normalizeGMFEvidenceText(replyText)
	if groupID == "" || sentID == "" || replyID == "" || sentID == replyID || replySenderID == "" || wantReply == "" {
		return GMFOrderEvidence{}, errors.New("zalo order evidence: incomplete message identity")
	}

	var evidence GMFOrderEvidence
	for page := 0; page < maxPages; page++ {
		result, err := z.doRequestQueryParams(ctx, http.MethodGet, zaloAPIBaseV3+"/group/conversation", map[string]string{
			"group_id": groupID,
			"offset":   strconv.Itoa(page * pageSize),
			"count":    strconv.Itoa(pageSize),
		})
		if err != nil {
			return GMFOrderEvidence{}, fmt.Errorf("zalo order evidence: %w", err)
		}
		status, hasStatus := result["error"].(float64)
		messages, hasMessages := result["data"].([]any)
		if !hasStatus || status != 0 || !hasMessages {
			return GMFOrderEvidence{}, errors.New("zalo order evidence: invalid group conversation response")
		}

		for _, raw := range messages {
			message, _ := raw.(map[string]any)
			switch {
			case matchesGroupMessage(message, groupID, sentID, 0):
				at := gmfEvidenceTimestamp(message["time"])
				kind, _ := message["type"].(string)
				value, _ := message["message"].(string)
				if at == 0 || (kind != "text" && kind != "photo") ||
					(kind == "text" && normalizeGMFEvidenceText(value) == "") {
					return GMFOrderEvidence{}, errors.New("zalo order evidence: invalid original OA message")
				}
				if evidence.SentAtMillis != 0 && (evidence.SentAtMillis != at || evidence.SentType != kind || evidence.SentText != value) {
					return GMFOrderEvidence{}, errors.New("zalo order evidence: inconsistent original OA message")
				}
				evidence.SentAtMillis, evidence.SentType, evidence.SentText = at, kind, value

			case matchesGroupMessage(message, groupID, replyID, 1):
				at := gmfEvidenceTimestamp(message["time"])
				text, _ := message["message"].(string)
				if at == 0 || message["type"] != "text" || message["from_id"] != replySenderID ||
					normalizeGMFEvidenceText(text) != wantReply {
					return GMFOrderEvidence{}, errors.New("zalo order evidence: reply does not match the verified confirmation")
				}
				if evidence.ReplyAtMillis != 0 && evidence.ReplyAtMillis != at {
					return GMFOrderEvidence{}, errors.New("zalo order evidence: inconsistent reply")
				}
				evidence.ReplyAtMillis = at
			}
		}

		if evidence.SentAtMillis != 0 && evidence.ReplyAtMillis != 0 {
			if evidence.SentAtMillis > evidence.ReplyAtMillis {
				return GMFOrderEvidence{}, errors.New("zalo order evidence: reply predates the OA message")
			}
			return evidence, nil
		}
		if len(messages) < pageSize {
			break
		}
	}
	return GMFOrderEvidence{}, errors.New("zalo order evidence: matching messages not found in recent group history")
}

func gmfEvidenceTimestamp(value any) int64 {
	// The generic JSON decoder reads numbers as float64. Browser Run receives
	// a JavaScript number, so keep the timestamp within its exact integer range.
	const maxSafeInteger = 1<<53 - 1
	ms, ok := value.(float64)
	if !ok || ms <= 0 || ms > maxSafeInteger || math.Trunc(ms) != ms {
		return 0
	}
	return int64(ms)
}

func normalizeGMFEvidenceText(text string) string {
	return strings.Join(strings.Fields(norm.NFC.String(text)), " ")
}
