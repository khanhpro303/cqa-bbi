package channels

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type facebookConversationRoundTrip func(*http.Request) (*http.Response, error)

func (fn facebookConversationRoundTrip) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

func TestFacebookConversationStoresParticipantPSID(t *testing.T) {
	adapter := NewFacebookAdapter(FacebookCredentials{PageID: "100", AccessToken: "token"})
	adapter.client.Transport = facebookConversationRoundTrip(func(request *http.Request) (*http.Response, error) {
		body := `{"data":[{"id":"conversation-1","updated_time":"2026-09-15T12:00:00+0000","participants":{"data":[{"id":"100","name":"Page"},{"id":"200","name":"Customer"}]}}]}`
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
	})
	conversations, err := adapter.FetchRecentConversations(context.Background(), time.Time{}, 10)
	if err != nil || len(conversations) != 1 {
		t.Fatalf("unexpected conversations: %+v error=%v", conversations, err)
	}
	if conversations[0].ExternalUserID != "200" {
		t.Fatalf("PSID=%q want 200", conversations[0].ExternalUserID)
	}
}
