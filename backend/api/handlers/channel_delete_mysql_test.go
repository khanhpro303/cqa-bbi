package handlers

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/vietbui/chat-quality-agent/db/models"
	"github.com/vietbui/chat-quality-agent/messengerjourney"
)

// A channel referenced by zalo_whitelists / crm_groups used to hit an FK
// RESTRICT that DeleteChannel swallowed, so the row survived and reappeared.
func TestMySQLDeleteChannelCascadeRemovesReferencedChannel(t *testing.T) {
	database := facebookOAuthTestDatabase(t)
	if err := database.AutoMigrate(
		&models.Conversation{}, &models.Message{}, &models.ZaloWhitelist{}, &models.CRMGroup{},
		&models.MessengerIntakeAttribution{}, &models.MessengerLabelSnapshot{},
		&models.MessengerJourneySequence{}, &models.MessengerJourneyReceipt{}, &models.MessengerJourneyEvent{}, &models.MessengerJourneyCustomer{},
		&models.MessengerJourneyOrder{}, &models.MessengerJourneyCoverage{}, &models.MessengerJourneyErasure{},
	); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	conversationID := "conv-1"
	createFacebookOAuthTenant(t, database, "tenant-a")
	rows := []any{
		&models.Channel{ID: "ch-bulldog", TenantID: "tenant-a", ChannelType: "zalo_oa", Name: "OA bulldog", ExternalID: "oa-1", CredentialsEncrypted: []byte("x"), Metadata: "{}", CreatedAt: now, UpdatedAt: now},
		&models.Conversation{ID: "conv-1", TenantID: "tenant-a", ChannelID: "ch-bulldog", ExternalConversationID: "ext-1", Metadata: "{}", CreatedAt: now, UpdatedAt: now},
		&models.Message{ID: "msg-1", TenantID: "tenant-a", ConversationID: "conv-1", ExternalMessageID: "m-1", SenderType: "customer", Attachments: "[]", RawData: "{}", SentAt: now, CreatedAt: now},
		&models.ZaloWhitelist{ID: "wl-1", TenantID: "tenant-a", ChannelID: "ch-bulldog", ZaloUserID: "u-1", Status: "active", CreatedAt: now, UpdatedAt: now},
		&models.CRMGroup{ID: "grp-1", TenantID: "tenant-a", Name: "Nhóm A", ChannelID: "ch-bulldog", CreatedAt: now, UpdatedAt: now},
		&models.CRMGroupEmployee{GroupID: "grp-1", ZaloWhitelistID: "wl-1"},
		&models.MessengerIntakeAttribution{ID: "intake-1", TenantID: "tenant-a", ChannelID: "ch-bulldog", ConversationID: &conversationID, PageID: "page-1", PSID: "psid-order-only", AdID: "ad-1", RawReferral: `{"ad_id":"ad-1"}`, CapturedAt: now, CreatedAt: now, UpdatedAt: now},
	}
	for _, row := range rows {
		if err := database.Create(row).Error; err != nil {
			t.Fatalf("seed %T: %v", row, err)
		}
	}
	if _, err := messengerjourney.Record(context.Background(), database, messengerjourney.RecordInput{
		BaseInput: messengerjourney.BaseInput{EventID: "order-only", TenantID: "tenant-a", PageID: "page-1", PSID: "psid-order-only", ChannelID: "ch-bulldog", ConversationID: "conv-1", Source: "test", OccurredAt: now, ObservedAt: now},
		Kind:      messengerjourney.KindOrder, EntityID: "order-1", Payload: json.RawMessage(`{"order_id":"order-1","order_ref":"DH-1","status":"confirmed","ordered_at":"` + now.Format(time.RFC3339Nano) + `","value_vnd":100000,"confidence":0.9,"evidence_message_ids":["message-1"]}`),
	}); err != nil {
		t.Fatal(err)
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

	leftovers := map[string]any{"channel": &models.Channel{}, "conversation": &models.Conversation{}, "message": &models.Message{}, "whitelist": &models.ZaloWhitelist{}, "employee": &models.CRMGroupEmployee{}, "intake": &models.MessengerIntakeAttribution{}}
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
	var piiEvents int64
	if err := database.Model(&models.MessengerJourneyEvent{}).Where("tenant_id = ? AND psid <> ''", "tenant-a").Count(&piiEvents).Error; err != nil || piiEvents != 0 {
		t.Fatalf("journey event identifiers survived channel deletion: %d %v", piiEvents, err)
	}
	var deletions int64
	if err := database.Model(&models.MessengerJourneyEvent{}).Where("tenant_id = ? AND kind = ?", "tenant-a", messengerjourney.KindDeletion).Count(&deletions).Error; err != nil || deletions != 1 {
		t.Fatalf("order-only subject deletion event missing: %d %v", deletions, err)
	}
}

func TestMySQLDeleteChannelEmitsTombstonesForLegacyOnlySubjects(t *testing.T) {
	database := facebookOAuthTestDatabase(t)
	if err := database.AutoMigrate(
		&models.Conversation{}, &models.Message{}, &models.ZaloWhitelist{}, &models.CRMGroup{},
		&models.MessengerIntakeAttribution{}, &models.MessengerLabelSnapshot{},
		&models.MessengerJourneySequence{}, &models.MessengerJourneyReceipt{}, &models.MessengerJourneyEvent{}, &models.MessengerJourneyCustomer{},
		&models.MessengerJourneyOrder{}, &models.MessengerJourneyCoverage{}, &models.MessengerJourneyErasure{},
	); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	createFacebookOAuthTenant(t, database, "tenant-a")
	createFacebookOAuthTenant(t, database, "tenant-b")
	rows := []any{
		&models.Channel{ID: "legacy-a", TenantID: "tenant-a", ChannelType: "facebook", Name: "Page A", ExternalID: "100", IsActive: true, CredentialsEncrypted: []byte("x"), Metadata: "{}", CreatedAt: now, UpdatedAt: now},
		&models.Channel{ID: "legacy-b", TenantID: "tenant-b", ChannelType: "facebook", Name: "Page B", ExternalID: "900", IsActive: true, CredentialsEncrypted: []byte("x"), Metadata: "{}", CreatedAt: now, UpdatedAt: now},
		&models.Conversation{ID: "label-conv", TenantID: "tenant-a", ChannelID: "legacy-a", ExternalConversationID: "label-thread", Metadata: `{"participants":{"data":[{"id":"100"},{"id":"202"}]}}`, CreatedAt: now, UpdatedAt: now},
		&models.Conversation{ID: "import-conv", TenantID: "tenant-a", ChannelID: "legacy-a", ExternalConversationID: "import-thread", Metadata: `{"participants":{"data":[{"id":"100"},{"id":"203"}]}}`, CreatedAt: now, UpdatedAt: now},
		&models.MessengerLabelSnapshot{ConversationID: "label-conv", TenantID: "tenant-a", ChannelID: "legacy-a", PSID: "202", Labels: `[]`, Status: "success", CheckedAt: &now, AttemptedAt: now},
		&models.MessengerIntakeAttribution{ID: "legacy-intake-a", TenantID: "tenant-a", ChannelID: "legacy-a", PageID: "100", PSID: "201", RawReferral: `{"ad_id":"ad-a"}`, CapturedAt: now, CreatedAt: now, UpdatedAt: now},
		&models.MessengerIntakeAttribution{ID: "legacy-intake-b", TenantID: "tenant-b", ChannelID: "legacy-b", PageID: "900", PSID: "901", RawReferral: `{"ad_id":"ad-b"}`, CapturedAt: now, CreatedAt: now, UpdatedAt: now},
	}
	for _, row := range rows {
		if err := database.Create(row).Error; err != nil {
			t.Fatalf("seed %T: %v", row, err)
		}
	}
	tenantChannels, err := tenantJourneyChannelIDs(database, "tenant-a")
	if err != nil || len(tenantChannels) != 1 || tenantChannels[0] != "legacy-a" {
		t.Fatalf("tenant deletion would miss legacy-only channel: ids=%v error=%v", tenantChannels, err)
	}

	if _, err := deleteChannelCascade(database, "tenant-a", "legacy-a"); err != nil {
		t.Fatal(err)
	}
	var tombstones []models.MessengerJourneyEvent
	if err := database.Where("tenant_id = ? AND kind = ?", "tenant-a", messengerjourney.KindDeletion).Order("sequence").Find(&tombstones).Error; err != nil {
		t.Fatal(err)
	}
	if len(tombstones) != 3 {
		t.Fatalf("legacy-only subjects missing tombstones: %+v", tombstones)
	}
	seenSubjects := make(map[string]bool, len(tombstones))
	for _, tombstone := range tombstones {
		if tombstone.PSID != "" || tombstone.Payload != "{}" || tombstone.SubjectKey == "" {
			t.Fatalf("tombstone retained PII: %+v", tombstone)
		}
		seenSubjects[tombstone.SubjectKey] = true
	}
	if len(seenSubjects) != 3 {
		t.Fatalf("legacy sources were not deduplicated by subject: %+v", tombstones)
	}
	var otherTenantIntake int64
	if err := database.Model(&models.MessengerIntakeAttribution{}).Where("tenant_id = ? AND psid = ?", "tenant-b", "901").Count(&otherTenantIntake).Error; err != nil || otherTenantIntake != 1 {
		t.Fatalf("other tenant legacy intake changed: count=%d error=%v", otherTenantIntake, err)
	}
	var otherTenantEvents int64
	if err := database.Model(&models.MessengerJourneyEvent{}).Where("tenant_id = ?", "tenant-b").Count(&otherTenantEvents).Error; err != nil || otherTenantEvents != 0 {
		t.Fatalf("other tenant received deletion event: count=%d error=%v", otherTenantEvents, err)
	}
}
