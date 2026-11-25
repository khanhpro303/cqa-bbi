package models

import "time"

// KnowledgeDocument is one business document the chatbot may answer from
// (policies, warranty, shipping, product guides...). MySQL holds the
// extracted text and the chunks; the vector index only holds one embedding
// per chunk and can be rebuilt from MySQL at any time.
type KnowledgeDocument struct {
	ID       string `gorm:"type:char(36);primaryKey" json:"id"`
	TenantID string `gorm:"type:char(36);not null;index:idx_kbdoc_tenant_status,priority:1" json:"tenant_id"`
	Title    string `gorm:"type:varchar(255);not null" json:"title"`
	FileName string `gorm:"type:varchar(255)" json:"file_name"`
	MimeType string `gorm:"type:varchar(100)" json:"mime_type"`
	// Source is "upload", "text", "url" or "astra_import".
	Source    string `gorm:"type:varchar(30);not null;default:'upload'" json:"source"`
	SourceURL string `gorm:"type:varchar(2048)" json:"source_url,omitempty"`
	SizeBytes int64  `json:"size_bytes"`
	// Audience is "public" (customer bot and staff bot) or "internal" (staff
	// bot only).
	Audience string `gorm:"type:varchar(20);not null;default:'public'" json:"audience"`
	Enabled  bool   `gorm:"not null;default:true" json:"enabled"`
	// Status: processing | ready | failed | archived.
	Status string `gorm:"type:varchar(20);not null;default:'processing';index:idx_kbdoc_tenant_status,priority:2" json:"status"`
	// Error is why the document failed, or a notice on a ready document
	// (for example "read with the built-in reader because Unstructured
	// failed").
	Error string `gorm:"type:text" json:"error,omitempty"`
	// Parser is how the source was read ("unstructured", "local", or "firecrawl").
	Parser string `gorm:"type:varchar(20)" json:"parser"`
	// Summaries are loaded on the document detail endpoint only.
	Summary          string     `gorm:"type:text" json:"-"`
	SummaryAttempted bool       `gorm:"not null;default:false" json:"-"`
	SummaryStartedAt *time.Time `json:"-"`
	CharCount        int        `json:"char_count"`
	ChunkCount       int        `json:"chunk_count"`
	IndexedAt        *time.Time `json:"indexed_at"`
	CreatedBy        string     `gorm:"type:varchar(255)" json:"created_by"`
	ArchivedAt       *time.Time `json:"archived_at"`
	CreatedAt        time.Time  `gorm:"not null" json:"created_at"`
	UpdatedAt        time.Time  `gorm:"not null" json:"updated_at"`
}

func (KnowledgeDocument) TableName() string { return "knowledge_documents" }

// KnowledgeChunk is one searchable passage of a document. Its ID is also the
// vector ID in the index.
type KnowledgeChunk struct {
	ID         string    `gorm:"type:char(36);primaryKey" json:"id"`
	TenantID   string    `gorm:"type:char(36);not null;index" json:"tenant_id"`
	DocumentID string    `gorm:"type:char(36);not null;index:idx_kbchunk_doc,priority:1" json:"document_id"`
	Position   int       `gorm:"not null;index:idx_kbchunk_doc,priority:2" json:"position"`
	Content    string    `gorm:"type:mediumtext;not null" json:"content"`
	Indexed    bool      `gorm:"not null;default:false" json:"indexed"`
	CreatedAt  time.Time `gorm:"not null" json:"created_at"`
}

func (KnowledgeChunk) TableName() string { return "knowledge_chunks" }
