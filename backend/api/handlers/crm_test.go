package handlers

import (
	"testing"

	"github.com/vietbui/chat-quality-agent/db/models"
)

func TestNormalizeZaloWhitelistMemberUserIDsTrimsDeduplicatesAndLimits(t *testing.T) {
	staff := []models.ZaloWhitelist{
		{ZaloUserID: "  user-1 "},
		{ZaloUserID: ""},
		{ZaloUserID: "user-2"},
		{ZaloUserID: "user-1"},
		{ZaloUserID: "user-3"},
	}

	members := normalizeZaloWhitelistMemberUserIDs(staff, 2)

	if len(members) != 2 {
		t.Fatalf("expected 2 members, got %d", len(members))
	}
	if members[0] != "user-1" || members[1] != "user-2" {
		t.Fatalf("unexpected members: %#v", members)
	}
}
