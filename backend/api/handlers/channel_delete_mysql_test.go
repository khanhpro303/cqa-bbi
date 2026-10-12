package handlers

import (
	"testing"
	"time"

	"github.com/vietbui/chat-quality-agent/db/models"
)

// A channel referenced by zalo_whitelists / crm_groups used to hit an FK
// RESTRICT that DeleteChannel swallowed, so the row survived and reappeared.
func TestMySQLDeleteChannelCascadeRemovesReferencedChannel(t *testing.T) {
	database := facebookOAuthTestDatabase(t)
	if err := database.AutoMigrate(&models.Conversation{}, &models.Message{}, &models.ZaloWhitelist{}, &models.CRMGroup{}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	createFacebookOAuthTenant(t, database, "tenant-a")
	rows := []any{
		&models.Channel{ID: "ch-bulldog", TenantID: "tenant-a", ChannelType: "zalo_oa", Name: "OA bulldog", ExternalID: "oa-1", CredentialsEncrypted: []byte("x"), Metadata: "{}", CreatedAt: now, UpdatedAt: now},
		&models.Conversation{ID: "conv-1", TenantID: "tenant-a", ChannelID: "ch-bulldog", ExternalConversationID: "ext-1", Metadata: "{}", CreatedAt: now, UpdatedAt: now},
		&models.Message{ID: "msg-1", TenantID: "tenant-a", ConversationID: "conv-1", ExternalMessageID: "m-1", SenderType: "customer", SentAt: now, CreatedAt: now},
		&models.ZaloWhitelist{ID: "wl-1", TenantID: "tenant-a", ChannelID: "ch-bulldog", ZaloUserID: "u-1", Status: "active", CreatedAt: now, UpdatedAt: now},
		&models.CRMGroup{ID: "grp-1", TenantID: "tenant-a", Name: "Nhóm A", ChannelID: "ch-bulldog", CreatedAt: now, UpdatedAt: now},
		&models.CRMGroupEmployee{GroupID: "grp-1", ZaloWhitelistID: "wl-1"},
	}
	for _, row := range rows {
		if err := database.Create(row).Error; err != nil {
			t.Fatalf("seed %T: %v", row, err)
		}
	}

	// Without the cascade the FK must block a bare delete — proves the test is meaningful.
	if err := database.Where("id = ?", "ch-bulldog").Delete(&models.Channel{}).Error; err == nil {
		t.Fatal("expected FK to block bare channel delete")
	}

	convIDs, err := deleteChannelCascade(database, "tenant-a", "ch-bulldog")
	if err != nil {
		t.Fatalf("deleteChannelCascade: %v", err)
	}
	if len(convIDs) != 1 || convIDs[0] != "conv-1" {
		t.Fatalf("convIDs = %v", convIDs)
	}

	leftovers := map[string]any{"channel": &models.Channel{}, "conversation": &models.Conversation{}, "message": &models.Message{}, "whitelist": &models.ZaloWhitelist{}, "employee": &models.CRMGroupEmployee{}}
	for name, model := range leftovers {
		var n int64
		database.Model(model).Count(&n)
		if n != 0 {
			t.Errorf("%s rows left = %d", name, n)
		}
	}
	var group models.CRMGroup
	if err := database.First(&group, "id = ?", "grp-1").Error; err != nil {
		t.Fatalf("crm group should survive: %v", err)
	}
	if group.ChannelID != "" {
		t.Errorf("crm group channel_id = %q, want cleared", group.ChannelID)
	}
}
