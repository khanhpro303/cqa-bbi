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
		// Log raw body for debugging group chats
		bodyBytes, err := io.ReadAll(c.Request.Body)
		if err == nil {
			c.Request.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))
			
			// Append to webhook_debug.log in the workspace root
			f, fileErr := os.OpenFile("/Users/kariendoan/Downloads/cqa-bbi/webhook_debug.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
			if fileErr == nil {
				_, _ = f.WriteString(string(bodyBytes) + "\n---\n")
				f.Close()
			}
			log.Printf("[debug-webhook] raw payload: %s", string(bodyBytes))
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
		if asynqClient != nil {
			task, err := workers.NewZaloWebhookTask(payload)
			if err == nil {
				asynqClient.Enqueue(task, asynq.MaxRetry(3))
			}
		}
	}
}
