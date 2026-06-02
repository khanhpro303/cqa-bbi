package handlers

import (
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/vietbui/chat-quality-agent/api/middleware"
	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/db/models"
	"github.com/vietbui/chat-quality-agent/pkg"
)

// Available ERP resources for validation
var erpAvailableResources = []string{"products", "inventory", "orders", "customers", "debt"}

// resourceRequiresProductGroups reports whether enabling a resource requires a
// non-empty VTHH product-group filter. Only product/inventory reads are scoped
// by product group; orders/customers/debt are scoped by partner instead.
func resourceRequiresProductGroups(resource string) bool {
	return resource == "products" || resource == "inventory"
}

// ---------------------------------------------------------------------------
// ListGroupERPEndpoints — GET /crm/groups/:id/erp/endpoints
// ---------------------------------------------------------------------------

func ListGroupERPEndpoints(c *gin.Context) {
	tenantID := middleware.GetTenantID(c)
	groupID := c.Param("id")
	if groupID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "group_id_required"})
		return
	}

	var endpoints []models.ERPEndpoint
	db.DB.Where("tenant_id = ? AND group_id = ?", tenantID, groupID).Find(&endpoints)

	// Index existing endpoints for quick lookup
	existing := make(map[string]*models.ERPEndpoint)
	for i := range endpoints {
		existing[endpoints[i].Resource] = &endpoints[i]
	}

	// Reconstruct the slice in canonical order, adding default entries for any missing resources
	var orderedEndpoints []models.ERPEndpoint
	for _, resource := range erpAvailableResources {
		if ep, found := existing[resource]; found {
			ep.ScopeType = "own" // Enforce 'own' scope for GMF groups
			orderedEndpoints = append(orderedEndpoints, *ep)
		} else {
			orderedEndpoints = append(orderedEndpoints, models.ERPEndpoint{
				TenantID:  tenantID,
				GroupID:   groupID,
				Resource:  resource,
				IsEnabled: false,
				ScopeType: "own",
			})
		}
	}

	c.JSON(http.StatusOK, gin.H{"endpoints": orderedEndpoints})
}

// ---------------------------------------------------------------------------
// SaveGroupERPEndpoints — PUT /crm/groups/:id/erp/endpoints
// Bulk upsert: caller sends the full list of endpoints to save.
// ---------------------------------------------------------------------------

func SaveGroupERPEndpoints(c *gin.Context) {
	tenantID := middleware.GetTenantID(c)
	groupID := c.Param("id")
	if groupID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "group_id_required"})
		return
	}

	var req struct {
		Endpoints []struct {
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

	// Validate first so we never persist a partial update: an enabled
	// product/inventory endpoint must carry a non-empty VTHH group filter.
	for _, ep := range req.Endpoints {
		if !isValidERPResource(ep.Resource) {
			continue
		}
		if ep.IsEnabled && resourceRequiresProductGroups(ep.Resource) &&
			strings.TrimSpace(ep.ProductGroups) == "" {
			c.JSON(http.StatusBadRequest, gin.H{
				"error":    "product_groups_required",
				"resource": ep.Resource,
			})
			return
		}
	}

	for _, ep := range req.Endpoints {
		if !isValidERPResource(ep.Resource) {
			continue
		}

		scopeType := "own" // Enforce 'own' scope for GMF groups

		var existing models.ERPEndpoint
		result := db.DB.Where("tenant_id = ? AND group_id = ? AND resource = ?",
			tenantID, groupID, ep.Resource).First(&existing)

		if result.Error == nil {
			// Update existing
			if err := db.DB.Model(&existing).Updates(map[string]interface{}{
				"is_enabled":     ep.IsEnabled,
				"scope_type":     scopeType,
				"product_groups": ep.ProductGroups,
				"updated_at":     time.Now(),
			}).Error; err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "update_failed", "details": err.Error()})
				return
			}
		} else {
			// Create new
			if err := db.DB.Create(&models.ERPEndpoint{
				ID:            pkg.NewUUID(),
				TenantID:      tenantID,
				GroupID:       groupID,
				Resource:      ep.Resource,
				IsEnabled:     ep.IsEnabled,
				ScopeType:     scopeType,
				ProductGroups: ep.ProductGroups,
				CreatedAt:     time.Now(),
				UpdatedAt:     time.Now(),
			}).Error; err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "create_failed", "details": err.Error()})
				return
			}
		}
	}

	c.JSON(http.StatusOK, gin.H{"message": "saved"})
}

// ---------------------------------------------------------------------------
// ToggleGroupERPEndpoint — POST /crm/groups/:id/erp/endpoints/toggle
// Quickly enable or disable a single endpoint without a full save.
// ---------------------------------------------------------------------------

func ToggleGroupERPEndpoint(c *gin.Context) {
	tenantID := middleware.GetTenantID(c)
	groupID := c.Param("id")
	if groupID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "group_id_required"})
		return
	}

	var req struct {
		Resource  string `json:"resource" binding:"required"`
		IsEnabled bool   `json:"is_enabled"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request", "details": err.Error()})
		return
	}

	if !isValidERPResource(req.Resource) {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "invalid_params",
			"message": "resource không hợp lệ",
		})
		return
	}

	var existing models.ERPEndpoint
	result := db.DB.Where("tenant_id = ? AND group_id = ? AND resource = ?",
		tenantID, groupID, req.Resource).First(&existing)

	if result.Error == nil {
		updates := map[string]interface{}{
			"is_enabled": req.IsEnabled,
			"scope_type": "own", // Enforce 'own' scope for GMF groups
			"updated_at": time.Now(),
		}
		if err := db.DB.Model(&existing).Updates(updates).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "update_failed", "details": err.Error()})
			return
		}
	} else {
		// Row doesn't exist yet — create it
		if err := db.DB.Create(&models.ERPEndpoint{
			ID:        pkg.NewUUID(),
			TenantID:  tenantID,
			GroupID:   groupID,
			Resource:  req.Resource,
			IsEnabled: req.IsEnabled,
			ScopeType: "own", // Enforce 'own' scope for GMF groups
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		}).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "create_failed", "details": err.Error()})
			return
		}
	}

	c.JSON(http.StatusOK, gin.H{"message": "updated"})
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func isValidERPResource(r string) bool {
	for _, v := range erpAvailableResources {
		if v == r {
			return true
		}
	}
	return false
}
