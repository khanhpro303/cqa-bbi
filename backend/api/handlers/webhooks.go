package handlers

import (
	"bytes"
	"io"
	"log"
	"net/http"
	"os"

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
		if _, err := asynqClient.Enqueue(task, asynq.MaxRetry(3)); err != nil {
			log.Printf("[zalo-webhook] failed to enqueue %s from Zalo user %s: %v", payload.EventName, payload.Sender.ID, err)
		}
	}
}
