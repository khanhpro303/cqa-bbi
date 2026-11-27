package engine

import (
	"testing"
	"time"

	"github.com/vietbui/chat-quality-agent/channels"
	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/db/models"
	"github.com/vietbui/chat-quality-agent/pkg"
)

func TestMySQLPersonalZaloIngestBeforeAccountIDIsSet(t *testing.T) {
	database := journeyEngineTestDatabase(t)
	previousDB := db.DB
	db.DB = database
	t.Cleanup(func() { db.DB = previousDB })

	tenant := models.Tenant{ID: pkg.NewUUID(), Name: "Test", Slug: pkg.NewUUID(), Settings: "{}"}
	channel := models.Channel{
		ID: pkg.NewUUID(), TenantID: tenant.ID, ChannelType: "personal_zalo_import",
		Name: "Personal Zalo", CredentialsEncrypted: []byte{}, IsActive: true, Metadata: "{}",
	}
	if err := database.Create(&tenant).Error; err != nil {
		t.Fatal(err)
	}
	if err := database.Create(&channel).Error; err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC().Truncate(time.Millisecond)
	conversation := channels.SyncedConversation{ExternalID: "user:contact", ExternalUserID: "contact", LastMessageAt: now}
	messages := []channels.SyncedMessage{{
		ExternalID: "message", SenderType: "customer", ContentType: "text", Content: "hello", SentAt: now,
	}}
	ingestor := NewIngestService()
	first, err := ingestor.IngestConversationBatch(tenant.ID, channel.ID, conversation, messages)
	if err != nil || first.ConversationID == "" || first.MessagesInserted != 1 {
		t.Fatalf("first import = %+v, %v", first, err)
	}
	second, err := ingestor.IngestConversationBatch(tenant.ID, channel.ID, conversation, messages)
	if err != nil || second.ConversationID != first.ConversationID || second.MessagesDeduplicated != 1 {
		t.Fatalf("retry import = %+v, %v", second, err)
	}
	var stored models.Conversation
	if err := database.First(&stored, "id = ?", first.ConversationID).Error; err != nil || stored.MessageCount != 1 {
		t.Fatalf("stored conversation message count = %d, %v", stored.MessageCount, err)
	}
}
