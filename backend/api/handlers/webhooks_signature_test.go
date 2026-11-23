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
	"github.com/vietbui/chat-quality-agent/workers"
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
	for _, tc := range []struct{ secret, mode, want string }{
		{"", "", "off"},
		{"s", "", "enforce"},
		{"s", "log", "log"},
		{"s", "enforce", "enforce"},
		{"s", "bogus", "enforce"},
		{"", "enforce", "enforce"},
	} {
		if got := zaloSignatureMode(&config.Config{ZaloOAWebhookSecret: tc.secret, ZaloWebhookSignature: tc.mode}); got != tc.want {
			t.Errorf("secret=%q mode=%q: got %s, want %s", tc.secret, tc.mode, got, tc.want)
		}
	}
}

// Only the handler says whether an event was signed: a body claiming
// cqa_signature_verified is not believed.
func TestZaloWebhookMarksVerifiedEvents(t *testing.T) {
	var got []workers.ZaloWebhookPayload
	zaloWebhookSink = func(p workers.ZaloWebhookPayload) { got = append(got, p) }
	t.Cleanup(func() { zaloWebhookSink = nil })
	cfg := &config.Config{ZaloOAWebhookSecret: "oa-secret", ZaloWebhookSignature: "log"}
	router := gin.New()
	router.POST("/webhooks/zalo", ZaloWebhookHandler(cfg))
	body := `{"app_id":"111","event_name":"user_send_text","timestamp":"1700000000000","sender":{"id":"u1"},"message":{"text":"hàng lỗi"},"cqa_signature_verified":true}`
	for _, sig := range []string{"", zaloMAC("111", body, "1700000000000", "oa-secret")} {
		req := httptest.NewRequest(http.MethodPost, "/webhooks/zalo", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if sig != "" {
			req.Header.Set("X-ZEvent-Signature", sig)
		}
		router.ServeHTTP(httptest.NewRecorder(), req)
	}
	if len(got) != 2 || got[0].SignatureVerified || !got[1].SignatureVerified {
		t.Fatalf("verified flags = %+v", got)
	}
}
