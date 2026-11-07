// Package pancake reads customer tags from Pancake (pancake.vn / pages.fm).
// It only reads: no tag, message or customer data is written back to Pancake.
package pancake

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	DefaultPublicAPIBase = "https://pages.fm/api/public_api"
	maxBodyBytes         = 4 << 20
	// MaxConversationPages bounds one scan: 500 × 60 = 30,000 conversations.
	MaxConversationPages = 500
)

// Tag is one Pancake tag. Pancake uses small integer IDs per Page.
type Tag struct {
	ID    string `json:"id"`
	Text  string `json:"text"`
	Color string `json:"color,omitempty"`
}

// APIError is a sanitized Pancake API error. It never contains the token,
// the request URL or the response body.
type APIError struct {
	Kind   string // auth | permission | rate_limit | invalid_response | transport | not_found
	Status int
}

func (e *APIError) Error() string {
	if e == nil {
		return "pancake api error"
	}
	if e.Status != 0 {
		return fmt.Sprintf("pancake api error: %s (http %d)", e.Kind, e.Status)
	}
	return "pancake api error: " + e.Kind
}

func ErrorKind(err error) string {
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return apiErr.Kind
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return "transport"
	}
	return "storage"
}

// Client calls Pancake Page API with one page_access_token.
type Client struct {
	BaseURL   string
	PageID    string
	PageToken string
	HTTP      *http.Client
	// MinInterval spaces requests (Pancake allows 5 calls per Page per second).
	MinInterval time.Duration
	last        time.Time
}

func NewClient(pageID, pageToken string) *Client {
	return &Client{BaseURL: DefaultPublicAPIBase, PageID: pageID, PageToken: pageToken, HTTP: &http.Client{Timeout: 30 * time.Second}, MinInterval: time.Second}
}

func (c *Client) wait(ctx context.Context) error {
	if c.MinInterval <= 0 || c.last.IsZero() {
		c.last = time.Now()
		return nil
	}
	delay := time.Until(c.last.Add(c.MinInterval))
	if delay > 0 {
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
		}
	}
	c.last = time.Now()
	return nil
}

func (c *Client) get(ctx context.Context, version, path string, query url.Values, out any) error {
	if strings.TrimSpace(c.PageToken) == "" || strings.TrimSpace(c.PageID) == "" {
		return &APIError{Kind: "auth"}
	}
	if err := c.wait(ctx); err != nil {
		return err
	}
	if query == nil {
		query = url.Values{}
	}
	query.Set("page_access_token", c.PageToken)
	endpoint := strings.TrimRight(c.BaseURL, "/") + "/" + version + "/pages/" + url.PathEscape(c.PageID) + path + "?" + query.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return &APIError{Kind: "transport"}
	}
	req.Header.Set("Accept", "application/json")
	client := c.HTTP
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return &APIError{Kind: "transport"}
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes+1))
	if err != nil || len(body) > maxBodyBytes {
		return &APIError{Kind: "invalid_response", Status: resp.StatusCode}
	}
	switch {
	case resp.StatusCode == http.StatusTooManyRequests:
		return &APIError{Kind: "rate_limit", Status: resp.StatusCode}
	case resp.StatusCode == http.StatusUnauthorized:
		return &APIError{Kind: "auth", Status: resp.StatusCode}
	case resp.StatusCode == http.StatusForbidden:
		return &APIError{Kind: "permission", Status: resp.StatusCode}
	case resp.StatusCode == http.StatusNotFound:
		return &APIError{Kind: "not_found", Status: resp.StatusCode}
	case resp.StatusCode >= 300:
		return &APIError{Kind: "transport", Status: resp.StatusCode}
	}
	var envelope struct {
		Success *bool  `json:"success"`
		Message string `json:"message"`
	}
	if json.Unmarshal(body, &envelope) == nil && envelope.Success != nil && !*envelope.Success {
		// Pancake answers some token errors with 200 + success:false.
		return &APIError{Kind: "auth", Status: resp.StatusCode}
	}
	if err := json.Unmarshal(body, out); err != nil {
		return &APIError{Kind: "invalid_response", Status: resp.StatusCode}
	}
	return nil
}

// FetchTags returns the Page's tag catalogue.
func (c *Client) FetchTags(ctx context.Context) ([]Tag, error) {
	var body struct {
		Tags []rawTag `json:"tags"`
	}
	if err := c.get(ctx, "v1", "/tags", nil, &body); err != nil {
		return nil, err
	}
	if body.Tags == nil {
		return nil, &APIError{Kind: "invalid_response"}
	}
	tags := make([]Tag, 0, len(body.Tags))
	seen := map[string]bool{}
	for _, raw := range body.Tags {
		id := raw.id()
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		tags = append(tags, Tag{ID: id, Text: strings.TrimSpace(raw.Text), Color: raw.Color})
	}
	return tags, nil
}

// ConversationPage is one page of inbox conversations, newest update first.
type ConversationPage struct {
	Updates []ConversationUpdate
	// Oldest is the smallest updated_at seen (zero if none parsed).
	Oldest time.Time
	// NextCursor is the last conversation id, for the next older page.
	NextCursor string
}

// ListInboxConversations lists INBOX conversations ordered by updated_at.
func (c *Client) ListInboxConversations(ctx context.Context, cursor string) (ConversationPage, error) {
	query := url.Values{}
	query.Set("type", "INBOX")
	query.Set("order_by", "updated_at")
	if cursor != "" {
		query.Set("last_conversation_id", cursor)
	}
	var body struct {
		Conversations []json.RawMessage `json:"conversations"`
	}
	if err := c.get(ctx, "v2", "/conversations", query, &body); err != nil {
		return ConversationPage{}, err
	}
	if body.Conversations == nil {
		return ConversationPage{}, &APIError{Kind: "invalid_response"}
	}
	page := ConversationPage{}
	for _, raw := range body.Conversations {
		var conv rawConversation
		if json.Unmarshal(raw, &conv) != nil {
			continue
		}
		if conv.ID != "" {
			page.NextCursor = conv.ID
		}
		if at, ok := parseTime(conv.UpdatedAt); ok && (page.Oldest.IsZero() || at.Before(page.Oldest)) {
			page.Oldest = at
		}
		if conv.Tags == nil {
			// A listed conversation is a full object: no tags means none.
			conv.Tags = &[]rawTag{}
		}
		if update, ok := conv.update(c.PageID, 0); ok {
			page.Updates = append(page.Updates, update)
		}
	}
	return page, nil
}

func parseTime(value string) (time.Time, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, false
	}
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05.999999", "2006-01-02T15:04:05"} {
		if at, err := time.Parse(layout, value); err == nil {
			return at.UTC(), true
		}
	}
	return time.Time{}, false
}

// rawTag accepts {"id":1,"text":"..."} or a bare number.
type rawTag struct {
	ID    json.RawMessage `json:"id"`
	Text  string          `json:"text"`
	Color string          `json:"color"`
	bare  string
}

func (t *rawTag) UnmarshalJSON(data []byte) error {
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "null" {
		return nil
	}
	if strings.HasPrefix(trimmed, "{") {
		type plain rawTag
		var p plain
		if err := json.Unmarshal(data, &p); err != nil {
			return err
		}
		*t = rawTag(p)
		return nil
	}
	t.bare = scalarID(data)
	return nil
}

func (t rawTag) id() string {
	if t.bare != "" {
		return t.bare
	}
	return scalarID(t.ID)
}

func scalarID(data json.RawMessage) string {
	if len(data) == 0 {
		return ""
	}
	var number json.Number
	if json.Unmarshal(data, &number) == nil {
		if _, err := strconv.ParseInt(number.String(), 10, 64); err == nil {
			return number.String()
		}
		return ""
	}
	var text string
	if json.Unmarshal(data, &text) == nil {
		text = strings.TrimSpace(text)
		if _, err := strconv.ParseInt(text, 10, 64); err == nil {
			return text
		}
	}
	return ""
}
