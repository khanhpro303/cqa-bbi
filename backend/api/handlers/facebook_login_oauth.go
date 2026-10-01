package handlers

import (
	"context"
	"crypto/subtle"
	"errors"
	"log"
	"net/http"
	"net/url"
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
	facebookLoginOAuthStatePrefix = "signin_"
	facebookLoginOAuthCookie      = "cqa_fb_login_oauth"
	facebookLoginOAuthTTL         = 10 * time.Minute
)

var (
	errFacebookLoginOAuthInvalid = errors.New("facebook login OAuth session is invalid")
	errFacebookLoginOAuthExpired = errors.New("facebook login OAuth session expired")
	errFacebookLoginOAuthReplay  = errors.New("facebook login OAuth state already used")
)

type facebookLoginOAuthStore interface {
	Create(context.Context, *models.FacebookLoginOAuthSession) error
	Claim(context.Context, string, string, time.Time) (models.FacebookLoginOAuthSession, error)
	Complete(context.Context, string, time.Time) error
	Fail(context.Context, string, time.Time) error
	Cleanup(context.Context, time.Time)
}

type FacebookLoginOAuthHandler struct {
	auth                 *FacebookAuthHandler
	now                  func() time.Time
	store                facebookLoginOAuthStore
	exchangeCode         func(context.Context, string, string) (string, error)
	generateRefreshToken func(models.User) (string, error)
}

func NewFacebookLoginOAuthHandler(auth *FacebookAuthHandler, accountOAuth *FacebookAccountOAuthHandler) *FacebookLoginOAuthHandler {
	h := &FacebookLoginOAuthHandler{auth: auth, now: time.Now, store: &gormFacebookLoginOAuthStore{}}
	if accountOAuth != nil {
		h.exchangeCode = accountOAuth.exchangeCode
	}
	h.generateRefreshToken = func(user models.User) (string, error) {
		return middleware.GenerateRefreshToken(user.ID, user.TokenVersion)
	}
	return h
}

func (h *FacebookLoginOAuthHandler) Start(c *gin.Context) {
	facebookOAuthNoStore(c)
	if !h.enabled() {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "facebook_login_not_configured"})
		return
	}
	stateSecret, err := pkg.GenerateRandomString(32)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "database_error"})
		return
	}
	browserSecret, err := pkg.GenerateRandomString(32)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "database_error"})
		return
	}
	now := h.now().UTC()
	redirectURI := facebookOAuthCallbackURL(c)
	session := models.FacebookLoginOAuthSession{
		ID:          pkg.NewUUID(),
		StateHash:   hashFacebookOAuthSecret(facebookLoginOAuthStatePrefix + stateSecret),
		BrowserHash: hashFacebookOAuthSecret(browserSecret),
		RedirectURI: redirectURI,
		Status:      "pending",
		ExpiresAt:   now.Add(facebookLoginOAuthTTL),
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	h.store.Cleanup(c.Request.Context(), now)
	if err := h.store.Create(c.Request.Context(), &session); err != nil {
		log.Printf("[facebook-login-oauth] create session failed: error=%v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "database_error"})
		return
	}
	http.SetCookie(c.Writer, &http.Cookie{
		Name: facebookLoginOAuthCookie, Value: browserSecret,
		Path: "/api/v1/channels/facebook/callback", MaxAge: int(facebookLoginOAuthTTL.Seconds()),
		HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode,
	})
	authorizeURL, _ := url.Parse("https://www.facebook.com/" + h.auth.apiVersion + "/dialog/oauth")
	query := authorizeURL.Query()
	query.Set("client_id", h.auth.appID)
	query.Set("redirect_uri", redirectURI)
	query.Set("state", facebookLoginOAuthStatePrefix+stateSecret)
	query.Set("config_id", h.auth.loginConfigID)
	query.Set("response_type", "code")
	authorizeURL.RawQuery = query.Encode()
	c.JSON(http.StatusOK, gin.H{"redirect_url": authorizeURL.String()})
}

func (h *FacebookLoginOAuthHandler) Callback(c *gin.Context) {
	facebookOAuthNoStore(c)
	state := c.Query("state")
	if !h.enabled() || !strings.HasPrefix(state, facebookLoginOAuthStatePrefix) {
		h.redirect(c, "invalid_state")
		return
	}
	cookie, err := c.Cookie(facebookLoginOAuthCookie)
	if err != nil || cookie == "" {
		h.redirect(c, "browser_mismatch")
		return
	}
	h.clearCookie(c)

	now := h.now().UTC()
	session, err := h.store.Claim(c.Request.Context(), hashFacebookOAuthSecret(state), hashFacebookOAuthSecret(cookie), now)
	if err != nil {
		result := safeFacebookLoginOAuthError(err)
		if errors.Is(err, errFacebookLoginOAuthInvalid) || errors.Is(err, errFacebookLoginOAuthExpired) || errors.Is(err, errFacebookLoginOAuthReplay) {
			log.Printf("[security] Facebook login OAuth callback rejected: reason=%s ip=%s", result, c.ClientIP())
		} else {
			log.Printf("[facebook-login-oauth] claim session failed: error=%v", err)
		}
		h.redirect(c, result)
		return
	}
	if c.Query("error") != "" || strings.TrimSpace(c.Query("code")) == "" {
		_ = h.store.Fail(c.Request.Context(), session.ID, now)
		h.redirect(c, "oauth_denied")
		return
	}

	providerContext, cancel := context.WithTimeout(c.Request.Context(), 25*time.Second)
	defer cancel()
	accessToken, err := h.exchangeCode(providerContext, c.Query("code"), session.RedirectURI)
	if err != nil {
		h.fail(c, session.ID, now, "code exchange", err, "facebook_service_unavailable")
		return
	}
	profile, err := h.auth.verifyToken(providerContext, accessToken)
	if err != nil {
		h.fail(c, session.ID, now, "token verification", err, "facebook_service_unavailable")
		return
	}
	user, err := h.auth.findLinkedUser(profile.ID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		_ = h.store.Fail(c.Request.Context(), session.ID, now)
		h.auth.logNotFound(c, profile)
		h.redirect(c, "facebook_account_not_linked")
		return
	}
	if err != nil {
		h.fail(c, session.ID, now, "linked user lookup", err, "database_error")
		return
	}
	refreshToken, err := h.generateRefreshToken(user)
	if err != nil {
		h.fail(c, session.ID, now, "refresh token generation", err, "database_error")
		return
	}
	if err := h.store.Complete(c.Request.Context(), session.ID, now); err != nil {
		log.Printf("[facebook-login-oauth] complete session failed: session=%s error=%v", session.ID, err)
		h.redirect(c, "database_error")
		return
	}
	setRefreshCookie(c, refreshToken)
	h.auth.logSuccess(c, user)
	h.redirect(c, "success")
}

func (h *FacebookLoginOAuthHandler) enabled() bool {
	return h != nil && h.auth != nil && h.auth.enabled() && h.auth.loginConfigID != "" && h.store != nil && h.exchangeCode != nil && h.generateRefreshToken != nil
}

func (h *FacebookLoginOAuthHandler) fail(c *gin.Context, sessionID string, now time.Time, operation string, err error, result string) {
	_ = h.store.Fail(c.Request.Context(), sessionID, now)
	log.Printf("[facebook-login-oauth] %s failed: session=%s error=%v", operation, sessionID, err)
	h.redirect(c, result)
}

func (h *FacebookLoginOAuthHandler) redirect(c *gin.Context, result string) {
	c.Redirect(http.StatusFound, "/login?facebook_login="+url.QueryEscape(result))
}

func (h *FacebookLoginOAuthHandler) clearCookie(c *gin.Context) {
	http.SetCookie(c.Writer, &http.Cookie{Name: facebookLoginOAuthCookie, Value: "", Path: "/api/v1/channels/facebook/callback", MaxAge: -1, HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode})
}

func safeFacebookLoginOAuthError(err error) string {
	switch {
	case errors.Is(err, errFacebookLoginOAuthExpired):
		return "session_expired"
	case errors.Is(err, errFacebookLoginOAuthReplay):
		return "invalid_state"
	case errors.Is(err, errFacebookLoginOAuthInvalid):
		return "invalid_state"
	default:
		return "database_error"
	}
}

type gormFacebookLoginOAuthStore struct{}

func (s *gormFacebookLoginOAuthStore) Create(ctx context.Context, session *models.FacebookLoginOAuthSession) error {
	return db.DB.WithContext(ctx).Create(session).Error
}

func (s *gormFacebookLoginOAuthStore) Claim(ctx context.Context, stateHash, browserHash string, now time.Time) (models.FacebookLoginOAuthSession, error) {
	var session models.FacebookLoginOAuthSession
	err := db.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("state_hash = ?", stateHash).First(&session).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return errFacebookLoginOAuthInvalid
			}
			return err
		}
		if session.ExpiresAt.Before(now) {
			return errFacebookLoginOAuthExpired
		}
		if session.Status != "pending" {
			return errFacebookLoginOAuthReplay
		}
		if subtle.ConstantTimeCompare([]byte(session.BrowserHash), []byte(browserHash)) != 1 {
			return errFacebookLoginOAuthInvalid
		}
		return tx.Model(&session).Updates(map[string]any{"status": "processing", "updated_at": now}).Error
	})
	return session, err
}

func (s *gormFacebookLoginOAuthStore) Complete(ctx context.Context, id string, now time.Time) error {
	result := db.DB.WithContext(ctx).Model(&models.FacebookLoginOAuthSession{}).Where("id = ? AND status = ?", id, "processing").Updates(map[string]any{"status": "completed", "completed_at": now, "updated_at": now})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return errFacebookLoginOAuthReplay
	}
	return nil
}

func (s *gormFacebookLoginOAuthStore) Fail(ctx context.Context, id string, now time.Time) error {
	return db.DB.WithContext(ctx).Model(&models.FacebookLoginOAuthSession{}).Where("id = ? AND status = ?", id, "processing").Updates(map[string]any{"status": "failed", "updated_at": now}).Error
}

func (s *gormFacebookLoginOAuthStore) Cleanup(ctx context.Context, now time.Time) {
	_ = db.DB.WithContext(ctx).Where("expires_at < ?", now.Add(-24*time.Hour)).Delete(&models.FacebookLoginOAuthSession{}).Error
}
