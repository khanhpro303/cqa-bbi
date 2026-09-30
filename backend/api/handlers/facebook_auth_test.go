package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	mysqlDriver "github.com/go-sql-driver/mysql"
	"github.com/vietbui/chat-quality-agent/api/middleware"
	"github.com/vietbui/chat-quality-agent/config"
	"github.com/vietbui/chat-quality-agent/db/models"
	"gorm.io/gorm"
)

func TestFacebookPublicConfig(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Run("disabled without secret", func(t *testing.T) {
		handler := NewFacebookAuthHandler(&config.Config{FacebookAppID: "123", FacebookAPIVersion: "v26.0"})
		response := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(response)

		handler.PublicConfig(ctx)

		if response.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
		}
		if response.Body.String() != `{"enabled":false}` {
			t.Fatalf("body = %s, want disabled config", response.Body.String())
		}
	})

	t.Run("never exposes app secret", func(t *testing.T) {
		handler := NewFacebookAuthHandler(&config.Config{
			FacebookAppID:             "123",
			FacebookAppSecret:         "do-not-expose",
			FacebookAPIVersion:        "v26.0",
			FacebookPageLoginConfigID: "business-config",
		})
		response := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(response)

		handler.PublicConfig(ctx)

		var body map[string]any
		if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		if body["enabled"] != true || body["app_id"] != "123" || body["api_version"] != "v26.0" || body["login_config_id"] != "business-config" {
			t.Fatalf("unexpected public config: %#v", body)
		}
		if _, exists := body["app_secret"]; exists {
			t.Fatalf("public config exposed app_secret: %#v", body)
		}
	})
}

func TestVerifyFacebookAccessToken(t *testing.T) {
	const (
		appID       = "123"
		appSecret   = "test-secret"
		accessToken = "user-token"
	)

	var handler *FacebookAuthHandler
	httpClient := &http.Client{Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		switch r.URL.Path {
		case "/v26.0/debug_token":
			if got := r.URL.Query().Get("input_token"); got != accessToken {
				t.Errorf("input_token = %q, want %q", got, accessToken)
			}
			if got := r.URL.Query().Get("access_token"); got != appID+"|"+appSecret {
				t.Errorf("app access token = %q, want app ID and secret", got)
			}
			return jsonResponse(http.StatusOK, `{"data":{"app_id":"123","is_valid":true,"user_id":"fb-user-1"}}`), nil
		case "/v26.0/me":
			if got := r.Header.Get("Authorization"); got != "Bearer "+accessToken {
				t.Errorf("Authorization = %q, want bearer token", got)
			}
			if got := r.URL.Query().Get("appsecret_proof"); got != handler.appSecretProof(accessToken) {
				t.Errorf("appsecret_proof = %q, want HMAC proof", got)
			}
			if got := r.URL.Query().Get("fields"); got != "id,name" {
				t.Errorf("fields = %q, want id,name only", got)
			}
			return jsonResponse(http.StatusOK, `{"id":"fb-user-1","name":"Test User","email":"USER@example.com"}`), nil
		default:
			return jsonResponse(http.StatusNotFound, `{}`), nil
		}
	})}

	handler = NewFacebookAuthHandler(&config.Config{
		FacebookAppID:      appID,
		FacebookAppSecret:  appSecret,
		FacebookAPIVersion: "v26.0",
	})
	handler.graphBaseURL = "https://graph.test"
	handler.httpClient = httpClient

	profile, err := handler.verifyAccessToken(t.Context(), accessToken)
	if err != nil {
		t.Fatalf("verifyAccessToken returned error: %v", err)
	}
	if profile.ID != "fb-user-1" || profile.Name != "Test User" {
		t.Fatalf("profile = %#v, want verified Facebook identity", profile)
	}
}

func TestVerifyFacebookAccessTokenRejectsWrongApp(t *testing.T) {
	handler := NewFacebookAuthHandler(&config.Config{
		FacebookAppID:      "expected-app",
		FacebookAppSecret:  "secret",
		FacebookAPIVersion: "v26.0",
	})
	handler.graphBaseURL = "https://graph.test"
	handler.httpClient = &http.Client{Transport: roundTripperFunc(func(_ *http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, `{"data":{"app_id":"different-app","is_valid":true,"user_id":"fb-user-1"}}`), nil
	})}

	if _, err := handler.verifyAccessToken(t.Context(), "token"); err == nil {
		t.Fatal("verifyAccessToken accepted a token issued for another app")
	}
}

func TestVerifyFacebookAccessTokenDoesNotRequireEmail(t *testing.T) {
	httpClient := &http.Client{Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/v26.0/debug_token" {
			return jsonResponse(http.StatusOK, `{"data":{"app_id":"123","is_valid":true,"user_id":"fb-user-1"}}`), nil
		}
		return jsonResponse(http.StatusOK, `{"id":"fb-user-1","name":"No Email"}`), nil
	})}

	handler := NewFacebookAuthHandler(&config.Config{
		FacebookAppID:      "123",
		FacebookAppSecret:  "secret",
		FacebookAPIVersion: "v26.0",
	})
	handler.graphBaseURL = "https://graph.test"
	handler.httpClient = httpClient

	profile, err := handler.verifyAccessToken(t.Context(), "token")
	if err != nil {
		t.Fatalf("verifyAccessToken returned error: %v", err)
	}
	if profile.ID != "fb-user-1" || profile.Name != "No Email" {
		t.Fatalf("profile = %#v, want verified identity without email", profile)
	}
}

func TestVerifyFacebookAccessTokenRedactsCredentialsFromTransportErrors(t *testing.T) {
	handler := NewFacebookAuthHandler(&config.Config{
		FacebookAppID:      "123",
		FacebookAppSecret:  "super-secret",
		FacebookAPIVersion: "v26.0",
	})
	handler.httpClient = &http.Client{Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		return nil, errors.New("request failed for " + r.URL.String())
	})}

	_, err := handler.verifyAccessToken(t.Context(), "user-access-token")
	if err == nil {
		t.Fatal("verifyAccessToken unexpectedly succeeded")
	}
	if strings.Contains(err.Error(), "user-access-token") || strings.Contains(err.Error(), "super-secret") {
		t.Fatalf("error leaked Facebook credentials: %v", err)
	}
}

func TestFacebookLoginIssuesApplicationTokens(t *testing.T) {
	middleware.SetJWTSecret("test-jwt-secret-at-least-32-chars-long")
	handler := configuredFacebookLoginHandler()
	handler.findLinkedUser = func(facebookUserID string) (models.User, error) {
		if facebookUserID != "fb-user-1" {
			t.Fatalf("lookup Facebook user ID = %q, want fb-user-1", facebookUserID)
		}
		return models.User{
			ID:           "user-1",
			Email:        "user@example.com",
			IsAdmin:      true,
			TokenVersion: 4,
		}, nil
	}

	response := performFacebookLogin(handler, `{"access_token":"facebook-token"}`)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusOK, response.Body.String())
	}

	var tokenResponse TokenResponse
	if err := json.Unmarshal(response.Body.Bytes(), &tokenResponse); err != nil {
		t.Fatalf("decode token response: %v", err)
	}
	claims, err := middleware.ParseToken(tokenResponse.AccessToken)
	if err != nil {
		t.Fatalf("parse issued access token: %v", err)
	}
	if claims.UserID != "user-1" || claims.Email != "user@example.com" || !claims.IsAdmin {
		t.Fatalf("unexpected access token claims: %#v", claims)
	}

	setCookie := response.Header().Get("Set-Cookie")
	for _, attribute := range []string{"cqa_refresh_token=", "Path=/api/v1/auth", "HttpOnly", "SameSite=Strict"} {
		if !strings.Contains(setCookie, attribute) {
			t.Errorf("Set-Cookie %q missing %q", setCookie, attribute)
		}
	}
}

func TestFacebookLoginDistinguishesMissingAccountFromDatabaseFailure(t *testing.T) {
	tests := []struct {
		name       string
		lookupErr  error
		wantStatus int
		wantError  string
	}{
		{name: "not linked", lookupErr: gorm.ErrRecordNotFound, wantStatus: http.StatusForbidden, wantError: "facebook_account_not_linked"},
		{name: "database unavailable", lookupErr: errors.New("database unavailable"), wantStatus: http.StatusInternalServerError, wantError: "database_error"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			handler := configuredFacebookLoginHandler()
			handler.findLinkedUser = func(string) (models.User, error) {
				return models.User{}, tc.lookupErr
			}

			response := performFacebookLogin(handler, `{"access_token":"facebook-token"}`)
			if response.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", response.Code, tc.wantStatus, response.Body.String())
			}
			if !strings.Contains(response.Body.String(), tc.wantError) {
				t.Fatalf("body = %s, want error %q", response.Body.String(), tc.wantError)
			}
		})
	}
}

func TestFacebookLoginReportsMetaOutagesSeparatelyFromInvalidTokens(t *testing.T) {
	tests := []struct {
		name       string
		graphError *facebookGraphError
		wantStatus int
	}{
		{name: "transport failure", graphError: &facebookGraphError{kind: "transport_failure"}, wantStatus: http.StatusBadGateway},
		{name: "server error", graphError: &facebookGraphError{kind: "server_error", statusCode: 500}, wantStatus: http.StatusBadGateway},
		{name: "rate limited", graphError: &facebookGraphError{kind: "rate_limited", statusCode: 429}, wantStatus: http.StatusServiceUnavailable},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			handler := configuredFacebookLoginHandler()
			handler.verifyToken = func(context.Context, string) (facebookProfile, error) {
				return facebookProfile{}, tc.graphError
			}
			handler.findLinkedUser = func(string) (models.User, error) {
				t.Fatal("user lookup must not run during a Meta outage")
				return models.User{}, nil
			}

			response := performFacebookLogin(handler, `{"access_token":"facebook-token"}`)
			if response.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", response.Code, tc.wantStatus, response.Body.String())
			}
			if !strings.Contains(response.Body.String(), "facebook_service_unavailable") {
				t.Fatalf("body = %s, want upstream service error", response.Body.String())
			}
		})
	}
}

func TestFacebookLinkConnectsVerifiedIdentityToCurrentUser(t *testing.T) {
	handler := configuredFacebookLoginHandler()
	var linkedUserID string
	var linkedProfile facebookProfile
	handler.linkIdentity = func(userID string, profile facebookProfile) error {
		linkedUserID = userID
		linkedProfile = profile
		return nil
	}

	response := performFacebookProfileRequest(handler.Link, http.MethodPost, "/api/v1/profile/facebook/link", `{"access_token":"facebook-token","current_password":"correct-password"}`)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusOK, response.Body.String())
	}
	if linkedUserID != "user-1" || linkedProfile.ID != "fb-user-1" {
		t.Fatalf("linked user=%q profile=%#v", linkedUserID, linkedProfile)
	}
	if !strings.Contains(response.Body.String(), `"linked":true`) {
		t.Fatalf("body = %s, want linked status", response.Body.String())
	}
}

func TestFacebookLinkRejectsIdentityAlreadyOwnedByAnotherUser(t *testing.T) {
	handler := configuredFacebookLoginHandler()
	handler.linkIdentity = func(string, facebookProfile) error {
		return errFacebookIdentityAlreadyLinked
	}

	response := performFacebookProfileRequest(handler.Link, http.MethodPost, "/api/v1/profile/facebook/link", `{"access_token":"facebook-token","current_password":"correct-password"}`)
	if response.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusConflict, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), "facebook_account_already_linked") {
		t.Fatalf("body = %s, want identity conflict", response.Body.String())
	}
}

func TestFacebookLinkStatusAndUnlink(t *testing.T) {
	handler := configuredFacebookLoginHandler()
	handler.getIdentity = func(userID string) (models.FacebookIdentity, error) {
		if userID != "user-1" {
			t.Fatalf("status lookup user = %q, want user-1", userID)
		}
		return models.FacebookIdentity{UserID: userID, FacebookUserID: "fb-user-1", FacebookName: "Review User"}, nil
	}
	unlinkedUserID := ""
	handler.unlinkIdentity = func(userID string) (int, error) {
		unlinkedUserID = userID
		return 5, nil
	}

	statusResponse := performFacebookProfileRequest(handler.LinkStatus, http.MethodGet, "/api/v1/profile/facebook", "")
	if statusResponse.Code != http.StatusOK || !strings.Contains(statusResponse.Body.String(), `"facebook_name":"Review User"`) {
		t.Fatalf("status response = %d %s", statusResponse.Code, statusResponse.Body.String())
	}

	unlinkResponse := performFacebookProfileRequest(handler.Unlink, http.MethodDelete, "/api/v1/profile/facebook/link", `{"current_password":"correct-password"}`)
	if unlinkResponse.Code != http.StatusOK || unlinkedUserID != "user-1" {
		t.Fatalf("unlink response = %d %s; user=%q", unlinkResponse.Code, unlinkResponse.Body.String(), unlinkedUserID)
	}
	if cookie := unlinkResponse.Header().Get("Set-Cookie"); !strings.Contains(cookie, "cqa_refresh_token=") {
		t.Fatalf("Set-Cookie = %q, want rotated refresh token", cookie)
	}
}

func TestFacebookLinkStatusKeepsLinkedIdentityVisibleWhenLoginIsDisabled(t *testing.T) {
	handler := configuredFacebookLoginHandler()
	handler.appSecret = ""
	handler.getIdentity = func(string) (models.FacebookIdentity, error) {
		return models.FacebookIdentity{UserID: "user-1", FacebookUserID: "fb-user-1", FacebookName: "Review User"}, nil
	}

	response := performFacebookProfileRequest(handler.LinkStatus, http.MethodGet, "/api/v1/profile/facebook", "")
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusOK, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), `"enabled":false`) || !strings.Contains(response.Body.String(), `"linked":true`) {
		t.Fatalf("body = %s, want disabled but linked status", response.Body.String())
	}
}

func TestFacebookLinkRequiresCurrentPassword(t *testing.T) {
	handler := configuredFacebookLoginHandler()
	handler.verifyToken = func(context.Context, string) (facebookProfile, error) {
		t.Fatal("Facebook token must not be verified with the wrong local password")
		return facebookProfile{}, nil
	}
	handler.verifyPassword = func(string, string) error {
		return errFacebookWrongPassword
	}
	handler.linkIdentity = func(string, facebookProfile) error {
		t.Fatal("identity must not be linked with the wrong password")
		return nil
	}

	response := performFacebookProfileRequest(handler.Link, http.MethodPost, "/api/v1/profile/facebook/link", `{"access_token":"facebook-token","current_password":"wrong-password"}`)
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "wrong_current_password") {
		t.Fatalf("response = %d %s, want wrong password", response.Code, response.Body.String())
	}
}

func TestMySQLDuplicateEntryIsRecognizedAsLinkConflict(t *testing.T) {
	if !isMySQLDuplicateEntry(&mysqlDriver.MySQLError{Number: 1062, Message: "Duplicate entry"}) {
		t.Fatal("MySQL duplicate entry should be recognized")
	}
	if isMySQLDuplicateEntry(errors.New("database unavailable")) {
		t.Fatal("unrelated database error must not be treated as a conflict")
	}
}

func configuredFacebookLoginHandler() *FacebookAuthHandler {
	handler := NewFacebookAuthHandler(&config.Config{
		FacebookAppID:      "123",
		FacebookAppSecret:  "secret",
		FacebookAPIVersion: "v26.0",
	})
	handler.verifyToken = func(context.Context, string) (facebookProfile, error) {
		return facebookProfile{ID: "fb-user-1", Name: "Review User"}, nil
	}
	handler.logSuccess = func(*gin.Context, models.User) {}
	handler.logNotFound = func(*gin.Context, facebookProfile) {}
	handler.logLinked = func(*gin.Context, models.FacebookIdentity) {}
	handler.logUnlinked = func(*gin.Context) {}
	handler.verifyPassword = func(string, string) error { return nil }
	return handler
}

func performFacebookLogin(handler *FacebookAuthHandler, body string) *httptest.ResponseRecorder {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/api/v1/auth/facebook", handler.Login)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/facebook", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
}

func performFacebookProfileRequest(handler gin.HandlerFunc, method, path, body string) *httptest.ResponseRecorder {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("user_id", "user-1")
		c.Set("user_email", "user@example.com")
		c.Next()
	})
	router.Handle(method, path, handler)
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	if f == nil {
		return nil, errors.New("round tripper is nil")
	}
	return f(request)
}

func jsonResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}
