package handlers

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/vietbui/chat-quality-agent/api/middleware"
	"github.com/vietbui/chat-quality-agent/config"
	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/db/models"
	"gorm.io/gorm"
)

const facebookGraphBaseURL = "https://graph.facebook.com"

var (
	errFacebookInvalidToken     = errors.New("invalid Facebook access token")
	errFacebookIdentityMismatch = errors.New("Facebook token identity mismatch")
	errFacebookEmailMissing     = errors.New("Facebook account did not provide an email")
)

type facebookGraphError struct {
	kind       string
	statusCode int
}

func (e *facebookGraphError) Error() string {
	if e.statusCode != 0 {
		return fmt.Sprintf("Facebook Graph API %s (HTTP %d)", e.kind, e.statusCode)
	}
	return "Facebook Graph API " + e.kind
}

func (e *facebookGraphError) upstreamFailure() bool {
	return e.kind == "timeout" || e.kind == "dns_failure" || e.kind == "transport_failure" ||
		e.kind == "rate_limited" || e.kind == "server_error" || e.kind == "unexpected_status" ||
		e.kind == "invalid_response"
}

type FacebookAuthHandler struct {
	appID        string
	appSecret    string
	apiVersion   string
	graphBaseURL string
	httpClient   *http.Client
	verifyToken  func(context.Context, string) (facebookProfile, error)
	findUser     func(string) (models.User, error)
	logSuccess   func(*gin.Context, models.User)
	logNotFound  func(*gin.Context, facebookProfile, string)
}

type facebookLoginRequest struct {
	AccessToken string `json:"access_token" binding:"required"`
}

type facebookProfile struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email"`
}

type facebookDebugTokenResponse struct {
	Data struct {
		AppID   string `json:"app_id"`
		IsValid bool   `json:"is_valid"`
		UserID  string `json:"user_id"`
	} `json:"data"`
}

func NewFacebookAuthHandler(cfg *config.Config) *FacebookAuthHandler {
	handler := &FacebookAuthHandler{
		appID:        strings.TrimSpace(cfg.FacebookAppID),
		appSecret:    strings.TrimSpace(cfg.FacebookAppSecret),
		apiVersion:   strings.TrimSpace(cfg.FacebookAPIVersion),
		graphBaseURL: facebookGraphBaseURL,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
	handler.verifyToken = handler.verifyAccessToken
	handler.findUser = func(email string) (models.User, error) {
		var user models.User
		err := db.DB.Where("LOWER(email) = ?", email).First(&user).Error
		return user, err
	}
	handler.logSuccess = func(c *gin.Context, user models.User) {
		db.LogActivity(resolveFirstTenantByEmail(user.Email), user.ID, user.Email, "user.login_facebook", "user", user.ID,
			"Facebook login from "+c.ClientIP(), "", c.ClientIP())
	}
	handler.logNotFound = func(c *gin.Context, profile facebookProfile, email string) {
		log.Printf("[security] Facebook login rejected: email=%s facebook_user=%s ip=%s reason=user_not_provisioned", email, profile.ID, c.ClientIP())
		db.LogActivity("", "", email, "user.login_failed", "user", "",
			"Đăng nhập Facebook thất bại (user_not_provisioned) từ IP "+c.ClientIP(), "", c.ClientIP())
	}
	return handler
}

func (h *FacebookAuthHandler) enabled() bool {
	return h.appID != "" && h.appSecret != "" && h.apiVersion != ""
}

// PublicConfig returns only browser-safe Facebook SDK settings. The app secret
// stays server-side and Facebook login is hidden until both credentials exist.
func (h *FacebookAuthHandler) PublicConfig(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	if !h.enabled() {
		c.JSON(http.StatusOK, gin.H{"enabled": false})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"enabled":     true,
		"app_id":      h.appID,
		"api_version": h.apiVersion,
	})
}

// Login exchanges a Facebook user access token for the application's own JWT.
// Only pre-provisioned local users can sign in; Facebook never creates users or
// grants tenant membership implicitly.
func (h *FacebookAuthHandler) Login(c *gin.Context) {
	if !h.enabled() {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "facebook_login_not_configured"})
		return
	}

	var req facebookLoginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request"})
		return
	}
	facebookAccessToken := strings.TrimSpace(req.AccessToken)
	if facebookAccessToken == "" || len(facebookAccessToken) > 4096 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request"})
		return
	}

	profile, err := h.verifyToken(c.Request.Context(), facebookAccessToken)
	if err != nil {
		if errors.Is(err, errFacebookEmailMissing) {
			c.JSON(http.StatusForbidden, gin.H{"error": "facebook_email_required"})
			return
		}
		var graphErr *facebookGraphError
		if errors.As(err, &graphErr) && graphErr.upstreamFailure() {
			log.Printf("[auth] Facebook login upstream failure: ip=%s error=%v", c.ClientIP(), graphErr)
			status := http.StatusBadGateway
			if graphErr.kind == "rate_limited" {
				status = http.StatusServiceUnavailable
			}
			c.JSON(status, gin.H{"error": "facebook_service_unavailable"})
			return
		}
		log.Printf("[security] Facebook login rejected: ip=%s error=%v", c.ClientIP(), err)
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid_facebook_token"})
		return
	}

	email := strings.ToLower(strings.TrimSpace(profile.Email))
	user, err := h.findUser(email)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		h.logNotFound(c, profile, email)
		c.JSON(http.StatusForbidden, gin.H{"error": "facebook_account_not_provisioned"})
		return
	}
	if err != nil {
		log.Printf("[auth] Facebook login database lookup failed: ip=%s error=%v", c.ClientIP(), err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "database_error"})
		return
	}

	accessToken, err := middleware.GenerateAccessToken(user.ID, user.Email, user.IsAdmin)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "token_generation_failed"})
		return
	}
	refreshToken, err := middleware.GenerateRefreshToken(user.ID, user.TokenVersion)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "token_generation_failed"})
		return
	}

	setRefreshCookie(c, refreshToken)
	h.logSuccess(c, user)

	c.JSON(http.StatusOK, TokenResponse{AccessToken: accessToken, ExpiresIn: 900})
}

func (h *FacebookAuthHandler) verifyAccessToken(ctx context.Context, accessToken string) (facebookProfile, error) {
	debugURL, err := url.Parse(h.graphBaseURL + "/" + h.apiVersion + "/debug_token")
	if err != nil {
		return facebookProfile{}, err
	}
	debugQuery := debugURL.Query()
	debugQuery.Set("input_token", accessToken)
	debugQuery.Set("access_token", h.appID+"|"+h.appSecret)
	debugURL.RawQuery = debugQuery.Encode()

	var debugResponse facebookDebugTokenResponse
	if err := h.getJSON(ctx, debugURL.String(), "", &debugResponse); err != nil {
		return facebookProfile{}, fmt.Errorf("debug Facebook token: %w", err)
	}
	if !debugResponse.Data.IsValid || debugResponse.Data.AppID != h.appID || debugResponse.Data.UserID == "" {
		return facebookProfile{}, errFacebookInvalidToken
	}

	profileURL, err := url.Parse(h.graphBaseURL + "/" + h.apiVersion + "/me")
	if err != nil {
		return facebookProfile{}, err
	}
	profileQuery := profileURL.Query()
	profileQuery.Set("fields", "id,name,email")
	profileQuery.Set("appsecret_proof", h.appSecretProof(accessToken))
	profileURL.RawQuery = profileQuery.Encode()

	var profile facebookProfile
	if err := h.getJSON(ctx, profileURL.String(), accessToken, &profile); err != nil {
		return facebookProfile{}, fmt.Errorf("fetch Facebook profile: %w", err)
	}
	if profile.ID == "" || profile.ID != debugResponse.Data.UserID {
		return facebookProfile{}, errFacebookIdentityMismatch
	}
	if strings.TrimSpace(profile.Email) == "" {
		return facebookProfile{}, errFacebookEmailMissing
	}

	return profile, nil
}

func (h *FacebookAuthHandler) getJSON(ctx context.Context, endpoint, bearerToken string, target any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	if bearerToken != "" {
		req.Header.Set("Authorization", "Bearer "+bearerToken)
	}

	resp, err := h.httpClient.Do(req)
	if err != nil {
		// url.Error includes the full request URL. The debug-token URL contains
		// both the user access token and the app secret, so only retain a safe
		// failure category for diagnostics.
		return &facebookGraphError{kind: facebookTransportFailureKind(err)}
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
		kind := "unexpected_status"
		if resp.StatusCode == http.StatusTooManyRequests {
			kind = "rate_limited"
		} else if resp.StatusCode >= http.StatusInternalServerError {
			kind = "server_error"
		} else if resp.StatusCode == http.StatusBadRequest || resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			kind = "client_error"
		}
		return &facebookGraphError{kind: kind, statusCode: resp.StatusCode}
	}

	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(target); err != nil {
		return &facebookGraphError{kind: "invalid_response"}
	}
	return nil
}

func facebookTransportFailureKind(err error) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}
	var networkError net.Error
	if errors.As(err, &networkError) && networkError.Timeout() {
		return "timeout"
	}
	var dnsError *net.DNSError
	if errors.As(err, &dnsError) {
		return "dns_failure"
	}
	return "transport_failure"
}

func (h *FacebookAuthHandler) appSecretProof(accessToken string) string {
	mac := hmac.New(sha256.New, []byte(h.appSecret))
	_, _ = mac.Write([]byte(accessToken))
	return hex.EncodeToString(mac.Sum(nil))
}
