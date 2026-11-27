package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/vietbui/chat-quality-agent/config"
)

func TestPersonalZaloApprovalRoutesRequireAuthentication(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := SetupRouter(&config.Config{RateLimitPerIP: 500})
	const base = "/api/v1/tenants/tenant/channels/channel/personal-zalo/approvals"
	for _, route := range []struct{ method, path string }{
		{http.MethodGet, base},
		{http.MethodPut, base + "/group/123"},
		{http.MethodPost, "/api/internal/imports/personal-zalo/roster"},
		{http.MethodPost, "/api/internal/chatbot/personal-zalo/thread-access"},
	} {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(route.method, route.path, nil))
		if w.Code != http.StatusUnauthorized {
			t.Errorf("%s %s returned %d, want %d", route.method, route.path, w.Code, http.StatusUnauthorized)
		}
	}
}
