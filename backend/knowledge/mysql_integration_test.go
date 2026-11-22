package knowledge

import (
	"context"
	"hash/fnv"
	"math"
	"os"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vietbui/chat-quality-agent/chatmemory"
	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/db/models"
	"github.com/vietbui/chat-quality-agent/pkg"
)

// Needs MySQL 8, e.g.
//
//	docker run -d -e MYSQL_ROOT_PASSWORD=root -e MYSQL_DATABASE=cqa -p 3307:3306 mysql:8.0
//	CQA_TEST_MYSQL_DSN='root:root@tcp(127.0.0.1:3307)/cqa?parseTime=true&loc=UTC&charset=utf8mb4' go test ./knowledge
func connectTestMySQL(t *testing.T) {
	t.Helper()
	dsn := os.Getenv("CQA_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("CQA_TEST_MYSQL_DSN not set")
	}
	if db.DB == nil {
		if err := db.Connect(dsn, false); err != nil {
			t.Fatal(err)
		}
		if err := db.AutoMigrate(); err != nil {
			t.Fatal(err)
		}
	}
}

// bowEmbed is a tiny bag-of-words embedding: good enough to rank a passage
// that shares words with the query above one that does not.
func bowEmbed(_ context.Context, _ string, text string) ([]float32, error) {
	v := make([]float32, 64)
	for _, w := range queryWords(text) {
		h := fnv.New32a()
		h.Write([]byte(w))
		v[h.Sum32()%64]++
	}
	for _, w := range strings.Fields(strings.ToLower(text)) {
		h := fnv.New32a()
		h.Write([]byte(w))
		v[h.Sum32()%64] += 0.5
	}
	return v, nil
}

type memIndex struct {
	mu   sync.Mutex
	docs map[string]chatmemory.VectorDoc
}

func (m *memIndex) Name() string { return "mem" }
func (m *memIndex) Upsert(_ context.Context, docs []chatmemory.VectorDoc) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, d := range docs {
		m.docs[d.ID] = d
	}
	return nil
}
func (m *memIndex) Query(_ context.Context, vec []float32, filter map[string]string, topK int) ([]chatmemory.VectorHit, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var hits []chatmemory.VectorHit
	for _, d := range m.docs {
		match := true
		for k, v := range filter {
			if d.Metadata[k] != v {
				match = false
			}
		}
		if !match {
			continue
		}
		hits = append(hits, chatmemory.VectorHit{ID: d.ID, Score: cosine(vec, d.Vector), Text: d.Text})
	}
	sort.Slice(hits, func(i, j int) bool { return hits[i].Score > hits[j].Score })
	if len(hits) > topK {
		hits = hits[:topK]
	}
	return hits, nil
}
func (m *memIndex) Delete(_ context.Context, ids []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, id := range ids {
		delete(m.docs, id)
	}
	return nil
}

func cosine(a, b []float32) float64 {
	var dot, na, nb float64
	for i := range a {
		dot += float64(a[i] * b[i])
		na += float64(a[i] * a[i])
		nb += float64(b[i] * b[i])
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / math.Sqrt(na*nb)
}

func waitStatus(t *testing.T, docID, want string) {
	t.Helper()
	for i := 0; i < 100; i++ {
		var d models.KnowledgeDocument
		db.DB.First(&d, "id = ?", docID)
		if d.Status == want {
			return
		}
		time.Sleep(30 * time.Millisecond)
	}
	t.Fatalf("document %s never reached %s", docID, want)
}

func TestKnowledgeLifecycleMySQL(t *testing.T) {
	connectTestMySQL(t)
	ctx := context.Background()
	idx := &memIndex{docs: map[string]chatmemory.VectorDoc{}}
	svc := NewService(idx, bowEmbed)
	tenant := pkg.NewUUID()

	pub, err := svc.CreateDocument(NewDocumentInput{TenantID: tenant, Title: "Chính sách bảo hành",
		Text: "Mũ bảo hiểm được bảo hành 12 tháng cho lỗi sản xuất.\n\nKính chắn gió bảo hành 6 tháng."})
	if err != nil {
		t.Fatal(err)
	}
	internal, err := svc.CreateDocument(NewDocumentInput{TenantID: tenant, Title: "Chiết khấu đại lý", Audience: "internal",
		Text: "Chiết khấu đại lý cấp 1 là 25 phần trăm, không báo cho khách lẻ."})
	if err != nil {
		t.Fatal(err)
	}
	other, _ := svc.CreateDocument(NewDocumentInput{TenantID: pkg.NewUUID(), Title: "Tenant khác", Text: "bảo hành 99 tháng"})
	waitStatus(t, pub.ID, StatusReady)
	waitStatus(t, internal.ID, StatusReady)
	waitStatus(t, other.ID, StatusReady)

	hits, err := svc.Search(ctx, tenant, "public", "bảo hành mũ bao lâu", 5)
	if err != nil || len(hits) == 0 || hits[0].DocumentID != pub.ID {
		t.Fatalf("public search: %+v %v", hits, err)
	}
	for _, h := range hits {
		if h.DocumentID == internal.ID || h.DocumentID == other.ID {
			t.Fatalf("customer bot saw %s", h.Title)
		}
	}
	hits, _ = svc.Search(ctx, tenant, "private", "chiết khấu đại lý", 5)
	if len(hits) == 0 || hits[0].DocumentID != internal.ID {
		t.Fatalf("staff bot must see internal doc: %+v", hits)
	}

	// A stale vector of a disabled document is dropped by the MySQL check.
	db.DB.Model(&models.KnowledgeDocument{}).Where("id = ?", pub.ID).Update("enabled", false)
	hits, _ = svc.Search(ctx, tenant, "public", "bảo hành mũ", 5)
	if len(hits) != 0 {
		t.Fatalf("disabled doc still served: %+v", hits)
	}
	db.DB.Model(&models.KnowledgeDocument{}).Where("id = ?", pub.ID).Update("enabled", true)

	// Archive removes the vectors; restore rebuilds them.
	if err := svc.RemoveVectors(ctx, pub.ID); err != nil {
		t.Fatal(err)
	}
	for id, d := range idx.docs {
		if d.Metadata["document_id"] == pub.ID {
			t.Fatalf("vector %s left after RemoveVectors", id)
		}
	}
	if err := svc.Reindex(ctx, pub.ID); err != nil {
		t.Fatal(err)
	}
	waitStatus(t, pub.ID, StatusReady)
	if hits, _ = svc.Search(ctx, tenant, "public", "bảo hành mũ", 5); len(hits) == 0 {
		t.Fatal("reindexed doc not found")
	}

	// Keyword fallback without a vector index applies the same rules.
	kw := NewService(nil, nil)
	hits, err = kw.Search(ctx, tenant, "public", "kính chắn gió bảo hành", 5)
	if err != nil || len(hits) == 0 || hits[0].DocumentID != pub.ID {
		t.Fatalf("keyword search: %+v %v", hits, err)
	}
	if hits, _ = kw.Search(ctx, tenant, "public", "chiết khấu đại lý", 5); len(hits) != 0 {
		t.Fatalf("keyword search leaked internal doc: %+v", hits)
	}
}

func TestIndexDoesNotResurrectArchivedDocMySQL(t *testing.T) {
	connectTestMySQL(t)
	tenant := pkg.NewUUID()
	svc := NewService(&memIndex{docs: map[string]chatmemory.VectorDoc{}}, bowEmbed)
	svc.indexMu.Lock() // hold indexing so the archive lands first
	doc, err := svc.CreateDocument(NewDocumentInput{TenantID: tenant, Title: "T", Text: "nội dung"})
	if err != nil {
		t.Fatal(err)
	}
	db.DB.Model(&models.KnowledgeDocument{}).Where("id = ?", doc.ID).Update("status", StatusArchived)
	svc.indexMu.Unlock()
	time.Sleep(200 * time.Millisecond)
	var d models.KnowledgeDocument
	db.DB.First(&d, "id = ?", doc.ID)
	if d.Status != StatusArchived {
		t.Fatalf("archived doc became %s", d.Status)
	}
}
