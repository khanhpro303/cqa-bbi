package middleware

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/vietbui/chat-quality-agent/api/contracts"
	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/db/models"
	"github.com/vietbui/chat-quality-agent/pkg"
)

const (
	internalSignatureHeader = "X-CQA-Signature"
	internalTimestampHeader = "X-CQA-Timestamp"
	internalRequestIDHeader = "X-CQA-Request-Id"
)

type replayGuard struct {
	mu       sync.Mutex
	requests map[string]time.Time
}

type InternalAuthContext struct {
	Secret string
	Scope  string
}

type InternalSecretResolver func(c *gin.Context, body []byte) (*InternalAuthContext, error)

func newReplayGuard() *replayGuard {
	return &replayGuard{requests: make(map[string]time.Time)}
}

func (g *replayGuard) Seen(requestID string, now time.Time, ttl time.Duration) bool {
	g.mu.Lock()
	defer g.mu.Unlock()

	cutoff := now.Add(-ttl)
	for id, seenAt := range g.requests {
		if seenAt.Before(cutoff) {
			delete(g.requests, id)
		}
	}

	if _, exists := g.requests[requestID]; exists {
		return true
	}

	g.requests[requestID] = now
	return false
}

func StaticInternalSecretResolver(secret string) InternalSecretResolver {
	return func(c *gin.Context, body []byte) (*InternalAuthContext, error) {
		return &InternalAuthContext{Secret: secret, Scope: c.FullPath()}, nil
	}
}

func PersonalZaloImportSecretResolver(encryptionKey, fallbackSecret string) InternalSecretResolver {
	return func(c *gin.Context, body []byte) (*InternalAuthContext, error) {
		var req contracts.PersonalZaloImportRequest
		if err := json.Unmarshal(body, &req); err != nil {
			return nil, err
		}
		if req.TenantID == "" || req.ChannelID == "" {
			return nil, errors.New("missing tenant_id or channel_id")
		}

		var channel models.Channel
		if err := db.DB.Where("id = ? AND tenant_id = ?", req.ChannelID, req.TenantID).First(&channel).Error; err != nil {
			return nil, err
		}
		if channel.ChannelType != "personal_zalo_import" {
			return nil, errors.New("channel_type_not_supported")
		}

		credBytes, err := pkg.Decrypt(channel.CredentialsEncrypted, encryptionKey)
		if err != nil {
			return nil, err
		}

		var creds contracts.PersonalZaloImportCredentials
		if err := json.Unmarshal(credBytes, &creds); err != nil {
			return nil, err
		}

		secret := creds.ImportSecret
		if secret == "" {
			secret = fallbackSecret
		}
		if secret == "" {
			return nil, errors.New("missing_import_secret")
		}

		return &InternalAuthContext{
			Secret: secret,
			Scope:  channel.ID,
		}, nil
	}
}

func InternalHMACAuth(resolveSecret InternalSecretResolver, maxSkew time.Duration) gin.HandlerFunc {
	guard := newReplayGuard()

	return func(c *gin.Context) {
		requestID := c.GetHeader(internalRequestIDHeader)
		timestampRaw := c.GetHeader(internalTimestampHeader)
		signature := c.GetHeader(internalSignatureHeader)

		if requestID == "" || timestampRaw == "" || signature == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "internal_auth_required"})
			return
		}

		timestampUnix, err := strconv.ParseInt(timestampRaw, 10, 64)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid_internal_timestamp"})
			return
		}

		now := time.Now().UTC()
		timestamp := time.Unix(timestampUnix, 0).UTC()
		delta := now.Sub(timestamp)
		if delta < 0 {
			delta = -delta
		}
		if delta > maxSkew {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "stale_internal_request"})
			return
		}

		body, err := io.ReadAll(c.Request.Body)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "read_body_failed"})
			return
		}
		c.Request.Body = io.NopCloser(bytes.NewReader(body))

		authCtx, err := resolveSecret(c, body)
		if err != nil || authCtx == nil || authCtx.Secret == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid_internal_target"})
			return
		}

		scopedRequestID := requestID
		if authCtx.Scope != "" {
			scopedRequestID = authCtx.Scope + ":" + requestID
		}
		if guard.Seen(scopedRequestID, now, maxSkew) {
			c.AbortWithStatusJSON(http.StatusConflict, gin.H{"error": "replayed_internal_request"})
			return
		}

		mac := hmac.New(sha256.New, []byte(authCtx.Secret))
		mac.Write([]byte(timestampRaw))
		mac.Write([]byte("."))
		mac.Write([]byte(requestID))
		mac.Write([]byte("."))
		mac.Write(body)
		expected := hex.EncodeToString(mac.Sum(nil))
		if !hmac.Equal([]byte(expected), []byte(signature)) {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid_internal_signature"})
			return
		}

		c.Set("internal_request_id", requestID)
		c.Next()
	}
}

// ABZExportHMACAuth protects the one read-only AdBazooka export route with a
// dedicated secret and tenant. Its signature covers the HTTP method, path and
// exact query string so a valid request cannot be replayed for another cursor.
func ABZExportHMACAuth(secret, tenantID string, maxSkew time.Duration) gin.HandlerFunc {
	guard := newReplayGuard()
	return func(c *gin.Context) {
		if secret == "" || tenantID == "" {
			c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{"error": "abz_export_not_configured"})
			return
		}
		requestID := c.GetHeader(internalRequestIDHeader)
		timestampRaw := c.GetHeader(internalTimestampHeader)
		signature := c.GetHeader(internalSignatureHeader)
		if requestID == "" || timestampRaw == "" || signature == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "internal_auth_required"})
			return
		}
		timestampUnix, err := strconv.ParseInt(timestampRaw, 10, 64)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid_internal_timestamp"})
			return
		}
		now := time.Now().UTC()
		delta := now.Sub(time.Unix(timestampUnix, 0).UTC())
		if delta < 0 {
			delta = -delta
		}
		if delta > maxSkew {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "stale_internal_request"})
			return
		}
		canonical := timestampRaw + "\n" + requestID + "\n" + c.Request.Method + "\n" + c.Request.URL.EscapedPath() + "\n" + c.Request.URL.RawQuery
		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write([]byte(canonical))
		expected := hex.EncodeToString(mac.Sum(nil))
		if !hmac.Equal([]byte(expected), []byte(signature)) {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid_internal_signature"})
			return
		}
		if guard.Seen(tenantID+":"+requestID, now, maxSkew) {
			c.AbortWithStatusJSON(http.StatusConflict, gin.H{"error": "replayed_internal_request"})
			return
		}
		c.Set("tenant_id", tenantID)
		c.Set("internal_request_id", requestID)
		c.Next()
	}
}
