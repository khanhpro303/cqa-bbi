package handlers

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/vietbui/chat-quality-agent/ai"
)

func TestListOpenRouterFreeModels(t *testing.T) {
	orig := listOpenRouterFreeModels
	defer func() { listOpenRouterFreeModels = orig }()
	router := gin.New()
	router.GET("/m", ListOpenRouterFreeModels)

	listOpenRouterFreeModels = func(context.Context) ([]ai.OpenRouterModel, error) {
		return []ai.OpenRouterModel{{ID: "x/y:free", Name: "Y", Vision: true}}, nil
	}
	res := httptest.NewRecorder()
	router.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/m", nil))
	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), `"id":"x/y:free"`) {
		t.Fatalf("ok case: %d %s", res.Code, res.Body.String())
	}

	listOpenRouterFreeModels = func(context.Context) ([]ai.OpenRouterModel, error) { return nil, errors.New("down") }
	res = httptest.NewRecorder()
	router.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/m", nil))
	if res.Code != http.StatusBadGateway || strings.Contains(res.Body.String(), "down") {
		t.Fatalf("error case leaks or wrong code: %d %s", res.Code, res.Body.String())
	}
}
