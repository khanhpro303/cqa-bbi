package workers

import (
	"context"
	"strings"
	"testing"

	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/engine"
)

func TestAppendOrderCartContext(t *testing.T) {
	saved := db.DB
	db.DB = nil
	t.Cleanup(func() { db.DB = saved })
	ctx := context.Background()
	canOrder := &engine.GroupPermissionContext{AgentType: "private", Groups: []engine.GroupPermission{{
		GroupID: "private_bot", Resources: []engine.ResourcePermission{{Resource: "order_create", IsEnabled: true}},
	}}}
	readOnly := &engine.GroupPermissionContext{AgentType: "private"}

	got := appendOrderCartContext(ctx, "t", "k", canOrder, "BASE")
	for _, want := range []string{"BASE\n\nLÊN ĐƠN QUA CHAT", "order_cart_checkout", "[RICH_MESSAGE_SENT]", "GIỎ HÀNG CỦA CUỘC CHAT NÀY:\n(chưa có giỏ hàng)"} {
		if !strings.Contains(got, want) {
			t.Errorf("prompt misses %q", want)
		}
	}
	if got := appendOrderCartContext(ctx, "t", "k", readOnly, "BASE"); got != "BASE" {
		t.Errorf("no order permission and no cart: prompt unchanged, got %q", got)
	}
	if got := appendOrderCartContext(ctx, "t", "k", canOrder, ""); got != "" {
		t.Errorf("an empty prompt keeps the flow's own prompt, got %q", got)
	}
	if keepOrderConversation(ctx, "t", "k", "HANDOVER") || keepOrderConversation(ctx, "t", "k", "CASUAL") {
		t.Error("without an open cart the classifier decides")
	}
}
