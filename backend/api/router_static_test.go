package api

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestFrontendIndexHandlerInjectsAbsoluteOrigin(t *testing.T) {
	gin.SetMode(gin.TestMode)
	indexPath := filepath.Join(t.TempDir(), "index.html")
	indexTemplate := `<meta property="og:url" content="__BBI_APP_ORIGIN__/"><meta property="og:image" content="__BBI_APP_ORIGIN__/assets/preview.png">`
	if err := os.WriteFile(indexPath, []byte(indexTemplate), 0o600); err != nil {
		t.Fatalf("write index fixture: %v", err)
	}

	router := gin.New()
	router.GET("/", frontendIndexHandler(indexPath))

	req := httptest.NewRequest(http.MethodGet, "http://internal/", nil)
	req.Host = "cqa.example.com"
	req.Header.Set("X-Forwarded-Proto", "https")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, req)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	if strings.Contains(response.Body.String(), frontendOriginPlaceholder) {
		t.Fatalf("response still contains origin placeholder: %s", response.Body.String())
	}
	if !strings.Contains(response.Body.String(), "https://cqa.example.com/assets/preview.png") {
		t.Fatalf("response does not contain absolute image URL: %s", response.Body.String())
	}
}

func TestRenderFrontendIndexEscapesOrigin(t *testing.T) {
	got := string(renderFrontendIndex(
		[]byte(`content="__BBI_APP_ORIGIN__/"`),
		`https://example.com"><script>alert(1)</script>`,
	))

	if strings.Contains(got, "<script>") {
		t.Fatalf("origin was not HTML escaped: %s", got)
	}
	if !strings.Contains(got, "&#34;&gt;&lt;script&gt;") {
		t.Fatalf("escaped origin missing from output: %s", got)
	}
}

func TestPrivacyPolicyStaticFileIsPubliclyAccessible(t *testing.T) {
	gin.SetMode(gin.TestMode)
	policyPath := filepath.Join(t.TempDir(), "privacy-policy.html")
	policy := `<!doctype html><html lang="vi"><title>Chính sách quyền riêng tư</title></html>`
	if err := os.WriteFile(policyPath, []byte(policy), 0o600); err != nil {
		t.Fatalf("write privacy policy fixture: %v", err)
	}

	router := gin.New()
	registerPrivacyPolicyRoute(router, policyPath)

	request := httptest.NewRequest(http.MethodGet, "http://cqa.example.com/privacy-policy", nil)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	if contentType := response.Header().Get("Content-Type"); !strings.Contains(contentType, "text/html") {
		t.Fatalf("Content-Type = %q, want text/html", contentType)
	}
	if !strings.Contains(response.Body.String(), "Chính sách quyền riêng tư") {
		t.Fatalf("response does not contain privacy policy title: %s", response.Body.String())
	}
}

func TestSecurityHeadersAllowFacebookLoginSDK(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(securityHeaders())
	router.GET("/", func(c *gin.Context) {
		c.Status(http.StatusNoContent)
	})

	request := httptest.NewRequest(http.MethodGet, "http://cqa.example.com/", nil)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	policy := response.Header().Get("Content-Security-Policy")
	for _, requiredSource := range []string{
		"https://connect.facebook.net",
		"frame-src https://www.facebook.com https://web.facebook.com",
	} {
		if !strings.Contains(policy, requiredSource) {
			t.Errorf("Content-Security-Policy %q does not allow %q", policy, requiredSource)
		}
	}
}
