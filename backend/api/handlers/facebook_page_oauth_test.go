package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/vietbui/chat-quality-agent/config"
	"github.com/vietbui/chat-quality-agent/db/models"
	"github.com/vietbui/chat-quality-agent/pkg"
)

const facebookOAuthTestKey = "0123456789abcdef0123456789abcdef"

type fakeFacebookPageOAuthStore struct {
	created       *models.FacebookOAuthSession
	claim         func(string, string, time.Time) (models.FacebookOAuthSession, error)
	load          func(string, string, string, time.Time) (models.FacebookOAuthSession, error)
	reserve       func(string, string, string, string, time.Time) (models.FacebookOAuthSession, error)
	selectPage    func(models.FacebookOAuthSession, facebookOAuthPage, facebookPageSelectRequest, []byte, time.Time) (models.Channel, bool, error)
	authorized    []byte
	failedSession string
}

type facebookOAuthRoundTripFunc func(*http.Request) (*http.Response, error)

func (f facebookOAuthRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func facebookOAuthJSONResponse(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
}

func (f *fakeFacebookPageOAuthStore) Create(_ context.Context, session *models.FacebookOAuthSession) error {
	copy := *session
	f.created = &copy
	return nil
}
func (f *fakeFacebookPageOAuthStore) Claim(_ context.Context, stateHash, browserHash string, now time.Time) (models.FacebookOAuthSession, error) {
	if f.claim == nil {
		return models.FacebookOAuthSession{}, errFacebookPageOAuthInvalid
	}
	return f.claim(stateHash, browserHash, now)
}
func (f *fakeFacebookPageOAuthStore) Authorize(_ context.Context, _ string, pages []byte, _ time.Time) error {
	f.authorized = pages
	return nil
}
func (f *fakeFacebookPageOAuthStore) Load(_ context.Context, id, tenantID, userID string, now time.Time) (models.FacebookOAuthSession, error) {
	if f.load == nil {
		return models.FacebookOAuthSession{}, errFacebookPageOAuthInvalid
	}
	return f.load(id, tenantID, userID, now)
}
func (f *fakeFacebookPageOAuthStore) Reserve(_ context.Context, id, tenantID, userID, pageID string, now time.Time) (models.FacebookOAuthSession, error) {
	if f.reserve == nil {
		return models.FacebookOAuthSession{}, errFacebookPageOAuthReplay
	}
	return f.reserve(id, tenantID, userID, pageID, now)
}
func (f *fakeFacebookPageOAuthStore) Select(_ context.Context, session models.FacebookOAuthSession, page facebookOAuthPage, req facebookPageSelectRequest, credentials []byte, now time.Time) (models.Channel, bool, error) {
	return f.selectPage(session, page, req, credentials, now)
}
func (f *fakeFacebookPageOAuthStore) Fail(_ context.Context, id string) error {
	f.failedSession = id
	return nil
}
func (*fakeFacebookPageOAuthStore) Cleanup(context.Context, time.Time) {}

func newFacebookPageOAuthTestHandler(store facebookPageOAuthStore) *FacebookPageOAuthHandler {
	return &FacebookPageOAuthHandler{
		cfg:          &config.Config{FacebookAppID: "app-1", FacebookAppSecret: "app-secret", FacebookAPIVersion: "v26.0", FacebookPageLoginConfigID: "config-1", EncryptionKey: facebookOAuthTestKey},
		graphBaseURL: facebookGraphBaseURL,
		httpClient:   &http.Client{Timeout: time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
		now:          func() time.Time { return time.Date(2026, 9, 30, 1, 2, 3, 0, time.UTC) },
		store:        store,
	}
}

func facebookOAuthRequest(handler gin.HandlerFunc, method, path, body, tenantID, userID string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	routePath := strings.SplitN(path, "?", 2)[0]
	router.Handle(method, routePath, func(c *gin.Context) {
		c.Set("tenant_id", tenantID)
		c.Set("user_id", userID)
		handler(c)
	})
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}
	response := httptest.NewRecorder()
	router.ServeHTTP(response, req)
	return response
}

func TestFacebookPageOAuthStartUsesBusinessConfigAndBrowserBoundState(t *testing.T) {
	store := &fakeFacebookPageOAuthStore{}
	handler := newFacebookPageOAuthTestHandler(store)
	response := facebookOAuthRequest(handler.Start, http.MethodPost, "/api/v1/tenants/tenant-a/facebook/connect", "", "tenant-a", "user-a")
	if response.Code != http.StatusOK || store.created == nil {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if store.created.TenantID != "tenant-a" || store.created.UserID != "user-a" || store.created.Status != "pending" {
		t.Fatalf("unexpected session: %#v", store.created)
	}
	cookies := response.Result().Cookies()
	if len(cookies) != 1 || !cookies[0].HttpOnly || !cookies[0].Secure || cookies[0].SameSite != http.SameSiteLaxMode {
		t.Fatalf("unsafe OAuth cookie: %#v", cookies)
	}
	if hashFacebookOAuthSecret(cookies[0].Value) != store.created.BrowserHash {
		t.Fatal("browser verifier is not bound to stored hash")
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
	if redirect.Query().Get("config_id") != "config-1" || redirect.Query().Get("response_type") != "code" {
		t.Fatalf("invalid business login URL: %s", payload.RedirectURL)
	}
	if redirect.Query().Get("scope") != "" {
		t.Fatal("business login URL must rely on config_id permissions")
	}
	state := redirect.Query().Get("state")
	if !strings.HasPrefix(state, facebookPageOAuthStatePrefix) || hashFacebookOAuthSecret(state) != store.created.StateHash {
		t.Fatal("state was not stored as a hash")
	}
	if response.Header().Get("Cache-Control") != "no-store" || response.Header().Get("Referrer-Policy") != "no-referrer" {
		t.Fatal("OAuth response may be cached or leaked by referrer")
	}
}

func TestFacebookPageOAuthCallbackConsumesDeniedStateAndRejectsReplay(t *testing.T) {
	state, browser := "page_state-secret", "browser-secret"
	store := &fakeFacebookPageOAuthStore{}
	claimed := false
	store.claim = func(stateHash, browserHash string, _ time.Time) (models.FacebookOAuthSession, error) {
		if claimed {
			return models.FacebookOAuthSession{}, errFacebookPageOAuthReplay
		}
		if stateHash != hashFacebookOAuthSecret(state) || browserHash != hashFacebookOAuthSecret(browser) {
			return models.FacebookOAuthSession{}, errFacebookPageOAuthInvalid
		}
		claimed = true
		return models.FacebookOAuthSession{ID: "session-a", TenantID: "tenant-a"}, nil
	}
	handler := newFacebookPageOAuthTestHandler(store)
	requestPath := "/api/v1/channels/facebook/callback?state=" + url.QueryEscape(state) + "&error=access_denied"
	first := facebookOAuthRequest(handler.Callback, http.MethodGet, requestPath, "", "", "", &http.Cookie{Name: facebookPageOAuthCookie, Value: browser})
	if first.Code != http.StatusFound || first.Header().Get("Location") != "/tenant-a/channels?facebook_connect_error=oauth_denied" || store.failedSession != "session-a" {
		t.Fatalf("denied callback not consumed safely: status=%d location=%s", first.Code, first.Header().Get("Location"))
	}
	second := facebookOAuthRequest(handler.Callback, http.MethodGet, requestPath, "", "", "", &http.Cookie{Name: facebookPageOAuthCookie, Value: browser})
	if second.Code != http.StatusFound || !strings.Contains(second.Header().Get("Location"), "facebook_connect_error=session_used") {
		t.Fatalf("replay accepted: %s", second.Header().Get("Location"))
	}
}

func TestFacebookPageOAuthCallbackRequiresBrowserCookieBeforeClaim(t *testing.T) {
	store := &fakeFacebookPageOAuthStore{claim: func(string, string, time.Time) (models.FacebookOAuthSession, error) {
		t.Fatal("state must not be claimed without browser cookie")
		return models.FacebookOAuthSession{}, nil
	}}
	response := facebookOAuthRequest(newFacebookPageOAuthTestHandler(store).Callback, http.MethodGet, "/api/v1/channels/facebook/callback?state=page_secret&code=code", "", "", "")
	if response.Code != http.StatusFound || response.Header().Get("Location") != "/login?facebook_connect_error=browser_mismatch" {
		t.Fatalf("unexpected response: %s", response.Header().Get("Location"))
	}
}

func TestFacebookPageOAuthPagesAreTenantAndUserBound(t *testing.T) {
	store := &fakeFacebookPageOAuthStore{load: func(id, tenantID, userID string, _ time.Time) (models.FacebookOAuthSession, error) {
		if id == "session-a" && tenantID == "tenant-a" && userID == "user-a" {
			return models.FacebookOAuthSession{}, nil
		}
		return models.FacebookOAuthSession{}, errFacebookPageOAuthInvalid
	}}
	handler := newFacebookPageOAuthTestHandler(store)
	response := facebookOAuthRequest(handler.Pages, http.MethodGet, "/api/v1/tenants/tenant-b/facebook/connect/session-a", "", "tenant-b", "user-a")
	if response.Code != http.StatusNotFound || !strings.Contains(response.Body.String(), "session_not_found") {
		t.Fatalf("cross-tenant session exposed: %d %s", response.Code, response.Body.String())
	}
}

func TestFacebookPageOAuthFetchPagesUsesCursorOnlyAndFiltersMessagingTask(t *testing.T) {
	requests := 0
	transport := facebookOAuthRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		requests++
		if r.Header.Get("Authorization") != "Bearer user-token" || r.URL.Query().Get("appsecret_proof") == "" {
			t.Error("Graph request missing bearer/proof")
		}
		if r.URL.Host != "graph.test" || r.URL.Path != "/v26.0/me/accounts" {
			t.Errorf("request escaped fixed Graph host: %s", r.URL)
		}
		if requests == 1 {
			return facebookOAuthJSONResponse(http.StatusOK, `{"data":[{"id":"skip","name":"No messages","access_token":"x","tasks":["ANALYZE"]},{"id":"page-1","name":"Page 1","access_token":"token-1","tasks":["MODERATE"]}],"paging":{"cursors":{"after":"cursor-1"},"next":"https://attacker.invalid/steal"}}`), nil
		}
		if r.URL.Query().Get("after") != "cursor-1" {
			t.Errorf("cursor not reconstructed on fixed Graph host: %s", r.URL.RawQuery)
		}
		return facebookOAuthJSONResponse(http.StatusOK, `{"data":[{"id":"page-2","name":"Page 2","access_token":"token-2"}],"paging":{}}`), nil
	})
	handler := newFacebookPageOAuthTestHandler(&fakeFacebookPageOAuthStore{})
	handler.graphBaseURL = "https://graph.test"
	handler.httpClient.Transport = transport
	pages, err := handler.fetchPages(context.Background(), "user-token")
	if err != nil || len(pages) != 2 || pages[0].ID != "page-1" || pages[1].ID != "page-2" || requests != 2 {
		t.Fatalf("pages=%#v requests=%d err=%v", pages, requests, err)
	}
}

func TestFacebookPageOAuthSubscribeRequestsReferralFieldsWithoutTokenInURL(t *testing.T) {
	handler := newFacebookPageOAuthTestHandler(&fakeFacebookPageOAuthStore{})
	handler.graphBaseURL = "https://graph.test"
	handler.httpClient.Transport = facebookOAuthRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path != "/v26.0/page-1/subscribed_apps" || req.URL.Query().Get("access_token") != "" {
			t.Fatalf("unsafe subscribe URL: %s", req.URL)
		}
		if req.Header.Get("Authorization") != "Bearer page-token" {
			t.Fatalf("missing bearer token")
		}
		body, _ := io.ReadAll(req.Body)
		values, _ := url.ParseQuery(string(body))
		if values.Get("subscribed_fields") != "messages,messaging_postbacks,messaging_referrals" {
			t.Fatalf("fields=%q", values.Get("subscribed_fields"))
		}
		return facebookOAuthJSONResponse(http.StatusOK, `{"success":true}`), nil
	})
	if err := handler.subscribePage(context.Background(), facebookOAuthPage{ID: "page-1", AccessToken: "page-token"}); err != nil {
		t.Fatal(err)
	}
}

func TestFacebookPageOAuthStorageFailureIsNotMaskedAsMissingSession(t *testing.T) {
	store := &fakeFacebookPageOAuthStore{load: func(string, string, string, time.Time) (models.FacebookOAuthSession, error) {
		return models.FacebookOAuthSession{}, errors.New("database unavailable")
	}}
	response := facebookOAuthRequest(newFacebookPageOAuthTestHandler(store).Pages, http.MethodGet, "/api/v1/tenants/tenant-a/facebook/connect/session-a", "", "tenant-a", "user-a")
	if response.Code != http.StatusInternalServerError || !strings.Contains(response.Body.String(), "connect_failed") {
		t.Fatalf("storage failure masked: %d %s", response.Code, response.Body.String())
	}
}

func TestFacebookPageOAuthLosingSelectionNeverSubscribesAnotherPage(t *testing.T) {
	pageData, _ := json.Marshal([]facebookOAuthPage{{ID: "page-1", Name: "Page 1", AccessToken: "token-1"}, {ID: "page-2", Name: "Page 2", AccessToken: "token-2"}})
	encrypted, _ := pkg.Encrypt(pageData, facebookOAuthTestKey)
	store := &fakeFacebookPageOAuthStore{
		load: func(_, tenantID, userID string, _ time.Time) (models.FacebookOAuthSession, error) {
			return models.FacebookOAuthSession{ID: "session-a", TenantID: tenantID, UserID: userID, Status: "selecting", SelectedPageID: "page-1", PagesEncrypted: encrypted}, nil
		},
		reserve: func(_, _, _, pageID string, _ time.Time) (models.FacebookOAuthSession, error) {
			if pageID != "page-1" {
				return models.FacebookOAuthSession{}, errFacebookPageOAuthReplay
			}
			return models.FacebookOAuthSession{ID: "session-a", SelectedPageID: pageID}, nil
		},
	}
	handler := newFacebookPageOAuthTestHandler(store)
	handler.httpClient.Transport = facebookOAuthRoundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("losing Page selection reached Facebook")
		return nil, nil
	})
	response := facebookOAuthRequest(handler.Select, http.MethodPost, "/api/v1/tenants/tenant-a/facebook/connect/session-a/select", `{"page_id":"page-2","sync_interval":1}`, "tenant-a", "user-a")
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "session_used") {
		t.Fatalf("losing selection accepted: %d %s", response.Code, response.Body.String())
	}
}

func TestFacebookPageOAuthSelectRejectsPageOutsideAuthorizedPayload(t *testing.T) {
	pageData, err := json.Marshal([]facebookOAuthPage{{ID: "page-1", Name: "Page 1", AccessToken: "token-1"}})
	if err != nil {
		t.Fatal(err)
	}
	encrypted, err := pkg.Encrypt(pageData, facebookOAuthTestKey)
	if err != nil {
		t.Fatal(err)
	}
	store := &fakeFacebookPageOAuthStore{load: func(_, tenantID, userID string, _ time.Time) (models.FacebookOAuthSession, error) {
		if tenantID != "tenant-a" || userID != "user-a" {
			return models.FacebookOAuthSession{}, errors.New("wrong binding")
		}
		return models.FacebookOAuthSession{ID: "session-a", TenantID: tenantID, UserID: userID, PagesEncrypted: encrypted}, nil
	}}
	response := facebookOAuthRequest(newFacebookPageOAuthTestHandler(store).Select, http.MethodPost, "/api/v1/tenants/tenant-a/facebook/connect/session-a/select", `{"page_id":"page-other","sync_interval":15,"sync_files":false}`, "tenant-a", "user-a")
	if response.Code != http.StatusForbidden || !strings.Contains(response.Body.String(), "page_not_found") {
		t.Fatalf("unauthorized page accepted: %d %s", response.Code, response.Body.String())
	}
}
