package engine

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/vietbui/chat-quality-agent/channels"
	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/db/models"
)

type personalZaloArchiveFixture struct {
	calls []string
}

func (*personalZaloArchiveFixture) RemoveConversation(string, string) error { return nil }

func (f *personalZaloArchiveFixture) Store(_ context.Context, tenantID, conversationID, messageID string, _ int, attachment channels.Attachment) (string, error) {
	f.calls = append(f.calls, messageID)
	switch messageID {
	case "expired":
		return "", channels.ErrPersonalZaloAttachmentUnavailable
	case "retry":
		return "", errors.New("temporary transport failure")
	}
	return tenantID + "/" + conversationID + "/personal-zalo/" + attachment.Name, nil
}

func TestPersonalZaloLegacyAttachmentPayloads(t *testing.T) {
	for _, tc := range []struct {
		name, kind, content, wantURL, wantName string
	}{
		{"original image", "image", `{"href":"https://photo.zadn.vn/full.jpg","thumb":"https://photo.zadn.vn/thumb.jpg"}`, "https://photo.zadn.vn/full.jpg", "full.jpg"},
		{"named file", "file", `{"href":"https://file-stal-1.dlfl.vn/dl?id=1","title":"Hóa đơn.pdf"}`, "https://file-stal-1.dlfl.vn/dl?id=1", "Hóa đơn.pdf"},
		{"file URL fallback", "file", `{"fileUrl":"https://file.zalo.me/dl","fileName":"folder\\data.csv"}`, "https://file.zalo.me/dl", "data.csv"},
		{"image URL fallback", "image", `{"oriUrl":"https://photo.zadn.vn/full.jpg"}`, "https://photo.zadn.vn/full.jpg", "full.jpg"},
		{"thumbnail only", "image", `{"thumb":"https://photo.zadn.vn/thumb.jpg"}`, "", ""},
		{"text fallback", "file", "unsupported payload", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := personalZaloMessageAttachments(models.Message{ContentType: tc.kind, Content: tc.content, Attachments: "[]"})
			if tc.wantURL == "" {
				if len(got) != 0 {
					t.Fatalf("unexpected attachments: %+v", got)
				}
				return
			}
			if len(got) != 1 || got[0].Type != tc.kind || got[0].URL != tc.wantURL || got[0].Name != tc.wantName {
				t.Fatalf("unexpected attachments: %+v", got)
			}
		})
	}
}

func TestMySQLPersonalZaloAttachmentArchive(t *testing.T) {
	database := journeyEngineTestDatabase(t)
	previousDB := db.DB
	db.DB = database
	t.Cleanup(func() { db.DB = previousDB })

	tenant := models.Tenant{ID: "tenant", Name: "Tenant", Slug: "tenant", Settings: "{}"}
	personal := models.Channel{ID: "personal", TenantID: tenant.ID, ChannelType: "personal_zalo_import", Name: "Personal Zalo", CredentialsEncrypted: []byte{}, IsActive: true, Metadata: "{}"}
	other := models.Channel{ID: "oa", TenantID: tenant.ID, ChannelType: "zalo_oa", Name: "Zalo OA", CredentialsEncrypted: []byte{}, IsActive: true, Metadata: `{"sync_files":true}`}
	for _, row := range []any{&tenant, &personal, &other} {
		if err := database.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now().UTC()
	ingestor := NewIngestService()
	conversation := channels.SyncedConversation{ExternalID: "thread", LastMessageAt: now}
	importMessages := func(channelID string, messages ...channels.SyncedMessage) string {
		t.Helper()
		result, err := ingestor.IngestConversationBatch(tenant.ID, channelID, conversation, messages)
		if err != nil {
			t.Fatal(err)
		}
		return result.ConversationID
	}
	image := func(id string, sentAt time.Time) channels.SyncedMessage {
		return channels.SyncedMessage{
			ExternalID: id, SenderType: "customer", ContentType: "image", SentAt: sentAt,
			Content: `{"href":"https://photo.zadn.vn/full.jpg"}`, RawData: map[string]any{"message_type": "chat.photo"},
		}
	}
	convID := importMessages(personal.ID, image("old", now.AddDate(0, -1, 0)), image("expired", now), image("retry", now))
	importMessages(other.ID, image("oa", now))
	archive := &personalZaloArchiveFixture{}
	run := func() {
		t.Helper()
		if err := archivePersonalZaloAttachments(context.Background(), database, archive); err != nil {
			t.Fatal(err)
		}
	}
	setMetadata := func(metadata string) {
		t.Helper()
		if err := database.Model(&personal).Update("metadata", metadata).Error; err != nil {
			t.Fatal(err)
		}
	}
	run()
	setMetadata(`{"sync_files":"true"}`)
	run()
	setMetadata(`{"sync_files":[true]}`)
	run()
	if len(archive.calls) != 0 {
		t.Fatalf("unconfigured channels downloaded %v", archive.calls)
	}

	setMetadata(`{"sync_files":true}`)
	run()
	file := channels.SyncedMessage{
		ExternalID: "new", SenderType: "customer", ContentType: "file", SentAt: now,
		Content: `{"href":"https://file.zalo.me/original","title":"invoice.pdf"}`,
	}
	importMessages(personal.ID, file)
	run()
	run()

	if got := strings.Join(archive.calls, ","); len(archive.calls) != 4 || !strings.HasSuffix(got, ",new") {
		t.Fatalf("archive downloads did not process history and new media once: %v", archive.calls)
	}
	checkCopy := func(id, name string) {
		t.Helper()
		var row models.Message
		if err := database.First(&row, "conversation_id = ? AND external_message_id = ?", convID, id).Error; err != nil {
			t.Fatal(err)
		}
		var attachments []channels.Attachment
		if err := json.Unmarshal([]byte(row.Attachments), &attachments); err != nil ||
			len(attachments) != 1 || attachments[0].Name != name || !strings.HasSuffix(attachments[0].LocalPath, "/"+name) {
			t.Fatalf("message %s has unexpected stored attachments: %s (error %v)", id, row.Attachments, err)
		}
		if id == "old" && !strings.Contains(row.RawData, "chat.photo") {
			t.Fatalf("archive overwrote Zalo message metadata: %s", row.RawData)
		}
	}
	checkCopy("old", "full.jpg")
	checkCopy("new", "invoice.pdf")

	// Importing the same original again must preserve the stored file path.
	importMessages(personal.ID, file)
	setMetadata(`{"sync_files":false}`)
	importMessages(personal.ID, image("disabled", now))
	run()
	checkCopy("new", "invoice.pdf")
	if len(archive.calls) != 4 {
		t.Fatalf("disabling should stop new downloads: %v", archive.calls)
	}
}
