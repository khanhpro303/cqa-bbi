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
		for _, model := range []any{&models.ProductAdvisoryImage{}, &models.ProductAdvisory{}, &models.CachedProduct{}} {
			if err := database.Where("tenant_id IN ?", []string{tenant, otherTenant}).Delete(model).Error; err != nil {
				t.Error(err)
			}
		}
		if sqlDB, err := database.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
	if err := database.AutoMigrate(&models.CachedProduct{}, &models.ProductAdvisory{}, &models.ProductAdvisoryImage{}); err != nil {
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
	if err := saveProductAdvisoryScrape(context.Background(), tenant, group, source, scraped); err != nil {
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
	if initial.Notes != "Vỏ nhẹ" || initial.ScrapedContent != scraped.Text || len(initial.Images) != 1 || initial.Images[0].Enabled {
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
	if err := saveProductAdvisoryScrape(context.Background(), tenant, group, source, scraped); err != nil {
		t.Fatal(err)
	}
	updated := get()
	if len(updated.Images) != 3 || updated.Images[0].ID != imageID || !updated.Images[0].Enabled || updated.Images[0].Caption != "Mặt trước" {
		t.Fatalf("re-scrape lost reviewed images: %+v", updated.Images)
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
	if cleared.ScrapedContent != "" || len(cleared.Images) != 1 || cleared.Images[0].ID != manual.ID {
		t.Fatalf("clearing source affected wrong fields: %+v", cleared)
	}
}
