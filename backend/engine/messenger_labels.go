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
	"github.com/vietbui/chat-quality-agent/pkg"
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
		runMessengerLabelSync(context.WithoutCancel(ctx), channel, creds.PageID, reader)
	}()
}

func runMessengerLabelSync(ctx context.Context, channel models.Channel, pageID string, reader messengerlabels.Reader) {
	captureCtx, cancel := context.WithTimeout(ctx, 2*time.Hour)
	defer cancel()
	token, err := messengerlabels.ClaimSync(db.DB.WithContext(captureCtx), channel, time.Now())
	if errors.Is(err, messengerlabels.ErrDisabled) {
		// Preserve the pre-existing baseline behavior for Pages that have not
		// enabled tracking. Enabled Pages claim the shared lease before any Meta
		// read, so scheduled/manual sweeps cannot duplicate external requests.
		if _, _, captureErr := messengerlabels.CaptureMissingIntakeLabels(captureCtx, db.DB, channel, pageID, reader); captureErr != nil {
			log.Printf("[messenger-labels] intake label capture failed for channel %s: %s", channel.ID, messengerlabels.ErrorMessage(messengerlabels.ErrorKind(captureErr)))
		}
		return
	}
	if errors.Is(err, messengerlabels.ErrBusy) {
		return
	}
	if err != nil {
		log.Printf("[messenger-labels] cannot start channel %s: %s", channel.ID, messengerlabels.ErrorMessage(messengerlabels.ErrorKind(err)))
		return
	}
	if _, _, err := messengerlabels.CaptureMissingIntakeLabels(captureCtx, db.DB, channel, pageID, reader); err != nil {
		log.Printf("[messenger-labels] intake label capture failed for channel %s: %s", channel.ID, messengerlabels.ErrorMessage(messengerlabels.ErrorKind(err)))
	}
	if err := messengerlabels.RunClaimedSync(captureCtx, db.DB, channel, pageID, token, reader); err != nil {
		log.Printf("[messenger-labels] scheduled sync failed for channel %s: %s", channel.ID, messengerlabels.ErrorMessage(messengerlabels.ErrorKind(err)))
	}
}

// syncMessengerLabelsTask keeps label observations independent from message
// ingestion. It claims at most two least-recently-finished Pages per tick; the
// database lease prevents duplicate work across replicas and overlapping ticks.
func (s *Scheduler) syncMessengerLabelsTask() {
	if s.cfg == nil || !s.cfg.MessengerJourneyEnabled {
		return
	}
	var states []models.MessengerLabelState
	now := time.Now()
	if err := db.DB.Model(&models.MessengerLabelState{}).
		Joins("JOIN channels ON channels.id = messenger_label_states.channel_id AND channels.tenant_id = messenger_label_states.tenant_id").
		Where("messenger_label_states.enabled = ? AND channels.is_active = ? AND channels.channel_type = ?", true, true, "facebook").
		Where("(messenger_label_states.lease_until IS NULL OR messenger_label_states.lease_until <= ?)", now).
		Where("(messenger_label_states.sync_finished_at IS NULL OR messenger_label_states.sync_finished_at <= ?)", now.Add(-5*time.Minute)).
		Order("COALESCE(messenger_label_states.sync_finished_at, '1970-01-01') ASC, messenger_label_states.channel_id ASC").
		Limit(2).Find(&states).Error; err != nil {
		log.Printf("[messenger-labels] cannot list enabled Pages: %v", err)
		return
	}
	for _, state := range states {
		var channel models.Channel
		if err := db.DB.Where("id = ? AND tenant_id = ? AND channel_type = ? AND is_active = ?", state.ChannelID, state.TenantID, "facebook", true).First(&channel).Error; err != nil {
			continue
		}
		credentials, err := pkg.Decrypt(channel.CredentialsEncrypted, s.cfg.EncryptionKey)
		if err != nil {
			log.Printf("[messenger-labels] cannot decrypt channel %s: %v", channel.ID, err)
			s.markMessengerLabelStartFailure(state, "Không đọc được thông tin kết nối Fanpage.")
			continue
		}
		adapter, err := channels.NewAdapter(channel.ChannelType, credentials)
		if err != nil {
			log.Printf("[messenger-labels] cannot initialize channel %s: %v", channel.ID, err)
			s.markMessengerLabelStartFailure(state, "Không khởi tạo được kết nối Fanpage.")
			continue
		}
		reader, ok := adapter.(*channels.FacebookAdapter)
		if !ok {
			continue
		}
		var creds channels.FacebookCredentials
		if json.Unmarshal(credentials, &creds) != nil || creds.PageID == "" {
			s.markMessengerLabelStartFailure(state, "Thông tin Fanpage thiếu Page ID.")
			continue
		}
		go runMessengerLabelSync(context.Background(), channel, creds.PageID, reader)
	}
}

func (s *Scheduler) markMessengerLabelStartFailure(state models.MessengerLabelState, message string) {
	now := time.Now()
	_ = db.DB.Model(&models.MessengerLabelState{}).
		Where("channel_id = ? AND tenant_id = ? AND (lease_until IS NULL OR lease_until <= ?)", state.ChannelID, state.TenantID, now).
		Updates(map[string]interface{}{"sync_status": "error", "sync_error": message, "sync_finished_at": now}).Error
}
