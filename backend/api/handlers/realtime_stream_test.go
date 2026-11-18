package handlers

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/vietbui/chat-quality-agent/realtime"
)

func TestStreamTenantEventsDeliversWhitelistUpdate(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/events", func(c *gin.Context) { c.Set("tenant_id", "tenant-sse") }, StreamTenantEvents)
	srv := httptest.NewServer(r)
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/events", nil)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if ct := res.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("content-type %q", ct)
	}

	reader := bufio.NewReader(res.Body)
	if line, _ := reader.ReadString('\n'); !strings.HasPrefix(line, ": connected") {
		t.Fatalf("first line %q", line)
	}
	realtime.Publish(ctx, "tenant-sse", realtime.EventZaloWhitelistUpdated)
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatalf("stream ended before event: %v", err)
		}
		if strings.TrimSpace(line) == "event: "+realtime.EventZaloWhitelistUpdated {
			return
		}
	}
}
