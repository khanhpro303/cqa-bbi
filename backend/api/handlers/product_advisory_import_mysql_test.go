package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/vietbui/chat-quality-agent/config"
	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/db/models"
	"github.com/vietbui/chat-quality-agent/knowledge"
	"github.com/vietbui/chat-quality-agent/pkg"
	"github.com/xuri/excelize/v2"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

func TestProductAdvisoryImportMySQL(t *testing.T) {
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
	tables := []any{
		&models.Tenant{}, &models.CachedProduct{}, &models.AppSetting{}, &models.ActivityLog{},
		&models.ProductAdvisory{}, &models.ProductAdvisoryImage{},
		&models.ProductAdvisoryImportJob{}, &models.ProductAdvisoryImportItem{}, &models.ProductAdvisoryImportImage{},
	}
	if err := database.AutoMigrate(tables...); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		db.DB = previous
		for i := len(tables) - 1; i > 0; i-- {
			if err := database.Where("tenant_id IN ?", []string{tenant, otherTenant}).Delete(tables[i]).Error; err != nil {
				t.Error(err)
			}
		}
		if err := database.Where("id IN ?", []string{tenant, otherTenant}).Delete(&models.Tenant{}).Error; err != nil {
			t.Error(err)
		}
		sqlDB, _ := database.DB()
		_ = sqlDB.Close()
	})
	insert := func(value any) {
		t.Helper()
		if err := database.Create(value).Error; err != nil {
			t.Fatal(err)
		}
	}
	insert(&[]models.Tenant{
		{ID: tenant, Name: "Import Test", Slug: tenant, Settings: "{}"},
		{ID: otherTenant, Name: "Other Test", Slug: otherTenant, Settings: "{}"},
	})
	parentCodes := []string{"001", "002", "003", "004"}
	for i, code := range parentCodes {
		insert(&models.CachedProduct{ID: pkg.NewUUID(), TenantID: tenant, MA: code + "-red", MA_CHA: code, TEN_DONG_BO_WEB: fmt.Sprintf("Product %d", i+1)})
	}
	insert(&models.CachedProduct{ID: pkg.NewUUID(), TenantID: tenant, MA: "005_DK", MA_CHA: "005_DK", TEN_DONG_BO_WEB: "Excluded"})
	insert(&models.CachedProduct{ID: pkg.NewUUID(), TenantID: otherTenant, MA: "hidden", TEN_DONG_BO_WEB: "Other tenant"})
	oldURL := "https://example.com/one"
	oldProfile := models.ProductAdvisory{ID: pkg.NewUUID(), TenantID: tenant, ParentCode: "001", WebName: "Product 1", Notes: "Manual notes", SourceURL: oldURL, SourceSummary: "Old summary"}
	failedProfile := models.ProductAdvisory{ID: pkg.NewUUID(), TenantID: tenant, ParentCode: "003", WebName: "Product 3", SourceURL: "https://example.com/prior", ScrapedContent: "Prior text", SourceSummary: "Keep this summary"}
	insert(&oldProfile)
	insert(&failedProfile)
	manualID, oldImageID, failedImageID := pkg.NewUUID(), pkg.NewUUID(), pkg.NewUUID()
	insert(&[]models.ProductAdvisoryImage{
		{ID: manualID, TenantID: tenant, ProductAdvisoryID: oldProfile.ID, Source: "upload", Enabled: true, Data: []byte{1, 2, 3}},
		{ID: oldImageID, TenantID: tenant, ProductAdvisoryID: oldProfile.ID, Source: "scraped", URL: "https://example.com/photo.jpg", Enabled: true},
		{ID: failedImageID, TenantID: tenant, ProductAdvisoryID: failedProfile.ID, Source: "scraped", URL: "https://example.com/old.jpg"},
	})

	t.Setenv("JWT_SECRET", strings.Repeat("j", 32))
	t.Setenv("DB_PASSWORD", "local-test")
	key := strings.Repeat("k", 32)
	t.Setenv("ENCRYPTION_KEY", key)
	encrypted, err := pkg.Encrypt([]byte("firecrawl-test-key"), key)
	if err != nil {
		t.Fatal(err)
	}
	insert(&models.AppSetting{ID: pkg.NewUUID(), TenantID: tenant, SettingKey: firecrawlKeySetting, ValueEncrypted: encrypted})
	aiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/responses" {
			http.NotFound(w, r)
			return
		}
		if !strings.Contains(readProductImportTestBody(t, r), "New description") {
			t.Error("AI request is missing scraped content")
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"output_text": "New AI summary"})
	}))
	t.Cleanup(aiServer.Close)
	for name, value := range map[string]string{
		"ai_provider": "openai", "ai_api_key": "ai-test-key", "ai_base_url": aiServer.URL,
		"ai_task_model_knowledge_document_summary": `{"provider":"openai","model":"test-summary-model"}`,
	} {
		insert(&models.AppSetting{ID: pkg.NewUUID(), TenantID: tenant, SettingKey: name, ValuePlain: value})
	}

	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("tenant_id", c.GetHeader("X-Test-Tenant"))
		c.Next()
	})
	router.GET("/template", DownloadProductAdvisoryLinkTemplate)
	router.POST("/imports", ImportProductAdvisoryLinks)
	router.GET("/imports/latest", GetLatestProductAdvisoryImportJob)
	router.GET("/imports/:jobId", GetProductAdvisoryImportJob)
	router.GET("/imports/:jobId/images", GetProductAdvisoryImportImages)
	router.POST("/imports/:jobId/images/approve", ApproveProductAdvisoryImportImages)
	request := func(method, path, requestTenant, body string, want int) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("X-Test-Tenant", requestTenant)
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != want {
			t.Fatalf("%s %s returned %d, want %d: %s", method, path, rec.Code, want, rec.Body.String())
		}
		return rec
	}
	template := request(http.MethodGet, "/template", tenant, "", http.StatusOK)
	workbook, err := excelize.OpenReader(template.Body)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = workbook.Close() }()
	rows, err := workbook.GetRows("San pham")
	if err != nil || len(rows) != 5 || len(rows[1]) < 3 || rows[1][1] != "001" || rows[1][2] != oldURL {
		t.Fatalf("unexpected template: %v, %v", rows, err)
	}
	templateCode, err := workbook.GetCellType("San pham", "B2")
	if err != nil || templateCode != excelize.CellTypeSharedString {
		t.Fatalf("template parent code should be stored as text, got %v: %v", templateCode, err)
	}

	csv := "ten_dong_bo_web,ma_cha,link\n" +
		",001,https://example.com/duplicate\n" +
		"Wrong name,001," + oldURL + "\n" +
		",002-red,https://example.com/two\n" +
		"Product 3,,https://example.com/fail\n" +
		"Product 4,005_DK,https://example.com/unmatched\n" +
		",004,\n" +
		",004,not a URL\n"
	submit := func() *httptest.ResponseRecorder {
		t.Helper()
		c := productImportMultipartContext(t, "products.csv", []byte(csv))
		c.Request.Header.Set("X-Test-Tenant", tenant)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, c.Request)
		return rec
	}
	var submissions [2]*httptest.ResponseRecorder
	var uploads sync.WaitGroup
	for i := range submissions {
		uploads.Add(1)
		go func(i int) {
			defer uploads.Done()
			submissions[i] = submit()
		}(i)
	}
	uploads.Wait()
	if submissions[0].Code == http.StatusConflict {
		submissions[0], submissions[1] = submissions[1], submissions[0]
	}
	if submissions[0].Code != http.StatusAccepted || submissions[1].Code != http.StatusConflict {
		t.Fatalf("wanted one accepted import and one conflict, got %d %s, %d %s", submissions[0].Code, submissions[0].Body, submissions[1].Code, submissions[1].Body)
	}
	var initial struct {
		Job   productAdvisoryImportJobDTO
		Items []productAdvisoryImportItemDTO
	}
	if err := json.Unmarshal(submissions[0].Body.Bytes(), &initial); err != nil || initial.Job.TotalRows != 7 ||
		initial.Job.MatchedCount != 3 || initial.Job.SkippedCount != 2 || initial.Job.FailedCount != 1 ||
		initial.Job.UnmatchedCount != 1 || initial.Items[2].MatchedBy != "child_sku" || initial.Items[3].MatchedBy != "web_name" {
		t.Fatalf("unexpected initial job %s: %v", submissions[0].Body, err)
	}
	importPath := "/imports/" + initial.Job.ID
	request(http.MethodPost, importPath+"/images/approve", tenant, `{"all":true}`, http.StatusConflict)
	request(http.MethodGet, importPath, otherTenant, "", http.StatusNotFound)

	ctx := context.Background()
	stale, claimed, err := claimProductAdvisoryImportItem(ctx)
	if err != nil || !claimed || stale.ParentCode != "001" {
		t.Fatalf("initial claim: %+v %t %v", stale, claimed, err)
	}
	if err := database.Model(&stale).Update("claimed_until", time.Now().Add(-time.Minute)).Error; err != nil {
		t.Fatal(err)
	}
	reclaimed, claimed, err := claimProductAdvisoryImportItem(ctx)
	if err != nil || !claimed || stale.ID != reclaimed.ID || stale.ClaimToken == reclaimed.ClaimToken || reclaimed.Attempts != 2 {
		t.Fatalf("restart claim: %+v %t %v", reclaimed, claimed, err)
	}
	if err := completeProductAdvisoryImportItem(ctx, stale, knowledge.ScrapedPage{Text: "Stale"}, "", ""); !errors.Is(err, errProductAdvisoryImportClaimLost) {
		t.Fatalf("stale worker was not fenced: %v", err)
	}

	claims := []models.ProductAdvisoryImportItem{reclaimed}
	for range 2 {
		item, claimed, err := claimProductAdvisoryImportItem(ctx)
		if err != nil || !claimed {
			t.Fatalf("claim remaining items: %t %v", claimed, err)
		}
		claims = append(claims, item)
	}
	scrape := func(_ context.Context, url string) (knowledge.ScrapedPage, error) {
		if strings.HasSuffix(url, "/fail") {
			return knowledge.ScrapedPage{}, errors.New("test scrape failure")
		}
		return knowledge.ScrapedPage{Title: "New title", Text: "New description", Images: []string{"https://example.com/photo.jpg", "https://example.com/second.jpg"}}, nil
	}
	var processing sync.WaitGroup
	for _, item := range claims {
		processing.Add(1)
		go func(item models.ProductAdvisoryImportItem) {
			defer processing.Done()
			if err := processClaimedProductAdvisoryImportItem(ctx, &config.Config{}, item, scrape); err != nil {
				t.Error(err)
			}
		}(item)
	}
	processing.Wait()
	status := request(http.MethodGet, "/imports/latest", tenant, "", http.StatusOK)
	var complete struct {
		Job   productAdvisoryImportJobDTO
		Items []productAdvisoryImportItemDTO
	}
	if err := json.Unmarshal(status.Body.Bytes(), &complete); err != nil || len(complete.Items) != 7 ||
		complete.Job.Status != "completed" || complete.Job.SucceededCount != 2 || complete.Job.FailedCount != 2 ||
		complete.Job.PendingImageCount != 4 || complete.Items[1].ImageCount != 2 || complete.Items[2].ImageCount != 2 ||
		complete.Items[3].ErrorCode != "scrape_failed" {
		t.Fatalf("unexpected job outcome %s: %v", status.Body, err)
	}

	verifyProfile := func(id, notes, url, content, summary string) {
		t.Helper()
		var record models.ProductAdvisory
		if err := database.Where("id = ?", id).Take(&record).Error; err != nil {
			t.Fatal(err)
		}
		if record.Notes != notes || record.SourceURL != url || record.ScrapedContent != content || record.SourceSummary != summary {
			t.Fatalf("unexpected advisory: %+v", record)
		}
	}
	verifyProfile(oldProfile.ID, "Manual notes", oldURL, "New description", "New AI summary")
	verifyProfile(failedProfile.ID, "", failedProfile.SourceURL, "Prior text", "Keep this summary")
	var previousCount int64
	if err := database.Model(&models.ProductAdvisoryImage{}).Where("id = ?", oldImageID).Count(&previousCount).Error; err != nil || previousCount != 0 {
		t.Fatalf("old scrape image still present: %d %v", previousCount, err)
	}

	firstItem := complete.Items[1]
	result := request(http.MethodGet, importPath+"/images?item_id="+firstItem.ID, tenant, "", http.StatusOK)
	var photos struct{ Images []productAdvisoryImageDTO }
	if err := json.Unmarshal(result.Body.Bytes(), &photos); err != nil || len(photos.Images) != 2 ||
		photos.Images[0].ID == oldImageID || photos.Images[0].Enabled || photos.Images[1].Enabled {
		t.Fatalf("images should be new and pending: %s %v", result.Body, err)
	}
	approve := func(approvalTenant, body string, responseStatus int, want int64) {
		t.Helper()
		result := request(http.MethodPost, importPath+"/images/approve", approvalTenant, body, responseStatus)
		if responseStatus != http.StatusOK {
			return
		}
		var payload struct {
			ApprovedCount int64 `json:"approved_count"`
		}
		if err := json.Unmarshal(result.Body.Bytes(), &payload); err != nil || payload.ApprovedCount != want {
			t.Fatalf("approve %s: %s %v", body, result.Body, err)
		}
	}
	approve(otherTenant, `{"all":true}`, http.StatusNotFound, 0)
	approve(tenant, `{"image_id":"`+failedImageID+`"}`, http.StatusOK, 0)
	approve(tenant, `{"image_id":"`+photos.Images[0].ID+`"}`, http.StatusOK, 1)

	// A later import which stops before saving cannot hide this batch's
	// remaining review work. Its exhausted claim must also finish the job.
	interrupted := models.ProductAdvisoryImportJob{
		ID: pkg.NewUUID(), TenantID: tenant, FileName: "interrupted.csv",
		TotalRows: 1, MatchedCount: 1, Status: "running", StartedAt: time.Now(), CreatedAt: time.Now().Add(time.Second),
	}
	insert(&interrupted)
	until := time.Now().Add(-time.Minute)
	interruptedItem := models.ProductAdvisoryImportItem{
		ID: pkg.NewUUID(), JobID: interrupted.ID, TenantID: tenant, RowNumber: 2,
		Status: "processing", Attempts: productAdvisoryImportMaxAttempts, ClaimToken: pkg.NewUUID(), ClaimedUntil: &until,
	}
	insert(&interruptedItem)
	if _, claimed, err := claimProductAdvisoryImportItem(ctx); err != nil || claimed {
		t.Fatalf("an exhausted item must be marked failed without processing it: %t %v", claimed, err)
	}
	reviewQueue := request(http.MethodGet, "/imports/latest?review_queue=1", tenant, "", http.StatusOK)
	if err := json.Unmarshal(reviewQueue.Body.Bytes(), &complete); err != nil ||
		complete.Job.ID != initial.Job.ID || complete.Job.PendingImageCount != 3 {
		t.Fatalf("previous pending batch disappeared: %s %v", reviewQueue.Body, err)
	}
	approve(tenant, `{"all":true}`, http.StatusOK, 3)
	approve(tenant, `{"all":true}`, http.StatusOK, 0)

	var manual, failedImage models.ProductAdvisoryImage
	if err := database.Where("id = ?", manualID).Take(&manual).Error; err != nil || !manual.Enabled || !bytes.Equal(manual.Data, []byte{1, 2, 3}) {
		t.Fatalf("manual upload changed: %+v %v", manual, err)
	}
	if err := database.Where("id = ?", failedImageID).Take(&failedImage).Error; err != nil || failedImage.Enabled {
		t.Fatalf("unrelated scrape image changed: %+v %v", failedImage, err)
	}
	final := request(http.MethodGet, importPath+"?summary=1", tenant, "", http.StatusOK)
	if err := json.Unmarshal(final.Body.Bytes(), &complete); err != nil || len(complete.Items) != 0 || complete.Job.PendingImageCount != 0 || complete.Job.ImageCount != 4 {
		t.Fatalf("unexpected post-review job %s: %v", final.Body, err)
	}
	latest := request(http.MethodGet, "/imports/latest?review_queue=1", tenant, "", http.StatusOK)
	if err := json.Unmarshal(latest.Body.Bytes(), &complete); err != nil || complete.Job.ID != interrupted.ID ||
		complete.Job.Status != "completed" || complete.Job.FailedCount != 1 || complete.Items[0].ErrorCode != "retry_exhausted" {
		t.Fatalf("interrupted job should finish as a row failure: %s %v", latest.Body, err)
	}
}

func readProductImportTestBody(t *testing.T, r *http.Request) string {
	t.Helper()
	var b bytes.Buffer
	if _, err := b.ReadFrom(r.Body); err != nil {
		t.Error(err)
	}
	return b.String()
}
