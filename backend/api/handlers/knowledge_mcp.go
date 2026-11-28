package handlers

import (
	"context"
	"encoding/json"
	"log"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/vietbui/chat-quality-agent/db/models"
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

type knowledgeMCPImage struct {
	ID       string `json:"image_id"`
	Document string `json:"document"`
	Caption  string `json:"caption"`
}

// handleKnowledgeTool runs search_knowledge. The customer bot (public agent
// token) only sees documents marked public; the staff bot sees all. tags,
// from the verified permission token of a specialist agent's turn, further
// limit the search to documents carrying one of them.
func handleKnowledgeTool(ctx context.Context, tenantID, agentType string, tags []string, args map[string]json.RawMessage) gin.H {
	var query string
	if raw, ok := args["query"]; !ok || json.Unmarshal(raw, &query) != nil || strings.TrimSpace(query) == "" {
		return erpMCPResult([]byte(`{"error":"invalid_argument","field":"query"}`), true)
	}
	for key := range args {
		if key != "query" && key != "limit" {
			body, _ := json.Marshal(gin.H{"error": "invalid_argument", "field": key})
			return erpMCPResult(body, true)
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
	hits, err := knowledge.Default().SearchTagged(ctx, tenantID, agentType, query, limit, tags)
	if err != nil {
		return erpMCPResult([]byte(`{"error":"knowledge_unavailable","message":"Chưa tra được tài liệu. Vui lòng thử lại sau."}`), true)
	}
	out := struct {
		Query    string                `json:"query"`
		Passages []knowledgeMCPPassage `json:"passages"`
		Images   []knowledgeMCPImage   `json:"images"`
		Note     string                `json:"note,omitempty"`
	}{Query: query, Passages: make([]knowledgeMCPPassage, 0, len(hits)), Images: []knowledgeMCPImage{}}
	titles := make(map[string]string, len(hits))
	for _, h := range hits {
		title := boundedKnowledgeField(h.Title, 255)
		content := h.Content
		if h.ImageID != "" {
			content = boundedKnowledgeField(content, 500)
		}
		titles[h.DocumentID] = title
		out.Passages = append(out.Passages, knowledgeMCPPassage{Source: title, Content: content, Score: roundScore(h.Score)})
	}
	for _, row := range knowledgeImagesForHits(ctx, tenantID, agentType, hits) {
		out.Images = append(out.Images, knowledgeMCPImage{
			ID: row.ID, Document: titles[row.DocumentID], Caption: boundedKnowledgeField(row.Caption, 500),
		})
	}
	if len(hits) == 0 {
		out.Note = "no_matching_documents"
	}
	body, _ := json.Marshal(out)
	return erpMCPResult(body, false)
}

func boundedKnowledgeField(text string, limit int) string {
	runes := []rune(engine.UntrustedText(text))
	return string(runes[:min(len(runes), limit)])
}

func knowledgeImagesForHits(ctx context.Context, tenantID, agentType string, hits []knowledge.Hit) []models.KnowledgeDocumentImage {
	const limit = 12
	var docIDs []string
	seenDoc := make(map[string]bool, len(hits))
	for _, hit := range hits {
		if !seenDoc[hit.DocumentID] {
			seenDoc[hit.DocumentID] = true
			docIDs = append(docIDs, hit.DocumentID)
		}
	}
	rows, err := engine.ListDeliverableKnowledgeImageMetadata(ctx, tenantID, agentType, docIDs)
	if err != nil {
		log.Printf("[knowledge] failed to load image metadata tenant=%s: %v", tenantID, err)
		return nil
	}
	byID := make(map[string]models.KnowledgeDocumentImage, len(rows))
	byDoc := make(map[string][]models.KnowledgeDocumentImage, len(docIDs))
	for _, row := range rows {
		byID[row.ID] = row
		byDoc[row.DocumentID] = append(byDoc[row.DocumentID], row)
	}
	out := make([]models.KnowledgeDocumentImage, 0, min(len(rows), limit))
	appendImage := func(row models.KnowledgeDocumentImage) {
		if _, exists := byID[row.ID]; exists && len(out) < limit {
			out = append(out, row)
			delete(byID, row.ID)
		}
	}
	for _, hit := range hits {
		if row, ok := byID[hit.ImageID]; ok {
			appendImage(row)
		}
	}
	// Give the highest-ranking documents one image each before filling the
	// remaining slots, so one document cannot hide other relevant sources.
	for i := 0; i < limit && len(out) < limit && len(byID) > 0; i++ {
		for _, docID := range docIDs {
			if images := byDoc[docID]; i < len(images) {
				appendImage(images[i])
			}
		}
	}
	return out
}

func roundScore(f float64) float64 {
	return float64(int(f*1000+0.5)) / 1000
}
