package workers

import (
	"context"
	"errors"
	"testing"

	"github.com/hibiken/asynq"
	"github.com/vietbui/chat-quality-agent/engine"
)

type recordingSender struct {
	userMsgs  []string
	groupMsgs []string
}

func (r *recordingSender) SendMessage(_ context.Context, _ string, text string) error {
	r.userMsgs = append(r.userMsgs, text)
	return nil
}

func (r *recordingSender) SendGroupMessage(_ context.Context, _ string, text string) error {
	r.groupMsgs = append(r.groupMsgs, text)
	return nil
}

func withRetryState(t *testing.T, retried, maxRetry int) {
	t.Helper()
	orig := retryState
	retryState = func(context.Context) (int, int, bool) { return retried, maxRetry, true }
	t.Cleanup(func() { retryState = orig })
}

func permanentErr() error {
	return &engine.LangflowAPIError{Prefix: "langflow api error", StatusCode: 403, Body: `{"detail":"Invalid API key"}`}
}

func transientErr() error {
	return &engine.LangflowAPIError{Prefix: "langflow api error", StatusCode: 502, Body: "bad gateway"}
}

func TestHandleLangflowFailure_PermanentErrorRepliesOnceAndSkipsRetry(t *testing.T) {
	withRetryState(t, 0, 3)
	sender := &recordingSender{}

	err := handleLangflowFailure(context.Background(), permanentErr(), langflowFailureTarget{adapter: sender, userID: "u1"})

	if !errors.Is(err, asynq.SkipRetry) {
		t.Fatalf("permanent error must skip retry, got %v", err)
	}
	if len(sender.userMsgs) != 1 || sender.userMsgs[0] != langflowFailureReply {
		t.Fatalf("expected one fallback reply, got %v", sender.userMsgs)
	}
}

func TestHandleLangflowFailure_TransientErrorRetriesSilently(t *testing.T) {
	withRetryState(t, 1, 3)
	sender := &recordingSender{}

	err := handleLangflowFailure(context.Background(), transientErr(), langflowFailureTarget{adapter: sender, userID: "u1"})

	if err == nil || errors.Is(err, asynq.SkipRetry) {
		t.Fatalf("transient error with retries left must be retried, got %v", err)
	}
	if len(sender.userMsgs)+len(sender.groupMsgs) != 0 {
		t.Fatalf("no reply expected before the last attempt, got %v %v", sender.userMsgs, sender.groupMsgs)
	}
}

func TestHandleLangflowFailure_TransientErrorOnLastAttemptRepliesToGroup(t *testing.T) {
	withRetryState(t, 3, 3)
	sender := &recordingSender{}

	err := handleLangflowFailure(context.Background(), transientErr(), langflowFailureTarget{adapter: sender, userID: "u1", groupID: "g1"})

	if !errors.Is(err, asynq.SkipRetry) {
		t.Fatalf("last attempt must skip retry, got %v", err)
	}
	if len(sender.groupMsgs) != 1 || len(sender.userMsgs) != 0 {
		t.Fatalf("expected one group fallback reply, got user=%v group=%v", sender.userMsgs, sender.groupMsgs)
	}
}

func TestHandleLangflowFailure_KeepsOriginalErrorInChain(t *testing.T) {
	withRetryState(t, 0, 3)
	orig := permanentErr()

	err := handleLangflowFailure(context.Background(), orig, langflowFailureTarget{})

	var apiErr *engine.LangflowAPIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != 403 {
		t.Fatalf("original Langflow error must stay in the chain, got %v", err)
	}
}
