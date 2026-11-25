package handlers

import (
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/vietbui/chat-quality-agent/api/middleware"
	"github.com/vietbui/chat-quality-agent/customercases"
	"gorm.io/gorm"
)

// GetCustomerCaseImage serves the bytes separately from case details.
func GetCustomerCaseImage(c *gin.Context) {
	c.Header("Cache-Control", "private, no-store")
	image, err := customercases.ReadCaseImage(c.Request.Context(), middleware.GetTenantID(c), c.Param("caseId"), c.Param("imageId"), time.Now())
	if errors.Is(err, gorm.ErrRecordNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": "case_image_not_found"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "case_image_unavailable"})
		return
	}
	c.Header("X-Content-Type-Options", "nosniff")
	c.Data(http.StatusOK, image.MediaType, image.Data)
}
