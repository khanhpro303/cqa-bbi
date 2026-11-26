package workers

import (
	"context"
	"strings"

	"github.com/vietbui/chat-quality-agent/engine"
	"github.com/vietbui/chat-quality-agent/orderactions"
)

const orderCartDeclinedMessage = "Dạ em chưa lên đơn. Giỏ hàng vẫn được giữ nguyên: anh/chị muốn sửa gì cứ nhắn em (ví dụ đổi số lượng, bỏ hoặc thêm sản phẩm). Nếu không đặt nữa, anh/chị nhắn \"huỷ giỏ hàng\" giúp em nhé."

// appendOrderCartContext adds the tenant's ordering playbook (editable in AI
// Engine) and the chat's current cart to the agent's system prompt, when the
// sender may create orders or a cart is already open. The cart text lets the
// agent continue an order across turns and sessions without re-asking. Like
// the chat-memory context, it is skipped when no system prompt is set, so the
// flow's own prompt is never replaced by this block alone.
func appendOrderCartContext(ctx context.Context, tenantID, workKey string, permissions *engine.GroupPermissionContext, systemPrompt string) string {
	if strings.TrimSpace(systemPrompt) == "" {
		return systemPrompt
	}
	cart, state, open := orderactions.OpenCart(ctx, tenantID, workKey)
	if !open {
		if allowed, _ := orderactions.WriteScope(permissions, orderactions.ResourceOrderCreate); !allowed {
			return systemPrompt
		}
	}
	agentType := "public"
	if permissions != nil && permissions.AgentType != "" {
		agentType = permissions.AgentType
	}
	rendered := orderactions.RenderCartForAgent(nil, state, agentType)
	if open {
		rendered = orderactions.RenderCartForAgent(cart, state, agentType)
	}
	block := engine.BuildTaskPrompt(tenantID, engine.PromptOrderCart, map[string]string{"cart": rendered})
	if strings.TrimSpace(block) == "" {
		return systemPrompt
	}
	return systemPrompt + "\n\n" + block
}

// keepOrderConversation reports whether the intent classifier's verdict must
// be overridden because the chat is in the middle of an order. Answers such
// as "2", "cái carbon", "ok lấy vậy" read as small talk on their own, and
// closing the session there would drop the order.
func keepOrderConversation(ctx context.Context, tenantID, workKey, intent string) bool {
	if intent != "CASUAL" && intent != "OUT_OF_SCOPE" {
		return false
	}
	_, _, open := orderactions.OpenCart(ctx, tenantID, workKey)
	return open
}
