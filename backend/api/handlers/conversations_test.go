package handlers

import (
	"strings"
	"testing"

	"github.com/vietbui/chat-quality-agent/db/models"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

func TestConversationListQueryAppliesChannelScopeToDeepLinks(t *testing.T) {
	database, err := gorm.Open(mysql.New(mysql.Config{
		DSN:                       "test:test@tcp(localhost:1)/test",
		SkipInitializeWithVersion: true,
	}), &gorm.Config{DryRun: true, DisableAutomaticPing: true})
	if err != nil {
		t.Fatal(err)
	}

	var position int64
	stmt := conversationListQuery(database, "tenant-a", conversationListFilters{ChannelID: "channel-a"}).
		Model(&models.Conversation{}).
		Where("tenant_id = ? AND last_message_at > ?", "tenant-a", "2026-09-15T10:16:00Z").
		Count(&position).Statement

	if !strings.Contains(stmt.SQL.String(), "conversations.channel_id = ?") {
		t.Fatalf("page lookup must retain the channel scope: %s", stmt.SQL.String())
	}
	if len(stmt.Vars) != 4 || stmt.Vars[0] != "tenant-a" || stmt.Vars[1] != "channel-a" {
		t.Fatalf("page lookup has unexpected scope parameters: %v", stmt.Vars)
	}
}
