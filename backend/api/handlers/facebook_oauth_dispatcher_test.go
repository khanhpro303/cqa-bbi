package handlers

import (
	"net/http"
	"net/url"
	"testing"
)

func TestFacebookOAuthCallbackDispatcherPreservesAllFlows(t *testing.T) {
	page := newFacebookPageOAuthTestHandler(&fakeFacebookPageOAuthStore{})
	account := newFacebookAccountOAuthTestHandler(&fakeFacebookAccountOAuthStore{})
	login := newFacebookLoginOAuthTestHandler(&fakeFacebookLoginOAuthStore{})
	dispatch := FacebookOAuthCallbackDispatcher(page, account, login)
	for _, test := range []struct {
		name, state, target string
	}{
		{"sign-in", "signin_secret", "/login?facebook_login=browser_mismatch"},
		{"page connection", "page_secret", "/login?facebook_connect_error=browser_mismatch"},
		{"account linking", "link_secret", "/?facebook_link=browser_mismatch"},
		{"legacy fallback", "legacy-state", "/login?zalo_auth=error&message=Missing+code+or+state"},
	} {
		t.Run(test.name, func(t *testing.T) {
			// No cookie/code: each handler must reject safely with its own flow's result.
			response := facebookOAuthRequest(dispatch, http.MethodGet, "/api/v1/channels/facebook/callback?state="+url.QueryEscape(test.state), "", "", "")
			if response.Code != http.StatusFound || response.Header().Get("Location") != test.target {
				t.Fatalf("status=%d redirect=%q want=%q", response.Code, response.Header().Get("Location"), test.target)
			}
		})
	}
}
