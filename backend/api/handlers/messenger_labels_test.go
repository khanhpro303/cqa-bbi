package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/vietbui/chat-quality-agent/api/middleware"
)

func TestMessengerLabelPolicyRejectsInvalidShapeBeforeDatabase(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, body := range []string{`invalid`, `{"enabled":true,"rules":[]}`, `{"enabled":true,"rules":[{"label_id":"1","category":"AI"}]}`, `{"enabled":true,"rules":[{"label_id":"1","category":"qualified"},{"label_id":"1","category":"potential"}]}`} {
		r := gin.New()
		r.PUT("/test", SaveMessengerLabelPolicy)
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPut, "/test", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		r.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("%s => %d", body, w.Code)
		}
	}
}

func TestMessengerLabelEndpointsEnforcePermissions(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		resource, action string
		handler          gin.HandlerFunc
	}{{"messages", "r", GetMessengerLabels}, {"settings", "w", RefreshMessengerLabelCatalog}, {"settings", "w", SaveMessengerLabelPolicy}, {"messages", "w", SyncMessengerLabels}} {
		r := gin.New()
		r.Use(func(c *gin.Context) { c.Set("tenant_role", "member"); c.Set("tenant_permissions", `{"jobs":"rw"}`) })
		r.POST("/test", middleware.RequirePermission(tc.resource, tc.action), tc.handler)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/test", nil))
		if w.Code != http.StatusForbidden {
			t.Fatalf("%s:%s => %d", tc.resource, tc.action, w.Code)
		}
	}
}
