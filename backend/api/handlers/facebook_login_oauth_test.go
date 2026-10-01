package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/vietbui/chat-quality-agent/db/models"
	"gorm.io/gorm"
)

type fakeFacebookLoginOAuthStore struct {
	created   *models.FacebookLoginOAuthSession
	claim     func(string, string, time.Time) (models.FacebookLoginOAuthSession, error)
	completed string
	failed    string
}

func (f *fakeFacebookLoginOAuthStore) Create(_ context.Context, session *models.FacebookLoginOAuthSession) error {
	copy := *session
	f.created = &copy
	return nil
}

func (f *fakeFacebookLoginOAuthStore) Claim(_ context.Context, stateHash, browserHash string, now time.Time) (models.FacebookLoginOAuthSession, error) {
	if f.claim == nil {
		return models.FacebookLoginOAuthSession{}, errFacebookLoginOAuthInvalid
	}
	return f.claim(stateHash, browserHash, now)
}

func (f *fakeFacebookLoginOAuthStore) Complete(_ context.Context, id string, _ time.Time) error {
	f.completed = id
	return nil
}

func (f *fakeFacebookLoginOAuthStore) Fail(_ context.Context, id string, _ time.Time) error {
	f.failed = id
	return nil
}

func (*fakeFacebookLoginOAuthStore) Cleanup(context.Context, time.Time) {}

func newFacebookLoginOAuthTestHandler(store facebookLoginOAuthStore) *FacebookLoginOAuthHandler {
	auth := &FacebookAuthHandler{
		appID: "app-1", appSecret: "secret-1", apiVersion: "v26.0", loginConfigID: "business-config",
		verifyToken: func(context.Context, string) (facebookProfile, error) {
			return facebookProfile{ID: "facebook-1", Name: "Test User"}, nil
		},
		findLinkedUser: func(string) (models.User, error) {
			return models.User{ID: "user-1", Email: "user@example.com", TokenVersion: 4}, nil
		},
		logSuccess:  func(*gin.Context, models.User) {},
		logNotFound: func(*gin.Context, facebookProfile) {},
	}
	return &FacebookLoginOAuthHandler{
		auth: auth, store: store,
		now:                  func() time.Time { return time.Date(2026, 10, 1, 1, 2, 3, 0, time.UTC) },
		exchangeCode:         func(context.Context, string, string) (string, error) { return "provider-token", nil },
		generateRefreshToken: func(models.User) (string, error) { return "refresh-token-secret", nil },
	}
}

func TestFacebookLoginOAuthStartCreatesBrowserBoundBusinessCodeFlow(t *testing.T) {
	store := &fakeFacebookLoginOAuthStore{}
	handler := newFacebookLoginOAuthTestHandler(store)
	response := facebookOAuthRequest(handler.Start, http.MethodPost, "/api/v1/auth/facebook/start", "", "", "")
	if response.Code != http.StatusOK || store.created == nil {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if store.created.Status != "pending" || store.created.RedirectURI != "http://example.com/api/v1/channels/facebook/callback" {
		t.Fatalf("unexpected session: %#v", store.created)
	}
	cookies := response.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != facebookLoginOAuthCookie || !cookies[0].HttpOnly || !cookies[0].Secure || cookies[0].SameSite != http.SameSiteLaxMode {
		t.Fatalf("unsafe OAuth cookie: %#v", cookies)
	}
	if hashFacebookOAuthSecret(cookies[0].Value) != store.created.BrowserHash {
		t.Fatal("browser secret was not stored as a hash")
	}
	var payload struct {
		RedirectURL string `json:"redirect_url"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	redirect, err := url.Parse(payload.RedirectURL)
	if err != nil {
		t.Fatal(err)
	}
	query := redirect.Query()
	if redirect.Host != "www.facebook.com" || query.Get("client_id") != "app-1" || query.Get("config_id") != "business-config" || query.Get("response_type") != "code" || query.Get("scope") != "" {
		t.Fatalf("invalid login OAuth URL: %s", payload.RedirectURL)
	}
	if state := query.Get("state"); !strings.HasPrefix(state, facebookLoginOAuthStatePrefix) || hashFacebookOAuthSecret(state) != store.created.StateHash {
		t.Fatal("state was not stored as a hash")
	}
}

func TestFacebookLoginOAuthCallbackSetsOnlyHttpOnlyRefreshCookie(t *testing.T) {
	state, browser := "signin_state-secret", "browser-secret"
	const storedRedirect = "https://crm.example.com/api/v1/channels/facebook/callback"
	store := &fakeFacebookLoginOAuthStore{claim: func(stateHash, browserHash string, _ time.Time) (models.FacebookLoginOAuthSession, error) {
		if stateHash != hashFacebookOAuthSecret(state) || browserHash != hashFacebookOAuthSecret(browser) {
			return models.FacebookLoginOAuthSession{}, errFacebookLoginOAuthInvalid
		}
		return models.FacebookLoginOAuthSession{ID: "session-1", RedirectURI: storedRedirect}, nil
	}}
	handler := newFacebookLoginOAuthTestHandler(store)
	var exchangedRedirect, lookedUpFacebookID string
	handler.exchangeCode = func(_ context.Context, code, redirectURI string) (string, error) {
		if code != "code-1" {
			t.Fatalf("code=%q", code)
		}
		exchangedRedirect = redirectURI
		return "provider-token", nil
	}
	handler.auth.findLinkedUser = func(facebookID string) (models.User, error) {
		lookedUpFacebookID = facebookID
		return models.User{ID: "user-1", Email: "user@example.com", TokenVersion: 7}, nil
	}
	response := facebookOAuthRequest(handler.Callback, http.MethodGet, "/api/v1/channels/facebook/callback?state="+url.QueryEscape(state)+"&code=code-1", "", "", "", &http.Cookie{Name: facebookLoginOAuthCookie, Value: browser})
	if response.Code != http.StatusFound || response.Header().Get("Location") != "/login?facebook_login=success" {
		t.Fatalf("status=%d location=%q", response.Code, response.Header().Get("Location"))
	}
	if exchangedRedirect != storedRedirect || lookedUpFacebookID != "facebook-1" || store.completed != "session-1" {
		t.Fatalf("redirect=%q facebookID=%q completed=%q", exchangedRedirect, lookedUpFacebookID, store.completed)
	}
	if strings.Contains(response.Header().Get("Location"), "refresh-token-secret") || strings.Contains(response.Body.String(), "refresh-token-secret") {
		t.Fatal("token leaked into redirect or response body")
	}
	var refreshCookie *http.Cookie
	for _, cookie := range response.Result().Cookies() {
		if cookie.Name == "cqa_refresh_token" {
			refreshCookie = cookie
		}
	}
	if refreshCookie == nil || refreshCookie.Value != "refresh-token-secret" || !refreshCookie.HttpOnly || refreshCookie.Path != "/api/v1/auth" || refreshCookie.SameSite != http.SameSiteStrictMode {
		t.Fatalf("unsafe refresh cookie: %#v", refreshCookie)
	}
}

func TestFacebookLoginOAuthCallbackRejectsReplayAndBrowserMismatch(t *testing.T) {
	store := &fakeFacebookLoginOAuthStore{claim: func(string, string, time.Time) (models.FacebookLoginOAuthSession, error) {
		return models.FacebookLoginOAuthSession{}, errFacebookLoginOAuthReplay
	}}
	handler := newFacebookLoginOAuthTestHandler(store)
	replay := facebookOAuthRequest(handler.Callback, http.MethodGet, "/api/v1/channels/facebook/callback?state=signin_secret&code=code", "", "", "", &http.Cookie{Name: facebookLoginOAuthCookie, Value: "browser"})
	if replay.Header().Get("Location") != "/login?facebook_login=invalid_state" {
		t.Fatalf("replay location=%q", replay.Header().Get("Location"))
	}
	missingCookie := facebookOAuthRequest(handler.Callback, http.MethodGet, "/api/v1/channels/facebook/callback?state=signin_secret&code=code", "", "", "")
	if missingCookie.Header().Get("Location") != "/login?facebook_login=browser_mismatch" {
		t.Fatalf("browser mismatch location=%q", missingCookie.Header().Get("Location"))
	}
}

func TestFacebookLoginOAuthCallbackMapsLinkedUserAndProviderErrors(t *testing.T) {
	newHandler := func() (*FacebookLoginOAuthHandler, *fakeFacebookLoginOAuthStore) {
		store := &fakeFacebookLoginOAuthStore{claim: func(string, string, time.Time) (models.FacebookLoginOAuthSession, error) {
			return models.FacebookLoginOAuthSession{ID: "session-1", RedirectURI: "https://crm.example.com/api/v1/channels/facebook/callback"}, nil
		}}
		return newFacebookLoginOAuthTestHandler(store), store
	}
	request := func(handler *FacebookLoginOAuthHandler) string {
		response := facebookOAuthRequest(handler.Callback, http.MethodGet, "/api/v1/channels/facebook/callback?state=signin_secret&code=code", "", "", "", &http.Cookie{Name: facebookLoginOAuthCookie, Value: "browser"})
		return response.Header().Get("Location")
	}

	handler, store := newHandler()
	handler.auth.findLinkedUser = func(string) (models.User, error) { return models.User{}, gorm.ErrRecordNotFound }
	if location := request(handler); location != "/login?facebook_login=facebook_account_not_linked" || store.failed != "session-1" {
		t.Fatalf("not linked location=%q failed=%q", location, store.failed)
	}

	handler, store = newHandler()
	handler.exchangeCode = func(context.Context, string, string) (string, error) { return "", errors.New("offline") }
	if location := request(handler); location != "/login?facebook_login=facebook_service_unavailable" || store.failed != "session-1" {
		t.Fatalf("provider location=%q failed=%q", location, store.failed)
	}

	handler, store = newHandler()
	handler.auth.findLinkedUser = func(string) (models.User, error) { return models.User{}, errors.New("database unavailable") }
	if location := request(handler); location != "/login?facebook_login=database_error" || store.failed != "session-1" {
		t.Fatalf("database location=%q failed=%q", location, store.failed)
	}

	handler, store = newHandler()
	handler.generateRefreshToken = func(models.User) (string, error) { return "", errors.New("signing failed") }
	response := facebookOAuthRequest(handler.Callback, http.MethodGet, "/api/v1/channels/facebook/callback?state=signin_secret&code=code", "", "", "", &http.Cookie{Name: facebookLoginOAuthCookie, Value: "browser"})
	if response.Header().Get("Location") != "/login?facebook_login=database_error" || store.failed != "session-1" {
		t.Fatalf("signer location=%q failed=%q", response.Header().Get("Location"), store.failed)
	}
	for _, cookie := range response.Result().Cookies() {
		if cookie.Name == "cqa_refresh_token" && cookie.Value != "" {
			t.Fatal("refresh cookie must not be issued when signing fails")
		}
	}
}

func TestFacebookLoginOAuthCallbackConsumesProviderDenial(t *testing.T) {
	store := &fakeFacebookLoginOAuthStore{claim: func(string, string, time.Time) (models.FacebookLoginOAuthSession, error) {
		return models.FacebookLoginOAuthSession{ID: "session-1"}, nil
	}}
	handler := newFacebookLoginOAuthTestHandler(store)
	response := facebookOAuthRequest(handler.Callback, http.MethodGet, "/api/v1/channels/facebook/callback?state=signin_secret&error=access_denied", "", "", "", &http.Cookie{Name: facebookLoginOAuthCookie, Value: "browser"})
	if response.Header().Get("Location") != "/login?facebook_login=oauth_denied" || store.failed != "session-1" {
		t.Fatalf("location=%q failed=%q", response.Header().Get("Location"), store.failed)
	}
}
