package handlers

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/vietbui/chat-quality-agent/api/middleware"
	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/db/models"
	"github.com/vietbui/chat-quality-agent/pkg"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	facebookAccountOAuthStatePrefix = "link_"
	facebookAccountOAuthCookie      = "cqa_fb_account_oauth"
	facebookAccountOAuthTTL         = 10 * time.Minute
)

var (
	errFacebookAccountOAuthInvalid = errors.New("facebook account OAuth session is invalid")
	errFacebookAccountOAuthExpired = errors.New("facebook account OAuth session expired")
	errFacebookAccountOAuthReplay  = errors.New("facebook account OAuth state already used")
	facebookSettingsReturnPath     = regexp.MustCompile(`^/[A-Za-z0-9_-]+/settings$`)
)

type facebookAccountOAuthStartRequest struct {
	CurrentPassword string `json:"current_password" binding:"required"`
	ReturnPath      string `json:"return_path"`
}

type facebookAccountOAuthStore interface {
	Create(context.Context, *models.FacebookAccountOAuthSession) error
	Claim(context.Context, string, string, time.Time) (models.FacebookAccountOAuthSession, error)
	Complete(context.Context, string, time.Time) error
	Fail(context.Context, string, time.Time) error
	Cleanup(context.Context, time.Time)
}

type FacebookAccountOAuthHandler struct {
	auth               *FacebookAuthHandler
	now                func() time.Time
	store              facebookAccountOAuthStore
	exchangeCode       func(context.Context, string, string) (string, error)
	loadCredentialHash func(context.Context, string) (string, error)
	logLinked          func(*gin.Context, string, facebookProfile)
}

func NewFacebookAccountOAuthHandler(auth *FacebookAuthHandler) *FacebookAccountOAuthHandler {
	h := &FacebookAccountOAuthHandler{auth: auth, now: time.Now}
	h.store = &gormFacebookAccountOAuthStore{}
	h.exchangeCode = h.exchangeAuthorizationCode
	h.loadCredentialHash = func(ctx context.Context, userID string) (string, error) {
		var user models.User
		if err := db.DB.WithContext(ctx).Select("password_hash").First(&user, "id = ?", userID).Error; err != nil {
			return "", err
		}
		return hashFacebookOAuthSecret(user.PasswordHash), nil
	}
	h.logLinked = func(c *gin.Context, userID string, profile facebookProfile) {
		var user models.User
		if err := db.DB.Select("email").First(&user, "id = ?", userID).Error; err != nil {
			log.Printf("[facebook-account-oauth] audit user lookup failed: user=%s error=%v", userID, err)
			return
		}
		db.LogActivity(resolveFirstTenantByEmail(user.Email), userID, user.Email, "user.facebook_linked", "user", userID,
			"Đã liên kết tài khoản Facebook "+strings.TrimSpace(profile.Name), "", c.ClientIP())
	}
	return h
}

func (h *FacebookAccountOAuthHandler) Start(c *gin.Context) {
	facebookOAuthNoStore(c)
	if h == nil || h.auth == nil || !h.auth.enabled() {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "facebook_login_not_configured"})
		return
	}
	var req facebookAccountOAuthStartRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request"})
		return
	}
	userID := middleware.GetUserID(c)
	// Capture before password verification so a concurrent password replacement
	// cannot authorize this flow against the newly installed credential.
	credentialHash, err := h.loadCredentialHash(c.Request.Context(), userID)
	if err != nil {
		log.Printf("[facebook-account-oauth] load credential fingerprint failed: user=%s error=%v", userID, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "connect_failed"})
		return
	}
	if err := h.auth.verifyPassword(userID, req.CurrentPassword); err != nil {
		if errors.Is(err, errFacebookWrongPassword) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "wrong_current_password"})
			return
		}
		log.Printf("[facebook-account-oauth] password verification failed: user=%s error=%v", userID, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "connect_failed"})
		return
	}

	stateSecret, err := pkg.GenerateRandomString(32)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "connect_failed"})
		return
	}
	browserSecret, err := pkg.GenerateRandomString(32)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "connect_failed"})
		return
	}
	now := h.now().UTC()
	redirectURI := facebookOAuthCallbackURL(c)
	session := models.FacebookAccountOAuthSession{
		ID:             pkg.NewUUID(),
		UserID:         userID,
		CredentialHash: credentialHash,
		StateHash:      hashFacebookOAuthSecret(facebookAccountOAuthStatePrefix + stateSecret),
		BrowserHash:    hashFacebookOAuthSecret(browserSecret),
		RedirectURI:    redirectURI,
		ReturnPath:     safeFacebookAccountReturnPath(req.ReturnPath),
		Status:         "pending",
		ExpiresAt:      now.Add(facebookAccountOAuthTTL),
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	h.store.Cleanup(c.Request.Context(), now)
	if err := h.store.Create(c.Request.Context(), &session); err != nil {
		log.Printf("[facebook-account-oauth] create session failed: user=%s error=%v", userID, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "connect_failed"})
		return
	}

	http.SetCookie(c.Writer, &http.Cookie{
		Name: facebookAccountOAuthCookie, Value: browserSecret,
		Path: "/api/v1/channels/facebook/callback", MaxAge: int(facebookAccountOAuthTTL.Seconds()),
		HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode,
	})
	authorizeURL, _ := url.Parse("https://www.facebook.com/" + h.auth.apiVersion + "/dialog/oauth")
	query := authorizeURL.Query()
	query.Set("client_id", h.auth.appID)
	query.Set("redirect_uri", redirectURI)
	query.Set("state", facebookAccountOAuthStatePrefix+stateSecret)
	query.Set("response_type", "code")
	if h.auth.loginConfigID != "" {
		query.Set("config_id", h.auth.loginConfigID)
	} else {
		query.Set("scope", "public_profile")
	}
	authorizeURL.RawQuery = query.Encode()
	c.JSON(http.StatusOK, gin.H{"redirect_url": authorizeURL.String()})
}

func (h *FacebookAccountOAuthHandler) Callback(c *gin.Context) {
	facebookOAuthNoStore(c)
	state := c.Query("state")
	if h == nil || h.auth == nil || !h.auth.enabled() || !strings.HasPrefix(state, facebookAccountOAuthStatePrefix) {
		h.redirect(c, "/", "invalid_state")
		return
	}
	cookie, err := c.Cookie(facebookAccountOAuthCookie)
	if err != nil || cookie == "" {
		h.redirect(c, "/", "browser_mismatch")
		return
	}
	h.clearCookie(c)

	now := h.now().UTC()
	session, err := h.store.Claim(c.Request.Context(), hashFacebookOAuthSecret(state), hashFacebookOAuthSecret(cookie), now)
	if err != nil {
		code := safeFacebookAccountOAuthError(err)
		if errors.Is(err, errFacebookAccountOAuthInvalid) || errors.Is(err, errFacebookAccountOAuthExpired) || errors.Is(err, errFacebookAccountOAuthReplay) {
			log.Printf("[security] Facebook account OAuth callback rejected: reason=%s ip=%s", code, c.ClientIP())
		} else {
			log.Printf("[facebook-account-oauth] claim session failed: error=%v", err)
		}
		h.redirect(c, safeFacebookAccountReturnPath(session.ReturnPath), code)
		return
	}
	if c.Query("error") != "" || strings.TrimSpace(c.Query("code")) == "" {
		_ = h.store.Fail(c.Request.Context(), session.ID, now)
		h.redirect(c, session.ReturnPath, "oauth_denied")
		return
	}

	providerContext, cancel := context.WithTimeout(c.Request.Context(), 25*time.Second)
	defer cancel()
	accessToken, err := h.exchangeCode(providerContext, c.Query("code"), session.RedirectURI)
	if err != nil {
		_ = h.store.Fail(c.Request.Context(), session.ID, now)
		log.Printf("[facebook-account-oauth] code exchange failed: session=%s error=%v", session.ID, err)
		h.redirect(c, session.ReturnPath, "facebook_unavailable")
		return
	}
	profile, err := h.auth.verifyToken(providerContext, accessToken)
	if err != nil {
		_ = h.store.Fail(c.Request.Context(), session.ID, now)
		log.Printf("[facebook-account-oauth] token verification failed: session=%s error=%v", session.ID, err)
		h.redirect(c, session.ReturnPath, "connect_failed")
		return
	}
	if err := h.auth.linkIdentity(session.UserID, profile); err != nil {
		_ = h.store.Fail(c.Request.Context(), session.ID, now)
		code := "connect_failed"
		if errors.Is(err, errFacebookIdentityAlreadyLinked) {
			code = "facebook_account_already_linked"
		} else if errors.Is(err, errFacebookUserAlreadyLinked) {
			code = "account_already_linked"
		} else {
			log.Printf("[facebook-account-oauth] link identity failed: session=%s error=%v", session.ID, err)
		}
		h.redirect(c, session.ReturnPath, code)
		return
	}
	if err := h.store.Complete(c.Request.Context(), session.ID, now); err != nil {
		log.Printf("[facebook-account-oauth] complete session failed: session=%s error=%v", session.ID, err)
	}
	h.logLinked(c, session.UserID, profile)
	h.redirect(c, session.ReturnPath, "success")
}

func (h *FacebookAccountOAuthHandler) exchangeAuthorizationCode(ctx context.Context, code, redirectURI string) (string, error) {
	endpoint, _ := url.Parse(strings.TrimRight(h.auth.graphBaseURL, "/") + "/" + h.auth.apiVersion + "/oauth/access_token")
	query := endpoint.Query()
	query.Set("client_id", h.auth.appID)
	query.Set("client_secret", h.auth.appSecret)
	query.Set("redirect_uri", redirectURI)
	query.Set("code", code)
	endpoint.RawQuery = query.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return "", errFacebookProviderUnavailable
	}
	resp, err := h.auth.httpClient.Do(req)
	if err != nil {
		return "", errFacebookProviderUnavailable
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
		return "", errFacebookProviderUnavailable
	}
	var payload struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&payload); err != nil || payload.AccessToken == "" {
		return "", errFacebookProviderUnavailable
	}
	return payload.AccessToken, nil
}

func facebookOAuthCallbackURL(c *gin.Context) string {
	origin := browserRequestOrigin(c)
	if origin == "" {
		origin = getBaseURL(c)
	}
	return origin + "/api/v1/channels/facebook/callback"
}

func safeFacebookAccountReturnPath(raw string) string {
	path := strings.TrimSpace(raw)
	if path == "/" || facebookSettingsReturnPath.MatchString(path) {
		return path
	}
	return "/"
}

func (h *FacebookAccountOAuthHandler) redirect(c *gin.Context, returnPath, result string) {
	target := safeFacebookAccountReturnPath(returnPath)
	c.Redirect(http.StatusFound, target+"?facebook_link="+url.QueryEscape(result))
}

func (h *FacebookAccountOAuthHandler) clearCookie(c *gin.Context) {
	http.SetCookie(c.Writer, &http.Cookie{Name: facebookAccountOAuthCookie, Value: "", Path: "/api/v1/channels/facebook/callback", MaxAge: -1, HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode})
}

func safeFacebookAccountOAuthError(err error) string {
	switch {
	case errors.Is(err, errFacebookAccountOAuthExpired):
		return "session_expired"
	case errors.Is(err, errFacebookAccountOAuthReplay):
		return "session_used"
	case errors.Is(err, errFacebookAccountOAuthInvalid):
		return "invalid_state"
	default:
		return "connect_failed"
	}
}

type gormFacebookAccountOAuthStore struct{}

func (s *gormFacebookAccountOAuthStore) Create(ctx context.Context, session *models.FacebookAccountOAuthSession) error {
	return db.DB.WithContext(ctx).Create(session).Error
}

func (s *gormFacebookAccountOAuthStore) Claim(ctx context.Context, stateHash, browserHash string, now time.Time) (models.FacebookAccountOAuthSession, error) {
	var session models.FacebookAccountOAuthSession
	err := db.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("state_hash = ?", stateHash).First(&session).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return errFacebookAccountOAuthInvalid
			}
			return err
		}
		if session.ExpiresAt.Before(now) {
			return errFacebookAccountOAuthExpired
		}
		if session.Status != "pending" {
			return errFacebookAccountOAuthReplay
		}
		if subtle.ConstantTimeCompare([]byte(session.BrowserHash), []byte(browserHash)) != 1 {
			return errFacebookAccountOAuthInvalid
		}
		var user models.User
		if err := tx.Select("password_hash").First(&user, "id = ?", session.UserID).Error; err != nil {
			return errFacebookAccountOAuthInvalid
		}
		currentCredentialHash := hashFacebookOAuthSecret(user.PasswordHash)
		if subtle.ConstantTimeCompare([]byte(session.CredentialHash), []byte(currentCredentialHash)) != 1 {
			return errFacebookAccountOAuthInvalid
		}
		return tx.Model(&session).Updates(map[string]any{"status": "processing", "updated_at": now}).Error
	})
	return session, err
}

func (s *gormFacebookAccountOAuthStore) Complete(ctx context.Context, id string, now time.Time) error {
	result := db.DB.WithContext(ctx).Model(&models.FacebookAccountOAuthSession{}).Where("id = ? AND status = ?", id, "processing").Updates(map[string]any{"status": "completed", "completed_at": now, "updated_at": now})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return errFacebookAccountOAuthReplay
	}
	return nil
}

func (s *gormFacebookAccountOAuthStore) Fail(ctx context.Context, id string, now time.Time) error {
	return db.DB.WithContext(ctx).Model(&models.FacebookAccountOAuthSession{}).Where("id = ? AND status = ?", id, "processing").Updates(map[string]any{"status": "failed", "updated_at": now}).Error
}

func (s *gormFacebookAccountOAuthStore) Cleanup(ctx context.Context, now time.Time) {
	_ = db.DB.WithContext(ctx).Where("expires_at < ?", now.Add(-24*time.Hour)).Delete(&models.FacebookAccountOAuthSession{}).Error
}
