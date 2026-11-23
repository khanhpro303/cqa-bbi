package handlers

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/vietbui/chat-quality-agent/config"
)

func zaloMAC(appID, body, timestamp, secret string) string {
	sum := sha256.Sum256([]byte(appID + body + timestamp + secret))
	return "mac=" + hex.EncodeToString(sum[:])
}

func TestVerifyZaloSignature(t *testing.T) {
	body := `{"app_id":"111","event_name":"user_send_text","timestamp":"1700000000000","sender":{"id":"u1"}}`
	good := zaloMAC("111", body, "1700000000000", "oa-secret")
	if !verifyZaloSignature(good, "111", []byte(body), "1700000000000", "oa-secret") {
		t.Fatal("valid mac= signature rejected")
	}
	if !verifyZaloSignature(strings.TrimPrefix(good, "mac="), "111", []byte(body), "1700000000000", "oa-secret") {
		t.Fatal("valid bare signature rejected")
	}
	forged := strings.Replace(body, `"u1"`, `"victim"`, 1)
	for name, ok := range map[string]bool{
		"tampered body": verifyZaloSignature(good, "111", []byte(forged), "1700000000000", "oa-secret"),
		"wrong secret":  verifyZaloSignature(good, "111", []byte(body), "1700000000000", "other"),
		"no header":     verifyZaloSignature("", "111", []byte(body), "1700000000000", "oa-secret"),
		"no secret":     verifyZaloSignature(good, "111", []byte(body), "1700000000000", ""),
	} {
		if ok {
			t.Errorf("%s: signature accepted", name)
		}
	}
}

func TestZaloWebhookEnforceDropsForgedEvents(t *testing.T) {
	cfg := &config.Config{ZaloOAWebhookSecret: "oa-secret", ZaloWebhookSignature: "enforce"}
	router := gin.New()
	router.POST("/webhooks/zalo", ZaloWebhookHandler(cfg))
	body := `{"app_id":"111","event_name":"user_send_text","timestamp":"1700000000000","sender":{"id":"victim"},"message":{"text":"công nợ"}}`
	send := func(signature string) int {
		req := httptest.NewRequest(http.MethodPost, "/webhooks/zalo", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if signature != "" {
			req.Header.Set("X-ZEvent-Signature", signature)
		}
		res := httptest.NewRecorder()
		router.ServeHTTP(res, req)
		return res.Code
	}
	if code := send(""); code != http.StatusUnauthorized {
		t.Errorf("unsigned event: got %d, want 401", code)
	}
	if code := send(zaloMAC("111", body, "1700000000000", "wrong")); code != http.StatusUnauthorized {
		t.Errorf("badly signed event: got %d, want 401", code)
	}
	if code := send(zaloMAC("111", body, "1700000000000", "oa-secret")); code != http.StatusOK {
		t.Errorf("signed event: got %d, want 200", code)
	}
}

func TestZaloSignatureModeDefaults(t *testing.T) {
	// Signatures are enforced unless an operator explicitly opts out: a missing
	// secret or a typo in the mode must not open the webhook to forged senders.
	for _, tc := range []struct{ secret, mode, want string }{
		{"", "", "enforce"},
		{"s", "", "enforce"},
		{"s", "enforce", "enforce"},
		{"s", "bogus", "enforce"},
		{"", "bogus", "enforce"},
		{"", "enforce", "enforce"},
		{"s", "log", "log"},
		{"", "off", "off"},
		{"s", "off", "off"},
	} {
		if got := zaloSignatureMode(&config.Config{ZaloOAWebhookSecret: tc.secret, ZaloWebhookSignature: tc.mode}); got != tc.want {
			t.Errorf("secret=%q mode=%q: got %s, want %s", tc.secret, tc.mode, got, tc.want)
		}
	}
}

func postZaloWebhook(t *testing.T, cfg *config.Config, signature string) int {
	t.Helper()
	router := gin.New()
	router.POST("/webhooks/zalo", ZaloWebhookHandler(cfg))
	body := `{"app_id":"111","event_name":"user_send_text","timestamp":"1700000000000","sender":{"id":"victim"},"message":{"text":"công nợ"}}`
	req := httptest.NewRequest(http.MethodPost, "/webhooks/zalo", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if signature != "" {
		req.Header.Set("X-ZEvent-Signature", signature)
	}
	res := httptest.NewRecorder()
	router.ServeHTTP(res, req)
	return res.Code
}

func TestZaloWebhookRejectsUnsignedEventsByDefault(t *testing.T) {
	if code := postZaloWebhook(t, &config.Config{ZaloOAWebhookSecret: "oa-secret"}, ""); code != http.StatusUnauthorized {
		t.Errorf("secret set, no mode, unsigned event: got %d, want 401", code)
	}
	if code := postZaloWebhook(t, &config.Config{}, ""); code != http.StatusUnauthorized {
		t.Errorf("no secret, no mode, unsigned event: got %d, want 401", code)
	}
}

func TestZaloWebhookExplicitOffAcceptsUnsignedEvents(t *testing.T) {
	if code := postZaloWebhook(t, &config.Config{ZaloWebhookSignature: "off"}, ""); code != http.StatusOK {
		t.Errorf("explicit off, unsigned event: got %d, want 200", code)
	}
}
