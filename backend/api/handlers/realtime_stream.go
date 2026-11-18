package handlers

import (
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/vietbui/chat-quality-agent/api/middleware"
	"github.com/vietbui/chat-quality-agent/realtime"
)

// realtimeHeartbeat keeps proxies (nginx defaults to a 60s read timeout) from
// closing an idle stream.
const realtimeHeartbeat = 20 * time.Second

// StreamTenantEvents is a Server-Sent Events stream of change signals for the
// tenant (see package realtime). Each event is just a name; the client
// re-fetches the matching REST endpoint.
func StreamTenantEvents(c *gin.Context) {
	tenantID := middleware.GetTenantID(c)
	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "streaming_unsupported"})
		return
	}

	ctx := c.Request.Context()
	events := realtime.Subscribe(ctx, tenantID)

	h := c.Writer.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("Connection", "keep-alive")
	h.Set("X-Accel-Buffering", "no") // disable nginx response buffering for this stream
	c.Status(http.StatusOK)
	fmt.Fprint(c.Writer, ": connected\n\n")
	flusher.Flush()

	ticker := time.NewTicker(realtimeHeartbeat)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			fmt.Fprint(c.Writer, ": ping\n\n")
			flusher.Flush()
		case event, ok := <-events:
			if !ok {
				return
			}
			fmt.Fprintf(c.Writer, "event: %s\ndata: {}\n\n", event)
			flusher.Flush()
		}
	}
}
