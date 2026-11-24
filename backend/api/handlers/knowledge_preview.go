package handlers

import (
	"context"
	"crypto/sha256"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/vietbui/chat-quality-agent/api/middleware"
	"github.com/vietbui/chat-quality-agent/config"
	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/db/models"
	"github.com/vietbui/chat-quality-agent/engine"
)

const knowledgePreviewTokenPrefix = "kb-preview:"

func NewKnowledgeChatPreview(cfg *config.Config) gin.HandlerFunc {
	client := engine.NewLangflowPreviewClient(cfg)
	return func(c *gin.Context) {
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 32<<10)
		var req struct {
			Question  string `json:"question"`
			AgentType string `json:"agent_type"`
			SessionID string `json:"session_id"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_preview_request"})
			return
		}
		question := strings.TrimSpace(req.Question)
		if question == "" || utf8.RuneCountInString(question) > 2000 || !validKnowledgePreviewAgent(req.AgentType) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_preview_request"})
			return
		}

		tenantID, userID := middleware.GetTenantID(c), middleware.GetUserID(c)
		role := middleware.GetTenantRole(c)
		if tenantID == "" || userID == "" || (req.AgentType == "private" && role != "owner" && role != "admin") {
			c.JSON(http.StatusForbidden, gin.H{"error": "preview_forbidden"})
			return
		}
		session, err := knowledgePreviewSession(tenantID, userID, req.AgentType, req.SessionID)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_preview_session"})
			return
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), 60*time.Second)
		defer cancel()

		promptKey := "ai_engine_system_prompt"
		if req.AgentType == "private" {
			promptKey = "ai_engine_system_prompt_internal"
		}
		keys := append(append([]string{}, engine.LangflowSettingKeys...), promptKey)
		var rows []models.AppSetting
		if db.DB == nil || db.DB.WithContext(ctx).Where("tenant_id = ? AND setting_key IN ?", tenantID, keys).Find(&rows).Error != nil {
			log.Printf("[knowledge-preview] tenant=%s failed to load chatbot settings", tenantID)
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "preview_unavailable"})
			return
		}
		settings := make(map[string]models.AppSetting, len(rows))
		for _, row := range rows {
			settings[row.SettingKey] = row
		}
		lf := engine.ResolveTenantLangflowConfig(settings, cfg)
		flowID, flowSource := lf.PublicFlowID, lf.PublicFlowIDSource
		if req.AgentType == "private" {
			flowID, flowSource = lf.FlowID, lf.FlowIDSource
		}
		// The environment credentials and flow IDs may belong to another
		// tenant. In particular, never mix a saved flow ID with the global key.
		if lf.APIURL == "" || flowSource != engine.LangflowSourceTenant ||
			lf.APIKeySource != engine.LangflowSourceTenant || cfg.EncryptionKey == "" {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "preview_flow_not_configured"})
			return
		}

		token, err := signKnowledgePreviewToken(cfg.EncryptionKey, tenantID, req.AgentType)
		if err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "preview_unavailable"})
			return
		}
		// The preview identity also scopes the flow's vector history to this
		// conversation. The MCP credential grants only search_knowledge.
		reply, err := client.RunFlowWithCustomerUsage(ctx, session.flowSessionID, session.flowSessionID, question,
			lf.APIURL, lf.APIKey, flowID, "", "", token, settings[promptKey].ValuePlain)
		if err != nil {
			log.Printf("[knowledge-preview] tenant=%s agent=%s Langflow request failed (%T)", tenantID, req.AgentType, err)
			c.JSON(http.StatusBadGateway, gin.H{"error": "preview_unavailable"})
			return
		}
		answer := strings.TrimSpace(reply.Text)
		if answer == "" || strings.Contains(answer, "[RICH_MESSAGE_SENT]") {
			c.JSON(http.StatusBadGateway, gin.H{"error": "preview_empty_reply"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"answer": answer, "session_id": session.id})
	}
}

type previewSession struct {
	id            string
	flowSessionID string
}

func knowledgePreviewSession(tenantID, userID, agentType, rawID string) (previewSession, error) {
	id := uuid.New()
	if rawID != "" {
		parsed, err := uuid.Parse(rawID)
		if err != nil || parsed.String() != rawID || parsed.Version() != 4 {
			return previewSession{}, fmt.Errorf("invalid preview session ID")
		}
		id = parsed
	}
	digest := sha256.Sum256([]byte(tenantID + "\x00" + userID + "\x00" + agentType + "\x00" + id.String()))
	return previewSession{id: id.String(), flowSessionID: "kb-preview-" + fmt.Sprintf("%x", digest)}, nil
}

func validKnowledgePreviewAgent(agentType string) bool {
	return agentType == "public" || agentType == "private"
}

func signKnowledgePreviewToken(key, tenantID, agentType string) (string, error) {
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
	return knowledgePreviewTokenPrefix + token, nil
}

func resolveKnowledgePreviewAgent(key, tenantID, token string) string {
	signed, ok := strings.CutPrefix(token, knowledgePreviewTokenPrefix)
	if !ok || key == "" || tenantID == "" {
		return ""
	}
	claims := &jwt.RegisteredClaims{}
	_, err := jwt.ParseWithClaims(signed, claims, func(*jwt.Token) (any, error) {
		return []byte(key), nil
	}, jwt.WithValidMethods([]string{"HS256"}), jwt.WithIssuer("cqa-knowledge-preview"),
		jwt.WithAudience(tenantID), jwt.WithExpirationRequired())
	if err != nil || !validKnowledgePreviewAgent(claims.Subject) {
		return ""
	}
	return claims.Subject
}
