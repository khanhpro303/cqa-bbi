package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/vietbui/chat-quality-agent/engine"
)

// An edit that drops a marker the code reads is refused before anything is
// stored.
func TestSaveTaskPromptRefusesMissingMarker(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "key", Value: engine.PromptPZHandover}}
	c.Request = httptest.NewRequest(http.MethodPut, "/", strings.NewReader(`{"value":"Khi khách cần nhân viên thì báo nhân viên."}`))
	c.Request.Header.Set("Content-Type", "application/json")
	SaveTaskPrompt(c)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body %s", w.Code, w.Body.String())
	}
	var body struct {
		Error   string   `json:"error"`
		Missing []string `json:"missing"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if body.Error != "prompt_missing_required" || len(body.Missing) != 1 || body.Missing[0] != engine.HumanReviewMarker {
		t.Fatalf("body = %s", w.Body.String())
	}
}
