package handlers

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/vietbui/chat-quality-agent/api/middleware"
	"github.com/vietbui/chat-quality-agent/config"
	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/db/models"
	"github.com/vietbui/chat-quality-agent/pkg"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	facebookPageOAuthStatePrefix = "page_"
	facebookPageOAuthCookie      = "cqa_fb_page_oauth"
	facebookPageOAuthTTL         = 10 * time.Minute
)

var (
	errFacebookPageOAuthInvalid     = errors.New("facebook OAuth session is invalid")
	errFacebookPageOAuthExpired     = errors.New("facebook OAuth session expired")
	errFacebookPageOAuthReplay      = errors.New("facebook OAuth state already used")
	errFacebookPageNotAuthorized    = errors.New("facebook page was not authorized")
	errFacebookPermissionsMissing   = errors.New("required Facebook permissions are missing")
	errFacebookSubscriptionRejected = errors.New("Facebook page subscription rejected")
	errFacebookNoPages              = errors.New("no Facebook pages found")
	errFacebookProviderUnavailable  = errors.New("Facebook provider unavailable")
)

var facebookPageRequiredScopes = []string{
	"pages_show_list",
	"pages_messaging",
	"pages_read_engagement",
	"pages_manage_metadata",
}

type facebookOAuthPage struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	AccessToken string   `json:"access_token"`
	Tasks       []string `json:"tasks,omitempty"`
}

type facebookPagePublic struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type facebookPageSelectRequest struct {
	PageID       string `json:"page_id" binding:"required"`
	SyncInterval int    `json:"sync_interval"`
	SyncFiles    bool   `json:"sync_files"`
}

type facebookPageOAuthStore interface {
	Create(context.Context, *models.FacebookOAuthSession) error
	Claim(context.Context, string, string, time.Time) (models.FacebookOAuthSession, error)
	Authorize(context.Context, string, []byte, time.Time) error
	Load(context.Context, string, string, string, time.Time) (models.FacebookOAuthSession, error)
	Reserve(context.Context, string, string, string, string, time.Time) (models.FacebookOAuthSession, error)
	Select(context.Context, models.FacebookOAuthSession, facebookOAuthPage, facebookPageSelectRequest, []byte, time.Time) (models.Channel, bool, error)
	Fail(context.Context, string) error
	Cleanup(context.Context, time.Time)
}

type FacebookPageOAuthHandler struct {
	cfg          *config.Config
	graphBaseURL string
	httpClient   *http.Client
	now          func() time.Time
	store        facebookPageOAuthStore
}

func NewFacebookPageOAuthHandler(cfg *config.Config) *FacebookPageOAuthHandler {
	h := &FacebookPageOAuthHandler{
		cfg:          cfg,
		graphBaseURL: facebookGraphBaseURL,
		httpClient: &http.Client{
			Timeout:       30 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
		now: time.Now,
	}
	h.store = &gormFacebookPageOAuthStore{cfg: cfg}
	return h
}

func (h *FacebookPageOAuthHandler) enabled() bool {
	return h != nil && h.cfg != nil && strings.TrimSpace(h.cfg.FacebookAppID) != "" && strings.TrimSpace(h.cfg.FacebookAppSecret) != "" && strings.TrimSpace(h.cfg.EncryptionKey) != "" && strings.TrimSpace(h.cfg.FacebookPageLoginConfigID) != ""
}

func (h *FacebookPageOAuthHandler) callbackURL(c *gin.Context) string {
	baseURL := browserRequestOrigin(c)
	if baseURL == "" {
		baseURL = getBaseURL(c)
	}
	return baseURL + "/api/v1/channels/facebook/callback"
}

func browserRequestOrigin(c *gin.Context) string {
	for _, raw := range []string{c.GetHeader("Origin"), c.GetHeader("Referer")} {
		candidate, err := url.Parse(strings.TrimSpace(raw))
		if err != nil || candidate.Host == "" || !strings.EqualFold(candidate.Host, c.Request.Host) {
			continue
		}
		if candidate.Scheme != "http" && candidate.Scheme != "https" {
			continue
		}
		return candidate.Scheme + "://" + candidate.Host
	}
	return ""
}

func (h *FacebookPageOAuthHandler) Config(c *gin.Context) {
	facebookOAuthNoStore(c)
	c.JSON(http.StatusOK, gin.H{"enabled": h.enabled(), "callback_url": h.callbackURL(c)})
}

func (h *FacebookPageOAuthHandler) Start(c *gin.Context) {
	facebookOAuthNoStore(c)
	if !h.enabled() {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "facebook_unavailable"})
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
	redirectURI := h.callbackURL(c)
	session := models.FacebookOAuthSession{
		ID:          pkg.NewUUID(),
		TenantID:    middleware.GetTenantID(c),
		UserID:      middleware.GetUserID(c),
		StateHash:   hashFacebookOAuthSecret(facebookPageOAuthStatePrefix + stateSecret),
		BrowserHash: hashFacebookOAuthSecret(browserSecret),
		RedirectURI: redirectURI,
		Status:      "pending",
		ExpiresAt:   now.Add(facebookPageOAuthTTL),
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	h.store.Cleanup(c.Request.Context(), now)
	if err := h.store.Create(c.Request.Context(), &session); err != nil {
		log.Printf("[facebook-page-oauth] create session failed: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "connect_failed"})
		return
	}

	http.SetCookie(c.Writer, &http.Cookie{
		Name:     facebookPageOAuthCookie,
		Value:    browserSecret,
		Path:     "/api/v1/channels/facebook/callback",
		MaxAge:   int(facebookPageOAuthTTL.Seconds()),
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	})

	authorizeURL, _ := url.Parse("https://www.facebook.com/" + facebookAPIVersion(h.cfg) + "/dialog/oauth")
	query := authorizeURL.Query()
	query.Set("client_id", h.cfg.FacebookAppID)
	query.Set("redirect_uri", redirectURI)
	query.Set("state", facebookPageOAuthStatePrefix+stateSecret)
	query.Set("config_id", h.cfg.FacebookPageLoginConfigID)
	query.Set("response_type", "code")
	authorizeURL.RawQuery = query.Encode()
	c.JSON(http.StatusOK, gin.H{"redirect_url": authorizeURL.String()})
}

func FacebookOAuthCallbackDispatcher(pageOAuth *FacebookPageOAuthHandler) gin.HandlerFunc {
	return func(c *gin.Context) {
		if strings.HasPrefix(c.Query("state"), facebookPageOAuthStatePrefix) {
			pageOAuth.Callback(c)
			return
		}
		FacebookOAuthCallback(c)
	}
}

func (h *FacebookPageOAuthHandler) Callback(c *gin.Context) {
	facebookOAuthNoStore(c)
	state := c.Query("state")
	if !h.enabled() || !strings.HasPrefix(state, facebookPageOAuthStatePrefix) {
		h.redirectError(c, "invalid_state")
		return
	}
	cookie, err := c.Cookie(facebookPageOAuthCookie)
	if err != nil || cookie == "" {
		h.redirectError(c, "browser_mismatch")
		return
	}
	http.SetCookie(c.Writer, &http.Cookie{Name: facebookPageOAuthCookie, Value: "", Path: "/api/v1/channels/facebook/callback", MaxAge: -1, HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode})

	now := h.now().UTC()
	session, err := h.store.Claim(c.Request.Context(), hashFacebookOAuthSecret(state), hashFacebookOAuthSecret(cookie), now)
	if err != nil {
		if !errors.Is(err, errFacebookPageOAuthInvalid) && !errors.Is(err, errFacebookPageOAuthExpired) && !errors.Is(err, errFacebookPageOAuthReplay) {
			log.Printf("[facebook-page-oauth] claim state failed: error=%v", err)
		} else {
			log.Printf("[security] Facebook Page OAuth callback rejected: reason=%s ip=%s", safeFacebookOAuthError(err), c.ClientIP())
		}
		h.redirectError(c, safeFacebookOAuthError(err))
		return
	}
	if c.Query("error") != "" || c.Query("code") == "" {
		_ = h.store.Fail(c.Request.Context(), session.ID)
		h.redirectTenantError(c, session.TenantID, "oauth_denied")
		return
	}

	redirectURI := strings.TrimSpace(session.RedirectURI)
	if redirectURI == "" {
		redirectURI = h.callbackURL(c)
	}
	userToken, err := h.exchangeCode(c.Request.Context(), c.Query("code"), redirectURI)
	if err == nil {
		userToken, err = h.exchangeLongLivedToken(c.Request.Context(), userToken)
	}
	if err == nil {
		err = h.requirePermissions(c.Request.Context(), userToken)
	}
	var pages []facebookOAuthPage
	if err == nil {
		pages, err = h.fetchPages(c.Request.Context(), userToken)
	}
	if err != nil || len(pages) == 0 {
		_ = h.store.Fail(c.Request.Context(), session.ID)
		log.Printf("[facebook-page-oauth] provider callback failed: session=%s reason=%s", session.ID, safeFacebookOAuthError(err))
		h.redirectTenantError(c, session.TenantID, safeFacebookOAuthError(err))
		return
	}
	payload, err := json.Marshal(pages)
	if err == nil {
		payload, err = pkg.Encrypt(payload, h.cfg.EncryptionKey)
	}
	if err != nil {
		_ = h.store.Fail(c.Request.Context(), session.ID)
		log.Printf("[facebook-page-oauth] encrypt page selection failed: session=%s error=%v", session.ID, err)
		h.redirectTenantError(c, session.TenantID, "connect_failed")
		return
	}
	if err := h.store.Authorize(c.Request.Context(), session.ID, payload, now.Add(facebookPageOAuthTTL)); err != nil {
		_ = h.store.Fail(c.Request.Context(), session.ID)
		log.Printf("[facebook-page-oauth] authorize session failed: session=%s error=%v", session.ID, err)
		h.redirectTenantError(c, session.TenantID, "connect_failed")
		return
	}
	c.Redirect(http.StatusFound, fmt.Sprintf("/%s/channels?facebook_connect=%s", url.PathEscape(session.TenantID), url.QueryEscape(session.ID)))
}

func (h *FacebookPageOAuthHandler) Pages(c *gin.Context) {
	facebookOAuthNoStore(c)
	session, pages, ok := h.loadAuthorizedSession(c)
	if !ok {
		return
	}
	publicPages := make([]facebookPagePublic, 0, len(pages))
	for _, page := range pages {
		publicPages = append(publicPages, facebookPagePublic{ID: page.ID, Name: page.Name})
	}
	c.JSON(http.StatusOK, gin.H{"pages": publicPages, "expires_at": session.ExpiresAt})
}

func (h *FacebookPageOAuthHandler) Select(c *gin.Context) {
	facebookOAuthNoStore(c)
	var req facebookPageSelectRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request"})
		return
	}
	if req.SyncInterval == 0 {
		req.SyncInterval = 15
	}
	if req.SyncInterval < 1 || req.SyncInterval > 1440 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_sync_interval"})
		return
	}
	session, pages, ok := h.loadAuthorizedSession(c)
	if !ok {
		return
	}
	var selected *facebookOAuthPage
	for i := range pages {
		if subtle.ConstantTimeCompare([]byte(pages[i].ID), []byte(req.PageID)) == 1 {
			selected = &pages[i]
			break
		}
	}
	if selected == nil {
		c.JSON(http.StatusForbidden, gin.H{"error": "page_not_found"})
		return
	}
	reserved, reserveErr := h.store.Reserve(c.Request.Context(), session.ID, session.TenantID, session.UserID, selected.ID, h.now().UTC())
	if reserveErr != nil {
		if errors.Is(reserveErr, errFacebookPageOAuthReplay) || errors.Is(reserveErr, errFacebookPageOAuthInvalid) {
			c.JSON(http.StatusConflict, gin.H{"error": "session_used"})
		} else {
			log.Printf("[facebook-page-oauth] reserve selection failed: session=%s error=%v", session.ID, reserveErr)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "connect_failed"})
		}
		return
	}
	session = reserved
	if err := h.subscribePage(c.Request.Context(), *selected); err != nil {
		log.Printf("[facebook-page-oauth] page subscription failed: session=%s page=%s", session.ID, selected.ID)
		c.JSON(http.StatusBadGateway, gin.H{"error": "subscription_failed"})
		return
	}
	credentials, err := json.Marshal(map[string]string{
		"page_id": selected.ID, "access_token": selected.AccessToken,
		"app_id": h.cfg.FacebookAppID, "app_secret": h.cfg.FacebookAppSecret,
	})
	if err == nil {
		credentials, err = pkg.Encrypt(credentials, h.cfg.EncryptionKey)
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "encryption_failed"})
		return
	}
	channel, created, err := h.store.Select(c.Request.Context(), session, *selected, req, credentials, h.now().UTC())
	if err != nil {
		if errors.Is(err, errFacebookPageOAuthReplay) {
			c.JSON(http.StatusConflict, gin.H{"error": "session_used"})
			return
		}
		log.Printf("[facebook-page-oauth] persist selection failed: session=%s page=%s error=%v", session.ID, selected.ID, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "connect_failed"})
		return
	}
	db.LogActivity(session.TenantID, session.UserID, middleware.GetUserEmail(c), "channel.facebook_connect", "channel", channel.ID,
		fmt.Sprintf("Đã kết nối Facebook Page '%s' qua OAuth", channel.Name), "", c.ClientIP())
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	c.JSON(status, channelToResponse(channel))
}

func (h *FacebookPageOAuthHandler) loadAuthorizedSession(c *gin.Context) (models.FacebookOAuthSession, []facebookOAuthPage, bool) {
	session, err := h.store.Load(c.Request.Context(), c.Param("sessionId"), middleware.GetTenantID(c), middleware.GetUserID(c), h.now().UTC())
	if err != nil {
		if !errors.Is(err, errFacebookPageOAuthInvalid) && !errors.Is(err, errFacebookPageOAuthExpired) && !errors.Is(err, errFacebookPageOAuthReplay) {
			log.Printf("[facebook-page-oauth] load session failed: session=%s error=%v", c.Param("sessionId"), err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "connect_failed"})
			return models.FacebookOAuthSession{}, nil, false
		}
		status := http.StatusNotFound
		code := "session_not_found"
		if errors.Is(err, errFacebookPageOAuthExpired) {
			status, code = http.StatusGone, "session_expired"
		} else if errors.Is(err, errFacebookPageOAuthReplay) {
			status, code = http.StatusConflict, "session_used"
		}
		c.JSON(status, gin.H{"error": code})
		return models.FacebookOAuthSession{}, nil, false
	}
	decrypted, err := pkg.Decrypt(session.PagesEncrypted, h.cfg.EncryptionKey)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "connect_failed"})
		return models.FacebookOAuthSession{}, nil, false
	}
	var pages []facebookOAuthPage
	if err := json.Unmarshal(decrypted, &pages); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "connect_failed"})
		return models.FacebookOAuthSession{}, nil, false
	}
	return session, pages, true
}

func (h *FacebookPageOAuthHandler) exchangeCode(ctx context.Context, code, redirectURI string) (string, error) {
	endpoint := h.graphURL("oauth/access_token")
	q := endpoint.Query()
	q.Set("client_id", h.cfg.FacebookAppID)
	q.Set("client_secret", h.cfg.FacebookAppSecret)
	q.Set("redirect_uri", redirectURI)
	q.Set("code", code)
	endpoint.RawQuery = q.Encode()
	var response struct {
		AccessToken string `json:"access_token"`
	}
	if err := h.graphJSON(ctx, http.MethodGet, endpoint, "", nil, &response); err != nil || response.AccessToken == "" {
		return "", errors.New("token_exchange_failed")
	}
	return response.AccessToken, nil
}

func (h *FacebookPageOAuthHandler) exchangeLongLivedToken(ctx context.Context, token string) (string, error) {
	endpoint := h.graphURL("oauth/access_token")
	q := endpoint.Query()
	q.Set("grant_type", "fb_exchange_token")
	q.Set("client_id", h.cfg.FacebookAppID)
	q.Set("client_secret", h.cfg.FacebookAppSecret)
	q.Set("fb_exchange_token", token)
	endpoint.RawQuery = q.Encode()
	var response struct {
		AccessToken string `json:"access_token"`
	}
	if err := h.graphJSON(ctx, http.MethodGet, endpoint, "", nil, &response); err != nil || response.AccessToken == "" {
		return "", errors.New("token_exchange_failed")
	}
	return response.AccessToken, nil
}

func (h *FacebookPageOAuthHandler) requirePermissions(ctx context.Context, token string) error {
	endpoint := h.graphURL("me/permissions")
	var response struct {
		Data []struct{ Permission, Status string } `json:"data"`
	}
	if err := h.graphJSON(ctx, http.MethodGet, endpoint, token, nil, &response); err != nil {
		return errors.New("permissions_check_failed")
	}
	granted := make(map[string]bool, len(response.Data))
	for _, permission := range response.Data {
		granted[permission.Permission] = permission.Status == "granted"
	}
	for _, required := range facebookPageRequiredScopes {
		if !granted[required] {
			return errFacebookPermissionsMissing
		}
	}
	return nil
}

func (h *FacebookPageOAuthHandler) fetchPages(ctx context.Context, token string) ([]facebookOAuthPage, error) {
	pages := make([]facebookOAuthPage, 0)
	after := ""
	seen := map[string]bool{}
	for pageNumber := 0; pageNumber < 50; pageNumber++ {
		endpoint := h.graphURL("me/accounts")
		q := endpoint.Query()
		q.Set("fields", "id,name,access_token,tasks")
		q.Set("limit", "100")
		if after != "" {
			q.Set("after", after)
		}
		endpoint.RawQuery = q.Encode()
		var response struct {
			Data   []facebookOAuthPage `json:"data"`
			Paging struct {
				Cursors struct {
					After string `json:"after"`
				} `json:"cursors"`
				Next string `json:"next"`
			} `json:"paging"`
		}
		if err := h.graphJSON(ctx, http.MethodGet, endpoint, token, nil, &response); err != nil {
			return nil, err
		}
		for _, page := range response.Data {
			if page.ID == "" || page.AccessToken == "" || seen[page.ID] || !facebookPageCanMessage(page.Tasks) {
				continue
			}
			seen[page.ID] = true
			pages = append(pages, page)
		}
		nextAfter := response.Paging.Cursors.After
		if response.Paging.Next == "" || nextAfter == "" || nextAfter == after {
			break
		}
		after = nextAfter
	}
	if len(pages) == 0 {
		return nil, errFacebookNoPages
	}
	return pages, nil
}

func (h *FacebookPageOAuthHandler) subscribePage(ctx context.Context, page facebookOAuthPage) error {
	endpoint := h.graphURL(page.ID + "/subscribed_apps")
	form := url.Values{"subscribed_fields": {"messages,messaging_postbacks,messaging_referrals"}}
	var response struct {
		Success bool `json:"success"`
	}
	if err := h.graphJSON(ctx, http.MethodPost, endpoint, page.AccessToken, form, &response); err != nil || !response.Success {
		return errFacebookSubscriptionRejected
	}
	return nil
}

func (h *FacebookPageOAuthHandler) graphURL(path string) *url.URL {
	base, _ := url.Parse(strings.TrimRight(h.graphBaseURL, "/") + "/" + facebookAPIVersion(h.cfg) + "/")
	ref, _ := url.Parse(strings.TrimLeft(path, "/"))
	return base.ResolveReference(ref)
}

func (h *FacebookPageOAuthHandler) graphJSON(ctx context.Context, method string, endpoint *url.URL, token string, form url.Values, target any) error {
	if token != "" {
		q := endpoint.Query()
		q.Set("appsecret_proof", h.appSecretProof(token))
		endpoint.RawQuery = q.Encode()
	}
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint.String(), body)
	if err != nil {
		return err
	}
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := h.httpClient.Do(req)
	if err != nil {
		return errFacebookProviderUnavailable
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
		return errors.New("facebook_provider_rejected_request")
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(target); err != nil {
		return errors.New("facebook_provider_invalid_response")
	}
	return nil
}

func (h *FacebookPageOAuthHandler) appSecretProof(token string) string {
	mac := hmac.New(sha256.New, []byte(h.cfg.FacebookAppSecret))
	_, _ = mac.Write([]byte(token))
	return hex.EncodeToString(mac.Sum(nil))
}

func (h *FacebookPageOAuthHandler) redirectError(c *gin.Context, code string) {
	c.Redirect(http.StatusFound, "/login?facebook_connect_error="+url.QueryEscape(code))
}
func (h *FacebookPageOAuthHandler) redirectTenantError(c *gin.Context, tenantID, code string) {
	c.Redirect(http.StatusFound, fmt.Sprintf("/%s/channels?facebook_connect_error=%s", url.PathEscape(tenantID), url.QueryEscape(code)))
}

func facebookAPIVersion(cfg *config.Config) string {
	version := strings.TrimSpace(cfg.FacebookAPIVersion)
	if version == "" {
		return "v26.0"
	}
	return version
}

func hashFacebookOAuthSecret(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func safeFacebookOAuthError(err error) string {
	switch {
	case err == nil:
		return "connect_failed"
	case errors.Is(err, errFacebookPageOAuthExpired):
		return "session_expired"
	case errors.Is(err, errFacebookPageOAuthReplay):
		return "session_used"
	case errors.Is(err, errFacebookPermissionsMissing):
		return "permissions_missing"
	case errors.Is(err, errFacebookNoPages):
		return "no_pages"
	case errors.Is(err, errFacebookProviderUnavailable):
		return "facebook_unavailable"
	default:
		return "connect_failed"
	}
}

func facebookPageCanMessage(tasks []string) bool {
	if len(tasks) == 0 {
		return true
	}
	for _, task := range tasks {
		if task == "MESSAGING" || task == "MODERATE" {
			return true
		}
	}
	return false
}

func facebookOAuthNoStore(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	c.Header("Pragma", "no-cache")
	c.Header("Referrer-Policy", "no-referrer")
}

type gormFacebookPageOAuthStore struct{ cfg *config.Config }

func (s *gormFacebookPageOAuthStore) Create(ctx context.Context, session *models.FacebookOAuthSession) error {
	return db.DB.WithContext(ctx).Create(session).Error
}

func (s *gormFacebookPageOAuthStore) Claim(ctx context.Context, stateHash, browserHash string, now time.Time) (models.FacebookOAuthSession, error) {
	var session models.FacebookOAuthSession
	err := db.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("state_hash = ?", stateHash).First(&session).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return errFacebookPageOAuthInvalid
			}
			return err
		}
		if session.ExpiresAt.Before(now) {
			return errFacebookPageOAuthExpired
		}
		if session.Status != "pending" {
			return errFacebookPageOAuthReplay
		}
		if subtle.ConstantTimeCompare([]byte(session.BrowserHash), []byte(browserHash)) != 1 {
			return errFacebookPageOAuthInvalid
		}
		return tx.Model(&session).Updates(map[string]any{"status": "processing", "updated_at": now}).Error
	})
	return session, err
}

func (s *gormFacebookPageOAuthStore) Authorize(ctx context.Context, id string, pages []byte, expires time.Time) error {
	result := db.DB.WithContext(ctx).Model(&models.FacebookOAuthSession{}).Where("id = ? AND status = ?", id, "processing").Updates(map[string]any{"status": "authorized", "pages_encrypted": pages, "expires_at": expires, "updated_at": time.Now().UTC()})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return errFacebookPageOAuthReplay
	}
	return nil
}

func (s *gormFacebookPageOAuthStore) Load(ctx context.Context, id, tenantID, userID string, now time.Time) (models.FacebookOAuthSession, error) {
	var session models.FacebookOAuthSession
	if err := db.DB.WithContext(ctx).Where("id = ? AND tenant_id = ? AND user_id = ?", id, tenantID, userID).First(&session).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return session, errFacebookPageOAuthInvalid
		}
		return session, err
	}
	if session.ExpiresAt.Before(now) {
		return session, errFacebookPageOAuthExpired
	}
	if session.Status != "authorized" && session.Status != "selecting" {
		return session, errFacebookPageOAuthReplay
	}
	return session, nil
}

func (s *gormFacebookPageOAuthStore) Reserve(ctx context.Context, id, tenantID, userID, pageID string, now time.Time) (models.FacebookOAuthSession, error) {
	var session models.FacebookOAuthSession
	err := db.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND tenant_id = ? AND user_id = ?", id, tenantID, userID).First(&session).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return errFacebookPageOAuthInvalid
			}
			return err
		}
		if session.ExpiresAt.Before(now) {
			return errFacebookPageOAuthReplay
		}
		if session.Status == "selecting" {
			if session.SelectedPageID != pageID {
				return errFacebookPageOAuthReplay
			}
			return nil
		}
		if session.Status != "authorized" {
			return errFacebookPageOAuthReplay
		}
		session.Status, session.SelectedPageID, session.UpdatedAt = "selecting", pageID, now
		return tx.Model(&session).Updates(map[string]any{"status": session.Status, "selected_page_id": pageID, "updated_at": now}).Error
	})
	return session, err
}

func (s *gormFacebookPageOAuthStore) Select(ctx context.Context, expected models.FacebookOAuthSession, page facebookOAuthPage, req facebookPageSelectRequest, credentials []byte, now time.Time) (models.Channel, bool, error) {
	var channel models.Channel
	created := false
	err := db.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var session models.FacebookOAuthSession
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND tenant_id = ? AND user_id = ?", expected.ID, expected.TenantID, expected.UserID).First(&session).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return errFacebookPageOAuthInvalid
			}
			return err
		}
		if session.Status != "selecting" || session.SelectedPageID != page.ID || session.ExpiresAt.Before(now) {
			return errFacebookPageOAuthReplay
		}
		var tenant models.Tenant
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", expected.TenantID).First(&tenant).Error; err != nil {
			return err
		}
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id = ? AND channel_type = ? AND external_id = ?", expected.TenantID, "facebook", page.ID).First(&channel).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			metadata, _ := json.Marshal(map[string]any{"sync_interval": req.SyncInterval, "sync_files": req.SyncFiles})
			channel = models.Channel{ID: pkg.NewUUID(), TenantID: expected.TenantID, ChannelType: "facebook", Name: page.Name, ExternalID: page.ID, CredentialsEncrypted: credentials, IsActive: true, Metadata: string(metadata), CreatedAt: now, UpdatedAt: now}
			if err := tx.Create(&channel).Error; err != nil {
				return err
			}
			created = true
		} else if err != nil {
			return err
		} else {
			metadata := map[string]any{}
			_ = json.Unmarshal([]byte(channel.Metadata), &metadata)
			metadata["sync_interval"], metadata["sync_files"] = req.SyncInterval, req.SyncFiles
			encoded, _ := json.Marshal(metadata)
			if err := tx.Model(&channel).Updates(map[string]any{"credentials_encrypted": credentials, "metadata": string(encoded), "is_active": true, "updated_at": now}).Error; err != nil {
				return err
			}
			channel.CredentialsEncrypted, channel.Metadata, channel.IsActive, channel.UpdatedAt = credentials, string(encoded), true, now
		}
		completed := now
		return tx.Model(&session).Updates(map[string]any{"status": "completed", "selected_page_id": page.ID, "pages_encrypted": nil, "completed_at": &completed, "updated_at": now}).Error
	})
	return channel, created, err
}

func (s *gormFacebookPageOAuthStore) Fail(ctx context.Context, id string) error {
	return db.DB.WithContext(ctx).Model(&models.FacebookOAuthSession{}).Where("id = ?", id).Updates(map[string]any{"status": "failed", "pages_encrypted": nil, "updated_at": time.Now().UTC()}).Error
}
func (s *gormFacebookPageOAuthStore) Cleanup(ctx context.Context, now time.Time) {
	_ = db.DB.WithContext(ctx).Where("expires_at < ?", now).Delete(&models.FacebookOAuthSession{}).Error
}
