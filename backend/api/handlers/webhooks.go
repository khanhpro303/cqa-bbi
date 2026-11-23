package handlers

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/hibiken/asynq"
	"github.com/vietbui/chat-quality-agent/config"
	"github.com/vietbui/chat-quality-agent/workers"
)

// zaloWebhookSink replaces the queue in tests.
var zaloWebhookSink func(workers.ZaloWebhookPayload)

// ZaloWebhookHandler processes incoming Zalo OA webhooks
func ZaloWebhookHandler(cfg *config.Config) gin.HandlerFunc {
	var asynqClient *asynq.Client
	if cfg.RedisURL != "" {
		opt, err := asynq.ParseRedisURI(cfg.RedisURL)
		if err == nil {
			asynqClient = asynq.NewClient(opt)
		}
	}

	signatureMode := zaloSignatureMode(cfg)
	switch {
	case signatureMode == zaloSignatureOff:
		log.Printf("[zalo-webhook] WARNING: webhook signatures are not checked (set ZALO_OA_SECRET_KEY). Anyone who knows the URL can send messages as any Zalo user.")
	case signatureMode == zaloSignatureEnforce && cfg.ZaloOAWebhookSecret == "":
		log.Printf("[zalo-webhook] ERROR: ZALO_WEBHOOK_SIGNATURE=enforce without ZALO_OA_SECRET_KEY: every Zalo webhook will be rejected")
	}
	var firstVerified sync.Once

	return func(c *gin.Context) {
		bodyBytes, err := io.ReadAll(io.LimitReader(c.Request.Body, zaloWebhookMaxBody))
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid payload"})
			return
		}
		// Raw-body logging is opt-in (ZALO_WEBHOOK_DEBUG=true): it dumps every
		// customer message into the logs, and the old version also appended it to
		// a developer-machine path that does not exist on the server.
		if os.Getenv("ZALO_WEBHOOK_DEBUG") == "true" {
			log.Printf("[debug-webhook] raw payload: %s", string(bodyBytes))
		}

		var payload workers.ZaloWebhookPayload
		if err := json.Unmarshal(bodyBytes, &payload); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid payload"})
			return
		}

		// Set here, never taken from the body: only a verified event may
		// write complaint cases (workers.ZaloWebhookPayload.SignatureVerified).
		payload.SignatureVerified = false
		if signatureMode != zaloSignatureOff {
			ok := verifyZaloSignature(c.GetHeader("X-ZEvent-Signature"), payload.AppID, bodyBytes, payload.Timestamp, cfg.ZaloOAWebhookSecret)
			switch {
			case ok:
				payload.SignatureVerified = true
				firstVerified.Do(func() { log.Printf("[zalo-webhook] signature verified (mode=%s)", signatureMode) })
			case signatureMode == zaloSignatureEnforce:
				log.Printf("[zalo-drop] reason=bad_signature event=%s sender=%s ip=%s", payload.EventName, payload.Sender.ID, c.ClientIP())
				c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid signature"})
				return
			default:
				log.Printf("[zalo-webhook] signature mismatch (mode=log, not dropped) event=%s sender=%s ip=%s has_header=%t", payload.EventName, payload.Sender.ID, c.ClientIP(), c.GetHeader("X-ZEvent-Signature") != "")
			}
		}

		// Always return 200 OK to Zalo quickly
		c.JSON(http.StatusOK, gin.H{"status": "ok"})

		if !acceptedZaloEvent(payload.EventName) || (payload.Message.Text == "" && len(payload.Message.Attachments) == 0) {
			return
		}

		if zaloWebhookSink != nil {
			zaloWebhookSink(payload)
			return
		}
		// Process async via Asynq if available
		if asynqClient == nil {
			log.Printf("[zalo-webhook] REDIS_URL not configured; dropping %s from Zalo user %s", payload.EventName, payload.Sender.ID)
			return
		}
		task, err := workers.NewZaloWebhookTask(payload)
		if err != nil {
			log.Printf("[zalo-webhook] failed to build task for Zalo user %s: %v", payload.Sender.ID, err)
			return
		}
		if _, err := asynqClient.Enqueue(task, zaloTaskOptions(payload)...); err != nil {
			if errors.Is(err, asynq.ErrTaskIDConflict) {
				log.Printf("[zalo-drop] reason=duplicate_webhook sender=%s msg_id=%s", payload.Sender.ID, payload.Message.MsgID)
				return
			}
			log.Printf("[zalo-webhook] failed to enqueue %s from Zalo user %s: %v", payload.EventName, payload.Sender.ID, err)
		}
	}
}

// zaloWebhookDedupeWindow is how long a processed Zalo message id is remembered
// so a redelivered webhook for the same message is dropped instead of answered
// twice. Zalo redelivers when it misses our 200 (slow network, restart), and a
// duplicate turn re-runs the whole Langflow agent: double reply, double AI
// spend — and, once the bot can write orders, a double write.
const zaloWebhookDedupeWindow = 2 * time.Hour

// zaloTaskOptions returns the enqueue options for one Zalo message. When the
// payload carries a msg_id the task gets a deterministic TaskID (scoped by OA)
// and is retained after completion, so asynq rejects a second enqueue of the
// same message with ErrTaskIDConflict for zaloWebhookDedupeWindow. Payloads
// without a msg_id keep the old behaviour (no dedupe).
func zaloTaskOptions(payload workers.ZaloWebhookPayload) []asynq.Option {
	opts := []asynq.Option{asynq.MaxRetry(3)}
	if msgID := strings.TrimSpace(payload.Message.MsgID); msgID != "" {
		opts = append(opts,
			asynq.TaskID("zalo:"+payload.OAID+":"+msgID),
			asynq.Retention(zaloWebhookDedupeWindow),
		)
	}
	return opts
}

const zaloWebhookMaxBody = 1 << 20

const (
	zaloSignatureOff     = "off"
	zaloSignatureLog     = "log"
	zaloSignatureEnforce = "enforce"
)

// zaloSignatureMode picks how strictly Zalo webhook signatures are checked.
// With no explicit mode, a configured secret means enforce: an unsigned or
// badly signed event is dropped. ZALO_WEBHOOK_SIGNATURE=log checks a new
// secret without dropping anything.
func zaloSignatureMode(cfg *config.Config) string {
	switch cfg.ZaloWebhookSignature {
	case zaloSignatureOff, zaloSignatureLog, zaloSignatureEnforce:
		return cfg.ZaloWebhookSignature
	}
	if cfg.ZaloOAWebhookSecret != "" {
		return zaloSignatureEnforce
	}
	return zaloSignatureOff
}

// verifyZaloSignature checks X-ZEvent-Signature, which Zalo sets to
// "mac=" + hex(sha256(app_id + raw body + timestamp + OA secret key)).
func verifyZaloSignature(header, appID string, body []byte, timestamp, secret string) bool {
	if secret == "" || header == "" {
		return false
	}
	got := strings.TrimSpace(header)
	if i := strings.Index(got, "="); i >= 0 && strings.EqualFold(strings.TrimSpace(got[:i]), "mac") {
		got = strings.TrimSpace(got[i+1:])
	}
	sum := sha256.Sum256([]byte(appID + string(body) + timestamp + secret))
	want := hex.EncodeToString(sum[:])
	return subtle.ConstantTimeCompare([]byte(strings.ToLower(got)), []byte(want)) == 1
}

// acceptedZaloEvent reports whether a Zalo webhook event reaches the bot:
// text, and images (the bot reads them, see workers/image_reader.go), in
// 1:1 and group chats.
func acceptedZaloEvent(name string) bool {
	switch name {
	case "user_send_text", "user_send_group_text", "user_send_image", "user_send_group_image":
		return true
	}
	return false
}
