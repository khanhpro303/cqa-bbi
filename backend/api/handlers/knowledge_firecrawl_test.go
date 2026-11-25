package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/db/models"
	"github.com/vietbui/chat-quality-agent/pkg"
)

func callKnowledgeFirecrawl(tenant, method, body string, handler gin.HandlerFunc) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Set("tenant_id", tenant)
	c.Request = httptest.NewRequest(method, "/knowledge", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	handler(c)
	return rec
}

func TestCreateKnowledgeURLRejectsInvalidSource(t *testing.T) {
	for _, body := range []string{`{"url":"file:///etc/hosts"}`, `{"url":"http://127.0.0.1/"}`} {
		rec := callKnowledgeFirecrawl("tenant", http.MethodPost, body, CreateKnowledgeURL)
		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "invalid_url") {
			t.Fatalf("invalid source: %d %s", rec.Code, rec.Body)
		}
	}
}

func TestKnowledgeFirecrawlSettingsMySQL(t *testing.T) {
	dsn := os.Getenv("CQA_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("CQA_TEST_MYSQL_DSN not set")
	}
	saved := db.DB
	t.Cleanup(func() { db.DB = saved })
	if err := db.Connect(dsn, true); err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(); err != nil {
		t.Fatal(err)
	}
	t.Setenv("JWT_SECRET", "0123456789abcdef0123456789abcdef")
	t.Setenv("DB_PASSWORD", "test-password")
	t.Setenv("CQA_MESSENGER_JOURNEY_ENABLED", "false")
	t.Setenv("ENCRYPTION_KEY", "0123456789abcdef0123456789abcdef")
	tenant, otherTenant := pkg.NewUUID(), pkg.NewUUID()
	const key = "fc-unit-test-key"
	assertStatus := func(rec *httptest.ResponseRecorder, configured bool) {
		t.Helper()
		var got map[string]bool
		if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &got) != nil ||
			len(got) != 2 || got["has_key"] != configured || got["enabled"] != configured {
			t.Fatalf("settings: %d %s", rec.Code, rec.Body)
		}
	}
	save := func(body string) *httptest.ResponseRecorder {
		return callKnowledgeFirecrawl(tenant, http.MethodPut, body, SaveKnowledgeFirecrawl)
	}
	get := func(tenant string) *httptest.ResponseRecorder {
		return callKnowledgeFirecrawl(tenant, http.MethodGet, "", GetKnowledgeFirecrawl)
	}

	assertStatus(get(tenant), false)
	assertStatus(save(`{"api_key":"`+key+`"}`), true)
	assertStatus(get(otherTenant), false)
	rec := callKnowledgeFirecrawl(otherTenant, http.MethodPost, `{"url":"https://example.com"}`, CreateKnowledgeURL)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "firecrawl_not_configured") {
		t.Fatalf("unconfigured tenant import: %d %s", rec.Code, rec.Body)
	}
	assertStatus(save(`{"api_key":""}`), true)
	var row models.AppSetting
	if err := db.DB.Where("tenant_id = ? AND setting_key = ?", tenant, firecrawlKeySetting).Take(&row).Error; err != nil {
		t.Fatal(err)
	}
	plain, err := pkg.Decrypt(row.ValueEncrypted, os.Getenv("ENCRYPTION_KEY"))
	if err != nil || string(plain) != key || row.ValuePlain != "" || strings.Contains(string(row.ValueEncrypted), key) {
		t.Fatal("Firecrawl API key was not stored only in encrypted form")
	}
	assertStatus(save(`{"api_key":"fc-replacement"}`), true)
	if current, _, err := loadFirecrawlKey(context.Background(), tenant); err != nil || current != "fc-replacement" {
		t.Fatal("rotation did not replace the existing key")
	}
	var count int64
	db.DB.Model(&models.AppSetting{}).Where("tenant_id = ? AND setting_key = ?", tenant, firecrawlKeySetting).Count(&count)
	if count != 1 {
		t.Fatalf("rotation created %d setting rows", count)
	}
	assertStatus(save(`{"clear_key":true}`), false)
	assertStatus(get(tenant), false)
}
