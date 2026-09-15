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
	go func() {
		captureCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Hour)
		defer cancel()
		if _, _, err := messengerlabels.CaptureMissingIntakeLabels(captureCtx, db.DB, channel, creds.PageID, reader); err != nil {
			log.Printf("[messenger-labels] intake label capture failed for channel %s: %s", channel.ID, messengerlabels.ErrorMessage(messengerlabels.ErrorKind(err)))
		}

		token, err := messengerlabels.ClaimSync(db.DB.WithContext(captureCtx), channel, time.Now())
		if errors.Is(err, messengerlabels.ErrDisabled) || errors.Is(err, messengerlabels.ErrBusy) {
			return
		}
		if err != nil {
			log.Printf("[messenger-labels] cannot start channel %s: %s", channel.ID, messengerlabels.ErrorMessage(messengerlabels.ErrorKind(err)))
			return
		}
		if err := messengerlabels.RunClaimedSync(captureCtx, db.DB, channel, creds.PageID, token, reader); err != nil {
			log.Printf("[messenger-labels] scheduled sync failed for channel %s: %s", channel.ID, messengerlabels.ErrorMessage(messengerlabels.ErrorKind(err)))
		}
	}()
}
