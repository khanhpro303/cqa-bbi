package handlers

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/vietbui/chat-quality-agent/api/middleware"
	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/db/models"
	"github.com/vietbui/chat-quality-agent/pkg"
)

// Available ERP resources and agent types for validation
var (
	erpAvailableResources = []string{"products", "inventory", "orders", "customers", "debt"}
	erpAgentTypes         = []string{"public", "private"}
)

// ---------------------------------------------------------------------------
// ListERPEndpoints — GET /settings/erp/endpoints
// ---------------------------------------------------------------------------

// ListERPEndpoints returns all ERP endpoint permission configs for the tenant.
// If no config exists yet, returns the default structure (all disabled) so the
// frontend can render the UI immediately.
func ListERPEndpoints(c *gin.Context) {
	tenantID := middleware.GetTenantID(c)

	var endpoints []models.ERPEndpoint
	db.DB.Where("tenant_id = ?", tenantID).Order("agent_type, resource").Find(&endpoints)

	// If no rows exist, seed defaults in memory (not persisted until user saves)
	if len(endpoints) == 0 {
		endpoints = buildDefaultEndpoints(tenantID)
	}

	c.JSON(http.StatusOK, gin.H{"endpoints": endpoints})
}

// ---------------------------------------------------------------------------
// SaveERPEndpoints — PUT /settings/erp/endpoints
// Bulk upsert: caller sends the full list of endpoints to save.
// ---------------------------------------------------------------------------

func SaveERPEndpoints(c *gin.Context) {
	tenantID := middleware.GetTenantID(c)

	var req struct {
		Endpoints []struct {
			AgentType     string `json:"agent_type" binding:"required"`
			Resource      string `json:"resource" binding:"required"`
			IsEnabled     bool   `json:"is_enabled"`
			ScopeType     string `json:"scope_type"`     // "all" | "own" | "assigned"
			ProductGroups string `json:"product_groups"` // comma-separated
		} `json:"endpoints" binding:"required"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request", "details": err.Error()})
		return
	}

	for _, ep := range req.Endpoints {
		if !isValidERPAgentType(ep.AgentType) || !isValidERPResource(ep.Resource) {
			continue
		}

		scopeType := ep.ScopeType
		if scopeType == "" {
			scopeType = "all"
		}

		var existing models.ERPEndpoint
		result := db.DB.Where("tenant_id = ? AND agent_type = ? AND resource = ?",
			tenantID, ep.AgentType, ep.Resource).First(&existing)

		if result.Error == nil {
			// Update existing
			db.DB.Model(&existing).Updates(map[string]interface{}{
				"is_enabled":     ep.IsEnabled,
				"scope_type":     scopeType,
				"product_groups": ep.ProductGroups,
				"updated_at":     time.Now(),
			})
		} else {
			// Create new
			db.DB.Create(&models.ERPEndpoint{
				ID:            pkg.NewUUID(),
				TenantID:      tenantID,
				AgentType:     ep.AgentType,
				Resource:      ep.Resource,
				IsEnabled:     ep.IsEnabled,
				ScopeType:     scopeType,
				ProductGroups: ep.ProductGroups,
				CreatedAt:     time.Now(),
				UpdatedAt:     time.Now(),
			})
		}
	}

	c.JSON(http.StatusOK, gin.H{"message": "saved"})
}

// ---------------------------------------------------------------------------
// ToggleERPEndpoint — POST /settings/erp/endpoints/toggle
// Quickly enable or disable a single endpoint without a full save.
// ---------------------------------------------------------------------------

func ToggleERPEndpoint(c *gin.Context) {
	tenantID := middleware.GetTenantID(c)

	var req struct {
		AgentType string `json:"agent_type" binding:"required"`
		Resource  string `json:"resource" binding:"required"`
		IsEnabled bool   `json:"is_enabled"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request", "details": err.Error()})
		return
	}

	if !isValidERPAgentType(req.AgentType) || !isValidERPResource(req.Resource) {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "invalid_params",
			"message": "agent_type hoặc resource không hợp lệ",
		})
		return
	}

	var existing models.ERPEndpoint
	result := db.DB.Where("tenant_id = ? AND agent_type = ? AND resource = ?",
		tenantID, req.AgentType, req.Resource).First(&existing)

	if result.Error == nil {
		db.DB.Model(&existing).Updates(map[string]interface{}{
			"is_enabled": req.IsEnabled,
			"updated_at": time.Now(),
		})
	} else {
		// Row doesn't exist yet — create it
		db.DB.Create(&models.ERPEndpoint{
			ID:        pkg.NewUUID(),
			TenantID:  tenantID,
			AgentType: req.AgentType,
			Resource:  req.Resource,
			IsEnabled: req.IsEnabled,
			ScopeType: "all",
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		})
	}

	c.JSON(http.StatusOK, gin.H{"message": "updated"})
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func buildDefaultEndpoints(tenantID string) []models.ERPEndpoint {
	var endpoints []models.ERPEndpoint
	for _, agentType := range erpAgentTypes {
		for _, resource := range erpAvailableResources {
			endpoints = append(endpoints, models.ERPEndpoint{
				TenantID:  tenantID,
				AgentType: agentType,
				Resource:  resource,
				IsEnabled: false,
				ScopeType: "all",
			})
		}
	}
	return endpoints
}

func isValidERPAgentType(t string) bool {
	for _, v := range erpAgentTypes {
		if v == t {
			return true
		}
	}
	return false
}

func isValidERPResource(r string) bool {
	for _, v := range erpAvailableResources {
		if v == r {
			return true
		}
	}
	return false
}
