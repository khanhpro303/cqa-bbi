package handlers

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/vietbui/chat-quality-agent/config"
)

func TestFacebookWebhookVerifyTokenIsStableAndAppScoped(t *testing.T) {
	first := facebookWebhookVerifyToken("123")
	if len(first) != sha256.Size*2 || first != facebookWebhookVerifyToken(" 123 ") {
		t.Fatalf("unexpected stable token %q", first)
	}
	if first == facebookWebhookVerifyToken("456") {
		t.Fatal("verify token must be app scoped")
	}
}

func TestValidFacebookWebhookSignature(t *testing.T) {
	body := []byte(`{"object":"page"}`)
	mac := hmac.New(sha256.New, []byte("app-secret"))
	_, _ = mac.Write(body)
	signature := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	if !validFacebookWebhookSignature(body, signature, "app-secret") {
		t.Fatal("valid signature rejected")
	}
	for _, invalid := range []string{"", "sha1=deadbeef", "sha256=invalid", signature + "00"} {
		if validFacebookWebhookSignature(body, invalid, "app-secret") {
			t.Fatalf("invalid signature accepted: %q", invalid)
		}
	}
	if validFacebookWebhookSignature([]byte(`{"object":"other"}`), signature, "app-secret") {
		t.Fatal("signature must cover exact body")
	}
}

func TestReferralFromFacebookWebhookMessage(t *testing.T) {
	var payload facebookWebhookPayload
	err := json.Unmarshal([]byte(`{
		"object":"page",
		"entry":[{"id":"page-1","messaging":[{
			"sender":{"id":"psid-1"},"recipient":{"id":"page-1"},"timestamp":1700000000123,
			"message":{"mid":"mid-1","referral":{"ref":"campaign-a","source":"ADS","type":"OPEN_THREAD","ad_id":"987","ads_context_data":{"ad_title":"Helmet"}}}
		}]}]
	}`), &payload)
	if err != nil {
		t.Fatal(err)
	}
	event := payload.Entry[0].Messaging[0]
	referral, mid := referralFromEvent(event)
	if !meaningfulFacebookReferral(referral) || mid != "mid-1" || referral.AdID != "987" || referral.Source != "ADS" || referral.Ref != "campaign-a" {
		t.Fatalf("unexpected referral: %+v mid=%q", referral, mid)
	}
}

func TestReferralFromFacebookWebhookSupportsTopLevelAndPostback(t *testing.T) {
	top := &facebookReferral{Ref: "top"}
	post := &facebookReferral{AdID: "42"}
	if got, _ := referralFromEvent(facebookMessagingEvent{Referral: top}); got != top {
		t.Fatal("top-level referral not found")
	}
	if got, _ := referralFromEvent(facebookMessagingEvent{Postback: &struct {
		Referral *facebookReferral `json:"referral"`
	}{Referral: post}}); got != post {
		t.Fatal("postback referral not found")
	}
	if meaningfulFacebookReferral(&facebookReferral{}) {
		t.Fatal("empty referral must not consume the immutable intake slot")
	}
}

func TestFacebookWebhookHTTPVerificationAndSignature(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfg := &config.Config{FacebookAppID: "123", FacebookAppSecret: "app-secret"}
	router := gin.New()
	handler := FacebookWebhookHandler(cfg)
	router.GET("/webhook", handler)
	router.POST("/webhook", handler)

	request := httptest.NewRequest(http.MethodGet, "/webhook?hub.mode=subscribe&hub.verify_token="+facebookWebhookVerifyToken("123")+"&hub.challenge=challenge-42", nil)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Body.String() != "challenge-42" {
		t.Fatalf("verification response=%d body=%q", response.Code, response.Body.String())
	}

	request = httptest.NewRequest(http.MethodGet, "/webhook?hub.mode=subscribe&hub.verify_token=wrong&hub.challenge=challenge-42", nil)
	response = httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("wrong verification token status=%d", response.Code)
	}

	body := `{"object":"page","entry":[]}`
	mac := hmac.New(sha256.New, []byte(cfg.FacebookAppSecret))
	_, _ = mac.Write([]byte(body))
	request = httptest.NewRequest(http.MethodPost, "/webhook", strings.NewReader(body))
	request.Header.Set("X-Hub-Signature-256", "sha256="+hex.EncodeToString(mac.Sum(nil)))
	response = httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Header().Get("X-CQA-Captured-Referrals") != "0" {
		t.Fatalf("signed delivery status=%d headers=%v body=%q", response.Code, response.Header(), response.Body.String())
	}

	request = httptest.NewRequest(http.MethodPost, "/webhook", strings.NewReader(body))
	response = httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("unsigned delivery status=%d", response.Code)
	}
}
