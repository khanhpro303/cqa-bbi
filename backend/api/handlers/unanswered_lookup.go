package handlers

import (
	"context"
	"encoding/json"

	"github.com/vietbui/chat-quality-agent/config"
	"github.com/vietbui/chat-quality-agent/engine"
)

func recordUnansweredLookup(ctx context.Context, tenantID, agentType, token string) {
	if token == "" || agentType != "public" {
		return
	}
	cfg, err := config.Load()
	if err != nil {
		return
	}
	permissions, err := engine.VerifyPermissionToken(token, cfg.EncryptionKey)
	if err == nil && permissions.TenantID == tenantID && permissions.AgentType == agentType {
		engine.RecordUnansweredLookup(ctx, permissions)
	}
}

func unresolvedERPProductResult(resource string, body []byte) bool {
	switch resource {
	case "products", "product_variants", "inventory":
	default:
		return false
	}
	var result struct {
		Status  string `json:"status"`
		Outcome string `json:"lookup_outcome"`
	}
	return json.Unmarshal(body, &result) == nil && result.Status == "success" && result.Outcome == "no_visible_match"
}
