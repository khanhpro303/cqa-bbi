package handlers

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/vietbui/chat-quality-agent/config"
	"github.com/vietbui/chat-quality-agent/db/models"
	"github.com/vietbui/chat-quality-agent/engine"
	"github.com/vietbui/chat-quality-agent/workers"
	"gorm.io/gorm"
)

type personalZaloInternbotDelivery struct {
	GroupID string                       `json:"group_id"`
	Text    string                       `json:"text"`
	Token   string                       `json:"token"`
	Images  []personalZaloKnowledgeImage `json:"images,omitempty"`
}

type personalZaloInternbotClaims struct {
	jwt.RegisteredClaims
	GroupID     string `json:"group_id"`
	Version     string `json:"version"`
	IdentityID  string `json:"identity,omitempty"`
	Permissions string `json:"permissions,omitempty"`
	Credential  string `json:"credential,omitempty"`
	Part        string `json:"part"`
	Digest      string `json:"hash"`
	ImageID     string `json:"image_id,omitempty"`
	Product     bool   `json:"product,omitempty"`
}

func personalZaloInternbotSigningKey(key string) []byte {
	mac := hmac.New(sha256.New, []byte(key))
	mac.Write([]byte("cqa-personal-zalo-internbot-delivery-v1"))
	return mac.Sum(nil)
}

func personalZaloInternbotBinding(req personalZaloChatbotRequest) string {
	return personalZaloIdentityDigest(req.ThreadType + "\x00" + personalZaloDeliveryBinding(req))
}

func buildPersonalZaloInternbotDelivery(key string, req personalZaloChatbotRequest, mode personalZaloInternbotConfig,
	identity personalZaloERPIdentity, grant personalZaloDirectGrant, text string, images []personalZaloKnowledgeImage) (personalZaloInternbotDelivery, error) {
	if key == "" {
		return personalZaloInternbotDelivery{}, errors.New("Internbot signing is unavailable")
	}
	delivery := personalZaloInternbotDelivery{GroupID: mode.SupervisorGroupID, Text: text, Images: images}
	claims := personalZaloInternbotClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   personalZaloInternbotBinding(req),
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(15 * time.Minute)),
		},
		GroupID: mode.SupervisorGroupID, Version: mode.Version, IdentityID: identity.ID,
		Part: "text", Digest: personalZaloIdentityDigest(text),
	}
	if grant.turnID != "" {
		claims.Permissions = grant.grantHash
		claims.Credential = personalZaloIdentityDigest(grant.agentToken)
	}
	signingKey := personalZaloInternbotSigningKey(key)
	sign := func(claims personalZaloInternbotClaims) (string, error) {
		return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(signingKey)
	}
	var err error
	delivery.Token, err = sign(claims)
	if err != nil {
		return personalZaloInternbotDelivery{}, err
	}
	for i, image := range images {
		digest := sha256.Sum256(image.Data)
		claims.Part, claims.Digest, claims.ImageID, claims.Product = "image", hex.EncodeToString(digest[:]), image.ID, image.product
		delivery.Images[i].DeliveryToken, err = sign(claims)
		if err != nil {
			return personalZaloInternbotDelivery{}, err
		}
	}
	return delivery, nil
}

func NewPersonalZaloInternbotDeliveryHandler(cfg *config.Config) gin.HandlerFunc {
	keys := append([]string{"chatbot_active", "ai_agent_erp_token_public"}, erpActivationKeys...)
	return func(c *gin.Context) {
		var body struct {
			personalZaloChatbotRequest
			SupervisorGroupID string `json:"supervisor_group_id" binding:"required"`
		}
		if err := c.ShouldBindJSON(&body); err != nil {
			c.Status(http.StatusBadRequest)
			return
		}
		req := body.personalZaloChatbotRequest
		if req.RequestID != c.GetString("internal_request_id") {
			c.Status(http.StatusUnauthorized)
			return
		}
		if cfg.EncryptionKey == "" || req.DeliveryToken == "" || req.DeliveryPart == "" || req.DeliveryDigest == "" ||
			req.DeliveryReceipt != "" || !personalZaloUIDPattern.MatchString(req.AccountExternalID) ||
			!personalZaloUIDPattern.MatchString(body.SupervisorGroupID) || !normalizePersonalZaloChatbotRequest(&req) ||
			(req.ThreadType == personalZaloThreadUser && req.SchemaVersion != personalZaloDirectProtocol) {
			c.Status(http.StatusNoContent)
			return
		}

		var claims personalZaloInternbotClaims
		_, err := jwt.ParseWithClaims(req.DeliveryToken, &claims,
			func(*jwt.Token) (any, error) { return personalZaloInternbotSigningKey(cfg.EncryptionKey), nil },
			jwt.WithValidMethods([]string{"HS256"}), jwt.WithExpirationRequired(), jwt.WithSubject(personalZaloInternbotBinding(req)))
		if err != nil || claims.GroupID != body.SupervisorGroupID || claims.Part != req.DeliveryPart || claims.Digest != req.DeliveryDigest {
			c.Status(http.StatusNoContent)
			return
		}

		ctx := c.Request.Context()
		channel, mode, err := personalZaloInternbotChannel(ctx, req)
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.Status(http.StatusNoContent)
			return
		}
		if err != nil {
			c.Status(http.StatusServiceUnavailable)
			return
		}
		if mode.Version != claims.Version || mode.SupervisorGroupID != claims.GroupID {
			c.Status(http.StatusNoContent)
			return
		}
		eligible, err := personalZaloInternbotEligible(ctx, channel, req, mode)
		if err != nil {
			c.Status(http.StatusServiceUnavailable)
			return
		}
		if !eligible {
			c.Status(http.StatusNoContent)
			return
		}
		settings, err := loadPersonalZaloChatbotSettings(ctx, req.TenantID, keys...)
		if err != nil {
			c.Status(http.StatusServiceUnavailable)
			return
		}
		if !personalZaloChatbotMasterEnabled(settings) {
			c.Status(http.StatusNoContent)
			return
		}
		allowed, err := authorizePersonalZaloInternbotContent(ctx, channel, req, claims, settings)
		switch {
		case err != nil:
			c.Status(http.StatusServiceUnavailable)
		case !allowed:
			c.Status(http.StatusNoContent)
		default:
			c.Status(http.StatusOK)
		}
	}
}

func authorizePersonalZaloInternbotContent(ctx context.Context, channel models.Channel, req personalZaloChatbotRequest,
	claims personalZaloInternbotClaims, settings map[string]models.AppSetting) (bool, error) {
	identity, staff, err := personalZaloInternbotIdentity(ctx, channel, req)
	if err != nil || staff || identity.ID != claims.IdentityID {
		return false, err
	}

	var permissions *engine.GroupPermissionContext
	if claims.Permissions != "" {
		credential := strings.TrimSpace(settings["ai_agent_erp_token_public"].ValuePlain)
		if identity.AgentType != "public" || !erpActiveForRole(settings, identity.AgentType) ||
			credential == "" || personalZaloIdentityDigest(credential) != claims.Credential {
			return false, nil
		}
		current, err := engine.ResolvePersonalZaloERPPermissions(ctx, req.TenantID, req.ChannelID, req.SenderID, identity.AgentType, identity.Customer)
		if err != nil || personalZaloPermissionsHash(current) != claims.Permissions {
			return false, err
		}
		permissions = &current
	}
	if claims.Part == "text" {
		return true, nil
	}
	if claims.ImageID == "" {
		return false, nil
	}
	if claims.Product {
		if permissions == nil {
			return false, nil
		}
		_, err := workers.LoadPersonalZaloProductAdviceImage(ctx, permissions, claims.ImageID)
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return false, nil
		}
		return err == nil, err
	}
	images, err := engine.LoadDeliverableKnowledgeReplyImageMetadata(ctx, req.TenantID, identity.AgentType, claims.ImageID)
	return len(images) > 0, err
}
