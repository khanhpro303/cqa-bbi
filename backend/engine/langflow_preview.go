package engine

import (
	"net/http"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/vietbui/chat-quality-agent/config"
)

const KnowledgePreviewTokenPrefix = "kb-preview:"

// SignKnowledgePreviewToken issues a short-lived MCP credential for knowledge search only.
func SignKnowledgePreviewToken(key, tenantID, agentType string) (string, error) {
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.RegisteredClaims{
		Issuer:    "cqa-knowledge-preview",
		Subject:   agentType,
		Audience:  jwt.ClaimStrings{tenantID},
		ID:        uuid.NewString(),
		ExpiresAt: jwt.NewNumericDate(time.Now().Add(2 * time.Minute)),
	}).SignedString([]byte(key))
	if err != nil {
		return "", err
	}
	return KnowledgePreviewTokenPrefix + token, nil
}

// NewLangflowPreviewClient keeps preview questions and credentials on the
// configured endpoint even when its response redirects.
func NewLangflowPreviewClient(cfg *config.Config) *LangflowClient {
	client := NewLangflowClient(cfg)
	client.client.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	return client
}
