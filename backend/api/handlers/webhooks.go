package handlers

import (
	"bytes"
	"errors"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/hibiken/asynq"
	"github.com/vietbui/chat-quality-agent/config"
	"github.com/vietbui/chat-quality-agent/workers"
)

// ZaloWebhookHandler processes incoming Zalo OA webhooks
func ZaloWebhookHandler(cfg *config.Config) gin.HandlerFunc {
	var asynqClient *asynq.Client
	if cfg.RedisURL != "" {
		opt, err := asynq.ParseRedisURI(cfg.RedisURL)
		if err == nil {
			asynqClient = asynq.NewClient(opt)
		}
	}

	return func(c *gin.Context) {
		// Raw-body logging is opt-in (ZALO_WEBHOOK_DEBUG=true): it dumps every
		// customer message into the logs, and the old version also appended it to
		// a developer-machine path that does not exist on the server.
		if os.Getenv("ZALO_WEBHOOK_DEBUG") == "true" {
			if bodyBytes, err := io.ReadAll(c.Request.Body); err == nil {
				c.Request.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))
				log.Printf("[debug-webhook] raw payload: %s", string(bodyBytes))
			}
		}

		var payload workers.ZaloWebhookPayload
		if err := c.ShouldBindJSON(&payload); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid payload"})
			return
		}

		// Always return 200 OK to Zalo quickly
		c.JSON(http.StatusOK, gin.H{"status": "ok"})

		if (payload.EventName != "user_send_text" && payload.EventName != "user_send_group_text") || (payload.Message.Text == "" && len(payload.Message.Attachments) == 0) {
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
