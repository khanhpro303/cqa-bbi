package api

import (
	"net/http"
	"testing"

	"github.com/vietbui/chat-quality-agent/config"
)

func TestServiceQualityProductGroupsRouteRegistered(t *testing.T) {
	router := SetupRouter(&config.Config{RateLimitPerIP: 500})
	const path = "/api/v1/tenants/:tenantId/service-quality/product-groups"
	for _, route := range router.Routes() {
		if route.Method == http.MethodPost && route.Path == path {
			return
		}
	}
	t.Fatalf("POST %s is not registered", path)
}
