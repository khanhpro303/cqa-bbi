package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/vietbui/chat-quality-agent/api/middleware"
	"github.com/vietbui/chat-quality-agent/config"
)

func TestProductGroupingJobRoutesRegistered(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := SetupRouter(&config.Config{RateLimitPerIP: 500})
	want := map[string]bool{
		http.MethodPost + " /api/v1/tenants/:tenantId/jobs/product-grouping":               false,
		http.MethodPut + " /api/v1/tenants/:tenantId/jobs/:jobId/product-grouping-prompt":  false,
		http.MethodGet + " /api/v1/tenants/:tenantId/jobs/:jobId/product-grouping-results": false,
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

func TestProductGroupingResultsRequiresJobsAndMessagesReadPermissions(t *testing.T) {
	gin.SetMode(gin.TestMode)
	request := func(permissions string) int {
		t.Helper()
		router := gin.New()
		router.GET("/results",
			func(c *gin.Context) {
				c.Set("tenant_role", "member")
				c.Set("tenant_permissions", permissions)
				c.Next()
			},
			middleware.RequirePermission("jobs", "r"),
			middleware.RequirePermission("messages", "r"),
			func(c *gin.Context) { c.Status(http.StatusNoContent) },
		)
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/results", nil))
		return response.Code
	}

	for _, permissions := range []string{`{"jobs":"r"}`, `{"messages":"r"}`, `{}`, `not-json`} {
		if code := request(permissions); code != http.StatusForbidden {
			t.Fatalf("permissions %q returned %d, want 403", permissions, code)
		}
	}
	if code := request(`{"jobs":"r","messages":"r"}`); code != http.StatusNoContent {
		t.Fatalf("both read permissions returned %d, want 204", code)
	}
}
