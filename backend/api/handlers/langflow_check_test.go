package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/vietbui/chat-quality-agent/db"
)

func TestLangflowConnectionChecksBothFlowsWithoutRunningThem(t *testing.T) {
	previousDB := db.DB
	db.DB = nil
	t.Cleanup(func() { db.DB = previousDB })

	t.Setenv("JWT_SECRET", strings.Repeat("j", 32))
	t.Setenv("ENCRYPTION_KEY", strings.Repeat("e", 32))
	t.Setenv("DB_PASSWORD", "test")
	t.Setenv("CQA_MESSENGER_JOURNEY_ENABLED", "false")
	t.Setenv("LANGFLOW_FLOW_ID", "saved-private")
	t.Setenv("LANGFLOW_PUBLIC_FLOW_ID", "env-public")
	const key = "test-langflow-key"
	t.Setenv("LANGFLOW_API_KEY", key)

	var mu sync.Mutex
	var paths []string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.Header.Get("x-api-key") != key {
			t.Errorf("unexpected probe method or credential")
			w.WriteHeader(http.StatusForbidden)
			return
		}
		mu.Lock()
		paths = append(paths, r.URL.Path)
		mu.Unlock()
		if r.URL.Path == "/api/v1/flows/missing-public" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(`{"name":"existing flow","data":{"nodes":[]}}`))
	}))
	defer upstream.Close()
	t.Setenv("LANGFLOW_API_URL", upstream.URL)

	router := gin.New()
	router.POST("/test", TestLangflowConnection)
	cases := []struct {
		name   string
		body   string
		status int
		public string
	}{
		{"unsaved public flow", `{"langflow_flow_id":"form-private","langflow_public_flow_id":"missing-public","langflow_token":"••••••••"}`, http.StatusBadRequest, "missing-public"},
		{"cleared public field restores environment", `{"langflow_flow_id":"form-private","langflow_public_flow_id":""}`, http.StatusOK, "env-public"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			router.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/test", strings.NewReader(tc.body)))
			if response.Code != tc.status {
				t.Fatalf("status = %d, want %d: %s", response.Code, tc.status, response.Body)
			}
			if strings.Contains(response.Body.String(), key) {
				t.Fatal("response leaked the Langflow key")
			}
			var result struct {
				Checks []struct {
					Role   string `json:"role"`
					FlowID string `json:"flow_id"`
					OK     bool   `json:"ok"`
				} `json:"checks"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if len(result.Checks) != 2 || result.Checks[0].Role != "private" || !result.Checks[0].OK ||
				result.Checks[1].Role != "public" || result.Checks[1].FlowID != tc.public ||
				result.Checks[1].OK != (tc.status == http.StatusOK) {
				t.Fatalf("unexpected flow checks: %+v", result.Checks)
			}
			mu.Lock()
			defer mu.Unlock()
			if len(paths) != 2 || paths[0] != "/api/v1/flows/form-private" || paths[1] != "/api/v1/flows/"+tc.public {
				t.Fatalf("unexpected requests: %v", paths)
			}
			paths = nil
		})
	}
}
