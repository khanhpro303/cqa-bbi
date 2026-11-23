package handlers

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

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
	var got []workers.ZaloWebhookPayload
	zaloWebhookSink = func(p workers.ZaloWebhookPayload) { got = append(got, p) }
	t.Cleanup(func() { zaloWebhookSink = nil })
	cfg := &config.Config{ZaloOAWebhookSecret: "oa-secret", ZaloWebhookSignature: "enforce"}
	router := gin.New()
	router.POST("/webhooks/zalo", ZaloWebhookHandler(cfg))
	now := strconv.FormatInt(time.Now().UnixMilli(), 10)
	old := strconv.FormatInt(time.Now().Add(-3*time.Hour).UnixMilli(), 10)
	bodyAt := func(ts string) string {
		return `{"app_id":"111","event_name":"user_send_text","timestamp":"` + ts + `","sender":{"id":"victim"},"message":{"text":"công nợ"}}`
	}
	send := func(body, signature string) int {
		req := httptest.NewRequest(http.MethodPost, "/webhooks/zalo", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if signature != "" {
			req.Header.Set("X-ZEvent-Signature", signature)
		}
		res := httptest.NewRecorder()
		router.ServeHTTP(res, req)
		return res.Code
	}
	body := bodyAt(now)
	if code := send(body, ""); code != http.StatusUnauthorized {
		t.Errorf("unsigned event: got %d, want 401", code)
	}
	if code := send(body, zaloMAC("111", body, now, "wrong")); code != http.StatusUnauthorized {
		t.Errorf("badly signed event: got %d, want 401", code)
	}
	if code := send(body, zaloMAC("111", body, now, "oa-secret")); code != http.StatusOK || len(got) != 1 {
		t.Errorf("signed event: got %d (%d queued), want 200 and queued", code, len(got))
	}
	// A correctly signed event replayed after the dedupe window is dropped.
	stale := bodyAt(old)
	if code := send(stale, zaloMAC("111", stale, old, "oa-secret")); code != http.StatusOK || len(got) != 1 {
		t.Errorf("replayed old event: got %d (%d queued), want 200 and not queued", code, len(got))
	}
}

// With no secret configured, every webhook is rejected unless the mode is
// explicitly off.
func TestZaloWebhookFailsClosedWithoutSecret(t *testing.T) {
	zaloWebhookSink = func(workers.ZaloWebhookPayload) {}
	t.Cleanup(func() { zaloWebhookSink = nil })
	body := `{"app_id":"111","event_name":"user_send_text","timestamp":"` + strconv.FormatInt(time.Now().UnixMilli(), 10) + `","sender":{"id":"u1"},"message":{"text":"x"}}`
	for mode, want := range map[string]int{"": http.StatusUnauthorized, "bogus": http.StatusUnauthorized, "off": http.StatusOK} {
		router := gin.New()
		router.POST("/webhooks/zalo", ZaloWebhookHandler(&config.Config{ZaloWebhookSignature: mode}))
		req := httptest.NewRequest(http.MethodPost, "/webhooks/zalo", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		res := httptest.NewRecorder()
		router.ServeHTTP(res, req)
		if res.Code != want {
			t.Errorf("mode %q without secret: got %d, want %d", mode, res.Code, want)
		}
	}
}

func TestStaleZaloEvent(t *testing.T) {
	now := time.Now()
	ms := func(d time.Duration) string { return strconv.FormatInt(now.Add(d).UnixMilli(), 10) }
	for ts, want := range map[string]bool{ms(-time.Minute): false, ms(-114 * time.Minute): false, ms(-116 * time.Minute): true, ms(10 * time.Minute): true, "": true, "abc": true} {
		if got := staleZaloEvent(ts, now); got != want {
			t.Errorf("staleZaloEvent(%q) = %t, want %t", ts, got, want)
		}
	}
}

func TestZaloSignatureModeDefaults(t *testing.T) {
	for _, tc := range []struct{ secret, mode, want string }{
		{"", "", "enforce"},
		{"", "bogus", "enforce"},
		{"", "off", "off"},
		{"s", "", "enforce"},
		{"s", "log", "log"},
		{"", "log", "enforce"},
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
	ts := strconv.FormatInt(time.Now().UnixMilli(), 10)
	body := `{"app_id":"111","event_name":"user_send_text","timestamp":"` + ts + `","sender":{"id":"u1"},"message":{"text":"hàng lỗi"},"cqa_signature_verified":true}`
	for _, sig := range []string{"", zaloMAC("111", body, ts, "oa-secret")} {
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
