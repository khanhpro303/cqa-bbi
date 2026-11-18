package workers

import (
	"context"
	"fmt"
	"log"
	"sync"

	"github.com/hibiken/asynq"
	"github.com/vietbui/chat-quality-agent/config"
	"github.com/vietbui/chat-quality-agent/engine"
	"github.com/vietbui/chat-quality-agent/pkg"
)

// langflowFailureReply is sent to the Zalo user when the AI flow cannot answer
// (bad Langflow config, or Langflow still down after the last retry). Before
// this existed the user got silence and the only trace was a worker log line.
const langflowFailureReply = "Dạ hệ thống trợ lý đang gặp sự cố tạm thời nên chưa trả lời được câu hỏi này ạ. Anh/chị vui lòng thử lại sau ít phút, hoặc liên hệ bộ phận hỗ trợ giúp em nhé!"

// zaloReplySender is the subset of the Zalo adapter the failure path needs.
type zaloReplySender interface {
	SendMessage(ctx context.Context, userID, text string) error
	SendGroupMessage(ctx context.Context, groupID, text string) error
}

type langflowFailureTarget struct {
	adapter  zaloReplySender
	groupID  string
	userID   string
	flowID   string
	config   engine.LangflowConfig
	tenantID string
}

// retryState reports asynq's retry counters; swapped in tests.
var retryState = func(ctx context.Context) (retried, maxRetry int, ok bool) {
	retried, ok1 := asynq.GetRetryCount(ctx)
	maxRetry, ok2 := asynq.GetMaxRetry(ctx)
	return retried, maxRetry, ok1 && ok2
}

// handleLangflowFailure decides what the worker does when a Langflow run fails:
//
//   - permanent errors (401/403/404/400/422 — bad key, deleted flow): retrying
//     cannot help, so reply to the user now and stop retrying;
//   - transient errors (timeouts, 5xx, 429) with retries left: return the error
//     so asynq retries the task;
//   - transient errors on the last attempt: reply to the user and stop.
//
// The returned error always wraps the Langflow error so the task log keeps it.
func handleLangflowFailure(ctx context.Context, err error, t langflowFailureTarget) error {
	permanent := engine.IsPermanentLangflowError(err)
	retried, maxRetry, known := retryState(ctx)
	lastAttempt := !known || retried >= maxRetry

	if permanent {
		log.Printf("[worker] langflow CONFIG error for tenant=%s flow=%s (%s): %v — fix it in AI Engines → Langflow, then press Test",
			t.tenantID, t.flowID, t.config.Describe(), err)
	} else {
		log.Printf("[worker] langflow transient error for tenant=%s flow=%s (attempt %d/%d): %v",
			t.tenantID, t.flowID, retried+1, maxRetry+1, err)
	}

	if !permanent && !lastAttempt {
		return fmt.Errorf("langflow error: %w", err)
	}

	if t.adapter != nil {
		var sendErr error
		if t.groupID != "" {
			sendErr = t.adapter.SendGroupMessage(ctx, t.groupID, langflowFailureReply)
		} else {
			sendErr = t.adapter.SendMessage(ctx, t.userID, langflowFailureReply)
		}
		if sendErr != nil {
			log.Printf("[worker] failed to send langflow-failure reply to %s: %v", t.userID, sendErr)
		}
	}
	return fmt.Errorf("langflow error: %w: %w", err, asynq.SkipRetry)
}

// langflowWarningsLogged de-duplicates config warnings so a stale channel
// override is reported once per process instead of on every message.
var langflowWarningsLogged sync.Map

// resolveChannelLangflowConfig resolves the effective Langflow config for a
// channel and logs any configuration warnings (once per channel+warning).
func resolveChannelLangflowConfig(tenantID, channelID string, meta ChannelMetadata, cfg *config.Config) engine.LangflowConfig {
	decrypt := func(ciphertext []byte) ([]byte, error) {
		return pkg.Decrypt(ciphertext, cfg.EncryptionKey)
	}
	lfCfg := engine.ResolveLangflowConfig(
		engine.DBSettingLookup(tenantID),
		decrypt,
		engine.LangflowValues{
			APIURL:       meta.LangflowAPIURL,
			APIKey:       meta.LangflowAPIKey,
			FlowID:       meta.LangflowFlowID,
			PublicFlowID: meta.LangflowPublicFlowID,
		},
		engine.LangflowValues{
			APIURL:       cfg.LangflowAPIURL,
			APIKey:       cfg.LangflowAPIKey,
			FlowID:       cfg.LangflowFlowID,
			PublicFlowID: cfg.LangflowPublicFlowID,
		},
	)
	for _, w := range lfCfg.Warnings {
		if _, seen := langflowWarningsLogged.LoadOrStore(channelID+"|"+w, struct{}{}); !seen {
			log.Printf("[worker] langflow config warning (channel=%s): %s", channelID, w)
		}
	}
	return lfCfg
}
