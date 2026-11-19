// Package chatmemory records Zalo OA chatbot conversations into MySQL as the
// source of truth and derives session summaries, customer profiles, issue
// alerts and a vector index over the summaries from them.
//
// Flow: the worker records each customer message (RecordInbound) and every
// message the OA sends during a session is reported by the Zalo adapter
// (RecordOutbound). Both land in `messages` under the chat's conversation and
// touch a `chat_sessions` row. Once a session has been idle long enough the
// summarizer turns its transcript into a summary, updates the subject's
// profile, raises alerts for serious issues and indexes the summary vector.
package chatmemory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/vietbui/chat-quality-agent/channels"
	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/db/models"
	"github.com/vietbui/chat-quality-agent/engine"
	"github.com/vietbui/chat-quality-agent/pkg"
)

// BotSenderName labels chatbot replies in `messages`. Their sender_type stays
// "agent" so existing reports and screens keep working; raw_data.cqa_source
// tells chatbot replies apart from staff replies.
const BotSenderName = "Chatbot AI"

const recordTimeout = 10 * time.Second

// Turn identifies the chat a message belongs to. The worker attaches it to the
// context for the rest of the turn with WithTurn, so every reply sent with
// that context is recorded under the right session.
type Turn struct {
	TenantID     string
	ChannelID    string
	SessionID    string
	ZaloUserID   string
	ZaloGroupID  string
	GroupName    string
	CustomerName string
	CustomerCode string
	AgentType    string
}

// Subject returns who the chat is with: a GMF group or a single Zalo user.
func (t Turn) Subject() (subjectType, subjectID string) {
	if t.ZaloGroupID != "" {
		return "group", t.ZaloGroupID
	}
	return "user", t.ZaloUserID
}

type turnKey struct{}

// WithTurn returns ctx carrying t.
func WithTurn(ctx context.Context, t Turn) context.Context {
	return context.WithValue(ctx, turnKey{}, t)
}

// TurnFrom returns the Turn attached by WithTurn.
func TurnFrom(ctx context.Context) (Turn, bool) {
	t, ok := ctx.Value(turnKey{}).(Turn)
	return t, ok
}

var (
	enabledMu sync.RWMutex
	enabled   bool
)

// SetEnabled switches live recording on or off (from CHAT_MEMORY_ENABLED).
func SetEnabled(on bool) {
	enabledMu.Lock()
	enabled = on
	enabledMu.Unlock()
}

// Enabled reports whether live recording is on.
func Enabled() bool {
	enabledMu.RLock()
	defer enabledMu.RUnlock()
	return enabled
}

// Inbound is one customer (or staff) message received by the webhook.
type Inbound struct {
	MessageID   string
	Text        string
	ContentType string
	SenderName  string
	SentAt      time.Time
	Raw         interface{}
}

// RecordInbound stores a received message and opens or touches its session.
// It runs synchronously (a few small queries) so the conversation row exists
// before any reply is recorded. Errors are logged, never returned: recording
// must not affect the chat.
func RecordInbound(ctx context.Context, t Turn, in Inbound) {
	if !Enabled() || t.TenantID == "" || t.ChannelID == "" || t.SessionID == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), recordTimeout)
	defer cancel()
	if in.SentAt.IsZero() {
		in.SentAt = time.Now()
	}
	if in.ContentType == "" {
		in.ContentType = "text"
	}
	raw := map[string]interface{}{
		"cqa_live":       true,
		"cqa_source":     "customer",
		"cqa_session_id": t.SessionID,
	}
	if in.Raw != nil {
		raw["webhook"] = in.Raw
	}
	senderName := in.SenderName
	if senderName == "" {
		senderName = t.CustomerName
	}
	if err := record(ctx, t, recordedMessage{
		ExternalID:  in.MessageID,
		SenderType:  "customer",
		SenderName:  senderName,
		SenderExtID: t.ZaloUserID,
		Content:     in.Text,
		ContentType: in.ContentType,
		SentAt:      in.SentAt,
		Raw:         raw,
	}, true); err != nil {
		log.Printf("[chat-memory] record inbound session=%s: %v", t.SessionID, err)
	}
}

// RecordOutbound is the Zalo adapter's sent-message observer. A message is
// recorded as a chatbot reply when the context carries a Turn (worker sends)
// or, failing that, when the recipient has an active chatbot session (pushes
// made from ERP tool calls). Messages outside any session, such as campaign
// broadcasts and staff alerts, are not recorded. It returns immediately and
// writes in the background.
func RecordOutbound(ctx context.Context, channelID string, msg channels.SentMessage) {
	if !Enabled() || channelID == "" {
		return
	}
	t, ok := TurnFrom(ctx)
	if ok && t.ChannelID != channelID {
		ok = false
	}
	if ok {
		// A reply into another chat (e.g. a staff alert group) is not part
		// of this session.
		if msg.GroupID != "" && msg.GroupID != t.ZaloGroupID {
			ok = false
		}
		if msg.GroupID == "" && msg.UserID != t.ZaloUserID {
			ok = false
		}
	}
	go func() {
		bg, cancel := context.WithTimeout(context.Background(), recordTimeout)
		defer cancel()
		turn := t
		if !ok {
			var found bool
			turn, found = turnForActiveSession(bg, channelID, msg)
			if !found {
				return
			}
		}
		if err := record(bg, turn, recordedMessage{
			ExternalID:  msg.MessageID,
			SenderType:  "agent",
			SenderName:  BotSenderName,
			Content:     msg.Text,
			ContentType: msg.ContentType,
			SentAt:      msg.SentAt,
			Raw: map[string]interface{}{
				"cqa_live":       true,
				"cqa_source":     "chatbot",
				"cqa_session_id": turn.SessionID,
			},
		}, false); err != nil {
			log.Printf("[chat-memory] record outbound session=%s: %v", turn.SessionID, err)
		}
	}()
}

// turnForActiveSession rebuilds a Turn for a send made outside the worker.
func turnForActiveSession(ctx context.Context, channelID string, msg channels.SentMessage) (Turn, bool) {
	sessionID := engine.ResolveActiveSessionID(ctx, channelID, msg.UserID, msg.GroupID)
	if sessionID == "" {
		return Turn{}, false
	}
	var sess models.ChatSession
	if err := db.DB.WithContext(ctx).Where("id = ?", sessionID).First(&sess).Error; err == nil {
		return Turn{
			TenantID: sess.TenantID, ChannelID: sess.ChannelID, SessionID: sess.ID,
			ZaloUserID: sess.ZaloUserID, ZaloGroupID: sess.ZaloGroupID,
			CustomerName: sess.CustomerName, CustomerCode: sess.CustomerCode, AgentType: sess.AgentType,
		}, true
	}
	var ch models.Channel
	if err := db.DB.WithContext(ctx).Select("id, tenant_id").Where("id = ?", channelID).First(&ch).Error; err != nil {
		return Turn{}, false
	}
	return Turn{TenantID: ch.TenantID, ChannelID: channelID, SessionID: sessionID, ZaloUserID: msg.UserID, ZaloGroupID: msg.GroupID}, true
}

type recordedMessage struct {
	ExternalID  string
	SenderType  string
	SenderName  string
	SenderExtID string
	Content     string
	ContentType string
	SentAt      time.Time
	Raw         map[string]interface{}
}

// convLocks serialises conversation creation per chat inside this process;
// the unique index on conversations covers other processes.
var convLocks sync.Map

func record(ctx context.Context, t Turn, m recordedMessage, inbound bool) error {
	if db.DB == nil {
		return errors.New("database not initialised")
	}
	tx := db.DB.WithContext(ctx)
	convID, err := ensureConversation(tx, t, m.SentAt)
	if err != nil {
		return fmt.Errorf("conversation: %w", err)
	}
	inserted, err := insertMessage(tx, t.TenantID, convID, m)
	if err != nil {
		return fmt.Errorf("message: %w", err)
	}
	// A redelivered webhook still keeps the session alive but is not counted.
	if err := touchSession(tx, t, convID, m.SentAt, inbound, inserted); err != nil {
		return fmt.Errorf("session: %w", err)
	}
	return nil
}

// ExternalConversationID is the conversation key used for a chat: the Zalo
// user id for 1:1 chats (the same key the Zalo OA sync uses, so both write
// into one conversation) and the group id for GMF group chats.
func ExternalConversationID(t Turn) string {
	if t.ZaloGroupID != "" {
		return t.ZaloGroupID
	}
	return t.ZaloUserID
}

func ensureConversation(tx *gorm.DB, t Turn, at time.Time) (string, error) {
	extID := ExternalConversationID(t)
	if extID == "" {
		return "", errors.New("chat has neither user nor group id")
	}
	lockKey := t.TenantID + "|" + t.ChannelID + "|" + extID
	mu, _ := convLocks.LoadOrStore(lockKey, &sync.Mutex{})
	mu.(*sync.Mutex).Lock()
	defer mu.(*sync.Mutex).Unlock()

	var conv models.Conversation
	err := tx.Where("tenant_id = ? AND channel_id = ? AND external_conversation_id = ?", t.TenantID, t.ChannelID, extID).First(&conv).Error
	if err == nil {
		// Only move last_message_at forward and count the message; never
		// overwrite names or metadata the sync maintains.
		tx.Model(&models.Conversation{}).Where("id = ? AND (last_message_at IS NULL OR last_message_at < ?)", conv.ID, at).
			Updates(map[string]interface{}{"last_message_at": at, "updated_at": time.Now()})
		return conv.ID, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return "", err
	}

	name := t.CustomerName
	extUser := t.ZaloUserID
	meta := map[string]interface{}{"source": "chatbot"}
	if t.ZaloGroupID != "" {
		name = t.GroupName
		extUser = ""
		meta["zalo_group_id"] = t.ZaloGroupID
	}
	metaJSON, _ := json.Marshal(meta)
	now := time.Now()
	conv = models.Conversation{
		ID:                     pkg.NewUUID(),
		TenantID:               t.TenantID,
		ChannelID:              t.ChannelID,
		ExternalConversationID: extID,
		ExternalUserID:         extUser,
		CustomerName:           name,
		LastMessageAt:          &at,
		Metadata:               string(metaJSON),
		CreatedAt:              now,
		UpdatedAt:              now,
	}
	res := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&conv)
	if res.Error != nil {
		return "", res.Error
	}
	if res.RowsAffected == 0 {
		var existing models.Conversation
		if err := tx.Where("tenant_id = ? AND channel_id = ? AND external_conversation_id = ?", t.TenantID, t.ChannelID, extID).First(&existing).Error; err != nil {
			return "", err
		}
		return existing.ID, nil
	}
	return conv.ID, nil
}

// insertMessage stores m unless the same message is already stored, either
// under the same Zalo id or (written by the sync under another id) as a twin
// with the same sender, text and time.
func insertMessage(tx *gorm.DB, tenantID, convID string, m recordedMessage) (bool, error) {
	if m.ExternalID == "" {
		m.ExternalID = "cqa:" + pkg.NewUUID()
	} else if m.Content != "" {
		var twins int64
		tx.Model(&models.Message{}).
			Where("tenant_id = ? AND conversation_id = ? AND sender_type = ? AND content = ? AND external_message_id <> ?", tenantID, convID, m.SenderType, m.Content, m.ExternalID).
			Where("sent_at BETWEEN ? AND ?", m.SentAt.Add(-engine.LiveTwinWindow), m.SentAt.Add(engine.LiveTwinWindow)).
			// Only rows the sync wrote: a bot that repeats itself within the
			// window must still be recorded twice.
			Where("(raw_data IS NULL OR JSON_EXTRACT(raw_data, '$.cqa_live') IS NULL)").
			Count(&twins)
		if twins > 0 {
			return false, nil
		}
	}
	rawJSON, _ := json.Marshal(m.Raw)
	msg := models.Message{
		ID:                pkg.NewUUID(),
		TenantID:          tenantID,
		ConversationID:    convID,
		ExternalMessageID: m.ExternalID,
		SenderType:        m.SenderType,
		SenderName:        m.SenderName,
		SenderExternalID:  m.SenderExtID,
		Content:           m.Content,
		ContentType:       m.ContentType,
		Attachments:       "[]",
		SentAt:            m.SentAt,
		RawData:           string(rawJSON),
		CreatedAt:         time.Now(),
	}
	res := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&msg)
	if res.Error != nil {
		return false, res.Error
	}
	if res.RowsAffected > 0 {
		tx.Model(&models.Conversation{}).Where("id = ?", convID).UpdateColumn("message_count", gorm.Expr("message_count + 1"))
	}
	return res.RowsAffected > 0, nil
}

func touchSession(tx *gorm.DB, t Turn, convID string, at time.Time, inbound, count bool) error {
	subjectType, subjectID := t.Subject()
	in, out := 0, 1
	if inbound {
		in, out = 1, 0
	}
	if !count {
		in, out = 0, 0
	}
	now := time.Now()
	sess := models.ChatSession{
		ID:             t.SessionID,
		TenantID:       t.TenantID,
		ChannelID:      t.ChannelID,
		ConversationID: convID,
		SubjectType:    subjectType,
		SubjectID:      subjectID,
		ZaloUserID:     t.ZaloUserID,
		ZaloGroupID:    t.ZaloGroupID,
		CustomerName:   firstNonEmpty(t.GroupName, t.CustomerName),
		CustomerCode:   t.CustomerCode,
		AgentType:      t.AgentType,
		StartedAt:      at,
		LastActivityAt: at,
		InboundCount:   in,
		OutboundCount:  out,
		Status:         "open",
		Intents:        "[]",
		Products:       "[]",
		Issues:         "[]",
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	return tx.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "id"}},
		DoUpdates: clause.Assignments(map[string]interface{}{
			"last_activity_at": gorm.Expr("GREATEST(last_activity_at, ?)", at),
			"started_at":       gorm.Expr("LEAST(started_at, ?)", at),
			"inbound_count":    gorm.Expr("inbound_count + ?", in),
			"outbound_count":   gorm.Expr("outbound_count + ?", out),
			"updated_at":       now,
		}),
	}).Create(&sess).Error
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
