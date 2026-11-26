package handlers

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/vietbui/chat-quality-agent/config"
	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/db/models"
	"github.com/vietbui/chat-quality-agent/engine"
	"github.com/vietbui/chat-quality-agent/knowledge"
	"github.com/vietbui/chat-quality-agent/pkg"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

func TestKnowledgeImageCRUDMySQL(t *testing.T) {
	dsn := os.Getenv("CQA_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("CQA_TEST_MYSQL_DSN not set")
	}
	database, err := gorm.Open(mysql.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal("connect to test MySQL")
	}
	if err := database.AutoMigrate(&models.KnowledgeDocument{}, &models.KnowledgeChunk{}, &models.KnowledgeDocumentImage{},
		&models.ActivityLog{}, &models.AppSetting{}); err != nil {
		t.Fatal(err)
	}
	previousDB := db.DB
	db.DB = database
	previousKnowledge := knowledge.Default()
	knowledge.SetDefault(knowledge.NewService(nil, nil))
	tenant, otherTenant := pkg.NewUUID(), pkg.NewUUID()
	doc := models.KnowledgeDocument{ID: pkg.NewUUID(), TenantID: tenant, Status: knowledge.StatusReady, Title: "Chính sách"}
	otherDoc := models.KnowledgeDocument{ID: pkg.NewUUID(), TenantID: tenant, Status: knowledge.StatusReady, Title: "Vận chuyển"}
	t.Cleanup(func() {
		db.DB = previousDB
		knowledge.SetDefault(previousKnowledge)
		for _, model := range []any{&models.KnowledgeDocumentImage{}, &models.KnowledgeChunk{}, &models.KnowledgeDocument{}, &models.ActivityLog{}, &models.AppSetting{}} {
			if err := database.Where("tenant_id = ?", tenant).Delete(model).Error; err != nil {
				t.Error(err)
			}
		}
		if sqlDB, err := database.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
	if err := database.Create(&[]models.KnowledgeDocument{doc, otherDoc}).Error; err != nil {
		t.Fatal(err)
	}
	chunk := models.KnowledgeChunk{ID: pkg.NewUUID(), DocumentID: doc.ID, TenantID: tenant, Content: "Đoạn văn cần xóa"}
	if err := database.Create(&chunk).Error; err != nil {
		t.Fatal(err)
	}

	gin.SetMode(gin.TestMode)
	request := func(tenantID, docID, imageID, method, path, contentType string, body io.Reader, handler gin.HandlerFunc) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Set("tenant_id", tenantID)
		c.Params = gin.Params{{Key: "docId", Value: docID}, {Key: "imageId", Value: imageID}}
		c.Request = httptest.NewRequest(method, path, body)
		c.Request.Header.Set("Content-Type", contentType)
		handler(c)
		c.Writer.WriteHeaderNow()
		return rec
	}
	expect := func(rec *httptest.ResponseRecorder, status int) *httptest.ResponseRecorder {
		t.Helper()
		if rec.Code != status {
			t.Fatalf("got %d, want %d: %s", rec.Code, status, rec.Body.String())
		}
		return rec
	}
	jsonRequest := func(tenantID, docID, imageID, method, path, body string, status int, handler gin.HandlerFunc) *httptest.ResponseRecorder {
		t.Helper()
		return expect(request(tenantID, docID, imageID, method, path, "application/json", strings.NewReader(body), handler), status)
	}
	imageData := knowledgeTestPNG(t)
	makeUpload := func(tenantID, docID string, count int, captions string) func() *httptest.ResponseRecorder {
		files := make([][]byte, count)
		for i := range files {
			files[i] = imageData
		}
		body, contentType := knowledgeImageMultipart(t, files, captions)
		return func() *httptest.ResponseRecorder {
			return request(tenantID, docID, "", http.MethodPost, "/", contentType, body, UploadKnowledgeDocumentImages)
		}
	}

	expect(makeUpload(otherTenant, doc.ID, 1, "")(), http.StatusNotFound)
	created := expect(makeUpload(tenant, doc.ID, 2, `["  Bảng bảo hành  ", "Sơ đồ 2"]`)(), http.StatusCreated)
	var body struct {
		Images []knowledgeDocumentImageDTO `json:"images"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &body); err != nil || len(body.Images) != 2 {
		t.Fatalf("invalid uploaded images: %s", created.Body.String())
	}
	first, second := body.Images[0], body.Images[1]
	if first.Caption != "Bảng bảo hành" || first.Width != 5 || first.Height != 7 || !first.Enabled || first.DocumentID != doc.ID ||
		first.URL != "/api/v1/tenants/"+tenant+"/knowledge/"+doc.ID+"/images/"+first.ID+"/content" {
		t.Fatalf("bad uploaded metadata: %+v", first)
	}
	content := jsonRequest(tenant, doc.ID, first.ID, http.MethodGet, "/", "", http.StatusOK, GetKnowledgeDocumentImageContent)
	if !bytes.Equal(content.Body.Bytes(), imageData) || content.Header().Get("Content-Type") != "image/png" ||
		content.Header().Get("X-Content-Type-Options") != "nosniff" || content.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatalf("bad image content: %v", content.Header())
	}
	jsonRequest(otherTenant, doc.ID, first.ID, http.MethodGet, "/", "", http.StatusNotFound, GetKnowledgeDocumentImageContent)
	jsonRequest(tenant, otherDoc.ID, first.ID, http.MethodGet, "/", "", http.StatusNotFound, GetKnowledgeDocumentImageContent)
	jsonRequest(otherTenant, doc.ID, first.ID, http.MethodPatch, "/", `{"caption":"wrong tenant"}`, http.StatusNotFound, UpdateKnowledgeDocumentImage)
	jsonRequest(tenant, otherDoc.ID, first.ID, http.MethodDelete, "/", "", http.StatusNotFound, DeleteKnowledgeDocumentImage)
	var visionRequests atomic.Int32
	visionServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		visionRequests.Add(1)
		var payload map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil || r.Header.Get("Authorization") != "Bearer tenant-key" ||
			!strings.Contains(string(payload["messages"]), "data:image/png;base64,"+base64.StdEncoding.EncodeToString(imageData)) ||
			string(payload["model"]) != `"test-vision-model"` {
			t.Error("expected Vision request with configured tenant key, model and image")
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":" Bảng\n bảo hành năm 2026 "}}]}`)
	}))
	t.Cleanup(visionServer.Close)
	set := func(key, value string) {
		t.Helper()
		if err := database.Create(&models.AppSetting{ID: pkg.NewUUID(), TenantID: tenant, SettingKey: key, ValuePlain: value}).Error; err != nil {
			t.Fatal(err)
		}
	}
	set("ai_provider", "openai")
	set("ai_base_url_openai", visionServer.URL)
	describe := func(c *gin.Context) { DescribeKnowledgeDocumentImage(c, &config.Config{LangflowAPIKey: "global-key"}) }
	noVision := jsonRequest(tenant, doc.ID, first.ID, http.MethodPost, "/", "", http.StatusConflict, describe)
	if visionRequests.Load() != 0 || !strings.Contains(noVision.Body.String(), `"image_caption_no_vision"`) {
		t.Fatal("global Langflow credential must never be used to describe an image")
	}
	set("ai_api_key_openai", "tenant-key")
	set(engine.AITaskModelSettingPrefix+engine.AITaskKnowledgeImageCaption, `{"provider":"openai","model":"test-vision-model"}`)
	suggestion := jsonRequest(tenant, doc.ID, first.ID, http.MethodPost, "/", "", http.StatusOK, describe)
	if visionRequests.Load() != 1 || !strings.Contains(suggestion.Body.String(), `"caption":"Bảng bảo hành năm 2026"`) {
		t.Fatalf("unexpected Vision suggestion: %s", suggestion.Body.String())
	}
	var stored models.KnowledgeDocumentImage
	if err := database.Select("caption").Take(&stored, "id = ?", first.ID).Error; err != nil || stored.Caption != first.Caption {
		t.Fatal("Vision suggestions must only be saved by an explicit update")
	}
	modified := jsonRequest(tenant, doc.ID, first.ID, http.MethodPatch, "/", `{"enabled":false,"sort_order":5,"caption":"Mô tả đã sửa"}`, http.StatusOK, UpdateKnowledgeDocumentImage)
	var updated knowledgeDocumentImageDTO
	if err := json.Unmarshal(modified.Body.Bytes(), &updated); err != nil || updated.Enabled || updated.Caption != "Mô tả đã sửa" || updated.SortOrder != 5 {
		t.Fatalf("invalid update response: %s", modified.Body.String())
	}
	loaded := jsonRequest(tenant, doc.ID, "", http.MethodGet, "/", "", http.StatusOK, GetKnowledgeDocument)
	if err := json.Unmarshal(loaded.Body.Bytes(), &body); err != nil || len(body.Images) != 2 || body.Images[0].ID != second.ID || body.Images[1].Enabled {
		t.Fatalf("wrong images or order in details: %s", loaded.Body.String())
	}
	list := jsonRequest(tenant, "", "", http.MethodGet, "/", "", http.StatusOK, ListKnowledgeDocuments)
	var listing struct {
		Documents []knowledgeDocumentListItem `json:"documents"`
	}
	if err := json.Unmarshal(list.Body.Bytes(), &listing); err != nil || len(listing.Documents) != 2 {
		t.Fatalf("bad document list: %s", list.Body.String())
	}
	for _, item := range listing.Documents {
		if (item.ID == doc.ID && item.ImageCount != 2) || (item.ID == otherDoc.ID && item.ImageCount != 0) {
			t.Fatalf("bad attachment count for %s at %d", item.ID, item.ImageCount)
		}
	}
	jsonRequest(tenant, doc.ID, second.ID, http.MethodDelete, "/", "", http.StatusNoContent, DeleteKnowledgeDocumentImage)
	jsonRequest(tenant, doc.ID, second.ID, http.MethodGet, "/", "", http.StatusNotFound, GetKnowledgeDocumentImageContent)

	expect(makeUpload(tenant, doc.ID, 8, "")(), http.StatusCreated)
	expect(makeUpload(tenant, doc.ID, 2, "")(), http.StatusCreated)
	responses := make(chan int, 2)
	for i := 0; i < 2; i++ {
		upload := makeUpload(tenant, doc.ID, 1, "")
		go func() { responses <- upload().Code }()
	}
	statuses := map[int]int{<-responses: 1}
	statuses[<-responses]++
	if statuses[http.StatusCreated] != 1 || statuses[http.StatusConflict] != 1 {
		t.Fatalf("concurrent uploads did not enforce %d-image cap: %v", maxKnowledgeImagesPerDoc, statuses)
	}
	if err := database.Model(&doc).Update("status", knowledge.StatusArchived).Error; err != nil {
		t.Fatal(err)
	}
	expect(makeUpload(tenant, doc.ID, 1, "")(), http.StatusConflict)
	jsonRequest(tenant, doc.ID, "", http.MethodDelete, "/", "", http.StatusOK, PurgeKnowledgeDocument)
	for _, model := range []any{&models.KnowledgeDocumentImage{}, &models.KnowledgeChunk{}} {
		var count int64
		if err := database.Model(model).Where("document_id = ?", doc.ID).Count(&count).Error; err != nil || count != 0 {
			t.Fatalf("purge left %d %T rows: %v", count, model, err)
		}
	}
}
