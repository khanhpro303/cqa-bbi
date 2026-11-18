package handlers

import (
	"testing"

	"github.com/hibiken/asynq"
	"github.com/vietbui/chat-quality-agent/workers"
)

func zaloPayload(oaID, msgID string) workers.ZaloWebhookPayload {
	var p workers.ZaloWebhookPayload
	p.OAID = oaID
	p.Message.MsgID = msgID
	return p
}

func optionValues(opts []asynq.Option) map[asynq.OptionType]interface{} {
	out := map[asynq.OptionType]interface{}{}
	for _, o := range opts {
		out[o.Type()] = o.Value()
	}
	return out
}

func TestZaloTaskOptionsDedupesByMsgID(t *testing.T) {
	got := optionValues(zaloTaskOptions(zaloPayload("oa1", "m-123")))
	if got[asynq.TaskIDOpt] != "zalo:oa1:m-123" {
		t.Fatalf("TaskID = %v, want zalo:oa1:m-123", got[asynq.TaskIDOpt])
	}
	if got[asynq.RetentionOpt] != zaloWebhookDedupeWindow {
		t.Fatalf("Retention = %v, want %v", got[asynq.RetentionOpt], zaloWebhookDedupeWindow)
	}
	if got[asynq.MaxRetryOpt] != 3 {
		t.Fatalf("MaxRetry = %v, want 3", got[asynq.MaxRetryOpt])
	}
}

func TestZaloTaskOptionsWithoutMsgIDKeepsOldBehaviour(t *testing.T) {
	got := optionValues(zaloTaskOptions(zaloPayload("oa1", "  ")))
	if _, ok := got[asynq.TaskIDOpt]; ok {
		t.Fatalf("unexpected TaskID for empty msg_id: %v", got[asynq.TaskIDOpt])
	}
	if _, ok := got[asynq.RetentionOpt]; ok {
		t.Fatalf("unexpected Retention for empty msg_id")
	}
	if got[asynq.MaxRetryOpt] != 3 {
		t.Fatalf("MaxRetry = %v, want 3", got[asynq.MaxRetryOpt])
	}
}
