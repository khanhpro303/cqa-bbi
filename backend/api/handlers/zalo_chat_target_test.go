package handlers

import (
	"testing"

	"github.com/vietbui/chat-quality-agent/engine"
)

// A token signed with the exact chat must target that chat: a 1:1 question
// (ZaloGroupID "") must NOT be redirected into one of the caller's CRM groups.
func TestResolveZaloGroupIDHonoursSignedChat(t *testing.T) {
	oneToOne := &engine.GroupPermissionContext{
		ChannelID: "ch-1",
		Groups:    []engine.GroupPermission{{GroupID: "crm-group-1"}},
	}
	if got := resolveZaloGroupID("tenant", oneToOne); got != "" {
		t.Fatalf("1:1 chat resolved to group %q; want \"\"", got)
	}

	inGroup := &engine.GroupPermissionContext{ChannelID: "ch-1", ZaloGroupID: "zg-42"}
	if got := resolveZaloGroupID("tenant", inGroup); got != "zg-42" {
		t.Fatalf("group chat resolved to %q; want zg-42", got)
	}

	if got := resolveZaloGroupID("tenant", nil); got != "" {
		t.Fatalf("nil ctx resolved to %q; want \"\"", got)
	}
}
