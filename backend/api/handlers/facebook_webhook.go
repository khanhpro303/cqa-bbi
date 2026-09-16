package handlers

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/vietbui/chat-quality-agent/config"
	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/db/models"
	"github.com/vietbui/chat-quality-agent/messengerintake"
)

const facebookWebhookBodyLimit = 1 << 20

type facebookWebhookPayload struct {
	Object string                 `json:"object"`
	Entry  []facebookWebhookEntry `json:"entry"`
}

type facebookWebhookEntry struct {
	ID        string                   `json:"id"`
	Messaging []facebookMessagingEvent `json:"messaging"`
}

type facebookMessagingEvent struct {
	Sender    facebookWebhookParty `json:"sender"`
	Recipient facebookWebhookParty `json:"recipient"`
	Timestamp int64                `json:"timestamp"`
	Message   *struct {
		MID      string            `json:"mid"`
		Referral *facebookReferral `json:"referral"`
	} `json:"message"`
	Postback *struct {
		Referral *facebookReferral `json:"referral"`
	} `json:"postback"`
	Referral *facebookReferral `json:"referral"`
}

type facebookWebhookParty struct {
	ID string `json:"id"`
}

type facebookReferral struct {
	Ref            string          `json:"ref"`
	Source         string          `json:"source"`
	Type           string          `json:"type"`
	AdID           string          `json:"ad_id"`
	AdsContextData json.RawMessage `json:"ads_context_data"`
}

func facebookWebhookVerifyToken(appID string) string {
	sum := sha256.Sum256([]byte("cqa-facebook-webhook:" + strings.TrimSpace(appID)))
	return hex.EncodeToString(sum[:])
}

func validFacebookWebhookSignature(body []byte, signature, appSecret string) bool {
	if !strings.HasPrefix(signature, "sha256=") || appSecret == "" {
		return false
	}
	provided, err := hex.DecodeString(strings.TrimPrefix(signature, "sha256="))
	if err != nil || len(provided) != sha256.Size {
		return false
	}
	mac := hmac.New(sha256.New, []byte(appSecret))
	_, _ = mac.Write(body)
	return subtle.ConstantTimeCompare(provided, mac.Sum(nil)) == 1
}

func referralFromEvent(event facebookMessagingEvent) (*facebookReferral, string) {
	if event.Message != nil && event.Message.Referral != nil {
		return event.Message.Referral, event.Message.MID
	}
	if event.Referral != nil {
		return event.Referral, ""
	}
	if event.Postback != nil && event.Postback.Referral != nil {
		return event.Postback.Referral, ""
	}
	return nil, ""
}

func meaningfulFacebookReferral(referral *facebookReferral) bool {
	return referral != nil && (referral.AdID != "" || referral.Ref != "" || referral.Source != "" || referral.Type != "" || len(referral.AdsContextData) > 0)
}

// FacebookWebhookHandler verifies Meta callbacks and immutably stores the
// first advertising/referral attribution. These records are never tracking labels.
func FacebookWebhookHandler(cfg *config.Config) gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.Method == http.MethodGet {
			if strings.TrimSpace(cfg.FacebookAppID) == "" || c.Query("hub.mode") != "subscribe" || subtle.ConstantTimeCompare(
				[]byte(c.Query("hub.verify_token")), []byte(facebookWebhookVerifyToken(cfg.FacebookAppID)),
			) != 1 {
				c.Status(http.StatusForbidden)
				return
			}
			c.Data(http.StatusOK, "text/plain; charset=utf-8", []byte(c.Query("hub.challenge")))
			return
		}

		body, err := io.ReadAll(io.LimitReader(c.Request.Body, facebookWebhookBodyLimit+1))
		if err != nil || len(body) > facebookWebhookBodyLimit {
			c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "invalid_payload"})
			return
		}
		if !validFacebookWebhookSignature(body, c.GetHeader("X-Hub-Signature-256"), cfg.FacebookAppSecret) {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid_signature"})
			return
		}

		var payload facebookWebhookPayload
		if json.Unmarshal(body, &payload) != nil || payload.Object != "page" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_payload"})
			return
		}

		captured := 0
		for _, entry := range payload.Entry {
			for _, event := range entry.Messaging {
				referral, messageID := referralFromEvent(event)
				if !meaningfulFacebookReferral(referral) || event.Sender.ID == "" || event.Recipient.ID == "" {
					continue
				}
				pageID := event.Recipient.ID
				if entry.ID != "" && entry.ID != pageID {
					continue
				}
				if db.DB == nil {
					log.Printf("[facebook-webhook] database unavailable")
					continue
				}
				var channel models.Channel
				if err := db.DB.Where("channel_type = ? AND external_id = ? AND is_active = ?", "facebook", pageID, true).First(&channel).Error; err != nil {
					log.Printf("[facebook-webhook] no active channel for page %s", pageID)
					continue
				}
				rawReferral, err := json.Marshal(referral)
				if err != nil {
					continue
				}
				capturedAt := time.Now()
				if event.Timestamp > 0 {
					capturedAt = time.UnixMilli(event.Timestamp)
				}
				if err := messengerintake.CaptureReferral(db.DB.WithContext(c.Request.Context()), channel, messengerintake.Referral{
					PageID: pageID, PSID: event.Sender.ID, AdID: referral.AdID, Ref: referral.Ref,
					Source: referral.Source, ReferralType: referral.Type, MessageID: messageID,
					RawJSON: string(rawReferral), CapturedAt: capturedAt,
				}); err != nil {
					log.Printf("[facebook-webhook] store failed page=%s psid=%s: %v", pageID, event.Sender.ID, err)
					continue
				}
				captured++
			}
		}
		c.Header("X-CQA-Captured-Referrals", strconv.Itoa(captured))
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	}
}
