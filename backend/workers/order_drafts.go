package workers

import (
	"context"
	"errors"
	"fmt"
	"log"

	"github.com/hibiken/asynq"
	"github.com/vietbui/chat-quality-agent/channels"
	"github.com/vietbui/chat-quality-agent/config"
	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/engine"
	"github.com/vietbui/chat-quality-agent/orderactions"
	"github.com/vietbui/chat-quality-agent/pkg"
)

const (
	orderDraftDeclinedMessage = "Đã huỷ đơn nháp. Chưa có thay đổi nào trên hệ thống."
	orderDraftERPDownMessage  = "Chưa kết nối được hệ thống ERP nên chưa ghi đơn được. Bạn thử lại sau ít phút giúp mình nhé."
)

// handleOrderDraftReply commits or drops the chat's pending order draft when
// the message is a bare confirmation ("1", "xác nhận") or refusal ("2",
// "huỷ"). It runs before the intent classifier, which would otherwise read
// "ok" as small talk. It reports false, and leaves the draft pending, for any
// other message, for a sender who is not the draft's owner, and when a newer
// numbered menu is waiting for the same "1"/"2".
func handleOrderDraftReply(ctx context.Context, cfg *config.Config, adapter *channels.ZaloOAAdapter, tenantID, workKey, senderID, zaloGroupID, text string) (bool, error) {
	reply := orderactions.ParseReply(text)
	if reply == orderactions.ReplyNone {
		return false, nil
	}
	draft, ok := orderactions.Pending(ctx, tenantID, workKey)
	if !ok || draft.ZaloUserID != senderID {
		return false, nil
	}
	if db.RedisClient != nil {
		if n, err := db.RedisClient.Exists(ctx, workKey+engine.PendingOptionsSuffix).Result(); err == nil && n > 0 {
			return false, nil
		}
	}
	send := func(message string) error {
		if err := sendChatReply(ctx, adapter, zaloGroupID, senderID, message); err != nil {
			// The draft is already settled; a retry must not run this turn again.
			return fmt.Errorf("send order draft reply: %w: %w", err, asynq.SkipRetry)
		}
		return nil
	}

	if reply == orderactions.ReplyDecline {
		if !orderactions.Decline(ctx, draft.ID, senderID) {
			return false, nil
		}
		log.Printf("[order_draft] draft %s declined by %s", draft.ID, senderID)
		return true, send(orderDraftDeclinedMessage)
	}

	erpURL, erpDB, erpLogin, erpPassword, err := loadCloudifyCredentials(tenantID, cfg)
	if err != nil || erpURL == "" || erpLogin == "" || erpPassword == "" {
		log.Printf("[order_draft] cannot commit draft %s: ERP credentials unavailable: %v", draft.ID, err)
		return true, send(orderDraftERPDownMessage)
	}
	client := &pkg.CloudifyClient{BaseURL: erpURL, DB: erpDB, Login: erpLogin, Password: erpPassword}
	outcome, err := orderactions.Commit(ctx, draft.ID, senderID, client)
	switch {
	case errors.Is(err, orderactions.ErrNotClaimable):
		log.Printf("[order_draft] draft %s no longer pending when %s confirmed", draft.ID, senderID)
	case err != nil:
		log.Printf("[order_draft] commit draft %s failed before writing: %v", draft.ID, err)
		return true, send(orderDraftERPDownMessage)
	default:
		log.Printf("[order_draft] draft %s (%s) committed by %s: status=%s order=%s", draft.ID, draft.Kind, senderID, outcome.Status, outcome.OrderCode)
	}
	return true, send(outcome.Message)
}
