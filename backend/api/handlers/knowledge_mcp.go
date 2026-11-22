package handlers

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/vietbui/chat-quality-agent/engine"
	"github.com/vietbui/chat-quality-agent/knowledge"
)

const knowledgeMCPToolName = "search_knowledge"

// knowledgeMCPTool is the search_knowledge tool. Its description is the
// tenant's editable task prompt (AI Engine → task prompts), so the wording
// that tells the agent when to search is not hard-coded.
func knowledgeMCPTool(tenantID string) erpMCPTool {
	return erpMCPTool{
		Name:        knowledgeMCPToolName,
		Description: engine.TaskPromptInstructions(tenantID, engine.PromptKnowledgeTool),
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query": gin.H{"type": "string", "description": "Câu hỏi cần tra, viết lại ngắn gọn bằng tiếng Việt."},
				"limit": gin.H{"type": "integer", "description": "Số đoạn tài liệu tối đa (1-10, mặc định 5)."},
			},
			"required":             []string{"query"},
			"additionalProperties": false,
		},
	}
}

type knowledgeMCPPassage struct {
	Source  string  `json:"source"`
	Content string  `json:"content"`
	Score   float64 `json:"score"`
}

// handleKnowledgeTool runs search_knowledge. The customer bot (public agent
// token) only sees documents marked public; the staff bot sees all.
func handleKnowledgeTool(ctx context.Context, tenantID, agentType string, args map[string]json.RawMessage) gin.H {
	var query string
	if raw, ok := args["query"]; !ok || json.Unmarshal(raw, &query) != nil || strings.TrimSpace(query) == "" {
		return erpMCPResult([]byte(`{"error":"invalid_argument","field":"query"}`), true)
	}
	for key := range args {
		if key != "query" && key != "limit" {
			return erpMCPResult([]byte(`{"error":"invalid_argument","field":"`+key+`"}`), true)
		}
	}
	limit := 5
	if raw, ok := args["limit"]; ok {
		if json.Unmarshal(raw, &limit) != nil {
			return erpMCPResult([]byte(`{"error":"invalid_argument","field":"limit"}`), true)
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	hits, err := knowledge.Default().Search(ctx, tenantID, agentType, query, limit)
	if err != nil {
		return erpMCPResult([]byte(`{"error":"knowledge_unavailable","message":"Chưa tra được tài liệu. Vui lòng thử lại sau."}`), true)
	}
	out := struct {
		Query    string                `json:"query"`
		Passages []knowledgeMCPPassage `json:"passages"`
		Note     string                `json:"note,omitempty"`
	}{Query: query, Passages: make([]knowledgeMCPPassage, 0, len(hits))}
	for _, h := range hits {
		out.Passages = append(out.Passages, knowledgeMCPPassage{Source: h.Title, Content: h.Content, Score: roundScore(h.Score)})
	}
	if len(hits) == 0 {
		out.Note = "no_matching_documents"
	}
	body, _ := json.Marshal(out)
	return erpMCPResult(body, false)
}

func roundScore(f float64) float64 {
	return float64(int(f*1000+0.5)) / 1000
}
