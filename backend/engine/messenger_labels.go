package engine

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"time"

	"github.com/vietbui/chat-quality-agent/channels"
	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/db/models"
	"github.com/vietbui/chat-quality-agent/messengerlabels"
)

// Label changes need not add a message. Start a sweep over all local conversations
// after every successful enabled Page sync, even when zero messages were new.
func syncMessengerLabelsAfterMessages(ctx context.Context, channel models.Channel, adapter channels.ChannelAdapter, credentials []byte) {
	reader, ok := adapter.(*channels.FacebookAdapter)
	if !ok {
		return
	}
	var creds channels.FacebookCredentials
	if json.Unmarshal(credentials, &creds) != nil || creds.PageID == "" {
		return
	}
	token, err := messengerlabels.ClaimSync(db.DB.WithContext(ctx), channel, time.Now())
	if errors.Is(err, messengerlabels.ErrDisabled) || errors.Is(err, messengerlabels.ErrBusy) {
		return
	}
	if err != nil {
		log.Printf("[messenger-labels] cannot start channel %s: %s", channel.ID, messengerlabels.ErrorMessage(messengerlabels.ErrorKind(err)))
		return
	}
	go func() {
		if err := messengerlabels.RunClaimedSync(context.Background(), db.DB, channel, creds.PageID, token, reader); err != nil {
			log.Printf("[messenger-labels] scheduled sync failed for channel %s: %s", channel.ID, messengerlabels.ErrorMessage(messengerlabels.ErrorKind(err)))
		}
	}()
}
