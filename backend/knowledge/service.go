// Package knowledge is the chatbot's business knowledge base: documents the
// tenant uploads in the CQA "Tài liệu chatbot" page (policies, warranty,
// shipping, product guides...). The bot reads it through the
// search_knowledge MCP tool, so the Langflow agent decides when to look
// something up.
//
// MySQL is the source of truth (knowledge_documents, knowledge_chunks). The
// vector index holds one embedding per chunk of every enabled, non-archived
// document and can be rebuilt from MySQL at any time.
package knowledge

import (
	"context"
	"errors"
	"fmt"
	"log"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/vietbui/chat-quality-agent/chatmemory"
	"github.com/vietbui/chat-quality-agent/config"
	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/db/models"
	"github.com/vietbui/chat-quality-agent/pkg"
	"gorm.io/gorm"
)

// Audience values.
const (
	AudiencePublic   = "public"   // customer bot and staff bot
	AudienceInternal = "internal" // staff bot only
)

// Document status values.
const (
	StatusProcessing = "processing"
	StatusReady      = "ready"
	StatusFailed     = "failed"
	StatusArchived   = "archived"
)

// Hit is one passage returned to the bot.
type Hit struct {
	DocumentID string  `json:"document_id"`
	ImageID    string  `json:"image_id,omitempty"`
	Title      string  `json:"title"`
	Content    string  `json:"content"`
	Score      float64 `json:"score"`
}

// EmbedFunc embeds a text for a tenant.
type EmbedFunc func(ctx context.Context, tenantID, text string) ([]float32, error)

// Service indexes and searches the knowledge base.
type Service struct {
	index chatmemory.VectorIndex
	embed EmbedFunc
	// indexMu serializes indexing so a large upload does not run many
	// embedding calls in parallel.
	indexMu sync.Mutex
}

// NewService builds a service on the configured vector store. index may be
// nil: search then falls back to keyword matching in MySQL.
func NewService(index chatmemory.VectorIndex, embed EmbedFunc) *Service {
	return &Service{index: index, embed: embed}
}

var (
	defaultOnce        sync.Once
	defaultSvc         *Service
	documentIndexLocks sync.Map
)

// LockDocumentIndex serializes vector writes and document changes per tenant
// and document. Mutations can decline to wait for a long-running index job.
func LockDocumentIndex(ctx context.Context, tenantID, docID string, wait bool) (release func(), ok bool) {
	key := [2]string{tenantID, docID}
	done := make(chan struct{})
	for ctx.Err() == nil {
		active, locked := documentIndexLocks.LoadOrStore(key, done)
		if !locked {
			return func() {
				documentIndexLocks.Delete(key)
				close(done)
			}, true
		}
		if !wait {
			break
		}
		select {
		case <-active.(chan struct{}):
		case <-ctx.Done():
		}
	}
	return nil, false
}

// Default returns the process-wide service built from the environment.
func Default() *Service {
	defaultOnce.Do(func() {
		cfg, err := config.Load()
		if err != nil || cfg == nil {
			log.Printf("[knowledge] config load failed (%v); keyword search only", err)
			defaultSvc = NewService(nil, nil)
			return
		}
		mc := cfg.ChatMemory
		idx := chatmemory.NewNamedVectorIndex(cfg, mc.KnowledgeAstraCollection, mc.KnowledgeVectorizeIndex)
		defaultSvc = NewService(idx, func(ctx context.Context, tenantID, text string) ([]float32, error) {
			return chatmemory.EmbedText(ctx, cfg, tenantID, text)
		})
		if idx == nil {
			log.Printf("[knowledge] no vector store configured; search uses keyword matching")
		}
	})
	return defaultSvc
}

// SetDefault replaces the process-wide service (tests).
func SetDefault(s *Service) {
	defaultOnce.Do(func() {})
	defaultSvc = s
}

// HasVectorIndex reports whether semantic search is available.
func (s *Service) HasVectorIndex() bool { return s.index != nil }

// IndexName names the vector store ("astra", "vectorize" or "none").
func (s *Service) IndexName() string {
	if s.index == nil {
		return "none"
	}
	return s.index.Name()
}

// NormalizeAudience maps input to a known audience (default public).
func NormalizeAudience(a string) string {
	if strings.EqualFold(strings.TrimSpace(a), AudienceInternal) {
		return AudienceInternal
	}
	return AudiencePublic
}

// NewDocumentInput describes a document to add.
type NewDocumentInput struct {
	TenantID  string
	Title     string
	FileName  string
	MimeType  string
	Source    string
	SizeBytes int64
	Audience  string
	// Tags are normalized here; invalid ones are dropped (handlers validate
	// user input first with NormalizeTags).
	Tags      []string
	CreatedBy string
	// Text is chunked with SplitChunks. Chunks, when set, are used as they
	// are instead (Astra import keeps the original passages).
	Text   string
	Chunks []string
}

// CreateDocument stores the document and its chunks, then indexes it in the
// background. The returned document is in status processing.
func (s *Service) CreateDocument(in NewDocumentInput) (*models.KnowledgeDocument, error) {
	chunks := prepareChunks(in)
	if len(chunks) == 0 {
		return nil, errors.New("document has no text")
	}
	doc := newDocument(in)
	if err := saveDocument(doc, chunks); err != nil {
		return nil, err
	}
	s.IndexAsync(doc.ID)
	return doc, nil
}

// CreateFromFile stores the document at once (status processing) and reads,
// chunks and indexes the file in the background, so a slow parser (a long
// scanned PDF through Unstructured) does not hold the upload request.
func (s *Service) CreateFromFile(in NewDocumentInput, data []byte, parser ParserSettings) (*models.KnowledgeDocument, error) {
	return s.createFromReader(newDocument(in), 30*time.Minute, func(ctx context.Context) (parsedDocument, error) {
		text, parserName, notice, err := ExtractDocument(ctx, parser, in.FileName, data)
		return parsedDocument{text: text, parser: parserName, notice: notice}, err
	})
}

type parsedDocument struct {
	text   string
	parser string
	notice string
	title  string
}

func (s *Service) createFromReader(doc *models.KnowledgeDocument, timeout time.Duration, read func(context.Context) (parsedDocument, error)) (*models.KnowledgeDocument, error) {
	if err := db.DB.Create(doc).Error; err != nil {
		return nil, err
	}
	go func(doc models.KnowledgeDocument) {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		parsed, err := read(ctx)
		cancel()
		if err != nil {
			s.fail(&doc, err)
			return
		}
		chunks := prepareChunks(NewDocumentInput{Text: parsed.text})
		if len(chunks) == 0 {
			s.fail(&doc, errors.New("document has no text"))
			return
		}
		if err := insertChunks(&doc, chunks, parsed); err != nil {
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				s.fail(&doc, fmt.Errorf("save parsed document: %w", err))
			}
			return
		}
		if err := s.IndexDocument(context.Background(), doc.ID); err != nil {
			log.Printf("[knowledge] index document %s: %v", doc.ID, err)
			return
		}
		if parsed.notice != "" {
			db.DB.Model(&models.KnowledgeDocument{}).Where("id = ? AND status = ?", doc.ID, StatusReady).Update("error", parsed.notice)
		}
	}(*doc)
	return doc, nil
}

func prepareChunks(in NewDocumentInput) []string {
	if len(in.Chunks) == 0 {
		return SplitChunks(in.Text, ChunkTargetChars, ChunkOverlapChars)
	}
	var split []string
	for _, c := range in.Chunks {
		c = NormalizeText(c)
		if c == "" {
			continue
		}
		if utf8.RuneCountInString(c) > 3*ChunkTargetChars {
			split = append(split, SplitChunks(c, ChunkTargetChars, ChunkOverlapChars)...)
		} else {
			split = append(split, c)
		}
	}
	return split
}

func newDocument(in NewDocumentInput) *models.KnowledgeDocument {
	title := strings.TrimSpace(in.Title)
	if title == "" {
		title = strings.TrimSpace(in.FileName)
	}
	if title == "" {
		title = "Tài liệu"
	}
	source := in.Source
	if source == "" {
		source = "upload"
	}
	tags, _ := filterTags(in.Tags)
	tags = slices.Compact(tags)
	if len(tags) > MaxDocumentTags {
		tags = tags[:MaxDocumentTags]
	}
	now := time.Now()
	return &models.KnowledgeDocument{
		ID: pkg.NewUUID(), TenantID: in.TenantID, Title: truncateRunes(title, 250), Tags: models.KnowledgeTagList(tags),
		FileName: truncateRunes(in.FileName, 250), MimeType: truncateRunes(in.MimeType, 100),
		Source: source, SizeBytes: in.SizeBytes, Audience: NormalizeAudience(in.Audience),
		Enabled: true, Status: StatusProcessing, CreatedBy: truncateRunes(in.CreatedBy, 250),
		CreatedAt: now, UpdatedAt: now,
	}
}

func chunkRows(doc *models.KnowledgeDocument, chunks []string) []models.KnowledgeChunk {
	now := time.Now()
	rows := make([]models.KnowledgeChunk, 0, len(chunks))
	chars := 0
	for i, c := range chunks {
		chars += utf8.RuneCountInString(c)
		rows = append(rows, models.KnowledgeChunk{
			ID: pkg.NewUUID(), TenantID: doc.TenantID, DocumentID: doc.ID, Position: i, Content: c, CreatedAt: now,
		})
	}
	doc.CharCount, doc.ChunkCount = chars, len(chunks)
	return rows
}

func saveDocument(doc *models.KnowledgeDocument, chunks []string) error {
	rows := chunkRows(doc, chunks)
	tx := db.DB.Begin()
	if err := tx.Create(doc).Error; err != nil {
		tx.Rollback()
		return err
	}
	if err := tx.CreateInBatches(rows, 200).Error; err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit().Error
}

func insertChunks(doc *models.KnowledgeDocument, chunks []string, parsed parsedDocument) error {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	release, ok := LockDocumentIndex(ctx, doc.TenantID, doc.ID, true)
	if !ok {
		return ctx.Err()
	}
	defer release()

	rows := chunkRows(doc, chunks)
	updates := map[string]interface{}{
		"parser": parsed.parser, "char_count": doc.CharCount, "chunk_count": doc.ChunkCount,
		// Extraction still finishes if the user disables the document.
		"status": gorm.Expr("CASE WHEN status = ? AND enabled = ? THEN ? ELSE status END", StatusProcessing, false, StatusReady),
	}
	if doc.SourceURL != "" {
		updates["size_bytes"] = len(parsed.text)
	}
	if parsed.title != "" {
		// Adopt the page title only while the provisional title is still present.
		updates["title"] = gorm.Expr("CASE WHEN title = ? THEN ? ELSE title END", doc.Title, truncateRunes(parsed.title, 250))
	}
	return db.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&models.KnowledgeDocument{}).Where("tenant_id = ? AND id = ?", doc.TenantID, doc.ID).
			Updates(updates)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}
		return tx.CreateInBatches(rows, 200).Error
	})
}

// IndexAsync indexes a document in the background.
func (s *Service) IndexAsync(docID string) {
	go func() {
		if err := s.IndexDocument(context.Background(), docID); err != nil {
			log.Printf("[knowledge] index document %s: %v", docID, err)
		}
	}()
}

// IndexDocument embeds every chunk not yet indexed and marks the document
// ready. Without a vector index the document is ready at once (keyword
// search).
func (s *Service) IndexDocument(ctx context.Context, docID string) error {
	s.indexMu.Lock()
	defer s.indexMu.Unlock()

	var doc models.KnowledgeDocument
	if err := db.DB.WithContext(ctx).Select("tenant_id").Where("id = ?", docID).First(&doc).Error; err != nil {
		return err
	}
	release, ok := LockDocumentIndex(ctx, doc.TenantID, docID, true)
	if !ok {
		return ctx.Err()
	}
	defer release()

	// Waiting behind another document and extracting a file do not use this
	// document's indexing time.
	ctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()

	if err := db.DB.WithContext(ctx).Where("id = ?", docID).First(&doc).Error; err != nil {
		return err
	}
	if doc.Status != StatusProcessing || !doc.Enabled {
		return nil
	}
	if doc.ChunkCount == 0 {
		return s.fail(&doc, errors.New("document has no extracted text"))
	}
	if s.index == nil || s.embed == nil {
		return markReady(docID, nil)
	}
	var chunks []models.KnowledgeChunk
	if err := db.DB.WithContext(ctx).Where("document_id = ? AND indexed = ?", docID, false).Order("position").Find(&chunks).Error; err != nil {
		return s.fail(&doc, fmt.Errorf("read unindexed chunks: %w", err))
	}
	const batch = 20
	for start := 0; start < len(chunks); start += batch {
		end := start + batch
		if end > len(chunks) {
			end = len(chunks)
		}
		docs := make([]chatmemory.VectorDoc, 0, end-start)
		ids := make([]string, 0, end-start)
		for _, ch := range chunks[start:end] {
			vec, err := s.embed(ctx, doc.TenantID, embedInput(doc.Title, ch.Content))
			if err != nil {
				return s.fail(&doc, fmt.Errorf("embed chunk %d: %w", ch.Position, err))
			}
			docs = append(docs, chatmemory.VectorDoc{
				ID: ch.ID, Vector: vec, Text: ch.Content,
				Metadata: map[string]string{
					"tenant_id": doc.TenantID, "document_id": doc.ID, "audience": doc.Audience,
				},
			})
			ids = append(ids, ch.ID)
		}
		if err := s.index.Upsert(ctx, docs); err != nil {
			return s.fail(&doc, fmt.Errorf("vector upsert: %w", err))
		}
		if err := db.DB.WithContext(ctx).Model(&models.KnowledgeChunk{}).Where("id IN ?", ids).Update("indexed", true).Error; err != nil {
			return s.fail(&doc, fmt.Errorf("save indexing progress: %w", err))
		}
	}
	now := time.Now()
	if err := markReady(docID, &now); err != nil {
		return s.fail(&doc, fmt.Errorf("finish indexing: %w", err))
	}
	return nil
}

// markReady and fail only move a document out of processing, so a document
// archived while it was being indexed stays archived.
func markReady(docID string, indexedAt *time.Time) error {
	return db.DB.Model(&models.KnowledgeDocument{}).Where("id = ? AND status = ?", docID, StatusProcessing).
		Updates(map[string]interface{}{"status": StatusReady, "error": "", "indexed_at": indexedAt, "updated_at": time.Now()}).Error
}

func (s *Service) fail(doc *models.KnowledgeDocument, err error) error {
	db.DB.Model(&models.KnowledgeDocument{}).Where("id = ? AND status = ?", doc.ID, StatusProcessing).Updates(map[string]interface{}{
		"status": StatusFailed, "error": truncateRunes(err.Error(), 1000), "updated_at": time.Now(),
	})
	return err
}

func embedInput(title, content string) string {
	return title + "\n\n" + content
}

// RemoveVectors deletes a document's chunk vectors and marks its chunks
// unindexed, so a later IndexDocument rebuilds them.
func (s *Service) RemoveVectors(ctx context.Context, docID string) error {
	var ids []string
	if err := db.DB.Model(&models.KnowledgeChunk{}).Where("document_id = ?", docID).Pluck("id", &ids).Error; err != nil {
		return err
	}
	if s.index != nil {
		// Astra's $in takes at most 100 values.
		for start := 0; start < len(ids); start += 100 {
			end := start + 100
			if end > len(ids) {
				end = len(ids)
			}
			if err := s.index.Delete(ctx, ids[start:end]); err != nil {
				return err
			}
		}
	}
	return db.DB.Model(&models.KnowledgeChunk{}).Where("document_id = ?", docID).Update("indexed", false).Error
}

// Reindex rebuilds a document's vectors (after an audience change, a failed
// run or a restore).
func (s *Service) Reindex(ctx context.Context, docID string) error {
	if err := s.RemoveVectors(ctx, docID); err != nil {
		return err
	}
	if err := db.DB.Model(&models.KnowledgeDocument{}).Where("id = ?", docID).
		Updates(map[string]interface{}{"status": StatusProcessing, "error": "", "indexed_at": nil, "archived_at": nil, "updated_at": time.Now()}).Error; err != nil {
		return err
	}
	s.IndexAsync(docID)
	return nil
}

// ResumePending restarts indexing that a restart interrupted. Documents
// whose text was not saved still need to be imported again.
func (s *Service) ResumePending() {
	var docs []models.KnowledgeDocument
	db.DB.Where("status = ? AND (enabled = ? OR chunk_count = 0)", StatusProcessing, true).Find(&docs)
	for i := range docs {
		if docs[i].ChunkCount == 0 {
			s.fail(&docs[i], errors.New("máy chủ khởi động lại khi đang đọc tài liệu; hãy nhập lại tài liệu này"))
			continue
		}
		s.IndexAsync(docs[i].ID)
	}
}

// Search returns up to topK passages for the query. agentType "public" (the
// customer bot) sees only public documents; the staff bot sees all.
func (s *Service) Search(ctx context.Context, tenantID, agentType, query string, topK int) ([]Hit, error) {
	return s.SearchTagged(ctx, tenantID, agentType, query, topK, nil)
}

// SearchTagged is Search limited to documents carrying at least one of tags
// (the answering specialist agent's knowledge tags). No tags searches every
// document the tier may read.
func (s *Service) SearchTagged(ctx context.Context, tenantID, agentType, query string, topK int, tags []string) ([]Hit, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, errors.New("empty query")
	}
	if topK <= 0 || topK > 10 {
		topK = 5
	}
	tags, matchNothing := filterTags(tags)
	if matchNothing {
		return nil, nil
	}
	scope := documentScope{publicOnly: agentType != "private", tags: tags}
	var hits []Hit
	var err error
	if s.index != nil && s.embed != nil {
		hits, err = s.vectorSearch(ctx, tenantID, scope, query, topK)
		if err != nil {
			log.Printf("[knowledge] vector search failed, using keyword search: %v", err)
		}
	}
	// The vector index does not know tags: when none of its nearest passages
	// is tagged for this agent, look through the tagged documents by keyword.
	if s.index == nil || s.embed == nil || err != nil || (len(hits) == 0 && len(tags) > 0) {
		hits, err = keywordSearch(tenantID, scope, query, topK)
		if err != nil {
			return nil, err
		}
	}
	captions, err := searchImageCaptions(ctx, tenantID, scope, captionSearchWords(query), topK)
	if err != nil {
		log.Printf("[knowledge] image caption search failed: %v", err)
		return hits, nil
	}
	return mergeCaptionHits(hits, captions, topK), nil
}

// documentScope is which documents a search may read: the tier's audience
// and, for a specialist agent, its knowledge tags.
type documentScope struct {
	publicOnly bool
	tags       []string
}

// where adds the scope to a query that joins knowledge_documents as d.
func (sc documentScope) where(q *gorm.DB) *gorm.DB {
	if sc.publicOnly {
		q = q.Where("d.audience = ?", AudiencePublic)
	}
	return whereDocumentTagged(q, sc.tags)
}

func (s *Service) vectorSearch(ctx context.Context, tenantID string, scope documentScope, query string, topK int) ([]Hit, error) {
	vec, err := s.embed(ctx, tenantID, query)
	if err != nil {
		return nil, err
	}
	filter := map[string]string{"tenant_id": tenantID}
	if scope.publicOnly {
		filter["audience"] = AudiencePublic
	}
	// Tags are filtered in MySQL below: ask for more neighbours so passages
	// of other topics do not crowd out the agent's documents.
	candidates := topK * 2
	if len(scope.tags) > 0 {
		candidates = topK * 8
	}
	raw, err := s.index.Query(ctx, vec, filter, candidates)
	if err != nil {
		return nil, err
	}
	scores := make(map[string]float64, len(raw))
	ids := make([]string, 0, len(raw))
	for _, h := range raw {
		scores[h.ID] = h.Score
		ids = append(ids, h.ID)
	}
	// MySQL decides what is live: a stale vector of an archived, disabled or
	// re-scoped document is dropped here.
	chunks, err := liveChunks(tenantID, scope, ids)
	if err != nil {
		return nil, err
	}
	hits := make([]Hit, 0, len(chunks))
	for _, c := range chunks {
		hits = append(hits, Hit{DocumentID: c.DocumentID, Title: c.Title, Content: c.Content, Score: scores[c.ID]})
	}
	sort.SliceStable(hits, func(i, j int) bool { return hits[i].Score > hits[j].Score })
	if len(hits) > topK {
		hits = hits[:topK]
	}
	return hits, nil
}

type liveChunk struct {
	ID         string
	DocumentID string
	Title      string
	Content    string
}

func liveChunks(tenantID string, scope documentScope, ids []string) ([]liveChunk, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	q := db.DB.Table("knowledge_chunks AS c").
		Select("c.id, c.document_id, d.title, c.content").
		Joins("JOIN knowledge_documents d ON d.id = c.document_id").
		Where("c.id IN ? AND c.tenant_id = ? AND d.tenant_id = ? AND d.enabled = ? AND d.status = ?", ids, tenantID, tenantID, true, StatusReady)
	q = scope.where(q)
	var out []liveChunk
	err := q.Scan(&out).Error
	return out, err
}

// keywordSearch is the fallback without a vector index: it ranks chunks by
// how many distinct query words they contain.
func keywordSearch(tenantID string, scope documentScope, query string, topK int) ([]Hit, error) {
	words := queryWords(query)
	if len(words) == 0 {
		return nil, nil
	}
	q := db.DB.Table("knowledge_chunks AS c").
		Select("c.id, c.document_id, d.title, c.content").
		Joins("JOIN knowledge_documents d ON d.id = c.document_id").
		Where("c.tenant_id = ? AND d.tenant_id = ? AND d.enabled = ? AND d.status = ?", tenantID, tenantID, true, StatusReady)
	q = scope.where(q)
	conds := make([]string, 0, len(words))
	args := make([]interface{}, 0, len(words))
	for _, w := range words {
		conds = append(conds, "c.content LIKE ?")
		args = append(args, "%"+escapeLike(w)+"%")
	}
	q = q.Where(strings.Join(conds, " OR "), args...).Limit(300)
	var rows []liveChunk
	if err := q.Scan(&rows).Error; err != nil {
		return nil, err
	}
	hits := make([]Hit, 0, len(rows))
	for _, r := range rows {
		lower := strings.ToLower(r.Content + " " + r.Title)
		n := 0
		for _, w := range words {
			if strings.Contains(lower, w) {
				n++
			}
		}
		hits = append(hits, Hit{DocumentID: r.DocumentID, Title: r.Title, Content: r.Content, Score: float64(n) / float64(len(words))})
	}
	sort.SliceStable(hits, func(i, j int) bool { return hits[i].Score > hits[j].Score })
	if len(hits) > topK {
		hits = hits[:topK]
	}
	return hits, nil
}

// queryWords returns the distinct lower-case words of at least 2 runes, the
// longest first, at most 8.
func queryWords(q string) []string {
	seen := map[string]bool{}
	var out []string
	for _, w := range strings.FieldsFunc(strings.ToLower(q), func(r rune) bool {
		return !(r == '_' || r == '-' || ('0' <= r && r <= '9') || ('a' <= r && r <= 'z') || r > 127)
	}) {
		if utf8.RuneCountInString(w) < 2 || seen[w] {
			continue
		}
		seen[w] = true
		out = append(out, w)
	}
	sort.SliceStable(out, func(i, j int) bool { return utf8.RuneCountInString(out[i]) > utf8.RuneCountInString(out[j]) })
	if len(out) > 8 {
		out = out[:8]
	}
	return out
}

func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}
