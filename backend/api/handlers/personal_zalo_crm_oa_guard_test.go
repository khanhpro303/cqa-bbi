package handlers

import (
	"testing"
	"time"

	"github.com/vietbui/chat-quality-agent/db/models"
	"gorm.io/gorm"
)

func TestPersonalZaloChatbotSkipsCRMGroupsOwnedByZaloOAMySQL(t *testing.T) {
	database := facebookOAuthTestDatabase(t)
	if err := database.AutoMigrate(&models.CRMGroup{}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	createFacebookOAuthTenant(t, database, "guard-tenant-a")
	createFacebookOAuthTenant(t, database, "guard-tenant-b")

	channels := []models.Channel{
		{
			ID: "guard-personal", TenantID: "guard-tenant-a", ChannelType: "personal_zalo_import",
			Name: "Personal", ExternalID: "personal-account", CredentialsEncrypted: []byte("x"), IsActive: true,
			PersonalZaloAutoReplyEnabled: true, Metadata: `{"personal_zalo_group_reply_enabled":true}`,
			CreatedAt: now, UpdatedAt: now,
		},
		{
			ID: "guard-disabled", TenantID: "guard-tenant-a", ChannelType: "personal_zalo_import",
			Name: "Personal disabled groups", ExternalID: "personal-account-disabled", CredentialsEncrypted: []byte("x"), IsActive: true,
			PersonalZaloAutoReplyEnabled: true, Metadata: `{}`,
			CreatedAt: now, UpdatedAt: now,
		},
		{
			ID: "guard-oa", TenantID: "guard-tenant-a", ChannelType: "zalo_oa",
			Name: "Any OA name", ExternalID: "oa-account", CredentialsEncrypted: []byte("x"),
			IsActive: false, AutoReplyEnabled: false, Metadata: `{}`, CreatedAt: now, UpdatedAt: now,
		},
		{
			ID: "guard-other-personal", TenantID: "guard-tenant-a", ChannelType: "personal_zalo_import",
			Name: "Another personal channel", ExternalID: "other-personal", CredentialsEncrypted: []byte("x"),
			IsActive: true, Metadata: `{}`, CreatedAt: now, UpdatedAt: now,
		},
		{
			ID: "guard-oa-tenant-b", TenantID: "guard-tenant-b", ChannelType: "zalo_oa",
			Name: "Other tenant OA", ExternalID: "other-oa", CredentialsEncrypted: []byte("x"),
			IsActive: true, Metadata: `{}`, CreatedAt: now, UpdatedAt: now,
		},
	}
	for i := range channels {
		if err := database.Create(&channels[i]).Error; err != nil {
			t.Fatalf("seed channel %s: %v", channels[i].ID, err)
		}
	}
	// GORM's default:true tags turn false booleans into true on Create. Persist
	// the paused state explicitly to prove OA ownership does not depend on it.
	if err := database.Model(&models.Channel{}).Where("id = ?", "guard-oa").
		Updates(map[string]any{"is_active": false, "auto_reply_enabled": false}).Error; err != nil {
		t.Fatal(err)
	}

	groups := []models.CRMGroup{
		{ID: "guard-group-active", TenantID: "guard-tenant-a", Name: "Active CRM", ZaloGroupID: "crm-active", ChannelID: "guard-oa", CreatedAt: now, UpdatedAt: now},
		{ID: "guard-group-archived", TenantID: "guard-tenant-a", Name: "Archived CRM", ZaloGroupID: "crm-archived", ChannelID: "guard-oa", CreatedAt: now, UpdatedAt: now, ArchivedAt: gorm.DeletedAt{Time: now, Valid: true}},
		{ID: "guard-group-other-type", TenantID: "guard-tenant-a", Name: "Personal owned", ZaloGroupID: "personal-owned", ChannelID: "guard-other-personal", CreatedAt: now, UpdatedAt: now},
		{ID: "guard-group-other-tenant", TenantID: "guard-tenant-b", Name: "Other tenant CRM", ZaloGroupID: "cross-tenant", ChannelID: "guard-oa-tenant-b", CreatedAt: now, UpdatedAt: now},
	}
	for i := range groups {
		if err := database.Create(&groups[i]).Error; err != nil {
			t.Fatalf("seed CRM group %s: %v", groups[i].ID, err)
		}
	}

	tests := []struct {
		name, channelID, accountID, threadType, threadID string
		wantEnabled                                      bool
	}{
		{name: "active CRM group linked to paused OA", channelID: "guard-personal", accountID: "personal-account", threadType: personalZaloThreadGroup, threadID: "crm-active"},
		{name: "archived CRM group linked to OA", channelID: "guard-personal", accountID: "personal-account", threadType: personalZaloThreadGroup, threadID: "crm-archived"},
		{name: "ordinary group", channelID: "guard-personal", accountID: "personal-account", threadType: personalZaloThreadGroup, threadID: "ordinary", wantEnabled: true},
		{name: "group owned by personal channel", channelID: "guard-personal", accountID: "personal-account", threadType: personalZaloThreadGroup, threadID: "personal-owned", wantEnabled: true},
		{name: "same tenant request ignores another tenants group", channelID: "guard-personal", accountID: "personal-account", threadType: personalZaloThreadGroup, threadID: "cross-tenant", wantEnabled: true},
		{name: "direct chat remains enabled", channelID: "guard-personal", accountID: "personal-account", threadType: personalZaloThreadUser, threadID: "crm-active", wantEnabled: true},
		{name: "group reply switch remains authoritative", channelID: "guard-disabled", accountID: "personal-account-disabled", threadType: personalZaloThreadGroup, threadID: "ordinary"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := personalZaloChatbotRequest{
				TenantID: "guard-tenant-a", ChannelID: tc.channelID, AccountExternalID: tc.accountID,
				ThreadType: tc.threadType, ThreadID: tc.threadID,
			}
			_, enabled, err := personalZaloChatbotChannel(t.Context(), req)
			if err != nil {
				t.Fatal(err)
			}
			if enabled != tc.wantEnabled {
				t.Fatalf("enabled=%t, want %t", enabled, tc.wantEnabled)
			}
		})
	}
}
