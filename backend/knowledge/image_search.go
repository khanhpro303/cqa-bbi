package knowledge

import (
	"context"
	"slices"
	"strings"

	"github.com/vietbui/chat-quality-agent/db"
)

func captionSearchWords(query string) []string {
	return slices.DeleteFunc(queryWords(query), func(word string) bool {
		switch word {
		case "anh", "chị", "chi", "em", "tôi", "toi", "mình", "minh", "bạn", "ban", "cho", "xem",
			"gửi", "gui", "giúp", "giup", "xin", "nhé", "nhe", "được", "duoc", "nào", "nao", "của", "cua",
			"thể", "the", "với", "voi", "hình", "hinh", "ảnh":
			return true
		}
		return false
	})
}

// Search captions in MySQL so an edit, deletion, or visibility change takes
// effect immediately. Never embed captions into the document's stored chunks.
func searchImageCaptions(ctx context.Context, tenantID string, publicOnly bool, words []string, limit int) ([]Hit, error) {
	if len(words) == 0 || tenantID == "" || db.DB == nil {
		return nil, nil
	}
	var matches, scores []string
	args := make([]any, 0, len(words))
	for _, word := range words {
		matches = append(matches, "i.caption LIKE ?")
		scores = append(scores, "CASE WHEN i.caption LIKE ? THEN 1 ELSE 0 END")
		args = append(args, "%"+escapeLike(word)+"%")
	}
	q := db.DB.WithContext(ctx).Table("knowledge_document_images AS i").
		Select("i.id AS image_id, i.document_id, d.title, i.caption AS content, "+strings.Join(scores, " + ")+" AS match_count", args...).
		Joins("JOIN knowledge_documents d ON d.id = i.document_id").
		Where("i.tenant_id = ? AND d.tenant_id = ? AND i.enabled = ? AND d.enabled = ? AND d.status = ?",
			tenantID, tenantID, true, true, StatusReady).
		Where("("+strings.Join(matches, " OR ")+")", args...)
	if publicOnly {
		q = q.Where("d.audience = ?", AudiencePublic)
	}
	var rows []struct {
		Hit
		MatchCount int
	}
	// A document can have up to 12 images. Read enough metadata to retain
	// one matching caption per document without loading any image bytes.
	err := q.Order("match_count DESC, i.document_id, i.sort_order, i.created_at, i.id").Limit(limit * 12).Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	var hits []Hit
	seen := make(map[string]bool)
	for _, row := range rows {
		if row.MatchCount*2 < len(words) || seen[row.DocumentID] {
			continue
		}
		seen[row.DocumentID] = true
		row.Score = float64(row.MatchCount) / float64(len(words))
		hits = append(hits, row.Hit)
		if len(hits) == limit {
			break
		}
	}
	return hits, nil
}

func mergeCaptionHits(passages, captions []Hit, limit int) []Hit {
	if len(captions) == 0 {
		return passages
	}
	if len(passages) == 0 {
		return captions
	}
	// Scores from vector stores and SQL describe different signals. Reserve
	// at most two slots for captions, keeping the lead passage when the best
	// caption covers only part of the query.
	hits := make([]Hit, 0, limit)
	captionLimit := min(2, max(1, limit-1))
	if captions[0].Score < 1 {
		hits = append(hits, passages[0])
		passages = passages[1:]
		captionLimit = min(captionLimit, limit-1)
	}
	hits = append(hits, captions[:min(len(captions), captionLimit)]...)
	return append(hits, passages[:min(len(passages), limit-len(hits))]...)
}
