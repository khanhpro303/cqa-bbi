package workers

import (
	"context"
	"time"

	"github.com/vietbui/chat-quality-agent/channels"
	"github.com/vietbui/chat-quality-agent/engine"
)

// sendKnowledgeImage reports whether it reached the send attempt so a reply
// that contains two different media markers cannot send both pictures.
func sendKnowledgeImage(ctx context.Context, adapter *channels.ZaloOAAdapter, tenantID, agentType, userID, groupID string, imageIDs []string) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	rows, err := engine.LoadDeliverableKnowledgeImages(ctx, tenantID, agentType, imageIDs)
	if err != nil || len(rows) == 0 {
		return false, err
	}
	image, err := channels.PrepareZaloImage(rows[0].Data)
	if err != nil {
		return false, err
	}
	attachment, err := adapter.UploadGroupImage(ctx, image.Data, image.FileName)
	if err != nil {
		return false, err
	}
	current, err := engine.LoadDeliverableKnowledgeReplyImageMetadata(ctx, tenantID, agentType, rows[0].ID)
	if err != nil || len(current) == 0 {
		return false, err
	}
	if groupID != "" {
		return true, adapter.SendGroupImage(ctx, groupID, attachment)
	}
	return true, adapter.SendDirectImage(ctx, userID, attachment)
}
