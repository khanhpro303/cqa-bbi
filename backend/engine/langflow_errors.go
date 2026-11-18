package engine

import (
	"context"
	"errors"
	"fmt"
	"net/http"
)

// LangflowAPIError is returned when Langflow answers with a non-200 status.
// Its Error() text matches the historical fmt.Errorf format
// ("langflow api error (status 403): ...") so existing log greps keep working,
// while callers can now inspect StatusCode to decide whether a retry can help.
type LangflowAPIError struct {
	// Prefix is "langflow api error" for /run calls and "flow api error" for
	// GET /flows/{id} calls.
	Prefix     string
	StatusCode int
	Body       string
}

func (e *LangflowAPIError) Error() string {
	return fmt.Sprintf("%s (status %d): %s", e.Prefix, e.StatusCode, e.Body)
}

func newRunAPIError(status int, body []byte) error {
	return &LangflowAPIError{Prefix: "langflow api error", StatusCode: status, Body: string(body)}
}

func newFlowAPIError(status int, body []byte) error {
	return &LangflowAPIError{Prefix: "flow api error", StatusCode: status, Body: string(body)}
}

// IsPermanentLangflowError reports whether err is a Langflow failure that a
// retry cannot fix: a configuration problem (bad/missing API key, unknown flow
// ID, malformed request). Retrying these only delays the user-facing fallback
// reply and burns queue slots.
func IsPermanentLangflowError(err error) bool {
	var apiErr *LangflowAPIError
	if !errors.As(err, &apiErr) {
		return false
	}
	switch apiErr.StatusCode {
	case http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden,
		http.StatusNotFound, http.StatusUnprocessableEntity:
		return true
	}
	return false
}

// CheckFlow verifies that apiKey can read flowID on the Langflow server at
// apiURL. It is a cheap GET /api/v1/flows/{id} — no LLM call, no token spend —
// so it is safe to run from health checks and the admin "Test" button.
func (l *LangflowClient) CheckFlow(ctx context.Context, apiURL, apiKey, flowID string) error {
	_, err := l.fetchAgentNodeIDs(ctx, apiURL, apiKey, flowID)
	return err
}
