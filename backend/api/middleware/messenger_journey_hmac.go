package middleware

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

const maxMessengerRelayBodyBytes = 1 << 20

var messengerRelayRequestIDPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[1-8][0-9a-fA-F]{3}-[89aAbB][0-9a-fA-F]{3}-[0-9a-fA-F]{12}$`)

// MessengerRelayHMACAuth protects the AdBazooka webhook relay. Unlike the
// read-only export signature, this canonical form also binds the raw body hash.
// The handler still performs event-level idempotency because request replay
// guards are deliberately an additional, short-lived defence only.
func MessengerRelayHMACAuth(secret, tenantID string, maxSkew time.Duration) gin.HandlerFunc {
	guard := newReplayGuard()
	return func(c *gin.Context) {
		if secret == "" || tenantID == "" {
			c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{"error": "messenger_relay_not_configured"})
			return
		}
		if c.Request.Method != http.MethodPost {
			c.AbortWithStatusJSON(http.StatusMethodNotAllowed, gin.H{"error": "method_not_allowed"})
			return
		}

		requestID := strings.TrimSpace(c.GetHeader(internalRequestIDHeader))
		timestampRaw := strings.TrimSpace(c.GetHeader(internalTimestampHeader))
		signature := strings.TrimSpace(c.GetHeader(internalSignatureHeader))
		if !messengerRelayRequestIDPattern.MatchString(requestID) || timestampRaw == "" || signature == "" {
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

		body, err := io.ReadAll(io.LimitReader(c.Request.Body, maxMessengerRelayBodyBytes+1))
		if err != nil || len(body) > maxMessengerRelayBodyBytes {
			c.AbortWithStatusJSON(http.StatusRequestEntityTooLarge, gin.H{"error": "payload_too_large"})
			return
		}
		c.Request.Body = io.NopCloser(bytes.NewReader(body))
		bodyHash := sha256.Sum256(body)
		canonical := timestampRaw + "\n" + requestID + "\n" + c.Request.Method + "\n" + c.Request.URL.EscapedPath() + "\n" + c.Request.URL.RawQuery + "\n" + hex.EncodeToString(bodyHash[:])
		mac := hmac.New(sha256.New, []byte(secret))
		_, _ = mac.Write([]byte(canonical))
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
