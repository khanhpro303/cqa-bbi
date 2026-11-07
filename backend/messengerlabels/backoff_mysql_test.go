package messengerlabels

import (
	"context"
	"testing"
	"time"

	"github.com/vietbui/chat-quality-agent/channels"
	"github.com/vietbui/chat-quality-agent/db/models"
	"github.com/vietbui/chat-quality-agent/pkg"
)

// A label read Meta refused is not repeated on every message sync; it is
// retried after FailedReadRetryAfter or as soon as the conversation has a new
// message.
func TestMySQLRefusedLabelReadsBackOffUntilNewMessage(t *testing.T) {
	database := labelTestDatabase(t)
	ctx := context.Background()
	tenant := models.Tenant{ID: pkg.NewUUID(), Name: "Test", Slug: pkg.NewUUID(), Settings: "{}"}
	channel := models.Channel{ID: pkg.NewUUID(), TenantID: tenant.ID, ChannelType: "facebook", ExternalID: "100", Name: "Page", IsActive: true, CredentialsEncrypted: []byte("x"), Metadata: "{}"}
	for _, row := range []interface{}{&tenant, &channel} {
		if err := database.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	old := time.Now().Add(-time.Hour).Truncate(time.Second)
	for _, psid := range []string{"201", "202"} {
		conv := models.Conversation{ID: "conv-" + psid, TenantID: tenant.ID, ChannelID: channel.ID, ExternalConversationID: "t_" + psid, LastMessageAt: &old,
			Metadata: `{"participants":{"data":[{"id":"100"},{"id":"` + psid + `"}]}}`, CreatedAt: old, UpdatedAt: old}
		if err := database.Create(&conv).Error; err != nil {
			t.Fatal(err)
		}
	}
	refuse := &channels.LabelAPIError{Kind: "unsupported"}
	reader := &fakeReader{errors: map[string]error{"201": refuse, "202": refuse}}

	if _, failed, err := CaptureMissingIntakeLabels(ctx, database, channel, "100", reader); err != nil || failed != 2 {
		t.Fatalf("first capture failed=%d err=%v", failed, err)
	}
	if _, _, err := CaptureMissingIntakeLabels(ctx, database, channel, "100", reader); err != nil {
		t.Fatal(err)
	}
	if len(reader.calls) != 2 {
		t.Fatalf("refused reads were repeated at once: %v", reader.calls)
	}

	// A new message in one conversation makes its read due again.
	now := time.Now().Add(time.Second)
	if err := database.Model(&models.Conversation{}).Where("id = ?", "conv-202").Update("last_message_at", now).Error; err != nil {
		t.Fatal(err)
	}
	if _, _, err := CaptureMissingIntakeLabels(ctx, database, channel, "100", reader); err != nil {
		t.Fatal(err)
	}
	if len(reader.calls) != 3 || reader.calls[2] != "202" {
		t.Fatalf("conversation with a new message was not read again: %v", reader.calls)
	}

	// After the retry window every refused read is due again.
	past := time.Now().Add(-FailedReadRetryAfter - time.Minute)
	if err := database.Model(&models.MessengerLabelSnapshot{}).Where("channel_id = ?", channel.ID).Update("attempted_at", past).Error; err != nil {
		t.Fatal(err)
	}
	if _, _, err := CaptureMissingIntakeLabels(ctx, database, channel, "100", reader); err != nil {
		t.Fatal(err)
	}
	if len(reader.calls) != 5 {
		t.Fatalf("expired refusals were not retried: %v", reader.calls)
	}
}
