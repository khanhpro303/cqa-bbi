package engine

import (
	"context"
	"math"
	"regexp"
	"strings"

	"github.com/google/uuid"
	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/db/models"
	"gorm.io/gorm"
)

const MaxKnowledgeReplyImages = 1

var (
	knowledgeImageMarker = regexp.MustCompile(`(?i)\[[ \t]*KB_IMAGE\b(?:[ \t\r\n]*:[ \t\r\n]*([0-9a-f-]{36})[ \t\r\n]*\]|[ \t]*:[ \t]*\r?\n[ \t]*[^\s\[\]]*[ \t]*\]|[^\r\n\[\]]*\]?)`)
	productImageSuffix   = regexp.MustCompile(`(?i)^\[PRODUCT_IMAGE:[1-9][0-9]?\]$`)
)

const knowledgeImageMetadataColumns = "knowledge_document_images.id, knowledge_document_images.document_id, knowledge_document_images.file_name, knowledge_document_images.caption, knowledge_document_images.media_type, knowledge_document_images.width, knowledge_document_images.height, knowledge_document_images.enabled, knowledge_document_images.sort_order"

// AddKnowledgeImageInstruction adds the tenant's OA image rules (task prompt
// chatbot_knowledge_images: at most one image). It preserves the flow's
// default system prompt when no tenant prompt is set.
func AddKnowledgeImageInstruction(tenantID, input, prompt string) (string, string) {
	return addKnowledgeImageInstruction(input, prompt, TaskPromptText(tenantID, PromptKnowledgeImages))
}

// AddPersonalZaloKnowledgeImageInstruction adds the personal Zalo image rules
// (task prompt personal_zalo_knowledge_images: every relevant image).
func AddPersonalZaloKnowledgeImageInstruction(tenantID, input, prompt string) (string, string) {
	return addKnowledgeImageInstruction(input, prompt, TaskPromptText(tenantID, PromptPZKnowledgeImages))
}

func addKnowledgeImageInstruction(input, prompt, instruction string) (string, string) {
	if strings.TrimSpace(prompt) == "" {
		return input + "\n\n" + instruction, prompt
	}
	return input, prompt + "\n\n" + instruction
}

// ParsePersonalZaloKnowledgeImageReply reads every distinct trailing image marker.
func ParsePersonalZaloKnowledgeImageReply(reply string) (string, []string) {
	return ParseKnowledgeImageReply(reply, math.MaxInt)
}

// ParseKnowledgeImageReply strips marker text everywhere. Delivery is limited
// to UUID markers on their own lines in the trailing media block.
func ParseKnowledgeImageReply(reply string, maxImages int) (string, []string) {
	matches := knowledgeImageMarker.FindAllStringSubmatchIndex(reply, -1)
	if len(matches) == 0 {
		return reply, nil
	}
	clean := strings.TrimSpace(knowledgeImageMarker.ReplaceAllString(reply, ""))
	if clean == "" || productImageSuffix.MatchString(clean) || maxImages <= 0 {
		return clean, nil
	}
	var ids []string
	suffixStart := len(reply)
	for i := len(matches) - 1; i >= 0; i-- {
		match := matches[i]
		gap := strings.TrimSpace(reply[match[1]:suffixStart])
		if gap != "" && !productImageSuffix.MatchString(gap) {
			break
		}
		lineStart := strings.LastIndexByte(reply[:match[0]], '\n') + 1
		if strings.TrimSpace(reply[lineStart:match[0]]) != "" {
			break
		}
		suffixStart = match[0]
		if match[2] < 0 {
			continue
		}
		id, err := uuid.Parse(reply[match[2]:match[3]])
		if err == nil && id != uuid.Nil {
			ids = append(ids, id.String())
		}
	}
	var chosen []string
	seen := make(map[string]bool)
	for i := len(ids) - 1; i >= 0 && len(chosen) < maxImages; i-- {
		if !seen[ids[i]] {
			chosen = append(chosen, ids[i])
			seen[ids[i]] = true
		}
	}
	return clean, chosen
}

func deliverableKnowledgeImages(ctx context.Context, tenantID, agentType string) *gorm.DB {
	q := db.DB.WithContext(ctx).Model(&models.KnowledgeDocumentImage{}).
		Joins("JOIN knowledge_documents AS d ON d.id = knowledge_document_images.document_id AND d.tenant_id = knowledge_document_images.tenant_id").
		Where("knowledge_document_images.tenant_id = ? AND knowledge_document_images.enabled = ? AND d.enabled = ? AND d.status = ?",
			tenantID, true, true, "ready")
	if agentType != "private" {
		q = q.Where("d.audience = ?", "public")
	}
	return q
}

// ListDeliverableKnowledgeImageMetadata fetches only image references and
// metadata for documents found by the search.
func ListDeliverableKnowledgeImageMetadata(ctx context.Context, tenantID, agentType string, documentIDs []string) ([]models.KnowledgeDocumentImage, error) {
	if len(documentIDs) == 0 || tenantID == "" || db.DB == nil {
		return nil, nil
	}
	var rows []models.KnowledgeDocumentImage
	err := deliverableKnowledgeImages(ctx, tenantID, agentType).
		Select(knowledgeImageMetadataColumns).
		Where("knowledge_document_images.document_id IN ?", documentIDs).
		Order("knowledge_document_images.sort_order, knowledge_document_images.created_at, knowledge_document_images.id").
		Find(&rows).Error
	if err != nil {
		return nil, err
	}
	return rows, nil
}

// LoadDeliverableKnowledgeReplyImageMetadata checks one image's current
// visibility without reading the binary payload.
func LoadDeliverableKnowledgeReplyImageMetadata(ctx context.Context, tenantID, agentType, imageID string) ([]models.KnowledgeDocumentImage, error) {
	if imageID == "" || tenantID == "" || db.DB == nil {
		return nil, nil
	}
	var rows []models.KnowledgeDocumentImage
	err := deliverableKnowledgeImages(ctx, tenantID, agentType).
		Select(knowledgeImageMetadataColumns).
		Where("knowledge_document_images.id = ?", imageID).
		Find(&rows).Error
	if err != nil {
		return nil, err
	}
	return rows, nil
}

// LoadDeliverableKnowledgeImages rechecks visibility for every selected ID
// immediately before delivery or chat preview.
func LoadDeliverableKnowledgeImages(ctx context.Context, tenantID, agentType string, imageIDs []string) ([]models.KnowledgeDocumentImage, error) {
	return LoadPersonalZaloKnowledgeImages(ctx, tenantID, agentType, imageIDs[:min(len(imageIDs), MaxKnowledgeReplyImages)])
}

// LoadPersonalZaloKnowledgeImages applies the same audience checks to each
// selected image. The caller delivers the images in the model's selected order.
func LoadPersonalZaloKnowledgeImages(ctx context.Context, tenantID, agentType string, imageIDs []string) ([]models.KnowledgeDocumentImage, error) {
	if len(imageIDs) == 0 || tenantID == "" || db.DB == nil {
		return nil, nil
	}
	var rows []models.KnowledgeDocumentImage
	err := deliverableKnowledgeImages(ctx, tenantID, agentType).
		Select("knowledge_document_images.*").
		Where("knowledge_document_images.id IN ?", imageIDs).
		Find(&rows).Error
	if err != nil {
		return nil, err
	}
	return rows, nil
}
