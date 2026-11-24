package handlers

import (
	"log"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/vietbui/chat-quality-agent/ai"
)

// listOpenRouterFreeModels is replaced in tests.
var listOpenRouterFreeModels = ai.ListOpenRouterFreeModels

// ListOpenRouterFreeModels returns OpenRouter's free models for the AI
// settings model picker.
func ListOpenRouterFreeModels(c *gin.Context) {
	models, err := listOpenRouterFreeModels(c.Request.Context())
	if err != nil {
		log.Printf("[settings] openrouter free models: %v", err)
		c.JSON(http.StatusBadGateway, gin.H{"error": "openrouter_unavailable"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"models": models})
}
