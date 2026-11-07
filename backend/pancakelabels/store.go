// Package pancakelabels classifies Messenger customers by Pancake tags. It is
// the alternative label source while Meta withholds labels (Advanced Access
// for pages_messaging). Meta label code stays unchanged; a Page uses one
// source at a time so journey classification has a single writer.
package pancakelabels

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/vietbui/chat-quality-agent/channels"
	"github.com/vietbui/chat-quality-agent/db/models"
	"github.com/vietbui/chat-quality-agent/messengerjourney"
	"github.com/vietbui/chat-quality-agent/messengerlabels"
	"github.com/vietbui/chat-quality-agent/pancake"
	"github.com/vietbui/chat-quality-agent/pkg"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	SourceSocket    = "socket"
	SourceReconcile = "reconcile"
	// ReadyWindow: a Page is classified while its last full or incremental
	// scan is recent, even if the realtime socket is down.
	ReadyWindow = 26 * time.Hour
)

var (
	ErrMetaEnabled   = errors.New("Fanpage này đang bật theo dõi nhãn Meta. Tắt nhãn Meta trước khi bật Pancake: mỗi Fanpage chỉ dùng một nguồn nhãn để phân loại.")
	ErrNoPageToken   = errors.New("Chưa lưu Page access token Pancake cho Fanpage này.")
	ErrNoUserToken   = errors.New("Chưa lưu User access token Pancake.")
	ErrDisabled      = errors.New("Chưa bật phân loại theo nhãn Pancake cho Fanpage này.")
	ErrBusy          = errors.New("Fanpage đang đối soát nhãn Pancake hoặc vừa chạy xong; vui lòng chờ 1 phút.")
	ErrConfiguration = messengerlabels.ErrConfiguration
	ErrTokenTooLong  = errors.New("Token Pancake quá dài.")
)

func journeyEnabled() bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv("CQA_MESSENGER_JOURNEY_ENABLED")), "true")
}

func hash(parts ...string) string {
	h := sha256.New()
	for _, part := range parts {
		_, _ = h.Write([]byte(part))
		_, _ = h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil)[:16])
}

func LoadState(database *gorm.DB, channel models.Channel) (models.PancakeLabelState, error) {
	state := models.PancakeLabelState{ChannelID: channel.ID, TenantID: channel.TenantID, PancakePageID: channel.ExternalID, Rules: "[]", Catalog: "[]", SyncStatus: "never"}
	err := database.Where("channel_id = ? AND tenant_id = ?", channel.ID, channel.TenantID).First(&state).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return state, nil
	}
	if state.PancakePageID == "" {
		state.PancakePageID = channel.ExternalID
	}
	return state, err
}

func ensureState(database *gorm.DB, channel models.Channel) error {
	return database.Clauses(clause.OnConflict{DoNothing: true}).Create(&models.PancakeLabelState{ChannelID: channel.ID, TenantID: channel.TenantID, PancakePageID: channel.ExternalID, Rules: "[]", Catalog: "[]", SyncStatus: "never"}).Error
}

func LoadConnection(database *gorm.DB, tenantID string) (models.PancakeConnection, error) {
	conn := models.PancakeConnection{TenantID: tenantID, SocketStatus: "never"}
	err := database.Where("tenant_id = ?", tenantID).First(&conn).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return conn, nil
	}
	return conn, err
}

// SavePageToken stores an encrypted page_access_token. An empty token clears it.
func SavePageToken(database *gorm.DB, channel models.Channel, token, key string) error {
	token = strings.TrimSpace(token)
	if len(token) > 2048 {
		return ErrTokenTooLong
	}
	var encrypted []byte
	if token != "" {
		var err error
		if encrypted, err = pkg.Encrypt([]byte(token), key); err != nil {
			return err
		}
	}
	if err := ensureState(database, channel); err != nil {
		return err
	}
	return database.Model(&models.PancakeLabelState{}).Where("channel_id = ? AND tenant_id = ?", channel.ID, channel.TenantID).
		Updates(map[string]any{"page_token_encrypted": encrypted, "pancake_page_id": channel.ExternalID}).Error
}

// SaveUserToken stores the encrypted Pancake user token used by the socket.
func SaveUserToken(database *gorm.DB, tenantID, token, key string) error {
	token = strings.TrimSpace(token)
	if len(token) > 2048 {
		return ErrTokenTooLong
	}
	var encrypted []byte
	if token != "" {
		var err error
		if encrypted, err = pkg.Encrypt([]byte(token), key); err != nil {
			return err
		}
	}
	now := time.Now()
	row := models.PancakeConnection{TenantID: tenantID, UserTokenEncrypted: encrypted, PancakeUserID: pancake.UserIDFromToken(token), SocketStatus: "never", UpdatedAt: now}
	return database.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "tenant_id"}},
		DoUpdates: clause.Assignments(map[string]any{"user_token_encrypted": encrypted, "pancake_user_id": row.PancakeUserID, "socket_status": "never", "socket_error": "", "updated_at": now}),
	}).Create(&row).Error
}

func decrypt(data []byte, key string) string {
	if len(data) == 0 {
		return ""
	}
	plain, err := pkg.Decrypt(data, key)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(plain))
}

func PageToken(state models.PancakeLabelState, key string) string {
	return decrypt(state.PageTokenEncrypted, key)
}
func UserToken(conn models.PancakeConnection, key string) string {
	return decrypt(conn.UserTokenEncrypted, key)
}

// TagsToLabels converts Pancake tags to the label shape shared with Meta
// ({id, page_label_name}), so reports, journey export and AdBazooka reuse
// one format. Missing text is filled from the catalogue.
func TagsToLabels(tags []pancake.Tag, catalog []channels.FacebookLabel) []channels.FacebookLabel {
	names := map[string]string{}
	for _, label := range catalog {
		names[label.ID] = label.Name
	}
	labels := make([]channels.FacebookLabel, 0, len(tags))
	for _, tag := range tags {
		name := strings.TrimSpace(tag.Text)
		if name == "" {
			name = names[tag.ID]
		}
		if name == "" {
			name = "Tag #" + tag.ID
		}
		labels = append(labels, channels.FacebookLabel{ID: tag.ID, Name: name})
	}
	return labels
}

func CatalogFromTags(tags []pancake.Tag) []channels.FacebookLabel {
	return TagsToLabels(tags, nil)
}

func SaveCatalog(database *gorm.DB, channel models.Channel, catalog []channels.FacebookLabel, now time.Time) error {
	if err := ensureState(database, channel); err != nil {
		return err
	}
	if catalog == nil {
		catalog = []channels.FacebookLabel{}
	}
	data, _ := json.Marshal(catalog)
	return database.Model(&models.PancakeLabelState{}).Where("channel_id = ? AND tenant_id = ?", channel.ID, channel.TenantID).
		Updates(map[string]any{"catalog": string(data), "catalog_synced_at": now}).Error
}

// SavePolicy validates rules against the stored Pancake catalogue. Enabling
// requires Meta tracking to be off for the same Page.
func SavePolicy(database *gorm.DB, channel models.Channel, policy messengerlabels.Policy) error {
	return database.Transaction(func(tx *gorm.DB) error {
		if err := ensureState(tx, channel); err != nil {
			return err
		}
		var state models.PancakeLabelState
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("channel_id = ? AND tenant_id = ?", channel.ID, channel.TenantID).First(&state).Error; err != nil {
			return err
		}
		if policy.Enabled {
			var meta models.MessengerLabelState
			err := tx.Select("enabled").Where("channel_id = ? AND tenant_id = ?", channel.ID, channel.TenantID).First(&meta).Error
			if err == nil && meta.Enabled {
				return ErrMetaEnabled
			}
			if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
			if len(state.PageTokenEncrypted) == 0 {
				return ErrNoPageToken
			}
		}
		var catalog []channels.FacebookLabel
		if json.Unmarshal([]byte(state.Catalog), &catalog) != nil {
			return ErrConfiguration
		}
		if err := policy.Validate(catalog); err != nil {
			return err
		}
		rules, _ := json.Marshal(policy.Rules)
		if policy.Rules == nil {
			rules = []byte("[]")
		}
		changed := state.Enabled != policy.Enabled || state.Rules != string(rules)
		if err := tx.Model(&state).Updates(map[string]any{"enabled": policy.Enabled, "rules": string(rules)}).Error; err != nil {
			return err
		}
		state.Enabled, state.Rules = policy.Enabled, string(rules)
		if !changed || !journeyEnabled() || channel.ExternalID == "" {
			return nil
		}
		var snapshots []models.PancakeLabelSnapshot
		if err := tx.Where("tenant_id = ? AND channel_id = ?", channel.TenantID, channel.ID).Find(&snapshots).Error; err != nil {
			return err
		}
		now := time.Now().UTC()
		for _, snapshot := range snapshots {
			// Enabling exports the current labels too: snapshots stored while
			// disabled were never written to the journey.
			var err error
			if state.Enabled {
				err = recordLabels(tx, channel, state, snapshot)
			} else {
				err = recordClassification(tx, channel, state, snapshot, now)
			}
			if err != nil {
				return err
			}
		}
		return nil
	})
}

// MetaGuard is used by the Meta policy handler: Meta cannot be enabled while
// Pancake classifies the same Page.
func PancakeEnabled(database *gorm.DB, channel models.Channel) (bool, error) {
	var state models.PancakeLabelState
	err := database.Select("enabled").Where("channel_id = ? AND tenant_id = ?", channel.ID, channel.TenantID).First(&state).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false, nil
	}
	return err == nil && state.Enabled, err
}

// PSIDIndex maps customer PSID to CQA conversation for one Page channel.
type PSIDIndex struct {
	mu    sync.Mutex
	built map[string]time.Time
	data  map[string]map[string]string
}

func NewPSIDIndex() *PSIDIndex {
	return &PSIDIndex{built: map[string]time.Time{}, data: map[string]map[string]string{}}
}

// Lookup returns the conversation id; it rebuilds the channel's index when
// older than maxAge, or on a miss when older than one minute.
func (i *PSIDIndex) Lookup(database *gorm.DB, channel models.Channel, psid string, maxAge time.Duration) string {
	i.mu.Lock()
	defer i.mu.Unlock()
	age := time.Since(i.built[channel.ID])
	index := i.data[channel.ID]
	if id, ok := index[psid]; ok && age <= maxAge {
		return id
	}
	if index != nil && age < time.Minute {
		return ""
	}
	built, err := buildPSIDIndex(database, channel)
	if err != nil {
		return index[psid]
	}
	i.data[channel.ID], i.built[channel.ID] = built, time.Now()
	return built[psid]
}

func buildPSIDIndex(database *gorm.DB, channel models.Channel) (map[string]string, error) {
	index := map[string]string{}
	var metaSnapshots []models.MessengerLabelSnapshot
	if err := database.Select("conversation_id,psid").Where("tenant_id = ? AND channel_id = ? AND psid <> ''", channel.TenantID, channel.ID).Find(&metaSnapshots).Error; err != nil {
		return nil, err
	}
	for _, snapshot := range metaSnapshots {
		index[snapshot.PSID] = snapshot.ConversationID
	}
	var convs []models.Conversation
	if err := database.Select("id,tenant_id,channel_id,metadata").Where("tenant_id = ? AND channel_id = ?", channel.TenantID, channel.ID).Limit(messengerlabels.MaxConversations * 4).Find(&convs).Error; err != nil {
		return nil, err
	}
	for _, conv := range convs {
		if psid := messengerlabels.ParticipantPSID(conv, channel.ExternalID); psid != "" {
			index[psid] = conv.ID
		}
	}
	return index, nil
}

// Apply stores one customer's tag set and records journey events when the
// labels change. It returns true when the stored labels changed.
func Apply(ctx context.Context, database *gorm.DB, channel models.Channel, update pancake.ConversationUpdate, source, conversationID string, now time.Time) (bool, error) {
	if update.PSID == "" || update.PSID == channel.ExternalID {
		return false, nil
	}
	timestamp := update.Timestamp
	if timestamp <= 0 {
		timestamp = now.UnixMicro()
	}
	changed := false
	err := database.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var state models.PancakeLabelState
		if err := tx.Where("channel_id = ? AND tenant_id = ?", channel.ID, channel.TenantID).First(&state).Error; err != nil {
			return err
		}
		var catalog []channels.FacebookLabel
		_ = json.Unmarshal([]byte(state.Catalog), &catalog)
		labels := TagsToLabels(update.Tags, catalog)
		data, _ := json.Marshal(labels)
		var existing models.PancakeLabelSnapshot
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("channel_id = ? AND psid = ?", channel.ID, update.PSID).First(&existing).Error
		found := err == nil
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if found && existing.SourceTimestamp > timestamp {
			return nil // stale event
		}
		var convID *string
		if conversationID != "" {
			convID = &conversationID
		} else if found {
			convID = existing.ConversationID
		}
		snapshot := models.PancakeLabelSnapshot{
			ChannelID: channel.ID, PSID: update.PSID, TenantID: channel.TenantID, ConversationID: convID,
			PancakeConversationID: update.PancakeConversationID, Labels: string(data), Source: source,
			SourceTimestamp: timestamp, CheckedAt: now,
		}
		if found && sameLabels(existing.Labels, snapshot.Labels) {
			return tx.Model(&models.PancakeLabelSnapshot{}).Where("channel_id = ? AND psid = ?", channel.ID, update.PSID).
				Updates(map[string]any{"checked_at": now, "source_timestamp": timestamp, "conversation_id": convID, "pancake_conversation_id": update.PancakeConversationID}).Error
		}
		if err := tx.Clauses(clause.OnConflict{UpdateAll: true}).Create(&snapshot).Error; err != nil {
			return err
		}
		changed = true
		if !state.Enabled || !journeyEnabled() || channel.ExternalID == "" {
			return nil
		}
		return recordLabels(tx, channel, state, snapshot)
	})
	return changed, err
}

// sameLabels compares label JSON semantically; MySQL normalizes JSON text.
func sameLabels(a, b string) bool {
	var left, right []channels.FacebookLabel
	if json.Unmarshal([]byte(a), &left) != nil || json.Unmarshal([]byte(b), &right) != nil {
		return false
	}
	l, _ := json.Marshal(left)
	r, _ := json.Marshal(right)
	return string(l) == string(r)
}

func conversationIDOf(snapshot models.PancakeLabelSnapshot) string {
	if snapshot.ConversationID == nil {
		return ""
	}
	return *snapshot.ConversationID
}

func observedAt(snapshot models.PancakeLabelSnapshot) time.Time {
	if snapshot.SourceTimestamp > 0 {
		return time.UnixMicro(snapshot.SourceTimestamp).UTC()
	}
	return snapshot.CheckedAt.UTC()
}

func recordLabels(tx *gorm.DB, channel models.Channel, state models.PancakeLabelState, snapshot models.PancakeLabelSnapshot) error {
	at := observedAt(snapshot)
	base := messengerjourney.BaseInput{
		TenantID: channel.TenantID, PageID: channel.ExternalID, PSID: snapshot.PSID,
		ChannelID: channel.ID, ConversationID: conversationIDOf(snapshot),
		Source: "pancake_tags", OccurredAt: at, ObservedAt: at,
	}
	base.EventID = "cqa_pancake_labels_" + hash(channel.TenantID, channel.ExternalID, snapshot.PSID, snapshot.Labels, strconv.FormatInt(snapshot.SourceTimestamp, 10))
	if _, err := messengerjourney.RecordInTransaction(context.Background(), tx, messengerjourney.RecordInput{
		BaseInput: base, Kind: messengerjourney.KindLabels, Payload: json.RawMessage(snapshot.Labels),
	}); err != nil && !errors.Is(err, messengerjourney.ErrSubjectErased) {
		return err
	}
	return recordClassification(tx, channel, state, snapshot, at)
}

// Classification uses the shared rule engine. Pancake data is pushed, so a
// stored snapshot is current while the Page is ready (see ReadyWindow).
func Classification(labels []channels.FacebookLabel, rules []messengerlabels.Rule, ready bool, now time.Time) string {
	checked := now
	return messengerlabels.Classify(labels, nil, "success", &checked, rules, ready, now)
}

func recordClassification(tx *gorm.DB, channel models.Channel, state models.PancakeLabelState, snapshot models.PancakeLabelSnapshot, at time.Time) error {
	var labels []channels.FacebookLabel
	var rules []messengerlabels.Rule
	if json.Unmarshal([]byte(snapshot.Labels), &labels) != nil || json.Unmarshal([]byte(state.Rules), &rules) != nil {
		return ErrConfiguration
	}
	rulesJSON, _ := json.Marshal(rules)
	version := "pancake_" + hash(string(rulesJSON))
	classification := Classification(labels, rules, state.Enabled, at)
	subject := messengerjourney.SubjectKey(channel.TenantID, channel.ExternalID, snapshot.PSID)
	var current models.MessengerJourneyCustomer
	if err := tx.Select("classification,classification_policy_version").Where("subject_id = ?", subject).First(&current).Error; err == nil {
		if current.Classification == classification && current.ClassificationPolicyVersion == version {
			return nil
		}
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	payload, _ := json.Marshal(map[string]string{"classification": classification, "source_activity_at": observedAt(snapshot).Format(time.RFC3339Nano)})
	_, err := messengerjourney.RecordInTransaction(context.Background(), tx, messengerjourney.RecordInput{
		BaseInput: messengerjourney.BaseInput{
			EventID:  "cqa_pancake_classification_" + hash(channel.TenantID, channel.ExternalID, snapshot.PSID, version, classification, at.Format(time.RFC3339Nano)),
			TenantID: channel.TenantID, PageID: channel.ExternalID, PSID: snapshot.PSID, ChannelID: channel.ID,
			ConversationID: conversationIDOf(snapshot), Source: "cqa_pancake_policy", OccurredAt: at, ObservedAt: at,
		},
		Kind: messengerjourney.KindClassification, PolicyVersion: version, Payload: payload,
	})
	if errors.Is(err, messengerjourney.ErrSubjectErased) {
		return nil
	}
	return err
}

// Ready reports whether stored snapshots may be classified now.
func Ready(channel models.Channel, state models.PancakeLabelState, now time.Time) bool {
	if !channel.IsActive || !state.Enabled || state.ReconciledAt == nil || state.CatalogSyncedAt == nil {
		return false
	}
	var rules []messengerlabels.Rule
	var catalog []channels.FacebookLabel
	if json.Unmarshal([]byte(state.Rules), &rules) != nil || json.Unmarshal([]byte(state.Catalog), &catalog) != nil {
		return false
	}
	if (messengerlabels.Policy{Enabled: true, Rules: rules}).Validate(catalog) != nil {
		return false
	}
	return now.Sub(*state.ReconciledAt) <= ReadyWindow
}
