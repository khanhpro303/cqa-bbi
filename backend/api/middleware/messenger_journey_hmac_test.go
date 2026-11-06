package middleware

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func signMessengerRelay(secret, timestamp, requestID, method, path, query, body string) string {
	bodyHash := sha256.Sum256([]byte(body))
	canonical := timestamp + "\n" + requestID + "\n" + method + "\n" + path + "\n" + query + "\n" + hex.EncodeToString(bodyHash[:])
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(canonical))
	return hex.EncodeToString(mac.Sum(nil))
}

func TestMessengerRelayHMACAuthBindsBodyAndTenant(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(MessengerRelayHMACAuth("relay-secret", "tenant-a", time.Minute))
	router.POST("/api/internal/messenger/referral-events", func(c *gin.Context) {
		body, _ := io.ReadAll(c.Request.Body)
		c.JSON(http.StatusOK, gin.H{"tenant_id": c.GetString("tenant_id"), "body": string(body)})
	})
	body := `{"events":[]}`
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	requestID := "53d47d73-3b90-4a3f-a614-3ba7c8670ca1"
	path := "/api/internal/messenger/referral-events"
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set(internalTimestampHeader, timestamp)
	req.Header.Set(internalRequestIDHeader, requestID)
	req.Header.Set(internalSignatureHeader, signMessengerRelay("relay-secret", timestamp, requestID, http.MethodPost, path, "", body))
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"tenant_id":"tenant-a"`) || !strings.Contains(recorder.Body.String(), `events`) {
		t.Fatalf("valid relay request failed: code=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestMessengerRelayHMACAuthRejectsBodySubstitutionAndReplay(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(MessengerRelayHMACAuth("relay-secret", "tenant-a", time.Minute))
	router.POST("/api/internal/messenger/referral-events", func(c *gin.Context) { c.Status(http.StatusOK) })
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	path := "/api/internal/messenger/referral-events"
	signedBody := `{"events":[]}`
	requestID := "f50ae70c-f941-46fa-a829-dbc75b0532f0"
	signature := signMessengerRelay("relay-secret", timestamp, requestID, http.MethodPost, path, "", signedBody)

	bad := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"events":[{}]}`))
	bad.Header.Set(internalTimestampHeader, timestamp)
	bad.Header.Set(internalRequestIDHeader, requestID)
	bad.Header.Set(internalSignatureHeader, signature)
	badRecorder := httptest.NewRecorder()
	router.ServeHTTP(badRecorder, bad)
	if badRecorder.Code != http.StatusUnauthorized {
		t.Fatalf("body substitution code=%d", badRecorder.Code)
	}

	for attempt, want := range []int{http.StatusOK, http.StatusConflict} {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(signedBody))
		req.Header.Set(internalTimestampHeader, timestamp)
		req.Header.Set(internalRequestIDHeader, requestID)
		req.Header.Set(internalSignatureHeader, signature)
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, req)
		if recorder.Code != want {
			t.Fatalf("attempt %d: got %d want %d", attempt, recorder.Code, want)
		}
	}
}

func TestMessengerRelayHMACAuthFailsClosed(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for name, auth := range map[string]gin.HandlerFunc{
		"missing config": MessengerRelayHMACAuth("", "tenant-a", time.Minute),
		"stale request":  MessengerRelayHMACAuth("relay-secret", "tenant-a", time.Second),
	} {
		t.Run(name, func(t *testing.T) {
			router := gin.New()
			router.Use(auth)
			router.POST("/api/internal/messenger/referral-events", func(c *gin.Context) { c.Status(http.StatusOK) })
			body := `{"events":[]}`
			timestamp := strconv.FormatInt(time.Now().Add(-time.Hour).Unix(), 10)
			requestID := "dc7c3f0e-4be7-43b6-86a2-a6089566eb78"
			path := "/api/internal/messenger/referral-events"
			req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
			req.Header.Set(internalTimestampHeader, timestamp)
			req.Header.Set(internalRequestIDHeader, requestID)
			req.Header.Set(internalSignatureHeader, signMessengerRelay("relay-secret", timestamp, requestID, http.MethodPost, path, "", body))
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, req)
			if recorder.Code == http.StatusOK {
				t.Fatal("unsafe request was accepted")
			}
		})
	}
}
