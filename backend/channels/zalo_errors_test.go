package channels

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestZaloErrorClassification(t *testing.T) {
	tests := []struct {
		name               string
		err                error
		wantRateLimited    bool
		wantConversationOK bool
	}{
		{"broken single conversation", &ZaloAPIError{Code: -1, Message: "Error fetching messages: -2"}, false, true},
		{"wrapped broken conversation", fmt.Errorf("fetch: %w", &ZaloAPIError{Code: -1}), false, true},
		{"call limit", &ZaloAPIError{Code: -32, Message: "Your OA reached limit call api"}, true, false},
		{"token invalid", &ZaloAPIError{Code: -216}, false, false},
		{"token expired", &ZaloAPIError{Code: -124}, false, false},
		{"network failure", errors.New("zalo api request failed: dial tcp: timeout"), false, false},
		{"nil", nil, false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsRateLimited(tt.err); got != tt.wantRateLimited {
				t.Errorf("IsRateLimited = %v, want %v", got, tt.wantRateLimited)
			}
			if got := IsConversationScoped(tt.err); got != tt.wantConversationOK {
				t.Errorf("IsConversationScoped = %v, want %v", got, tt.wantConversationOK)
			}
		})
	}
}

func TestZaloAPIErrorKeepsLegacyMessage(t *testing.T) {
	err := &ZaloAPIError{Code: -1, Message: "Error fetching messages: -2"}
	if got, want := err.Error(), "zalo api error -1: Error fetching messages: -2"; got != want {
		t.Fatalf("Error() = %q, want %q", got, want)
	}
}

type zaloRoundTrip func(*http.Request) (*http.Response, error)

func (fn zaloRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return fn(r) }

func TestFetchMessagesReturnsTypedZaloError(t *testing.T) {
	adapter := &ZaloOAAdapter{
		creds: ZaloOACredentials{AccessToken: "test-token"},
		client: &http.Client{Transport: zaloRoundTrip(func(r *http.Request) (*http.Response, error) {
			body := `{"error":-1,"message":"Error fetching messages: -2"}`
			return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
		})},
	}

	_, err := adapter.FetchMessages(context.Background(), "1052875424724271277", time.Now().Add(-time.Hour))

	var apiErr *ZaloAPIError
	if !errors.As(err, &apiErr) || apiErr.Code != -1 {
		t.Fatalf("FetchMessages error = %v, want *ZaloAPIError code -1", err)
	}
	if !IsConversationScoped(err) {
		t.Fatal("a -1 conversation error must be conversation scoped")
	}
}
