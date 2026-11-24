package handlers

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/vietbui/chat-quality-agent/api/middleware"
	"github.com/vietbui/chat-quality-agent/config"
	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/db/models"
	"github.com/vietbui/chat-quality-agent/knowledge"
	"github.com/vietbui/chat-quality-agent/pkg"
)

// ListKnowledgeDocuments lists the tenant's chatbot documents. ?view=archived
// lists the archive.
func ListKnowledgeDocuments(c *gin.Context) {
	tenantID := middleware.GetTenantID(c)
	q := db.DB.Where("tenant_id = ?", tenantID)
	if c.Query("view") == "archived" {
		q = q.Where("status = ?", knowledge.StatusArchived)
	} else {
		q = q.Where("status <> ?", knowledge.StatusArchived)
	}
	var docs []models.KnowledgeDocument
	if err := q.Omit("Summary", "SummaryAttempted", "SummaryStartedAt").Order("created_at DESC").Limit(1000).Find(&docs).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "query_failed"})
		return
	}
	svc := knowledge.Default()
	parser := tenantParser(tenantID)
	parserName := knowledge.ParserLocal
	if parser.Enabled() {
		parserName = knowledge.ParserUnstructured
	}
	c.JSON(http.StatusOK, gin.H{
		"documents":            docs,
		"vector_store":         svc.IndexName(),
		"parser":               parserName,
		"supported_extensions": knowledge.AcceptedExtensions(parser),
		"max_upload_mb":        knowledge.MaxUploadBytes >> 20,
	})
}

// UploadKnowledgeDocument takes a multipart file (field "file") plus optional
// "title" and "audience" (public | internal).
func UploadKnowledgeDocument(c *gin.Context) {
	tenantID := middleware.GetTenantID(c)
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, knowledge.MaxUploadBytes+(1<<20))
	fh, err := c.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "file_required"})
		return
	}
	if fh.Size > knowledge.MaxUploadBytes {
		c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "file_too_large"})
		return
	}
	f, err := fh.Open()
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "file_unreadable"})
		return
	}
	data, err := io.ReadAll(io.LimitReader(f, knowledge.MaxUploadBytes+1))
	f.Close()
	if err != nil || int64(len(data)) > knowledge.MaxUploadBytes {
		c.JSON(http.StatusBadRequest, gin.H{"error": "file_unreadable"})
		return
	}
	name := filepath.Base(fh.Filename)
	parser := tenantParser(tenantID)
	if !knowledge.SupportsExtension(parser, name) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "unsupported_type", "supported": knowledge.AcceptedExtensions(parser)})
		return
	}
	title := strings.TrimSpace(c.PostForm("title"))
	if title == "" {
		title = strings.TrimSuffix(name, filepath.Ext(name))
	}
	// Reading (Unstructured can take minutes on a scanned PDF), chunking and
	// indexing run in the background; the page polls the status.
	doc, err := knowledge.Default().CreateFromFile(knowledge.NewDocumentInput{
		TenantID: tenantID, Title: title, FileName: name, MimeType: fh.Header.Get("Content-Type"),
		Source: "upload", SizeBytes: int64(len(data)), Audience: c.PostForm("audience"),
		CreatedBy: middleware.GetUserEmail(c),
	}, data, parser)
	if err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": "save_failed", "message": err.Error()})
		return
	}
	db.LogActivity(tenantID, middleware.GetUserID(c), middleware.GetUserEmail(c), "knowledge.document_uploaded", "knowledge_document", doc.ID,
		fmt.Sprintf("%s (%s, %d bytes)", doc.Title, doc.Audience, doc.SizeBytes), "", c.ClientIP())
	c.JSON(http.StatusAccepted, doc)
}

// CreateKnowledgeText adds a document typed in the page.
func CreateKnowledgeText(c *gin.Context) {
	tenantID := middleware.GetTenantID(c)
	var req struct {
		Title    string `json:"title"`
		Content  string `json:"content"`
		Audience string `json:"audience"`
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 8<<20)
	if err := c.ShouldBindJSON(&req); err != nil || strings.TrimSpace(req.Title) == "" || strings.TrimSpace(req.Content) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "title_and_content_required"})
		return
	}
	text := knowledge.NormalizeText(req.Content)
	if utf8.RuneCountInString(text) > knowledge.MaxTextChars {
		c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "too_long"})
		return
	}
	doc, err := knowledge.Default().CreateDocument(knowledge.NewDocumentInput{
		TenantID: tenantID, Title: req.Title, Source: "text", SizeBytes: int64(len(req.Content)),
		Audience: req.Audience, CreatedBy: middleware.GetUserEmail(c), Text: text,
	})
	if err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": "save_failed", "message": err.Error()})
		return
	}
	db.LogActivity(tenantID, middleware.GetUserID(c), middleware.GetUserEmail(c), "knowledge.document_created", "knowledge_document", doc.ID,
		fmt.Sprintf("%s (%d đoạn, %s)", doc.Title, doc.ChunkCount, doc.Audience), "", c.ClientIP())
	c.JSON(http.StatusCreated, doc)
}

func loadKnowledgeDoc(c *gin.Context) (*models.KnowledgeDocument, bool) {
	var doc models.KnowledgeDocument
	if err := db.DB.Where("tenant_id = ? AND id = ?", middleware.GetTenantID(c), c.Param("docId")).First(&doc).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "not_found"})
		return nil, false
	}
	return &doc, true
}

func lockKnowledgeDoc(c *gin.Context) (*models.KnowledgeDocument, func()) {
	release, ok := knowledge.LockDocumentIndex(c.Request.Context(), middleware.GetTenantID(c), c.Param("docId"), false)
	if !ok {
		c.JSON(http.StatusConflict, gin.H{"error": "index_busy"})
		return nil, nil
	}
	doc, ok := loadKnowledgeDoc(c)
	if !ok {
		release()
		return nil, nil
	}
	return doc, release
}

// GetKnowledgeDocument returns a document with its saved reading summary.
func GetKnowledgeDocument(c *gin.Context) {
	doc, ok := loadKnowledgeDoc(c)
	if !ok {
		return
	}
	c.JSON(http.StatusOK, gin.H{"document": doc, "summary": knowledge.ReadDocumentSummary(doc)})
}

func GenerateKnowledgeSummary(c *gin.Context, cfg *config.Config) {
	doc, ok := loadKnowledgeDoc(c)
	if !ok {
		return
	}
	summary, err := knowledge.StartDocumentSummary(c.Request.Context(), doc, cfg)
	switch {
	case errors.Is(err, knowledge.ErrSummaryNoText):
		c.JSON(http.StatusConflict, gin.H{"error": "summary_no_text"})
	case errors.Is(err, knowledge.ErrSummaryNoAI):
		c.JSON(http.StatusConflict, gin.H{"error": "summary_no_ai"})
	case errors.Is(err, knowledge.ErrSummarySize):
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": "summary_too_large"})
	case errors.Is(err, knowledge.ErrSummaryBusy):
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "summary_busy"})
	case err != nil:
		c.JSON(http.StatusInternalServerError, gin.H{"error": "summary_failed"})
	default:
		c.JSON(http.StatusAccepted, gin.H{"summary": summary})
	}
}

// UpdateKnowledgeDocument changes title, audience or enabled. A change of
// audience or enabled rebuilds the document's vectors.
func UpdateKnowledgeDocument(c *gin.Context) {
	doc, release := lockKnowledgeDoc(c)
	if doc == nil {
		return
	}
	defer release()
	var req struct {
		Title    *string `json:"title"`
		Audience *string `json:"audience"`
		Enabled  *bool   `json:"enabled"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_body"})
		return
	}
	if doc.Status == knowledge.StatusArchived {
		c.JSON(http.StatusConflict, gin.H{"error": "archived"})
		return
	}
	updates := map[string]interface{}{"updated_at": time.Now()}
	reindex, unindex := false, false
	enabled := doc.Enabled
	if req.Title != nil && strings.TrimSpace(*req.Title) != "" {
		updates["title"] = strings.TrimSpace(*req.Title)
		reindex = true // the title is embedded with each passage
	}
	if req.Audience != nil {
		if a := knowledge.NormalizeAudience(*req.Audience); a != doc.Audience {
			updates["audience"] = a
			reindex = true
		}
	}
	if req.Enabled != nil && *req.Enabled != doc.Enabled {
		updates["enabled"] = *req.Enabled
		enabled = *req.Enabled
		if *req.Enabled {
			reindex = true
		} else {
			unindex = true
		}
	}
	if err := db.DB.Model(doc).Updates(updates).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "update_failed"})
		return
	}
	svc := knowledge.Default()
	ctx, cancel := context.WithTimeout(c.Request.Context(), 60*time.Second)
	defer cancel()
	switch {
	case unindex:
		if err := svc.RemoveVectors(ctx, doc.ID); err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": "vector_delete_failed", "message": err.Error()})
			return
		}
	case reindex && enabled:
		if err := svc.Reindex(ctx, doc.ID); err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": "reindex_failed", "message": err.Error()})
			return
		}
	}
	db.LogActivity(doc.TenantID, middleware.GetUserID(c), middleware.GetUserEmail(c), "knowledge.document_updated", "knowledge_document", doc.ID, doc.Title, "", c.ClientIP())
	db.DB.First(doc, "id = ?", doc.ID)
	c.JSON(http.StatusOK, doc)
}

// ReindexKnowledgeDocument rebuilds a document's vectors.
func ReindexKnowledgeDocument(c *gin.Context) {
	doc, release := lockKnowledgeDoc(c)
	if doc == nil {
		return
	}
	defer release()
	if doc.Status == knowledge.StatusArchived || !doc.Enabled {
		c.JSON(http.StatusConflict, gin.H{"error": "not_active"})
		return
	}
	if doc.ChunkCount == 0 {
		c.JSON(http.StatusConflict, gin.H{"error": "index_no_text"})
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 60*time.Second)
	defer cancel()
	if err := knowledge.Default().Reindex(ctx, doc.ID); err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "reindex_failed", "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// ArchiveKnowledgeDocument hides a document from the bot (soft delete): its
// vectors are removed, the text is kept so it can be restored.
func ArchiveKnowledgeDocument(c *gin.Context) {
	doc, release := lockKnowledgeDoc(c)
	if doc == nil {
		return
	}
	defer release()
	ctx, cancel := context.WithTimeout(c.Request.Context(), 60*time.Second)
	defer cancel()
	if err := knowledge.Default().RemoveVectors(ctx, doc.ID); err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "vector_delete_failed", "message": err.Error()})
		return
	}
	now := time.Now()
	if err := db.DB.Model(doc).Updates(map[string]interface{}{"status": knowledge.StatusArchived, "archived_at": now, "indexed_at": nil, "updated_at": now}).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "archive_failed"})
		return
	}
	db.LogActivity(doc.TenantID, middleware.GetUserID(c), middleware.GetUserEmail(c), "knowledge.document_archived", "knowledge_document", doc.ID, doc.Title, "", c.ClientIP())
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// RestoreKnowledgeDocument brings an archived document back and re-indexes it.
func RestoreKnowledgeDocument(c *gin.Context) {
	doc, release := lockKnowledgeDoc(c)
	if doc == nil {
		return
	}
	defer release()
	if doc.Status != knowledge.StatusArchived {
		c.JSON(http.StatusConflict, gin.H{"error": "not_archived"})
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 60*time.Second)
	defer cancel()
	if err := knowledge.Default().Reindex(ctx, doc.ID); err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "reindex_failed", "message": err.Error()})
		return
	}
	db.LogActivity(doc.TenantID, middleware.GetUserID(c), middleware.GetUserEmail(c), "knowledge.document_restored", "knowledge_document", doc.ID, doc.Title, "", c.ClientIP())
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// PurgeKnowledgeDocument permanently deletes an archived document.
func PurgeKnowledgeDocument(c *gin.Context) {
	doc, release := lockKnowledgeDoc(c)
	if doc == nil {
		return
	}
	defer release()
	if doc.Status != knowledge.StatusArchived {
		c.JSON(http.StatusConflict, gin.H{"error": "archive_first"})
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 60*time.Second)
	defer cancel()
	if err := knowledge.Default().RemoveVectors(ctx, doc.ID); err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "vector_delete_failed", "message": err.Error()})
		return
	}
	tx := db.DB.Begin()
	tx.Where("document_id = ?", doc.ID).Delete(&models.KnowledgeChunk{})
	tx.Delete(doc)
	if err := tx.Commit().Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "delete_failed"})
		return
	}
	db.LogActivity(doc.TenantID, middleware.GetUserID(c), middleware.GetUserEmail(c), "knowledge.document_purged", "knowledge_document", doc.ID, doc.Title, "", c.ClientIP())
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// SearchKnowledgeTest runs the same search the bot runs, for the page's
// "thử hỏi" box. agent_type public shows what customers can get.
func SearchKnowledgeTest(c *gin.Context) {
	var req struct {
		Query     string `json:"query"`
		AgentType string `json:"agent_type"`
		Limit     int    `json:"limit"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || strings.TrimSpace(req.Query) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "query_required"})
		return
	}
	agent := "public"
	if req.AgentType == "private" {
		agent = "private"
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()
	hits, err := knowledge.Default().Search(ctx, middleware.GetTenantID(c), agent, req.Query, req.Limit)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "search_failed", "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"hits": hits, "vector_store": knowledge.Default().IndexName()})
}

// ImportKnowledgeFromAstra copies the passages of an old Langflow/Astra
// collection into the knowledge base. The Astra token is used for this call
// only and never stored or logged.
func ImportKnowledgeFromAstra(c *gin.Context) {
	tenantID := middleware.GetTenantID(c)
	var req struct {
		APIEndpoint string `json:"api_endpoint"`
		Token       string `json:"token"`
		Keyspace    string `json:"keyspace"`
		Collection  string `json:"collection"`
		Audience    string `json:"audience"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_body"})
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Minute)
	defer cancel()
	res, err := knowledge.Default().ImportAstra(ctx, knowledge.AstraImportInput{
		TenantID: tenantID, Endpoint: req.APIEndpoint, Token: req.Token, Keyspace: req.Keyspace,
		Collection: req.Collection, Audience: req.Audience, CreatedBy: middleware.GetUserEmail(c),
	})
	if err != nil {
		status := http.StatusBadGateway
		if errors.Is(err, knowledge.ErrInvalidImport) {
			status = http.StatusBadRequest
		}
		c.JSON(status, gin.H{"error": "import_failed", "message": err.Error(), "partial": res})
		return
	}
	db.LogActivity(tenantID, middleware.GetUserID(c), middleware.GetUserEmail(c), "knowledge.astra_imported", "knowledge_document", "",
		req.Collection+": "+strconv.Itoa(res.Documents)+" tài liệu, "+strconv.Itoa(res.Chunks)+" đoạn", "", c.ClientIP())
	c.JSON(http.StatusOK, res)
}

func tenantParser(tenantID string) knowledge.ParserSettings {
	cfg, _ := config.Load()
	key := ""
	if cfg != nil {
		key = cfg.EncryptionKey
	}
	return knowledge.LoadParserSettings(tenantID, key)
}

// GetKnowledgeParser returns the tenant's Unstructured setup (the key is
// never returned, only whether one is set).
func GetKnowledgeParser(c *gin.Context) {
	tenantID := middleware.GetTenantID(c)
	p := tenantParser(tenantID)
	var keyRow models.AppSetting
	tenantKey := db.DB.Where("tenant_id = ? AND setting_key = ?", tenantID, knowledge.SettingUnstructuredKey).First(&keyRow).Error == nil
	c.JSON(http.StatusOK, gin.H{
		"api_url":         getPlainSetting(tenantID, knowledge.SettingUnstructuredURL),
		"default_api_url": knowledge.DefaultUnstructuredURL,
		"strategy":        p.Strategy,
		"languages":       p.Languages,
		"has_key":         tenantKey,
		"env_configured":  !tenantKey && p.APIKey != "",
		"enabled":         p.Enabled(),
	})
}

// SaveKnowledgeParser stores the tenant's Unstructured setup. An empty
// api_key keeps the stored key; clear_key removes it.
func SaveKnowledgeParser(c *gin.Context) {
	tenantID := middleware.GetTenantID(c)
	var req struct {
		APIURL    string `json:"api_url"`
		APIKey    string `json:"api_key"`
		ClearKey  bool   `json:"clear_key"`
		Strategy  string `json:"strategy"`
		Languages string `json:"languages"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_body"})
		return
	}
	req.APIURL = strings.TrimSpace(req.APIURL)
	if err := knowledge.ValidateUnstructuredURL(req.APIURL); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_url", "message": err.Error()})
		return
	}
	switch req.Strategy {
	case "", "auto", "fast", "hi_res", "ocr_only":
	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_strategy"})
		return
	}
	setOrClear := func(key, value string) {
		if value != "" {
			upsertSetting(tenantID, key, value, nil)
		} else {
			db.DB.Where("tenant_id = ? AND setting_key = ?", tenantID, key).Delete(&models.AppSetting{})
		}
	}
	setOrClear(knowledge.SettingUnstructuredURL, req.APIURL)
	setOrClear(knowledge.SettingUnstructuredStrategy, req.Strategy)
	setOrClear(knowledge.SettingUnstructuredLanguages, strings.TrimSpace(req.Languages))
	if req.ClearKey {
		db.DB.Where("tenant_id = ? AND setting_key = ?", tenantID, knowledge.SettingUnstructuredKey).Delete(&models.AppSetting{})
	} else if k := strings.TrimSpace(req.APIKey); k != "" && !isMaskedSecret(k) {
		cfg, _ := config.Load()
		if cfg == nil || cfg.EncryptionKey == "" {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "encryption_unavailable"})
			return
		}
		enc, err := pkg.Encrypt([]byte(k), cfg.EncryptionKey)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "encryption_failed"})
			return
		}
		upsertSetting(tenantID, knowledge.SettingUnstructuredKey, "", enc)
	}
	db.LogActivity(tenantID, middleware.GetUserID(c), middleware.GetUserEmail(c), "knowledge.parser_updated", "settings", "knowledge_unstructured", "", "", c.ClientIP())
	GetKnowledgeParser(c)
}

// TestKnowledgeParser checks the Unstructured setup: a partition endpoint
// reads a tiny text file; Transform (v2) lists one job, which costs nothing.
func TestKnowledgeParser(c *gin.Context) {
	p := tenantParser(middleware.GetTenantID(c))
	if !p.Enabled() {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "not_configured"})
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 60*time.Second)
	defer cancel()
	if err := knowledge.CheckUnstructured(ctx, p); err != nil {
		c.JSON(http.StatusOK, gin.H{"ok": false, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "transform": p.IsTransform()})
}
