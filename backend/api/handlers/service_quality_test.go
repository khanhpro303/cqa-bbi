package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/vietbui/chat-quality-agent/api/middleware"
)

func TestServiceQualityRejectsInvalidMutationsBeforeDB(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		name, body string
		handler    gin.HandlerFunc
	}{
		{"missing policy", `{}`, SaveServiceQualityPolicy},
		{"invalid timezone", `{"timezone":"Not/AZone","work_start":"08:00","work_end":"22:00","target_minutes":5,"overdue_minutes":15}`, SaveServiceQualityPolicy},
		{"invalid thresholds", `{"timezone":"Asia/Ho_Chi_Minh","work_start":"08:00","work_end":"22:00","target_minutes":15,"overdue_minutes":5}`, SaveServiceQualityPolicy},
		{"missing resolution marker", `{"note":"Đã gọi khách"}`, ResolveServiceConversation},
		{"blank reason", `{"through_message_id":"message","note":"   "}`, ResolveServiceConversation},
		{"oversized reason", `{"through_message_id":"message","note":"` + strings.Repeat("a", 501) + `"}`, ResolveServiceConversation},
		{"missing stale conversation ids", `{}`, ReanalyseStaleJobConversations},
		{"empty stale conversation ids", `{"conversation_ids":[]}`, ReanalyseStaleJobConversations},
		{"blank stale conversation id", `{"conversation_ids":[" "]}`, ReanalyseStaleJobConversations},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := gin.New()
			r.POST("/test", tc.handler)
			req := httptest.NewRequest(http.MethodPost, "/test", strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("got %d: %s", w.Code, w.Body.String())
			}
		})
	}
}

func TestServiceQualityPermissionsDenyWithoutDatabaseAccess(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		resource, action string
		handler          gin.HandlerFunc
	}{
		{"messages", "r", GetServiceQuality},
		{"settings", "w", SaveServiceQualityPolicy},
		{"messages", "w", ResolveServiceConversation},
	} {
		t.Run(tc.resource+tc.action, func(t *testing.T) {
			r := gin.New()
			r.Use(func(c *gin.Context) { c.Set("tenant_role", "member"); c.Set("tenant_permissions", `{"jobs":"rw"}`) })
			r.POST("/test", middleware.RequirePermission(tc.resource, tc.action), tc.handler)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/test", nil))
			if w.Code != http.StatusForbidden {
				t.Fatalf("got %d: %s", w.Code, w.Body.String())
			}
		})
	}
}

func TestStaleReanalysisRequiresJobWritePermission(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) { c.Set("tenant_role", "member"); c.Set("tenant_permissions", `{}`) })
	r.POST("/test", middleware.RequirePermission("jobs", "w"), ReanalyseStaleJobConversations)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/test", strings.NewReader(`{"conversation_ids":["conversation"]}`)))
	if w.Code != http.StatusForbidden {
		t.Fatalf("got %d: %s", w.Code, w.Body.String())
	}
}
