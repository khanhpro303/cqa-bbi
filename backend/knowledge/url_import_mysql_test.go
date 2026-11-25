package knowledge

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/vietbui/chat-quality-agent/chatmemory"
	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/db/models"
	"github.com/vietbui/chat-quality-agent/pkg"
)

func TestCreateFromURLIndexesWithAudienceMySQL(t *testing.T) {
	connectTestMySQL(t)
	client := NewFirecrawlClient("fc-test")
	client.client = &http.Client{Transport: firecrawlTransport(func(*http.Request) (*http.Response, error) {
		return firecrawlStub(http.StatusOK, `{"success":true,"data":{"markdown":"Kho nội bộ giao hàng mỗi thứ ba.","metadata":{"title":"Lịch giao hàng","statusCode":200}}}`), nil
	})}
	svc := NewService(&memIndex{docs: map[string]chatmemory.VectorDoc{}}, bowEmbed)
	tenant := pkg.NewUUID()
	source, err := ParseSourceURL("https://example.com/lich")
	if err != nil {
		t.Fatal(err)
	}
	doc, err := svc.CreateFromURL(NewDocumentInput{TenantID: tenant, Audience: AudienceInternal}, source, client)
	if err != nil {
		t.Fatal(err)
	}
	if doc.Status != StatusProcessing || doc.Source != "url" || doc.SourceURL != source.String() || doc.Parser != ParserFirecrawl {
		t.Fatalf("unexpected accepted document: %+v", doc)
	}

	waitStatus(t, doc.ID, StatusReady)
	var saved models.KnowledgeDocument
	if err := db.DB.First(&saved, "id = ?", doc.ID).Error; err != nil {
		t.Fatal(err)
	}
	if saved.SourceURL != source.String() || saved.Title != "Lịch giao hàng" || saved.SizeBytes <= 0 || saved.ChunkCount != 1 {
		t.Fatalf("unexpected indexed document: %+v", saved)
	}
	for _, agent := range []string{"private", "public"} {
		hits, err := svc.Search(context.Background(), tenant, agent, "giao hàng thứ ba", 3)
		if err != nil {
			t.Fatal(err)
		}
		if agent == "private" {
			if len(hits) != 1 || !strings.Contains(hits[0].Content, "mỗi thứ ba") {
				t.Fatalf("internal search: %+v", hits)
			}
		} else if len(hits) != 0 {
			t.Fatalf("public search exposed an internal document: %+v", hits)
		}
	}
}
