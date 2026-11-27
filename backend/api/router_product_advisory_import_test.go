package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/vietbui/chat-quality-agent/config"
)

func TestProductAdvisoryImportRoutesRequireAuthentication(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := SetupRouter(&config.Config{RateLimitPerIP: 500})
	const base = "/api/v1/tenants/:tenantId/product-advisory-imports"
	expected := map[string][]string{
		http.MethodGet:  {base + "/template", base + "/latest", base + "/:jobId", base + "/:jobId/images"},
		http.MethodPost: {base, base + "/:jobId/images/approve"},
	}
	registered := make(map[string]bool)
	for _, route := range router.Routes() {
		registered[route.Method+" "+route.Path] = true
	}
	for method, paths := range expected {
		for _, path := range paths {
			t.Run(method+" "+path, func(t *testing.T) {
				if !registered[method+" "+path] {
					t.Fatalf("route not registered: %s %s", method, path)
				}
				resolved := strings.NewReplacer(":tenantId", "test-tenant", ":jobId", "test-job").Replace(path)
				response := httptest.NewRecorder()
				router.ServeHTTP(response, httptest.NewRequest(method, resolved, nil))
				if response.Code != http.StatusUnauthorized {
					t.Fatalf("request without credentials returned %d, want %d", response.Code, http.StatusUnauthorized)
				}
			})
		}
	}
}
