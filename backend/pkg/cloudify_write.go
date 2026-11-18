package pkg

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// CloudifyWriteTimeout bounds one write call. A write that times out may or
// may not have been applied, so callers must not retry it blindly.
const CloudifyWriteTimeout = 60 * time.Second

// ErrCloudifyRejected marks a write Cloudify answered with an error status or
// an error body. The request reached Cloudify and was refused, so nothing was
// written and the caller may report a plain failure.
var ErrCloudifyRejected = errors.New("cloudify rejected the write")

// WriteJSON sends one write request (POST create/update) to
// /rest_api/private/<endpoint> and returns the raw response body.
//
// Unlike the search helpers it never retries after the request was sent,
// except when Cloudify answers 401/403: an expired session is refused before
// any write happens, so re-authenticating and sending once more is safe.
// Errors wrapping ErrCloudifyRejected mean "not written"; any other error
// (timeout, connection reset, unreadable body) means "outcome unknown".
func (c *CloudifyClient) WriteJSON(ctx context.Context, method, endpoint string, body any) (json.RawMessage, error) {
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("%w: marshal body: %v", ErrCloudifyRejected, err)
	}
	session, err := c.getSession()
	if err != nil {
		return nil, fmt.Errorf("%w: get session: %v", ErrCloudifyRejected, err)
	}
	raw, status, err := c.doWrite(ctx, method, endpoint, payload, session)
	if err == nil && (status == http.StatusUnauthorized || status == http.StatusForbidden) {
		c.InvalidateSession()
		if session, err = c.getSession(); err != nil {
			return nil, fmt.Errorf("%w: re-authenticate: %v", ErrCloudifyRejected, err)
		}
		raw, status, err = c.doWrite(ctx, method, endpoint, payload, session)
	}
	if err != nil {
		return nil, err
	}
	if status == http.StatusUnauthorized || status == http.StatusForbidden {
		return nil, fmt.Errorf("%w: HTTP %d", ErrCloudifyRejected, status)
	}
	if status >= 400 {
		return nil, fmt.Errorf("%w: HTTP %d: %s", ErrCloudifyRejected, status, preview(raw, 300))
	}
	if msg, failed := cloudifyBodyError(raw); failed {
		return nil, fmt.Errorf("%w: %s", ErrCloudifyRejected, msg)
	}
	return raw, nil
}

func (c *CloudifyClient) doWrite(ctx context.Context, method, endpoint string, payload []byte, session string) (json.RawMessage, int, error) {
	ctx, cancel := context.WithTimeout(ctx, CloudifyWriteTimeout)
	defer cancel()
	apiURL := fmt.Sprintf("%s/rest_api/private/%s", strings.TrimRight(c.BaseURL, "/"), strings.TrimLeft(endpoint, "/"))
	req, err := http.NewRequestWithContext(ctx, method, apiURL, bytes.NewReader(payload))
	if err != nil {
		return nil, 0, fmt.Errorf("%w: create request: %v", ErrCloudifyRejected, err)
	}
	q := req.URL.Query()
	if c.DB != "" {
		q.Set("db", c.DB)
	}
	if session != "" {
		q.Set("session_id", strings.TrimPrefix(session, "session_id="))
	}
	req.URL.RawQuery = q.Encode()
	req.Header.Set("Cookie", session)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("cloudify write to %s: %w", endpoint, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("read cloudify write response: %w", err)
	}
	return raw, resp.StatusCode, nil
}

// cloudifyBodyError detects the error bodies Cloudify sends with HTTP 200:
// {"status":"error",...}, {"success":false,...} or a JSON-RPC {"error":{...}}.
func cloudifyBodyError(raw []byte) (string, bool) {
	var body map[string]any
	if json.Unmarshal(raw, &body) != nil {
		return "", false
	}
	message := func() string {
		for _, key := range []string{"message", "msg", "error_message", "detail"} {
			if s, ok := body[key].(string); ok && s != "" {
				return s
			}
		}
		if e, ok := body["error"].(map[string]any); ok {
			if data, ok := e["data"].(map[string]any); ok {
				if s, ok := data["message"].(string); ok && s != "" {
					return s
				}
			}
			if s, ok := e["message"].(string); ok && s != "" {
				return s
			}
		}
		if s, ok := body["error"].(string); ok && s != "" {
			return s
		}
		return preview(raw, 300)
	}
	// Cloudify's envelope: {"code":200,"message":"Success","data":{...}} or
	// {"code":400,"name":"Bad Request","description":"<p>...</p>"}.
	if code, ok := body["code"].(float64); ok && code >= 400 {
		if d, ok := body["description"].(string); ok && d != "" {
			return stripTags(d), true
		}
		return message(), true
	}
	if s, ok := body["status"].(string); ok && strings.EqualFold(s, "error") {
		return message(), true
	}
	if ok, present := body["success"].(bool); present && !ok {
		return message(), true
	}
	if _, present := body["error"]; present && body["error"] != nil && body["error"] != false {
		return message(), true
	}
	return "", false
}

func preview(raw []byte, n int) string {
	s := strings.TrimSpace(string(raw))
	if len(s) > n {
		return s[:n]
	}
	return s
}

func stripTags(s string) string {
	var b strings.Builder
	inTag := false
	for _, r := range s {
		switch {
		case r == '<':
			inTag = true
		case r == '>':
			inTag = false
		case !inTag:
			b.WriteRune(r)
		}
	}
	return strings.TrimSpace(b.String())
}
