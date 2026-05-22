package handlers

import (
	"net/http"

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
		var payload workers.ZaloWebhookPayload
		if err := c.ShouldBindJSON(&payload); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid payload"})
			return
		}

		// Always return 200 OK to Zalo quickly
		c.JSON(http.StatusOK, gin.H{"status": "ok"})

		if payload.EventName != "user_send_text" || payload.Message.Text == "" {
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
