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
	mysqlDriver "github.com/go-sql-driver/mysql"
	"github.com/vietbui/chat-quality-agent/api/middleware"
	"github.com/vietbui/chat-quality-agent/config"
	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/db/models"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

const facebookGraphBaseURL = "https://graph.facebook.com"

var (
	errFacebookInvalidToken          = errors.New("invalid Facebook access token")
	errFacebookIdentityMismatch      = errors.New("Facebook token identity mismatch")
	errFacebookIdentityAlreadyLinked = errors.New("Facebook identity belongs to another user")
	errFacebookUserAlreadyLinked     = errors.New("local user already has a different Facebook identity")
	errFacebookWrongPassword         = errors.New("wrong current password")
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
	appID          string
	appSecret      string
	apiVersion     string
	loginConfigID  string
	graphBaseURL   string
	httpClient     *http.Client
	verifyToken    func(context.Context, string) (facebookProfile, error)
	findLinkedUser func(string) (models.User, error)
	getIdentity    func(string) (models.FacebookIdentity, error)
	linkIdentity   func(string, facebookProfile) error
	unlinkIdentity func(string) (int, error)
	verifyPassword func(string, string) error
	logSuccess     func(*gin.Context, models.User)
	logNotFound    func(*gin.Context, facebookProfile)
	logLinked      func(*gin.Context, models.FacebookIdentity)
	logUnlinked    func(*gin.Context)
}

type facebookLoginRequest struct {
	AccessToken string `json:"access_token" binding:"required"`
}

type facebookLinkRequest struct {
	AccessToken     string `json:"access_token" binding:"required"`
	CurrentPassword string `json:"current_password" binding:"required"`
}

type facebookCurrentPasswordRequest struct {
	CurrentPassword string `json:"current_password" binding:"required"`
}

type facebookProfile struct {
	ID   string `json:"id"`
	Name string `json:"name"`
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
		appID:         strings.TrimSpace(cfg.FacebookAppID),
		appSecret:     strings.TrimSpace(cfg.FacebookAppSecret),
		apiVersion:    strings.TrimSpace(cfg.FacebookAPIVersion),
		loginConfigID: strings.TrimSpace(cfg.FacebookPageLoginConfigID),
		graphBaseURL:  facebookGraphBaseURL,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
	handler.verifyToken = handler.verifyAccessToken
	handler.findLinkedUser = func(facebookUserID string) (models.User, error) {
		var identity models.FacebookIdentity
		err := db.DB.Preload("User").Where("facebook_app_id = ? AND facebook_user_id = ?", handler.appID, facebookUserID).First(&identity).Error
		return identity.User, err
	}
	handler.getIdentity = func(userID string) (models.FacebookIdentity, error) {
		var identity models.FacebookIdentity
		err := db.DB.Where("user_id = ?", userID).First(&identity).Error
		return identity, err
	}
	handler.linkIdentity = func(userID string, profile facebookProfile) error {
		return db.DB.Transaction(func(tx *gorm.DB) error {
			var byFacebook models.FacebookIdentity
			err := tx.Where("facebook_app_id = ? AND facebook_user_id = ?", handler.appID, profile.ID).First(&byFacebook).Error
			if err == nil {
				if byFacebook.UserID != userID {
					return errFacebookIdentityAlreadyLinked
				}
				return tx.Model(&byFacebook).Update("facebook_name", strings.TrimSpace(profile.Name)).Error
			}
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}

			var byUser models.FacebookIdentity
			err = tx.Where("user_id = ?", userID).First(&byUser).Error
			if err == nil {
				return errFacebookUserAlreadyLinked
			}
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}

			identity := models.FacebookIdentity{
				UserID:         userID,
				FacebookAppID:  handler.appID,
				FacebookUserID: profile.ID,
				FacebookName:   strings.TrimSpace(profile.Name),
			}
			if err := tx.Create(&identity).Error; err != nil {
				if isMySQLDuplicateEntry(err) {
					return errFacebookUserAlreadyLinked
				}
				return err
			}
			return nil
		})
	}
	handler.unlinkIdentity = func(userID string) (int, error) {
		newTokenVersion := 0
		err := db.DB.Transaction(func(tx *gorm.DB) error {
			if err := tx.Where("user_id = ?", userID).Delete(&models.FacebookIdentity{}).Error; err != nil {
				return err
			}
			result := tx.Model(&models.User{}).Where("id = ?", userID).
				UpdateColumn("token_version", gorm.Expr("token_version + ?", 1))
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != 1 {
				return gorm.ErrRecordNotFound
			}
			return tx.Model(&models.User{}).Select("token_version").Where("id = ?", userID).Scan(&newTokenVersion).Error
		})
		return newTokenVersion, err
	}
	handler.verifyPassword = func(userID, currentPassword string) error {
		var user models.User
		if err := db.DB.Select("password_hash").First(&user, "id = ?", userID).Error; err != nil {
			return err
		}
		if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(currentPassword)); err != nil {
			return errFacebookWrongPassword
		}
		return nil
	}
	handler.logSuccess = func(c *gin.Context, user models.User) {
		db.LogActivity(resolveFirstTenantByEmail(user.Email), user.ID, user.Email, "user.login_facebook", "user", user.ID,
			"Facebook login from "+c.ClientIP(), "", c.ClientIP())
	}
	handler.logNotFound = func(c *gin.Context, profile facebookProfile) {
		log.Printf("[security] Facebook login rejected: facebook_user=%s ip=%s reason=account_not_linked", profile.ID, c.ClientIP())
	}
	handler.logLinked = func(c *gin.Context, identity models.FacebookIdentity) {
		userID := middleware.GetUserID(c)
		email := middleware.GetUserEmail(c)
		db.LogActivity(resolveFirstTenantByEmail(email), userID, email, "user.facebook_linked", "user", userID,
			"Đã liên kết tài khoản Facebook "+identity.FacebookName, "", c.ClientIP())
	}
	handler.logUnlinked = func(c *gin.Context) {
		userID := middleware.GetUserID(c)
		email := middleware.GetUserEmail(c)
		db.LogActivity(resolveFirstTenantByEmail(email), userID, email, "user.facebook_unlinked", "user", userID,
			"Đã hủy liên kết tài khoản Facebook", "", c.ClientIP())
	}
	return handler
}

func isMySQLDuplicateEntry(err error) bool {
	var mysqlErr *mysqlDriver.MySQLError
	return errors.As(err, &mysqlErr) && mysqlErr.Number == 1062
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
		"enabled":         true,
		"app_id":          h.appID,
		"api_version":     h.apiVersion,
		"login_config_id": h.loginConfigID,
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

	profile, ok := h.verifiedProfile(c, req.AccessToken)
	if !ok {
		return
	}

	user, err := h.findLinkedUser(profile.ID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		h.logNotFound(c, profile)
		c.JSON(http.StatusForbidden, gin.H{"error": "facebook_account_not_linked"})
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

// LinkStatus reports whether the authenticated local user has linked Facebook.
func (h *FacebookAuthHandler) LinkStatus(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	enabled := h.enabled()

	identity, err := h.getIdentity(middleware.GetUserID(c))
	if errors.Is(err, gorm.ErrRecordNotFound) {
		c.JSON(http.StatusOK, gin.H{"enabled": enabled, "linked": false})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "database_error"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"enabled":       enabled,
		"linked":        true,
		"facebook_name": identity.FacebookName,
	})
}

// Link verifies a fresh Facebook user access token and attaches its app-scoped
// identity to the authenticated local user. The access token is never stored.
func (h *FacebookAuthHandler) Link(c *gin.Context) {
	if !h.enabled() {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "facebook_login_not_configured"})
		return
	}

	var req facebookLinkRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request"})
		return
	}
	if !h.currentPasswordValid(c, req.CurrentPassword) {
		return
	}

	profile, ok := h.verifiedProfile(c, req.AccessToken)
	if !ok {
		return
	}

	userID := middleware.GetUserID(c)
	if err := h.linkIdentity(userID, profile); err != nil {
		switch {
		case errors.Is(err, errFacebookIdentityAlreadyLinked), errors.Is(err, errFacebookUserAlreadyLinked):
			c.JSON(http.StatusConflict, gin.H{"error": "facebook_account_already_linked"})
		default:
			log.Printf("[auth] Facebook account link failed: user=%s ip=%s error=%v", userID, c.ClientIP(), err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "database_error"})
		}
		return
	}

	identity := models.FacebookIdentity{UserID: userID, FacebookAppID: h.appID, FacebookUserID: profile.ID, FacebookName: strings.TrimSpace(profile.Name)}
	h.logLinked(c, identity)
	c.JSON(http.StatusOK, gin.H{"linked": true, "facebook_name": identity.FacebookName})
}

// Unlink removes the authenticated user's Facebook identity. Password login is
// always retained, so unlinking cannot lock the user out of the local account.
func (h *FacebookAuthHandler) Unlink(c *gin.Context) {
	var req facebookCurrentPasswordRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request"})
		return
	}
	if !h.currentPasswordValid(c, req.CurrentPassword) {
		return
	}

	userID := middleware.GetUserID(c)
	newTokenVersion, err := h.unlinkIdentity(userID)
	if err != nil {
		log.Printf("[auth] Facebook account unlink failed: user=%s ip=%s error=%v", userID, c.ClientIP(), err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "database_error"})
		return
	}
	refreshToken, err := middleware.GenerateRefreshToken(userID, newTokenVersion)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "token_generation_failed"})
		return
	}
	setRefreshCookie(c, refreshToken)

	h.logUnlinked(c)
	c.JSON(http.StatusOK, gin.H{"linked": false})
}

func (h *FacebookAuthHandler) currentPasswordValid(c *gin.Context, currentPassword string) bool {
	if currentPassword == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request"})
		return false
	}

	err := h.verifyPassword(middleware.GetUserID(c), currentPassword)
	if errors.Is(err, errFacebookWrongPassword) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "wrong_current_password"})
		return false
	}
	if err != nil {
		log.Printf("[auth] Facebook account password verification failed: user=%s ip=%s error=%v", middleware.GetUserID(c), c.ClientIP(), err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "database_error"})
		return false
	}
	return true
}

func (h *FacebookAuthHandler) verifiedProfile(c *gin.Context, accessToken string) (facebookProfile, bool) {
	facebookAccessToken := strings.TrimSpace(accessToken)
	if facebookAccessToken == "" || len(facebookAccessToken) > 4096 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request"})
		return facebookProfile{}, false
	}

	profile, err := h.verifyToken(c.Request.Context(), facebookAccessToken)
	if err == nil {
		return profile, true
	}

	var graphErr *facebookGraphError
	if errors.As(err, &graphErr) && graphErr.upstreamFailure() {
		log.Printf("[auth] Facebook verification upstream failure: ip=%s error=%v", c.ClientIP(), graphErr)
		status := http.StatusBadGateway
		if graphErr.kind == "rate_limited" {
			status = http.StatusServiceUnavailable
		}
		c.JSON(status, gin.H{"error": "facebook_service_unavailable"})
		return facebookProfile{}, false
	}

	log.Printf("[security] Facebook token rejected: ip=%s error=%v", c.ClientIP(), err)
	c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid_facebook_token"})
	return facebookProfile{}, false
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
	profileQuery.Set("fields", "id,name")
	profileQuery.Set("appsecret_proof", h.appSecretProof(accessToken))
	profileURL.RawQuery = profileQuery.Encode()

	var profile facebookProfile
	if err := h.getJSON(ctx, profileURL.String(), accessToken, &profile); err != nil {
		return facebookProfile{}, fmt.Errorf("fetch Facebook profile: %w", err)
	}
	if profile.ID == "" || profile.ID != debugResponse.Data.UserID {
		return facebookProfile{}, errFacebookIdentityMismatch
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
