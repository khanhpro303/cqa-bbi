package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/vietbui/chat-quality-agent/config"
	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/db/models"
	"github.com/vietbui/chat-quality-agent/knowledge"
	"github.com/vietbui/chat-quality-agent/pkg"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

func TestProductAdvisoryMySQL(t *testing.T) {
	dsn := os.Getenv("CQA_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("CQA_TEST_MYSQL_DSN not set")
	}
	database, err := gorm.Open(mysql.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal("connect to test MySQL")
	}
	previous := db.DB
	db.DB = database
	tenant, otherTenant := pkg.NewUUID(), pkg.NewUUID()
	t.Cleanup(func() {
		db.DB = previous
		for _, model := range []any{&models.ProductAdvisoryImage{}, &models.ProductAdvisory{}, &models.CachedProduct{}, &models.AppSetting{}} {
			if err := database.Where("tenant_id IN ?", []string{tenant, otherTenant}).Delete(model).Error; err != nil {
				t.Error(err)
			}
		}
		if sqlDB, err := database.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
	if err := database.AutoMigrate(&models.CachedProduct{}, &models.ProductAdvisory{}, &models.ProductAdvisoryImage{}, &models.AppSetting{}); err != nil {
		t.Fatal(err)
	}
	rows := []models.CachedProduct{
		{ID: pkg.NewUUID(), TenantID: tenant, MA: "P1-1", MA_CHA: "P1", TEN_DONG_BO_WEB: "Storm II"},
		{ID: pkg.NewUUID(), TenantID: tenant, MA: "P1-2", MA_CHA: "P1", TEN_DONG_BO_WEB: "Storm II"},
		{ID: pkg.NewUUID(), TenantID: tenant, MA: "P1-3_DK", MA_CHA: "P1_DK", TEN_DONG_BO_WEB: "Storm II"},
		{ID: pkg.NewUUID(), TenantID: tenant, MA: "P2", TEN_DONG_BO_WEB: "Vector II"},
		{ID: pkg.NewUUID(), TenantID: otherTenant, MA: "P1-1", MA_CHA: "P1", TEN_DONG_BO_WEB: "Other tenant"},
	}
	if err := database.Create(&rows).Error; err != nil {
		t.Fatal(err)
	}
	call := func(tenantID, method, path, contentType string, body *bytes.Reader, parent, imageID string, status int, handler gin.HandlerFunc) *httptest.ResponseRecorder {
		t.Helper()
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Set("tenant_id", tenantID)
		c.Params = gin.Params{{Key: "parentCode", Value: parent}, {Key: "imageId", Value: imageID}}
		c.Request = httptest.NewRequest(method, path, body)
		c.Request.Header.Set("Content-Type", contentType)
		handler(c)
		if rec.Code != status {
			t.Fatalf("%s %s returned %d, want %d: %s", method, path, rec.Code, status, rec.Body.String())
		}
		return rec
	}
	request := func(tenantID, method, path, body, imageID string, status int, handler gin.HandlerFunc) *httptest.ResponseRecorder {
		t.Helper()
		return call(tenantID, method, path, "application/json", bytes.NewReader([]byte(body)), "P1", imageID, status, handler)
	}
	list := request(tenant, http.MethodGet, "/?page=1&per_page=1", "", "", http.StatusOK, ListProductAdvisories)
	var result struct {
		Items []productAdvisoryListItem `json:"items"`
		Total int                       `json:"total"`
	}
	if err := json.Unmarshal(list.Body.Bytes(), &result); err != nil || result.Total != 2 || len(result.Items) != 1 || result.Items[0].ParentCode != "P1" || result.Items[0].VariantCount != 2 {
		t.Fatalf("unexpected catalog page: %s", list.Body.String())
	}
	const source = "https://shop.example.com/storm"
	request(tenant, http.MethodPut, "/P1", `{"notes":"Vỏ nhẹ","source_url":"`+source+`"}`, "", http.StatusOK, SaveProductAdvisory)
	group, _, err := loadProductCatalogGroup(context.Background(), tenant, "P1")
	if err != nil {
		t.Fatal(err)
	}
	scraped := knowledge.ScrapedPage{Title: "Storm", Text: "Kính chống UV", Images: []string{"https://cdn.example.com/storm.jpg"}}
	const summaryText = "Storm II có kính chống UV."
	aiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Model string `json:"model"`
			Input []struct {
				Role    string `json:"role"`
				Content []struct {
					Text string `json:"text"`
				} `json:"content"`
			} `json:"input"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Model != "test-preview-model" ||
			len(body.Input) != 2 || body.Input[1].Role != "user" || len(body.Input[1].Content) != 1 {
			t.Error("expected product page through the workspace's summary model")
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		var sourceData map[string]string
		if err := json.Unmarshal([]byte(body.Input[1].Content[0].Text), &sourceData); err != nil ||
			sourceData["title"] != group.WebName || sourceData["content"] != scraped.Text {
			t.Error("expected product name and scraped page as summary data")
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"output_text": summaryText})
	}))
	t.Cleanup(aiServer.Close)
	for key, value := range map[string]string{
		"ai_provider": "openai", "ai_api_key": "test-key", "ai_base_url": aiServer.URL,
		"ai_task_model_knowledge_document_summary": `{"provider":"openai","model":"test-preview-model"}`,
	} {
		if err := database.Create(&models.AppSetting{ID: pkg.NewUUID(), TenantID: tenant, SettingKey: key, ValuePlain: value}).Error; err != nil {
			t.Fatal(err)
		}
	}
	summary, err := knowledge.SummarizeProductPage(context.Background(), tenant, &config.Config{}, group.WebName, scraped)
	if err != nil || summary != summaryText {
		t.Fatalf("unexpected AI summary %q: %v", summary, err)
	}
	if err := saveProductAdvisoryScrape(context.Background(), tenant, group, source, scraped, summary); err != nil {
		t.Fatal(err)
	}
	get := func() productAdvisoryDetail {
		t.Helper()
		rec := request(tenant, http.MethodGet, "/P1", "", "", http.StatusOK, GetProductAdvisory)
		var detail productAdvisoryDetail
		if err := json.Unmarshal(rec.Body.Bytes(), &detail); err != nil {
			t.Fatal(err)
		}
		return detail
	}
	initial := get()
	if initial.Notes != "Vỏ nhẹ" || initial.ScrapedContent != scraped.Text || initial.SourceSummary != summaryText || len(initial.Images) != 1 || initial.Images[0].Enabled {
		t.Fatalf("unexpected initial details: %+v", initial)
	}
	imageID := initial.Images[0].ID
	request(tenant, http.MethodPatch, "/P1/image", `{"enabled":true,"caption":"Mặt trước"}`, imageID, http.StatusOK, UpdateProductAdvisoryImage)

	var imageData, formData bytes.Buffer
	if err := png.Encode(&imageData, image.NewNRGBA(image.Rect(0, 0, 4, 4))); err != nil {
		t.Fatal(err)
	}
	form := multipart.NewWriter(&formData)
	file, err := form.CreateFormFile("images", "photo.png")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write(imageData.Bytes()); err != nil {
		t.Fatal(err)
	}
	if err := form.Close(); err != nil {
		t.Fatal(err)
	}
	upload := call(tenant, http.MethodPost, "/P1/images", form.FormDataContentType(), bytes.NewReader(formData.Bytes()), "P1", "", http.StatusCreated, UploadProductAdvisoryImages)
	var images struct{ Images []productAdvisoryImageDTO }
	if err := json.Unmarshal(upload.Body.Bytes(), &images); err != nil || len(images.Images) != 1 {
		t.Fatalf("unexpected upload: %s", upload.Body.String())
	}
	manual := images.Images[0]
	scraped.Images = append(scraped.Images, "https://cdn.example.com/new.jpg")
	if err := saveProductAdvisoryScrape(context.Background(), tenant, group, source, scraped, ""); err != nil {
		t.Fatal(err)
	}
	updated := get()
	if updated.SourceSummary != summaryText || len(updated.Images) != 3 || updated.Images[0].ID != imageID || !updated.Images[0].Enabled || updated.Images[0].Caption != "Mặt trước" {
		t.Fatalf("re-scrape lost summary or reviewed images: %+v", updated)
	}
	if rec := request(tenant, http.MethodGet, manual.URL, "", manual.ID, http.StatusOK, GetProductAdvisoryImageContent); !bytes.Equal(rec.Body.Bytes(), imageData.Bytes()) {
		t.Fatal("uploaded image changed")
	}
	request(otherTenant, http.MethodGet, manual.URL, "", manual.ID, http.StatusNotFound, GetProductAdvisoryImageContent)
	other := request(otherTenant, http.MethodGet, "/P1", "", "", http.StatusOK, GetProductAdvisory)
	if strings.Contains(other.Body.String(), "Vỏ nhẹ") || strings.Contains(other.Body.String(), source) {
		t.Fatal("another tenant saw shared product context")
	}
	request(tenant, http.MethodPut, "/P1", `{"notes":"Vỏ nhẹ","source_url":""}`, "", http.StatusOK, SaveProductAdvisory)
	cleared := get()
	if cleared.ScrapedContent != "" || cleared.SourceSummary != "" || len(cleared.Images) != 1 || cleared.Images[0].ID != manual.ID {
		t.Fatalf("clearing source affected wrong fields: %+v", cleared)
	}

	const sharedParent = "SP462693"
	shared := []models.CachedProduct{
		{ID: pkg.NewUUID(), TenantID: tenant, MA: "SP462693", MA_CHA: sharedParent, TEN_DONG_BO_WEB: "LS2 FF818", LIST_TEN_NHOM_VTHH: "Nguyên Đầu"},
		{ID: pkg.NewUUID(), TenantID: tenant, MA: "SP462696", MA_CHA: sharedParent, TEN_DONG_BO_WEB: " ls2 ff818 ", LIST_TEN_NHOM_VTHH: " nguyên đầu "},
		{ID: pkg.NewUUID(), TenantID: tenant, MA: "SP462552", MA_CHA: sharedParent, TEN_DONG_BO_WEB: "Phụ Kiện LS2", TEN: "Vỏ mũ FF818", LIST_TEN_NHOM_VTHH: "Phụ kiện"},
		{ID: pkg.NewUUID(), TenantID: otherTenant, MA: "SP462696", MA_CHA: sharedParent, TEN_DONG_BO_WEB: "Other tenant"},
	}
	if err := database.Create(&shared).Error; err != nil {
		t.Fatal(err)
	}
	const legacyIndex = "idx_product_advisory_tenant_parent"
	if err := database.Exec("CREATE UNIQUE INDEX " + legacyIndex + " ON product_advisories (tenant_id, parent_code)").Error; err != nil {
		t.Fatal(err)
	}
	legacy := models.ProductAdvisory{ID: pkg.NewUUID(), TenantID: tenant, ParentCode: sharedParent, WebName: "Phụ Kiện LS2", Notes: "Ghi chú phụ kiện cũ"}
	if err := database.Create(&legacy).Error; err != nil {
		t.Fatal(err)
	}
	legacyImage := models.ProductAdvisoryImage{ID: pkg.NewUUID(), TenantID: tenant, ProductAdvisoryID: legacy.ID, Source: "upload", Enabled: true, MediaType: "image/png", Data: imageData.Bytes()}
	if err := database.Create(&legacyImage).Error; err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := db.MigrateProductAdvisoryGroups(database); err != nil {
			t.Fatal(err)
		}
	}
	search := func(query string) []productAdvisoryListItem {
		t.Helper()
		list := request(tenant, http.MethodGet, "/?q="+query, "", "", http.StatusOK, ListProductAdvisories)
		var payload struct {
			Items []productAdvisoryListItem
			Total int
		}
		if err := json.Unmarshal(list.Body.Bytes(), &payload); err != nil || payload.Total != len(payload.Items) {
			t.Fatalf("unexpected search result for %q: %s", query, list.Body.String())
		}
		return payload.Items
	}
	byParent := search(sharedParent)
	byName, bySKU, accessory := search("FF818"), search("sp462696"), search("SP462552")
	if len(byParent) != 2 || len(byName) != 1 || len(bySKU) != 1 || len(accessory) != 1 ||
		len(byName[0].GroupKey) != 64 || byName[0].GroupKey != bySKU[0].GroupKey ||
		bySKU[0].VariantCount != 2 || bySKU[0].HasContent ||
		accessory[0].GroupKey == bySKU[0].GroupKey || !accessory[0].HasContent {
		t.Fatalf("wrong group split: parent=%+v name=%+v sku=%+v accessory=%+v", byParent, byName, bySKU, accessory)
	}
	line := func(method, key, body, imageID string, status int, handler gin.HandlerFunc) *httptest.ResponseRecorder {
		t.Helper()
		return call(tenant, method, "/"+key, "application/json", bytes.NewReader([]byte(body)), key, imageID, status, handler)
	}
	line(http.MethodGet, sharedParent, "", "", http.StatusConflict, GetProductAdvisory)
	line(http.MethodPut, bySKU[0].GroupKey, `{"notes":"Tư vấn cho mũ FF818"}`, "", http.StatusOK, SaveProductAdvisory)
	for key, note := range map[string]string{bySKU[0].GroupKey: "Tư vấn cho mũ FF818", accessory[0].GroupKey: legacy.Notes} {
		rec := line(http.MethodGet, key, "", "", http.StatusOK, GetProductAdvisory)
		var detail productAdvisoryDetail
		if err := json.Unmarshal(rec.Body.Bytes(), &detail); err != nil || detail.Notes != note {
			t.Fatalf("wrong advisory for %s: %s", key, rec.Body.String())
		}
	}
	line(http.MethodGet, accessory[0].GroupKey, "", legacyImage.ID, http.StatusOK, GetProductAdvisoryImageContent)
	line(http.MethodGet, bySKU[0].GroupKey, "", legacyImage.ID, http.StatusNotFound, GetProductAdvisoryImageContent)
	line(http.MethodGet, sharedParent, "", legacyImage.ID, http.StatusOK, GetProductAdvisoryImageContent)

	// Even a shared synchronized name cannot mix helmets with accessories.
	if err := database.Model(&shared[2]).UpdateColumn("ten_dong_bo_web", "LS2 FF818").Error; err != nil {
		t.Fatal(err)
	}
	sameName := search("FF818")
	if len(sameName) != 2 || sameName[0].ProductCategory == sameName[1].ProductCategory {
		t.Fatalf("same-name groups were not split by category: %+v", sameName)
	}
	saved := search("Phụ%20Kiện%20LS2")
	if len(saved) != 1 || !saved[0].CatalogMissing || !saved[0].HasContent {
		t.Fatalf("previously saved advice disappeared after ERP renamed its line: %+v", saved)
	}
	detail := line(http.MethodGet, saved[0].GroupKey, "", "", http.StatusOK, GetProductAdvisory)
	if !strings.Contains(detail.Body.String(), legacy.Notes) {
		t.Fatalf("saved advisory is not readable: %s", detail.Body.String())
	}
	line(http.MethodGet, saved[0].GroupKey, "", legacyImage.ID, http.StatusOK, GetProductAdvisoryImageContent)
	line(http.MethodPut, saved[0].GroupKey, `{"notes":"Changed"}`, "", http.StatusNotFound, SaveProductAdvisory)
}
