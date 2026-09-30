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
)

type fakeFacebookAccountOAuthStore struct {
	created   *models.FacebookAccountOAuthSession
	claim     func(string, string, time.Time) (models.FacebookAccountOAuthSession, error)
	completed string
	failed    string
}

func (f *fakeFacebookAccountOAuthStore) Create(_ context.Context, session *models.FacebookAccountOAuthSession) error {
	copy := *session
	f.created = &copy
	return nil
}
func (f *fakeFacebookAccountOAuthStore) Claim(_ context.Context, stateHash, browserHash string, now time.Time) (models.FacebookAccountOAuthSession, error) {
	if f.claim == nil {
		return models.FacebookAccountOAuthSession{}, errFacebookAccountOAuthInvalid
	}
	return f.claim(stateHash, browserHash, now)
}
func (f *fakeFacebookAccountOAuthStore) Complete(_ context.Context, id string, _ time.Time) error {
	f.completed = id
	return nil
}
func (f *fakeFacebookAccountOAuthStore) Fail(_ context.Context, id string, _ time.Time) error {
	f.failed = id
	return nil
}
func (*fakeFacebookAccountOAuthStore) Cleanup(context.Context, time.Time) {}

func newFacebookAccountOAuthTestHandler(store facebookAccountOAuthStore) *FacebookAccountOAuthHandler {
	auth := &FacebookAuthHandler{
		appID: "app-1", appSecret: "secret-1", apiVersion: "v26.0", graphBaseURL: facebookGraphBaseURL,
		verifyPassword: func(string, string) error { return nil },
		verifyToken: func(context.Context, string) (facebookProfile, error) {
			return facebookProfile{ID: "facebook-1", Name: "Test User"}, nil
		},
		linkIdentity: func(string, facebookProfile) error { return nil },
	}
	return &FacebookAccountOAuthHandler{
		auth: auth, store: store,
		now: func() time.Time { return time.Date(2026, 9, 30, 1, 2, 3, 0, time.UTC) },
		loadCredentialHash: func(context.Context, string) (string, error) {
			return hashFacebookOAuthSecret("password-hash"), nil
		},
		exchangeCode: func(context.Context, string, string) (string, error) { return "access-token", nil },
		logLinked:    func(*gin.Context, string, facebookProfile) {},
	}
}

func TestFacebookAccountOAuthStartCreatesBrowserBoundCodeFlow(t *testing.T) {
	store := &fakeFacebookAccountOAuthStore{}
	handler := newFacebookAccountOAuthTestHandler(store)
	response := facebookOAuthRequest(handler.Start, http.MethodPost, "/api/v1/profile/facebook/link/start", `{"current_password":"password","return_path":"/tenant-a/settings"}`, "", "user-a")
	if response.Code != http.StatusOK || store.created == nil {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if store.created.UserID != "user-a" || store.created.CredentialHash != hashFacebookOAuthSecret("password-hash") || store.created.ReturnPath != "/tenant-a/settings" || store.created.Status != "pending" {
		t.Fatalf("unexpected session: %#v", store.created)
	}
	cookies := response.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != facebookAccountOAuthCookie || !cookies[0].HttpOnly || !cookies[0].Secure || cookies[0].SameSite != http.SameSiteLaxMode {
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
	if redirect.Host != "www.facebook.com" || query.Get("client_id") != "app-1" || query.Get("scope") != "public_profile" || query.Get("response_type") != "code" || query.Get("config_id") != "" {
		t.Fatalf("invalid account OAuth URL: %s", payload.RedirectURL)
	}
	if state := query.Get("state"); !strings.HasPrefix(state, facebookAccountOAuthStatePrefix) || hashFacebookOAuthSecret(state) != store.created.StateHash {
		t.Fatal("state was not stored as a hash")
	}
}

func TestFacebookAccountOAuthStartCapturesCredentialBeforeVerification(t *testing.T) {
	store := &fakeFacebookAccountOAuthStore{}
	handler := newFacebookAccountOAuthTestHandler(store)
	credential := hashFacebookOAuthSecret("original-hash")
	handler.loadCredentialHash = func(context.Context, string) (string, error) { return credential, nil }
	handler.auth.verifyPassword = func(string, string) error {
		credential = hashFacebookOAuthSecret("replacement-hash")
		return nil
	}
	response := facebookOAuthRequest(handler.Start, http.MethodPost, "/api/v1/profile/facebook/link/start", `{"current_password":"password"}`, "", "user-a")
	if response.Code != http.StatusOK || store.created.CredentialHash != hashFacebookOAuthSecret("original-hash") {
		t.Fatal("password replacement during verification must not authorize the new credential")
	}
}

func TestFacebookAccountOAuthStartUsesConfiguredBusinessLogin(t *testing.T) {
	store := &fakeFacebookAccountOAuthStore{}
	handler := newFacebookAccountOAuthTestHandler(store)
	handler.auth.loginConfigID = "business-config"
	response := facebookOAuthRequest(handler.Start, http.MethodPost, "/api/v1/profile/facebook/link/start", `{"current_password":"password","return_path":"/"}`, "", "user-a")
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
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
	if redirect.Query().Get("config_id") != "business-config" || redirect.Query().Get("scope") != "" {
		t.Fatalf("business login must use config permissions only: %s", payload.RedirectURL)
	}
}

func TestFacebookAccountOAuthStartRejectsWrongPassword(t *testing.T) {
	store := &fakeFacebookAccountOAuthStore{}
	handler := newFacebookAccountOAuthTestHandler(store)
	handler.auth.verifyPassword = func(string, string) error { return errFacebookWrongPassword }
	response := facebookOAuthRequest(handler.Start, http.MethodPost, "/api/v1/profile/facebook/link/start", `{"current_password":"wrong"}`, "", "user-a")
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "wrong_current_password") || store.created != nil {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestFacebookAccountOAuthStartSanitizesReturnPath(t *testing.T) {
	store := &fakeFacebookAccountOAuthStore{}
	handler := newFacebookAccountOAuthTestHandler(store)
	response := facebookOAuthRequest(handler.Start, http.MethodPost, "/api/v1/profile/facebook/link/start", `{"current_password":"password","return_path":"//attacker.example/steal"}`, "", "user-a")
	if response.Code != http.StatusOK || store.created == nil || store.created.ReturnPath != "/" {
		t.Fatalf("unsafe return path accepted: %#v", store.created)
	}
}

func TestFacebookAccountOAuthCallbackLinksBoundUserAndUsesStoredRedirect(t *testing.T) {
	state, browser := "link_state-secret", "browser-secret"
	const storedRedirect = "https://crm.example.com/api/v1/channels/facebook/callback"
	store := &fakeFacebookAccountOAuthStore{claim: func(stateHash, browserHash string, _ time.Time) (models.FacebookAccountOAuthSession, error) {
		if stateHash != hashFacebookOAuthSecret(state) || browserHash != hashFacebookOAuthSecret(browser) {
			return models.FacebookAccountOAuthSession{}, errFacebookAccountOAuthInvalid
		}
		return models.FacebookAccountOAuthSession{ID: "session-a", UserID: "user-a", RedirectURI: storedRedirect, ReturnPath: "/tenant-a/settings"}, nil
	}}
	handler := newFacebookAccountOAuthTestHandler(store)
	var exchangedRedirect, linkedUser string
	handler.exchangeCode = func(_ context.Context, code, redirectURI string) (string, error) {
		if code != "code-a" {
			t.Fatalf("code=%q", code)
		}
		exchangedRedirect = redirectURI
		return "access-token", nil
	}
	handler.auth.linkIdentity = func(userID string, profile facebookProfile) error {
		linkedUser = userID
		if profile.ID != "facebook-1" {
			t.Fatalf("profile=%#v", profile)
		}
		return nil
	}
	response := facebookOAuthRequest(handler.Callback, http.MethodGet, "/api/v1/channels/facebook/callback?state="+url.QueryEscape(state)+"&code=code-a", "", "", "", &http.Cookie{Name: facebookAccountOAuthCookie, Value: browser})
	if response.Code != http.StatusFound || response.Header().Get("Location") != "/tenant-a/settings?facebook_link=success" {
		t.Fatalf("status=%d location=%s", response.Code, response.Header().Get("Location"))
	}
	if exchangedRedirect != storedRedirect || linkedUser != "user-a" || store.completed != "session-a" {
		t.Fatalf("redirect=%q user=%q completed=%q", exchangedRedirect, linkedUser, store.completed)
	}
}

func TestFacebookAccountOAuthCallbackConsumesDenialAndRejectsReplay(t *testing.T) {
	state, browser := "link_state-secret", "browser-secret"
	claimed := false
	store := &fakeFacebookAccountOAuthStore{claim: func(string, string, time.Time) (models.FacebookAccountOAuthSession, error) {
		if claimed {
			return models.FacebookAccountOAuthSession{ReturnPath: "/tenant-a/settings"}, errFacebookAccountOAuthReplay
		}
		claimed = true
		return models.FacebookAccountOAuthSession{ID: "session-a", ReturnPath: "/tenant-a/settings"}, nil
	}}
	handler := newFacebookAccountOAuthTestHandler(store)
	path := "/api/v1/channels/facebook/callback?state=" + url.QueryEscape(state) + "&error=access_denied"
	first := facebookOAuthRequest(handler.Callback, http.MethodGet, path, "", "", "", &http.Cookie{Name: facebookAccountOAuthCookie, Value: browser})
	if first.Header().Get("Location") != "/tenant-a/settings?facebook_link=oauth_denied" || store.failed != "session-a" {
		t.Fatalf("denial not consumed: %s", first.Header().Get("Location"))
	}
	second := facebookOAuthRequest(handler.Callback, http.MethodGet, path, "", "", "", &http.Cookie{Name: facebookAccountOAuthCookie, Value: browser})
	if second.Header().Get("Location") != "/tenant-a/settings?facebook_link=session_used" {
		t.Fatalf("replay not rejected: %s", second.Header().Get("Location"))
	}
}

func TestFacebookAccountOAuthCallbackRejectsMissingBrowserCookieWithoutClaim(t *testing.T) {
	store := &fakeFacebookAccountOAuthStore{claim: func(string, string, time.Time) (models.FacebookAccountOAuthSession, error) {
		t.Fatal("must not claim state without browser cookie")
		return models.FacebookAccountOAuthSession{}, nil
	}}
	response := facebookOAuthRequest(newFacebookAccountOAuthTestHandler(store).Callback, http.MethodGet, "/api/v1/channels/facebook/callback?state=link_secret&code=code", "", "", "")
	if response.Code != http.StatusFound || response.Header().Get("Location") != "/?facebook_link=browser_mismatch" {
		t.Fatalf("unexpected response: %s", response.Header().Get("Location"))
	}
}

func TestFacebookAccountOAuthCallbackReturnsStableIdentityConflict(t *testing.T) {
	store := &fakeFacebookAccountOAuthStore{claim: func(string, string, time.Time) (models.FacebookAccountOAuthSession, error) {
		return models.FacebookAccountOAuthSession{ID: "session-a", UserID: "user-a", RedirectURI: "https://crm.example.com/api/v1/channels/facebook/callback", ReturnPath: "/"}, nil
	}}
	handler := newFacebookAccountOAuthTestHandler(store)
	handler.auth.linkIdentity = func(string, facebookProfile) error { return errFacebookIdentityAlreadyLinked }
	response := facebookOAuthRequest(handler.Callback, http.MethodGet, "/api/v1/channels/facebook/callback?state=link_secret&code=code", "", "", "", &http.Cookie{Name: facebookAccountOAuthCookie, Value: "browser"})
	if response.Header().Get("Location") != "/?facebook_link=facebook_account_already_linked" || store.failed != "session-a" {
		t.Fatalf("unexpected conflict response: %s", response.Header().Get("Location"))
	}
}

func TestFacebookAccountOAuthCallbackMapsExpiredState(t *testing.T) {
	store := &fakeFacebookAccountOAuthStore{claim: func(string, string, time.Time) (models.FacebookAccountOAuthSession, error) {
		return models.FacebookAccountOAuthSession{ReturnPath: "/tenant-a/settings"}, errFacebookAccountOAuthExpired
	}}
	response := facebookOAuthRequest(newFacebookAccountOAuthTestHandler(store).Callback, http.MethodGet, "/api/v1/channels/facebook/callback?state=link_secret&code=code", "", "", "", &http.Cookie{Name: facebookAccountOAuthCookie, Value: "browser"})
	if response.Header().Get("Location") != "/tenant-a/settings?facebook_link=session_expired" {
		t.Fatalf("unexpected expired response: %s", response.Header().Get("Location"))
	}
}

func TestFacebookAccountOAuthCallbackMapsClaimDatabaseFailure(t *testing.T) {
	store := &fakeFacebookAccountOAuthStore{claim: func(string, string, time.Time) (models.FacebookAccountOAuthSession, error) {
		return models.FacebookAccountOAuthSession{ReturnPath: "/tenant-a/settings"}, errors.New("database unavailable")
	}}
	response := facebookOAuthRequest(newFacebookAccountOAuthTestHandler(store).Callback, http.MethodGet, "/api/v1/channels/facebook/callback?state=link_secret&code=code", "", "", "", &http.Cookie{Name: facebookAccountOAuthCookie, Value: "browser"})
	if response.Header().Get("Location") != "/tenant-a/settings?facebook_link=connect_failed" {
		t.Fatalf("unexpected database failure response: %s", response.Header().Get("Location"))
	}
}

func TestFacebookAccountOAuthCallbackMapsProviderFailure(t *testing.T) {
	store := &fakeFacebookAccountOAuthStore{claim: func(string, string, time.Time) (models.FacebookAccountOAuthSession, error) {
		return models.FacebookAccountOAuthSession{ID: "session-a", RedirectURI: "https://crm.example.com/api/v1/channels/facebook/callback", ReturnPath: "/"}, nil
	}}
	handler := newFacebookAccountOAuthTestHandler(store)
	handler.exchangeCode = func(context.Context, string, string) (string, error) { return "", errors.New("offline") }
	response := facebookOAuthRequest(handler.Callback, http.MethodGet, "/api/v1/channels/facebook/callback?state=link_secret&code=code", "", "", "", &http.Cookie{Name: facebookAccountOAuthCookie, Value: "browser"})
	if response.Header().Get("Location") != "/?facebook_link=facebook_unavailable" || store.failed != "session-a" {
		t.Fatalf("unexpected provider response: %s", response.Header().Get("Location"))
	}
}
