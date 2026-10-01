package api

import (
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/vietbui/chat-quality-agent/config"
)

func TestProductGroupingJobRoutesRegistered(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := SetupRouter(&config.Config{RateLimitPerIP: 500})
	want := map[string]bool{
		http.MethodPost + " /api/v1/tenants/:tenantId/jobs/product-grouping":              false,
		http.MethodPut + " /api/v1/tenants/:tenantId/jobs/:jobId/product-grouping-prompt": false,
	}
	for _, route := range router.Routes() {
		key := route.Method + " " + route.Path
		if _, ok := want[key]; ok {
			want[key] = true
		}
	}
	for key, found := range want {
		if !found {
			t.Fatalf("route not registered: %s", key)
		}
	}
}
