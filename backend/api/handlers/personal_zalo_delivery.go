package handlers

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/vietbui/chat-quality-agent/db/models"
	"github.com/vietbui/chat-quality-agent/engine"
)

const personalZaloDirectProtocol = "personal-zalo-chatbot-v3"

// A persisted reply must still belong to the same turn, recipient and grant
// when its text or image reaches the head of the gateway's rate limiter.
type personalZaloDeliveryClaims struct {
	jwt.RegisteredClaims
	Role        string `json:"role,omitempty"`
	IdentityID  string `json:"identity,omitempty"`
	Permissions string `json:"permissions,omitempty"`
	Credential  string `json:"credential,omitempty"`
	TextDigest  string `json:"text_hash"`
	ImageID     string `json:"image_id,omitempty"`
	ImageDigest string `json:"image_hash,omitempty"`
}

func personalZaloDeliveryKey(key string) []byte {
	mac := hmac.New(sha256.New, []byte(key))
	mac.Write([]byte("cqa-personal-zalo-delivery-v3"))
	return mac.Sum(nil)
}

func personalZaloDeliveryBinding(req personalZaloChatbotRequest) string {
	return personalZaloIdentityDigest(strings.Join([]string{req.TenantID, req.ChannelID, req.AccountExternalID, req.ThreadID, req.SenderID, req.MessageID, req.SentAt.UTC().Format(time.RFC3339Nano)}, "\x00"))
}

func writePersonalZaloDirectReply(c *gin.Context, key string, req personalZaloChatbotRequest, response gin.H, identity personalZaloERPIdentity, grant personalZaloDirectGrant) {
	if key == "" {
		c.Status(http.StatusServiceUnavailable)
		return
	}
	claims := personalZaloDeliveryClaims{
		RegisteredClaims: jwt.RegisteredClaims{Subject: personalZaloDeliveryBinding(req), ExpiresAt: jwt.NewNumericDate(time.Now().Add(15 * time.Minute))},
		Role:             identity.AgentType, IdentityID: identity.ID, Permissions: grant.grantHash,
		TextDigest: personalZaloIdentityDigest(response["reply"].(string)),
	}
	if grant.turnID != "" {
		claims.Credential = personalZaloIdentityDigest(grant.agentToken)
	}
	if images, ok := response["images"].([]personalZaloKnowledgeImage); ok && len(images) == 1 {
		imageHash := sha256.Sum256(images[0].Data)
		claims.ImageID, claims.ImageDigest = images[0].ID, hex.EncodeToString(imageHash[:])
	}
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(personalZaloDeliveryKey(key))
	if err != nil {
		c.Status(http.StatusServiceUnavailable)
		return
	}
	response["delivery_token"] = token
	c.JSON(http.StatusOK, response)
}

func checkPersonalZaloDelivery(c *gin.Context, ctx context.Context, key string, channel models.Channel, req personalZaloChatbotRequest, settings map[string]models.AppSetting) {
	var claims personalZaloDeliveryClaims
	if key == "" || !personalZaloIdentityAccountMatches(channel, req) {
		c.Status(http.StatusNoContent)
		return
	}
	_, err := jwt.ParseWithClaims(req.DeliveryToken, &claims, func(*jwt.Token) (any, error) { return personalZaloDeliveryKey(key), nil },
		jwt.WithValidMethods([]string{"HS256"}), jwt.WithExpirationRequired(), jwt.WithSubject(personalZaloDeliveryBinding(req)))
	if err != nil || (req.DeliveryPart == "text" && req.DeliveryDigest != claims.TextDigest) ||
		(req.DeliveryPart == "image" && (claims.ImageID == "" || req.DeliveryDigest != claims.ImageDigest)) {
		c.Status(http.StatusNoContent)
		return
	}
	if claims.Role != "" {
		identity, err := resolvePersonalZaloERPIdentity(ctx, channel, req)
		if err != nil {
			c.Status(http.StatusServiceUnavailable)
			return
		}
		if identity.AgentType != claims.Role || identity.ID != claims.IdentityID {
			c.Status(http.StatusNoContent)
			return
		}
		if claims.Permissions != "" {
			if !erpActiveForRole(settings, identity.AgentType) {
				c.Status(http.StatusNoContent)
				return
			}
			current, err := engine.ResolvePersonalZaloERPPermissions(ctx, req.TenantID, req.ChannelID, req.SenderID, identity.AgentType, identity.Customer)
			if err != nil {
				c.Status(http.StatusServiceUnavailable)
				return
			}
			token := strings.TrimSpace(settings["ai_agent_erp_token_"+identity.AgentType].ValuePlain)
			if token == "" && identity.AgentType == "private" {
				token = strings.TrimSpace(settings["ai_agent_erp_token"].ValuePlain)
			}
			if claims.Permissions != personalZaloPermissionsHash(current) || (claims.Credential != "" && claims.Credential != personalZaloIdentityDigest(token)) {
				c.Status(http.StatusNoContent)
				return
			}
		}
	}
	if req.DeliveryPart == "image" {
		images, err := engine.LoadDeliverableKnowledgeReplyImageMetadata(ctx, req.TenantID, claims.Role, claims.ImageID)
		if err != nil {
			c.Status(http.StatusServiceUnavailable)
			return
		}
		if len(images) == 0 {
			c.Status(http.StatusNoContent)
			return
		}
	}
	c.JSON(http.StatusOK, gin.H{"allowed": true})
}
