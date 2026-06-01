package engine

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/db/models"
	"github.com/vietbui/chat-quality-agent/pkg"
)

// inventoryEndpointConfig is one entry of the erp_global_method_permissions
// setting (the "Cấu hình HTTP Method cho Endpoint (Global dùng chung)" admin UI).
type inventoryEndpointConfig struct {
	Get  bool   `json:"get"`
	Post bool   `json:"post"`
	Path string `json:"path"`
}

// ResolveInventoryEndpoint returns the inventory endpoint and whether to call it
// over POST, derived from the tenant's Global HTTP Method config
// (erp_global_method_permissions). When no custom override is configured it
// falls back to the official total-stock endpoint over POST.
//
// This is the single source of truth shared by the HTTP handler and the Zalo
// worker so both honor the same admin-configured endpoint instead of any
// hardcoded path.
func ResolveInventoryEndpoint(ctx context.Context, tenantID string) (endpoint string, usePost bool) {
	endpoint = pkg.InventoryTotalStockEndpoint
	usePost = true

	var setting models.AppSetting
	err := db.DB.WithContext(ctx).
		Where("tenant_id = ? AND setting_key = 'erp_global_method_permissions'", tenantID).
		First(&setting).Error
	if err != nil || setting.ValuePlain == "" {
		return endpoint, usePost
	}

	var perms map[string]inventoryEndpointConfig
	if json.Unmarshal([]byte(setting.ValuePlain), &perms) != nil {
		return endpoint, usePost
	}

	inv, ok := perms["inventory"]
	if !ok || inv.Path == "" || inv.Path == "inventory" {
		return endpoint, usePost
	}

	path := strings.TrimPrefix(inv.Path, "/")
	path = strings.TrimPrefix(path, "rest_api/private/")
	path = strings.TrimPrefix(path, "/")
	return path, inv.Post
}
