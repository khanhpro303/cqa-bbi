package middleware

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"

	"github.com/gin-gonic/gin"
)

// PersonalZaloChatbotSecretResolver requires both the deployment-only gateway
// signature and the channel signature used by personal Zalo imports.
func PersonalZaloChatbotSecretResolver(encryptionKey, gatewaySecret string) InternalSecretResolver {
	channelSecret := PersonalZaloImportSecretResolver(encryptionKey, "")
	return func(c *gin.Context, body []byte) (*InternalAuthContext, error) {
		if len(gatewaySecret) < 32 {
			return nil, errors.New("personal_zalo_chatbot_disabled")
		}
		mac := hmac.New(sha256.New, []byte(gatewaySecret))
		mac.Write([]byte(c.GetHeader(internalTimestampHeader) + "." + c.GetHeader(internalRequestIDHeader) + "."))
		mac.Write(body)
		expected := hex.EncodeToString(mac.Sum(nil))
		if !hmac.Equal([]byte(expected), []byte(c.GetHeader("X-CQA-Chatbot-Signature"))) {
			return nil, errors.New("invalid_personal_zalo_chatbot_signature")
		}
		return channelSecret(c, body)
	}
}
