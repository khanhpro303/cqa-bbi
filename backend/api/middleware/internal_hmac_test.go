package middleware

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func TestInternalHMACAuthAcceptsValidRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(InternalHMACAuth(StaticInternalSecretResolver("top-secret"), time.Minute))
	router.POST("/internal", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	body := `{"hello":"world"}`
	requestID := "req-1"
	timestamp := time.Now().UTC().Unix()
	req := httptest.NewRequest(http.MethodPost, "/internal", strings.NewReader(body))
	req.Header.Set(internalRequestIDHeader, requestID)
	req.Header.Set(internalTimestampHeader, strconv.FormatInt(timestamp, 10))
	req.Header.Set(internalSignatureHeader, signInternalRequest("top-secret", strconv.FormatInt(timestamp, 10), requestID, body))

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestInternalHMACAuthRejectsReplay(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(InternalHMACAuth(StaticInternalSecretResolver("top-secret"), time.Minute))
	router.POST("/internal", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	body := `{"hello":"world"}`
	requestID := "req-replay"
	timestamp := strconv.FormatInt(time.Now().UTC().Unix(), 10)
	signature := signInternalRequest("top-secret", timestamp, requestID, body)

	firstReq := httptest.NewRequest(http.MethodPost, "/internal", strings.NewReader(body))
	firstReq.Header.Set(internalRequestIDHeader, requestID)
	firstReq.Header.Set(internalTimestampHeader, timestamp)
	firstReq.Header.Set(internalSignatureHeader, signature)

	firstRecorder := httptest.NewRecorder()
	router.ServeHTTP(firstRecorder, firstReq)
	if firstRecorder.Code != http.StatusOK {
		t.Fatalf("expected first request to succeed, got %d", firstRecorder.Code)
	}

	secondReq := httptest.NewRequest(http.MethodPost, "/internal", strings.NewReader(body))
	secondReq.Header.Set(internalRequestIDHeader, requestID)
	secondReq.Header.Set(internalTimestampHeader, timestamp)
	secondReq.Header.Set(internalSignatureHeader, signature)

	secondRecorder := httptest.NewRecorder()
	router.ServeHTTP(secondRecorder, secondReq)
	if secondRecorder.Code != http.StatusConflict {
		t.Fatalf("expected replay to be rejected with 409, got %d body=%s", secondRecorder.Code, secondRecorder.Body.String())
	}
}

func TestInternalHMACAuthRejectsInvalidSignature(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(InternalHMACAuth(StaticInternalSecretResolver("top-secret"), time.Minute))
	router.POST("/internal", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	req := httptest.NewRequest(http.MethodPost, "/internal", strings.NewReader(`{"hello":"world"}`))
	req.Header.Set(internalRequestIDHeader, "req-invalid")
	req.Header.Set(internalTimestampHeader, strconv.FormatInt(time.Now().UTC().Unix(), 10))
	req.Header.Set(internalSignatureHeader, "bad-signature")

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d body=%s", recorder.Code, recorder.Body.String())
	}
}

func signInternalRequest(secret, timestamp, requestID, body string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(timestamp))
	mac.Write([]byte("."))
	mac.Write([]byte(requestID))
	mac.Write([]byte("."))
	mac.Write([]byte(body))
	return hex.EncodeToString(mac.Sum(nil))
}

func TestABZExportHMACAuthBindsMethodPathQueryAndTenant(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(ABZExportHMACAuth("abz-secret", "tenant-a", time.Minute))
	router.GET("/api/internal/abz/order-events", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"tenant_id": c.GetString("tenant_id")})
	})
	timestamp := strconv.FormatInt(time.Now().UTC().Unix(), 10)
	requestID := "abz-req-1"
	path := "/api/internal/abz/order-events"
	query := "after=cursor-a&limit=25"
	canonical := timestamp + "\n" + requestID + "\nGET\n" + path + "\n" + query
	mac := hmac.New(sha256.New, []byte("abz-secret"))
	mac.Write([]byte(canonical))
	signature := hex.EncodeToString(mac.Sum(nil))

	req := httptest.NewRequest(http.MethodGet, path+"?"+query, nil)
	req.Header.Set(internalRequestIDHeader, requestID)
	req.Header.Set(internalTimestampHeader, timestamp)
	req.Header.Set(internalSignatureHeader, signature)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"tenant_id":"tenant-a"`) {
		t.Fatalf("valid export signature failed: code=%d body=%s", recorder.Code, recorder.Body.String())
	}

	tampered := httptest.NewRequest(http.MethodGet, path+"?after=cursor-b&limit=25", nil)
	tampered.Header.Set(internalRequestIDHeader, "abz-req-2")
	tampered.Header.Set(internalTimestampHeader, timestamp)
	tampered.Header.Set(internalSignatureHeader, signature)
	tamperedRecorder := httptest.NewRecorder()
	router.ServeHTTP(tamperedRecorder, tampered)
	if tamperedRecorder.Code != http.StatusUnauthorized {
		t.Fatalf("query tampering must invalidate signature: %d", tamperedRecorder.Code)
	}
}
