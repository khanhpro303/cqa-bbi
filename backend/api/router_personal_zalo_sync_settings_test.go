package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/vietbui/chat-quality-agent/config"
)

func TestPersonalZaloSyncSettingsRequiresAuthentication(t *testing.T) {
	router := SetupRouter(&config.Config{RateLimitPerIP: 500})
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/internal/imports/personal-zalo/settings", nil))
	if response.Code != http.StatusUnauthorized || response.Body.String() != `{"error":"internal_auth_required"}` {
		t.Fatalf("sync settings must require import HMAC, got %d %s", response.Code, response.Body)
	}
}
