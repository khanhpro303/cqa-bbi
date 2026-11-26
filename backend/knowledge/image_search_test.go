package knowledge

import (
	"context"
	"slices"
	"testing"

	"github.com/vietbui/chat-quality-agent/chatmemory"
	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/db/models"
	"github.com/vietbui/chat-quality-agent/pkg"
)

func TestCaptionSearchWordsSkipsRequestPhrasing(t *testing.T) {
	if got := captionSearchWords("Cho chị xem mômen siết bulông nhé"); !slices.Equal(got, []string{"bulông", "mômen", "siết"}) {
		t.Fatalf("caption words: %q", got)
	}
	if got := captionSearchWords("Cho em xem hình ảnh với"); len(got) != 0 {
		t.Fatalf("caption words: %q", got)
	}
}

func TestMergeCaptionHitsPreservesPassageRanking(t *testing.T) {
	passages := []Hit{{DocumentID: "A"}, {DocumentID: "B"}, {DocumentID: "C"}}
	captions := []Hit{{DocumentID: "D", ImageID: "image-d", Score: 1}, {DocumentID: "E", ImageID: "image-e", Score: 0.5}}
	for _, tc := range []struct {
		name  string
		limit int
		want  string
	}{
		{"single hit", 1, "D"},
		{"reserves a passage", 2, "DA"},
		{"keeps passage order", 4, "DEAB"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var got string
			for _, hit := range mergeCaptionHits(passages, captions, tc.limit) {
				got += hit.DocumentID
			}
			if got != tc.want {
				t.Fatalf("hits = %q, want %q", got, tc.want)
			}
		})
	}
	for _, tc := range []struct {
		limit int
		want  string
	}{{1, "A"}, {2, "AE"}, {3, "AEB"}} {
		var got string
		for _, hit := range mergeCaptionHits(passages, captions[1:], tc.limit) {
			got += hit.DocumentID
		}
		if got != tc.want {
			t.Fatalf("partial match at limit %d = %q, want %q", tc.limit, got, tc.want)
		}
	}
}

func TestImageCaptionSearchUsesLiveVisibilityMySQL(t *testing.T) {
	connectTestMySQL(t)
	ctx := context.Background()
	tenant := pkg.NewUUID()
	const caption = "Mômen siết bulông là 20 Nm"
	const query = "mômen siết bulông"
	newDoc := func(tenantID, audience, status string, enabled bool) string {
		t.Helper()
		doc := models.KnowledgeDocument{
			ID: pkg.NewUUID(), TenantID: tenantID, Title: "Hướng dẫn lắp ráp",
			Audience: audience, Status: status, Enabled: true,
		}
		if err := db.DB.Create(&doc).Error; err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { db.DB.Delete(&doc) })
		if !enabled {
			if err := db.DB.Model(&doc).Update("enabled", false).Error; err != nil {
				t.Fatal(err)
			}
		}
		return doc.ID
	}
	newImage := func(tenantID, docID, text string, enabled bool) string {
		t.Helper()
		im := models.KnowledgeDocumentImage{
			ID: pkg.NewUUID(), TenantID: tenantID, DocumentID: docID,
			Caption: text, FileName: "guide.png", MediaType: "image/png", Width: 1, Height: 1, Data: []byte{1}, Enabled: true,
		}
		if err := db.DB.Create(&im).Error; err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { db.DB.Delete(&im) })
		if !enabled {
			if err := db.DB.Model(&im).Update("enabled", false).Error; err != nil {
				t.Fatal(err)
			}
		}
		return im.ID
	}
	pub := newDoc(tenant, AudiencePublic, StatusReady, true)
	imageID := newImage(tenant, pub, caption, true)
	newImage(tenant, pub, "Mômen siết cần kiểm tra ở bảng khác", true)
	internalImage := newImage(tenant, newDoc(tenant, AudienceInternal, StatusReady, true), caption, true)
	newImage(tenant, newDoc(tenant, AudiencePublic, StatusReady, true), caption, false)
	newImage(tenant, newDoc(tenant, AudiencePublic, StatusReady, false), caption, true)
	newImage(tenant, newDoc(tenant, AudiencePublic, StatusArchived, true), caption, true)
	newImage(tenant, newDoc(tenant, AudiencePublic, StatusProcessing, true), caption, true)
	otherTenant := pkg.NewUUID()
	otherDoc := newDoc(otherTenant, AudiencePublic, StatusReady, true)
	newImage(otherTenant, otherDoc, caption, true)
	newImage(tenant, otherDoc, caption, true)
	newImage(otherTenant, pub, "Mômen siết bulông từ tenant khác", true)

	assertHits := func(t *testing.T, svc *Service, audience, query string, expected ...string) {
		t.Helper()
		hits, err := svc.Search(ctx, tenant, audience, query, 5)
		if err != nil || len(hits) != len(expected) {
			t.Fatalf("%s search %q: hits = %+v, error = %v", audience, query, hits, err)
		}
		want := make(map[string]bool, len(expected))
		for _, id := range expected {
			want[id] = true
		}
		for _, h := range hits {
			if !want[h.ImageID] || h.Content == "" {
				t.Fatalf("unexpected or duplicate caption: %+v", h)
			}
			delete(want, h.ImageID)
		}
	}
	keyword := NewService(nil, nil)
	vector := NewService(&memIndex{docs: map[string]chatmemory.VectorDoc{}}, bowEmbed)
	for _, svc := range []*Service{keyword, vector} {
		assertHits(t, svc, "public", query, imageID)
		assertHits(t, svc, "unknown", query, imageID)
		assertHits(t, svc, "private", query, imageID, internalImage)
	}
	if err := db.DB.Model(&models.KnowledgeDocumentImage{}).Where("document_id = ?", pub).Update("enabled", false).Error; err != nil {
		t.Fatal(err)
	}
	assertHits(t, vector, "public", query)
	if err := db.DB.Model(&models.KnowledgeDocumentImage{}).Where("id = ?", imageID).Updates(map[string]any{
		"enabled": true, "caption": "Cỡ lục giác 7 mm",
	}).Error; err != nil {
		t.Fatal(err)
	}
	assertHits(t, vector, "public", query)
	assertHits(t, vector, "public", "lục giác", imageID)
	if err := db.DB.Model(&models.KnowledgeDocument{}).Where("id = ?", pub).Update("audience", AudienceInternal).Error; err != nil {
		t.Fatal(err)
	}
	assertHits(t, vector, "public", "lục giác")
	assertHits(t, vector, "private", "lục giác", imageID)
	if err := db.DB.Delete(&models.KnowledgeDocumentImage{ID: imageID}).Error; err != nil {
		t.Fatal(err)
	}
	assertHits(t, vector, "private", "lục giác")
}
