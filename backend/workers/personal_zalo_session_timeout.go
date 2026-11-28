package workers

import (
	"encoding/json"

	"github.com/hibiken/asynq"
)

// TypePersonalZaloSessionTimeout closes an idle personal Zalo session and
// sends the channel's timeout message through the gateway. The handler lives
// in the api/handlers package next to the rest of the personal Zalo chatbot
// and is passed to NewWorkerPool.
const TypePersonalZaloSessionTimeout = "personal_zalo:session_timeout"

// PersonalZaloSessionTimeoutPayload names one personal Zalo session. The
// message text is read from the channel when the task runs.
type PersonalZaloSessionTimeoutPayload struct {
	TenantID          string `json:"tenant_id"`
	ChannelID         string `json:"channel_id"`
	AccountExternalID string `json:"account_external_id"`
	ThreadType        string `json:"thread_type"`
	ThreadID          string `json:"thread_id"`
	SenderID          string `json:"sender_id"`
	SessionKey        string `json:"session_key"`
	SessionID         string `json:"session_id"`
	TimeoutMinutes    int    `json:"timeout_minutes"`
}

func NewPersonalZaloSessionTimeoutTask(payload PersonalZaloSessionTimeoutPayload) (*asynq.Task, error) {
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	// One try: the session is closed before the send, so a retry would find
	// nothing to do anyway.
	return asynq.NewTask(TypePersonalZaloSessionTimeout, payloadBytes, asynq.MaxRetry(0)), nil
}
