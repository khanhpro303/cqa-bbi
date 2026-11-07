package handlers

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/vietbui/chat-quality-agent/api/middleware"
	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/db/models"
	"github.com/vietbui/chat-quality-agent/messengerintake"
	"github.com/vietbui/chat-quality-agent/messengerjourney"
	"github.com/vietbui/chat-quality-agent/messengerlabels"
	"gorm.io/gorm"
)

const (
	maxRelayEvents      = 100
	defaultJourneyLimit = 50
	maxJourneyLimit     = 100
	journeyFreshness    = messengerlabels.FreshnessMinutes * time.Minute
)

var journeyEventIDPattern = regexp.MustCompile(`^[A-Za-z0-9:_./-]{1,191}$`)

var errInvalidJourneyScope = errors.New("invalid messenger journey scope")

type relayReferralEvent struct {
	EventID    string          `json:"event_id"`
	PageID     string          `json:"page_id"`
	PSID       string          `json:"psid"`
	AdID       *string         `json:"ad_id"`
	Ref        *string         `json:"ref"`
	Source     *string         `json:"source"`
	Type       *string         `json:"type"`
	MessageID  *string         `json:"message_id"`
	AdsContext json.RawMessage `json:"ads_context"`
	ReferredAt string          `json:"referred_at"`
	ReceivedAt string          `json:"received_at"`
}

type relayReferralRequest struct {
	Events []relayReferralEvent `json:"events"`
}

type relayReferralPayload struct {
	AdID       *string         `json:"ad_id"`
	Ref        *string         `json:"ref"`
	Source     *string         `json:"source"`
	Type       *string         `json:"type"`
	MessageID  *string         `json:"message_id"`
	AdsContext json.RawMessage `json:"ads_context"`
}

func validNullable(value *string, max int) bool {
	return value == nil || (*value == strings.TrimSpace(*value) && *value != "" && len(*value) <= max)
}

func nullableText(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func validateRelayReferral(event relayReferralEvent) (time.Time, time.Time, json.RawMessage, bool) {
	if !journeyEventIDPattern.MatchString(event.EventID) || !pageIDPattern.MatchString(event.PageID) || !pageIDPattern.MatchString(event.PSID) || event.PageID == event.PSID {
		return time.Time{}, time.Time{}, nil, false
	}
	if !validNullable(event.AdID, 64) || !validNullable(event.Ref, 512) || !validNullable(event.Source, 64) ||
		!validNullable(event.Type, 64) || !validNullable(event.MessageID, 255) || len(event.AdsContext) > 16<<10 {
		return time.Time{}, time.Time{}, nil, false
	}
	var adsContext map[string]any
	if len(event.AdsContext) == 0 || json.Unmarshal(event.AdsContext, &adsContext) != nil || adsContext == nil {
		return time.Time{}, time.Time{}, nil, false
	}
	allowedContext := map[string]bool{"ad_title": true, "photo_url": true, "video_url": true, "post_id": true, "product_id": true}
	for key, value := range adsContext {
		text, stringValue := value.(string)
		if !allowedContext[key] || !stringValue || text == "" || len(text) > 2048 {
			return time.Time{}, time.Time{}, nil, false
		}
	}
	if event.AdID != nil && !pageIDPattern.MatchString(*event.AdID) {
		return time.Time{}, time.Time{}, nil, false
	}
	if (event.Source == nil || !strings.EqualFold(*event.Source, "ADS")) && event.Ref == nil {
		return time.Time{}, time.Time{}, nil, false
	}
	referredAt, err := time.Parse(time.RFC3339Nano, event.ReferredAt)
	if err != nil {
		return time.Time{}, time.Time{}, nil, false
	}
	receivedAt, err := time.Parse(time.RFC3339Nano, event.ReceivedAt)
	if err != nil {
		return time.Time{}, time.Time{}, nil, false
	}
	now := time.Now().UTC()
	if receivedAt.After(now.Add(10*time.Minute)) || referredAt.After(receivedAt.Add(5*time.Minute)) {
		return time.Time{}, time.Time{}, nil, false
	}
	payload, err := json.Marshal(relayReferralPayload{
		AdID: event.AdID, Ref: event.Ref, Source: event.Source, Type: event.Type,
		MessageID: event.MessageID, AdsContext: event.AdsContext,
	})
	return referredAt.UTC(), receivedAt.UTC(), payload, err == nil
}

// IngestMessengerReferralEvents receives the durable, sanitized referral
// relay. Validation happens before the transaction so a 2xx always means the
// complete event-id set is durably accepted, including prior duplicates.
func IngestMessengerReferralEvents(c *gin.Context) {
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	var request relayReferralRequest
	if decoder.Decode(&request) != nil || len(request.Events) == 0 || len(request.Events) > maxRelayEvents {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_referral_events"})
		return
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_referral_events"})
		return
	}
	tenantID := middleware.GetTenantID(c)
	if tenantID == "" || db.DB == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "messenger_journey_unavailable"})
		return
	}

	pageSet := make(map[string]bool)
	seenIDs := make(map[string]bool)
	type preparedEvent struct {
		relayReferralEvent
		referredAt, receivedAt time.Time
		payload                json.RawMessage
	}
	prepared := make([]preparedEvent, 0, len(request.Events))
	for _, event := range request.Events {
		if seenIDs[event.EventID] {
			c.JSON(http.StatusBadRequest, gin.H{"error": "duplicate_event_id_in_request"})
			return
		}
		seenIDs[event.EventID] = true
		referredAt, receivedAt, payload, ok := validateRelayReferral(event)
		if !ok {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_referral_event", "event_id": event.EventID})
			return
		}
		pageSet[event.PageID] = true
		prepared = append(prepared, preparedEvent{event, referredAt, receivedAt, payload})
	}
	pageIDs := make([]string, 0, len(pageSet))
	for pageID := range pageSet {
		pageIDs = append(pageIDs, pageID)
	}
	var channels []models.Channel
	if err := db.DB.WithContext(c.Request.Context()).Where(
		"tenant_id = ? AND channel_type = ? AND is_active = ? AND external_id IN ?", tenantID, "facebook", true, pageIDs,
	).Find(&channels).Error; err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "messenger_journey_unavailable"})
		return
	}
	channelByPage := make(map[string]models.Channel, len(channels))
	for _, channel := range channels {
		channelByPage[channel.ExternalID] = channel
	}
	if len(channelByPage) != len(pageSet) {
		c.JSON(http.StatusForbidden, gin.H{"error": "foreign_or_inactive_page"})
		return
	}

	err := db.DB.WithContext(c.Request.Context()).Transaction(func(tx *gorm.DB) error {
		for _, event := range prepared {
			channel := channelByPage[event.PageID]
			_, err := messengerjourney.RecordInTransaction(c.Request.Context(), tx, messengerjourney.RecordInput{
				BaseInput: messengerjourney.BaseInput{
					EventID: event.EventID, TenantID: tenantID, PageID: event.PageID, PSID: event.PSID,
					ChannelID: channel.ID, Source: "adbazooka_relay", OccurredAt: event.referredAt, ObservedAt: event.receivedAt,
				},
				Kind: messengerjourney.KindReferral, Payload: event.payload,
			})
			// A privacy tombstone is a durable denial marker. A delayed relay for
			// that subject is acknowledged as consumed without recreating PII.
			if errors.Is(err, messengerjourney.ErrSubjectErased) {
				continue
			}
			if err != nil {
				return err
			}
			if err := messengerintake.CaptureFirstReferralInTransaction(tx, channel, messengerintake.Referral{
				PageID: event.PageID, PSID: event.PSID, AdID: nullableText(event.AdID), Ref: nullableText(event.Ref),
				Source: nullableText(event.Source), ReferralType: nullableText(event.Type), MessageID: nullableText(event.MessageID),
				RawJSON: string(event.payload), CapturedAt: event.referredAt,
			}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, messengerjourney.ErrEventConflict) {
			c.JSON(http.StatusConflict, gin.H{"error": "event_id_conflict"})
			return
		}
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "messenger_journey_unavailable"})
		return
	}
	accepted := make([]string, 0, len(prepared))
	for _, event := range prepared {
		accepted = append(accepted, event.EventID)
	}
	c.JSON(http.StatusOK, gin.H{"accepted_event_ids": accepted})
}

type journeyCursor struct {
	Version   int    `json:"v"`
	Scope     string `json:"s"`
	Sequence  uint64 `json:"q,omitempty"`
	Watermark uint64 `json:"w,omitempty"`
	Kind      string `json:"k,omitempty"`
	AfterKey  string `json:"a,omitempty"`
}

func journeyScope(tenantID string, pageIDs []string, purpose string) string {
	pages := append([]string(nil), pageIDs...)
	sort.Strings(pages)
	sum := sha256.Sum256([]byte(tenantID + "\x00" + purpose + "\x00" + strings.Join(pages, ",")))
	return hex.EncodeToString(sum[:])
}

func encodeJourneyCursor(cursor journeyCursor, secret string) string {
	payload, _ := json.Marshal(cursor)
	body := base64.RawURLEncoding.EncodeToString(payload)
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(body))
	return body + "." + hex.EncodeToString(mac.Sum(nil))
}

func decodeJourneyCursor(raw, scope, secret string) (journeyCursor, bool) {
	if raw == "" {
		return journeyCursor{Version: 1, Scope: scope}, true
	}
	if secret == "" || len(raw) > 1024 {
		return journeyCursor{}, false
	}
	parts := strings.Split(raw, ".")
	if len(parts) != 2 {
		return journeyCursor{}, false
	}
	provided, err := hex.DecodeString(parts[1])
	if err != nil {
		return journeyCursor{}, false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(parts[0]))
	if !hmac.Equal(provided, mac.Sum(nil)) {
		return journeyCursor{}, false
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return journeyCursor{}, false
	}
	var cursor journeyCursor
	if json.Unmarshal(payload, &cursor) != nil || cursor.Version != 1 || cursor.Scope != scope {
		return journeyCursor{}, false
	}
	return cursor, true
}

func journeyLimit(c *gin.Context) (int, bool) {
	if c.Query("limit") == "" {
		return defaultJourneyLimit, true
	}
	value, err := strconv.Atoi(c.Query("limit"))
	return value, err == nil && value > 0 && value <= maxJourneyLimit
}

func authorizedJourneyPages(c *gin.Context) (string, []string, error) {
	tenantID := middleware.GetTenantID(c)
	pageIDs, ok := parseOrderEventPageIDs(c.Query("page_ids"))
	if !ok || tenantID == "" || db.DB == nil {
		return tenantID, nil, errInvalidJourneyScope
	}
	var activePageIDs []string
	err := db.DB.WithContext(c.Request.Context()).Model(&models.Channel{}).Where(
		"tenant_id = ? AND channel_type = ? AND is_active = ? AND external_id IN ?", tenantID, "facebook", true, pageIDs,
	).Distinct().Pluck("external_id", &activePageIDs).Error
	if err != nil {
		return tenantID, nil, err
	}
	// A removed channel must remain exportable long enough for its non-PII
	// tombstones to reach replicas. Historical pages are still tenant-scoped and
	// require the same HMAC secret; the relay write path remains active-only.
	var historicalPageIDs []string
	err = db.DB.WithContext(c.Request.Context()).Model(&models.MessengerJourneyEvent{}).Where(
		"tenant_id = ? AND page_id IN ?", tenantID, pageIDs,
	).Distinct().Pluck("page_id", &historicalPageIDs).Error
	if err != nil {
		return tenantID, nil, err
	}
	allowed := make(map[string]bool, len(activePageIDs)+len(historicalPageIDs))
	for _, pageID := range append(activePageIDs, historicalPageIDs...) {
		allowed[pageID] = true
	}
	for _, pageID := range pageIDs {
		if !allowed[pageID] {
			return tenantID, nil, errInvalidJourneyScope
		}
	}
	return tenantID, pageIDs, nil
}

type journeyEventResponse struct {
	Sequence       uint64          `json:"sequence"`
	EventID        string          `json:"event_id"`
	Kind           string          `json:"kind"`
	PageID         string          `json:"page_id"`
	SubjectID      string          `json:"subject_id"`
	PSID           string          `json:"psid,omitempty"`
	ConversationID string          `json:"conversation_id,omitempty"`
	EntityID       string          `json:"entity_id,omitempty"`
	Version        uint64          `json:"version"`
	PolicyVersion  string          `json:"policy_version,omitempty"`
	Source         string          `json:"source"`
	OccurredAt     time.Time       `json:"occurred_at"`
	ObservedAt     time.Time       `json:"observed_at"`
	Payload        json.RawMessage `json:"payload"`
}

func journeyEvent(row models.MessengerJourneyEvent) journeyEventResponse {
	payload := json.RawMessage(row.Payload)
	switch row.Kind {
	case messengerjourney.KindLabels:
		var stored []any
		if json.Unmarshal(payload, &stored) == nil {
			labels := make([]any, 0, len(stored))
			for _, item := range stored {
				label, object := item.(map[string]any)
				if !object {
					labels = append(labels, item)
					continue
				}
				canonical := make(map[string]any, 2)
				if id, exists := label["id"]; exists {
					canonical["id"] = id
				}
				if name, exists := label["page_label_name"]; exists {
					canonical["name"] = name
				} else if name, exists := label["name"]; exists {
					canonical["name"] = name
				}
				labels = append(labels, canonical)
			}
			if encoded, err := json.Marshal(labels); err == nil {
				payload = encoded
			}
		}
		wrapped, err := json.Marshal(struct {
			Labels json.RawMessage `json:"labels"`
			Status string          `json:"status"`
		}{Labels: payload, Status: "success"})
		if err == nil {
			payload = wrapped
		}
	case messengerjourney.KindReferral:
		var referral map[string]any
		if json.Unmarshal(payload, &referral) == nil {
			if value, exists := referral["type"]; exists {
				referral["referral_type"] = value
				delete(referral, "type")
			}
			if encoded, err := json.Marshal(referral); err == nil {
				payload = encoded
			}
		}
	}
	return journeyEventResponse{
		Sequence: row.Sequence, EventID: row.EventID, Kind: row.Kind, PageID: row.PageID,
		SubjectID: row.SubjectKey, PSID: row.PSID, ConversationID: row.ConversationID,
		EntityID: row.EntityID, Version: row.Sequence, PolicyVersion: row.PolicyVersion,
		Source: row.Source, OccurredAt: row.OccurredAt, ObservedAt: row.ObservedAt, Payload: payload,
	}
}

// legacyJourneyKinds is the v2 kind enum served when a caller does not ask for
// kinds. Coverage is exposed through /status, never the business-event feed.
var legacyJourneyKinds = []string{messengerjourney.KindReferral, messengerjourney.KindLabels, messengerjourney.KindClassification, messengerjourney.KindOrder, messengerjourney.KindDeletion}

// journeyKinds reads ?kinds=a,b. Without it the legacy enum is used, so a
// replica that does not know "insight" keeps working. Deletion is always
// included: a replica must never miss an erasure.
func journeyKinds(c *gin.Context) ([]string, bool) {
	raw := strings.TrimSpace(c.Query("kinds"))
	if raw == "" {
		return legacyJourneyKinds, true
	}
	allowed := map[string]bool{messengerjourney.KindInsight: true}
	for _, kind := range legacyJourneyKinds {
		allowed[kind] = true
	}
	seen := map[string]bool{messengerjourney.KindDeletion: true}
	kinds := []string{messengerjourney.KindDeletion}
	for _, part := range strings.Split(raw, ",") {
		kind := strings.TrimSpace(part)
		if !allowed[kind] || len(kinds) > len(allowed) {
			return nil, false
		}
		if !seen[kind] {
			seen[kind] = true
			kinds = append(kinds, kind)
		}
	}
	return kinds, true
}

func exportableJourneyEvent(row models.MessengerJourneyEvent, kinds []string) bool {
	for _, kind := range kinds {
		if row.Kind == kind {
			return row.Kind == messengerjourney.KindDeletion || row.PSID != ""
		}
	}
	return false
}

func ListMessengerJourneyEvents(cursorSecret string) gin.HandlerFunc {
	return func(c *gin.Context) {
		tenantID, pageIDs, authErr := authorizedJourneyPages(c)
		limit, limitOK := journeyLimit(c)
		kinds, kindsOK := journeyKinds(c)
		if errors.Is(authErr, errInvalidJourneyScope) || !limitOK || !kindsOK {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_journey_scope"})
			return
		}
		if authErr != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "messenger_journey_unavailable"})
			return
		}
		scope := journeyScope(tenantID, pageIDs, "events")
		cursor, ok := decodeJourneyCursor(c.Query("after"), scope, cursorSecret)
		if !ok || cursor.Watermark != 0 || cursor.Kind != "" || cursor.AfterKey != "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_cursor"})
			return
		}
		page, err := messengerjourney.ListEvents(c.Request.Context(), db.DB, messengerjourney.EventQuery{
			TenantID: tenantID, PageIDs: pageIDs, AfterSequence: cursor.Sequence, Limit: limit,
		})
		if err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "messenger_journey_unavailable"})
			return
		}
		watermark, err := messengerjourney.HighWatermark(c.Request.Context(), db.DB, tenantID)
		if err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "messenger_journey_unavailable"})
			return
		}
		events := make([]journeyEventResponse, 0, len(page.Events))
		for _, event := range page.Events {
			if exportableJourneyEvent(event, kinds) {
				events = append(events, journeyEvent(event))
			}
		}
		next := encodeJourneyCursor(journeyCursor{Version: 1, Scope: scope, Sequence: page.NextSequence}, cursorSecret)
		c.JSON(http.StatusOK, gin.H{"tenant_id": tenantID, "events": events, "next_cursor": next, "has_more": page.HasMore, "watermark": watermark})
	}
}

func ListMessengerJourneySnapshot(cursorSecret string) gin.HandlerFunc {
	return func(c *gin.Context) {
		tenantID, pageIDs, authErr := authorizedJourneyPages(c)
		limit, limitOK := journeyLimit(c)
		kinds, kindsOK := journeyKinds(c)
		if errors.Is(authErr, errInvalidJourneyScope) || !limitOK || !kindsOK {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_journey_scope"})
			return
		}
		if authErr != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "messenger_journey_unavailable"})
			return
		}
		scope := journeyScope(tenantID, pageIDs, "snapshot")
		cursor, ok := decodeJourneyCursor(c.Query("after"), scope, cursorSecret)
		if !ok || cursor.Kind != "" || cursor.AfterKey != "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_cursor"})
			return
		}
		if c.Query("after") == "" {
			var err error
			cursor.Watermark, err = messengerjourney.HighWatermark(c.Request.Context(), db.DB, tenantID)
			if err != nil {
				c.JSON(http.StatusServiceUnavailable, gin.H{"error": "messenger_journey_unavailable"})
				return
			}
		}
		var rows []models.MessengerJourneyEvent
		query := db.DB.WithContext(c.Request.Context()).Where(
			"tenant_id = ? AND sequence > ? AND sequence <= ? AND page_id IN ?", tenantID, cursor.Sequence, cursor.Watermark, pageIDs,
		).Where("kind IN ?", kinds).
			Where("kind = ? OR psid <> ''", messengerjourney.KindDeletion).Order("sequence ASC").Limit(limit + 1)
		if err := query.Find(&rows).Error; err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "messenger_journey_unavailable"})
			return
		}
		hasMore := len(rows) > limit
		if hasMore {
			rows = rows[:limit]
		}
		records := make([]journeyEventResponse, 0, len(rows))
		nextSequence := cursor.Sequence
		for _, row := range rows {
			records = append(records, journeyEvent(row))
			nextSequence = row.Sequence
		}
		nextCursor := ""
		if hasMore {
			nextCursor = encodeJourneyCursor(journeyCursor{Version: 1, Scope: scope, Watermark: cursor.Watermark, Sequence: nextSequence}, cursorSecret)
		}
		eventScope := journeyScope(tenantID, pageIDs, "events")
		eventCursor := encodeJourneyCursor(journeyCursor{Version: 1, Scope: eventScope, Sequence: cursor.Watermark}, cursorSecret)
		c.JSON(http.StatusOK, gin.H{
			"tenant_id": tenantID, "watermark": cursor.Watermark, "records": records, "next_cursor": nextCursor,
			"has_more": hasMore, "event_cursor": eventCursor,
		})
	}
}

type journeyMessageStatus struct {
	Complete   bool       `json:"complete"`
	Status     string     `json:"status"`
	LastSyncAt *time.Time `json:"last_sync_at,omitempty"`
}

type journeyLabelStatus struct {
	Complete  bool       `json:"complete"`
	Status    string     `json:"status"`
	CheckedAt *time.Time `json:"checked_at,omitempty"`
	Pending   int64      `json:"pending"`
	Error     string     `json:"error,omitempty"`
}

type journeyAnalysisStatus struct {
	Complete        bool       `json:"complete"`
	Status          string     `json:"status"`
	AnalyzedThrough *time.Time `json:"analyzed_through,omitempty"`
	Pending         int64      `json:"pending"`
	Error           string     `json:"error,omitempty"`
}

type journeyPageStatus struct {
	PageID   string                `json:"page_id"`
	Messages journeyMessageStatus  `json:"messages"`
	Labels   journeyLabelStatus    `json:"labels"`
	Analysis journeyAnalysisStatus `json:"analysis"`
	Complete bool                  `json:"complete"`
}

func isMessengerInsightJob(job models.Job, channelID string) bool {
	var config struct {
		Profile string `json:"profile"`
	}
	if json.Unmarshal([]byte(job.RulesConfig), &config) != nil || config.Profile != "messenger_insights" || !job.IsActive {
		return false
	}
	var channels []string
	return json.Unmarshal([]byte(job.InputChannelIDs), &channels) == nil && containsString(channels, channelID)
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func labelDisplayNames(raw string) []string {
	var values []string
	if json.Unmarshal([]byte(raw), &values) == nil {
		return values
	}
	var labels []struct {
		Name string `json:"page_label_name"`
	}
	if json.Unmarshal([]byte(raw), &labels) != nil {
		return []string{}
	}
	values = make([]string, 0, len(labels))
	for _, label := range labels {
		if name := strings.TrimSpace(label.Name); name != "" {
			values = append(values, name)
		}
	}
	return values
}

type journeyMessageCursor = messengerjourney.MessageCursor

func coverageSourceCursor(item models.MessengerJourneyCoverage) (journeyMessageCursor, bool) {
	return messengerjourney.CoverageCursor(item)
}

func coverageMatchesSource(coverage, current journeyMessageCursor) bool {
	return messengerjourney.CursorsMatch(coverage, current)
}

func loadJourneyMessageCursors(ctx context.Context, database *gorm.DB, tenantID string, conversationIDs []string) (map[string]journeyMessageCursor, error) {
	return messengerjourney.LoadMessageCursors(ctx, database, tenantID, conversationIDs)
}

func ListMessengerJourneyStatus(c *gin.Context) {
	tenantID, pageIDs, authErr := authorizedJourneyPages(c)
	if errors.Is(authErr, errInvalidJourneyScope) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_journey_scope"})
		return
	}
	if authErr != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "messenger_journey_unavailable"})
		return
	}
	now := time.Now().UTC()
	var channels []models.Channel
	if err := db.DB.WithContext(c.Request.Context()).Where(
		"tenant_id = ? AND channel_type = ? AND is_active = ? AND external_id IN ?", tenantID, "facebook", true, pageIDs,
	).Find(&channels).Error; err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "messenger_journey_unavailable"})
		return
	}
	var jobs []models.Job
	if err := db.DB.WithContext(c.Request.Context()).Where("tenant_id = ? AND is_active = ?", tenantID, true).Find(&jobs).Error; err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "messenger_journey_unavailable"})
		return
	}
	pages := make([]journeyPageStatus, 0, len(channels))
	activePages := make(map[string]bool, len(channels))
	for _, channel := range channels {
		activePages[channel.ExternalID] = true
		page := journeyPageStatus{PageID: channel.ExternalID}
		page.Messages = journeyMessageStatus{Status: channel.LastSyncStatus, LastSyncAt: channel.LastSyncAt}
		page.Messages.Complete = channel.LastSyncAt != nil && channel.LastSyncStatus == "success" && now.Sub(channel.LastSyncAt.UTC()) <= journeyFreshness

		var labelState models.MessengerLabelState
		labelErr := db.DB.WithContext(c.Request.Context()).Where("tenant_id = ? AND channel_id = ?", tenantID, channel.ID).First(&labelState).Error
		if labelErr != nil && !errors.Is(labelErr, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "messenger_journey_unavailable"})
			return
		}
		page.Labels = journeyLabelStatus{Status: labelState.SyncStatus, CheckedAt: labelState.SyncFinishedAt}
		if labelErr != nil {
			page.Labels.Error = "not_synced"
		} else if labelState.SyncStatus == "error" {
			page.Labels.Error = "sync_failed"
		}

		var conversations []models.Conversation
		if err := db.DB.WithContext(c.Request.Context()).Where("tenant_id = ? AND channel_id = ?", tenantID, channel.ID).Find(&conversations).Error; err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "messenger_journey_unavailable"})
			return
		}
		var freshLabelCount int64
		freshAfter := now.Add(-journeyFreshness)
		if err := db.DB.WithContext(c.Request.Context()).Model(&models.MessengerLabelSnapshot{}).Where(
			"tenant_id = ? AND channel_id = ? AND status = ? AND checked_at >= ?", tenantID, channel.ID, "success", freshAfter,
		).Count(&freshLabelCount).Error; err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "messenger_journey_unavailable"})
			return
		}
		page.Labels.Pending = int64(len(conversations)) - freshLabelCount
		if page.Labels.Pending < 0 {
			page.Labels.Pending = 0
		}
		page.Labels.Complete = labelErr == nil && labelState.Enabled && labelState.SyncStatus == "success" && labelState.SyncFinishedAt != nil && now.Sub(labelState.SyncFinishedAt.UTC()) <= journeyFreshness && page.Labels.Pending == 0
		var coverage []models.MessengerJourneyCoverage
		if err := db.DB.WithContext(c.Request.Context()).Where("tenant_id = ? AND page_id = ?", tenantID, channel.ExternalID).Find(&coverage).Error; err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "messenger_journey_unavailable"})
			return
		}
		coverageByConversation := make(map[string]models.MessengerJourneyCoverage, len(coverage))
		for _, item := range coverage {
			coverageByConversation[item.ConversationID] = item
			if source, valid := coverageSourceCursor(item); valid && source.SourceLastMessageAt != nil && (page.Analysis.AnalyzedThrough == nil || source.SourceLastMessageAt.After(*page.Analysis.AnalyzedThrough)) {
				page.Analysis.AnalyzedThrough = source.SourceLastMessageAt
			}
			if item.Status == "error" {
				page.Analysis.Error = "analysis_failed"
			}
		}
		conversationIDs := make([]string, 0, len(conversations))
		for _, conversation := range conversations {
			conversationIDs = append(conversationIDs, conversation.ID)
		}
		currentCursorByConversation, err := loadJourneyMessageCursors(c.Request.Context(), db.DB, tenantID, conversationIDs)
		if err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "messenger_journey_unavailable"})
			return
		}
		// Per-conversation coverage that matches the current stored messages is
		// the proof of analysis. The job only has to exist and be healthy; tying
		// readiness to "job ran after the latest sync" made every Page flip to
		// incomplete between each 15-minute sync and the job run that follows it.
		jobReady := false
		for _, job := range jobs {
			if isMessengerInsightJob(job, channel.ID) {
				if job.LastRunStatus == "error" {
					page.Analysis.Error = "analysis_failed"
				}
				if job.LastRunAt != nil && job.LastRunStatus != "error" {
					jobReady = true
				}
			}
		}
		for _, conversation := range conversations {
			current := currentCursorByConversation[conversation.ID]
			if current.SourceMessageCount == 0 {
				continue // No stored message: there is nothing for the AI to analyze.
			}
			item, exists := coverageByConversation[conversation.ID]
			if !messengerjourney.CoverageComplete(item, exists, current) {
				page.Analysis.Pending++
			}
		}
		page.Analysis.Complete = jobReady && page.Analysis.Pending == 0 && page.Analysis.Error == ""
		page.Analysis.Status = "pending"
		if page.Analysis.Complete {
			page.Analysis.Status = "success"
		} else if page.Analysis.Error != "" {
			page.Analysis.Status = "error"
		}
		// Labels sync independently of messages and AI (Meta only exposes a
		// customer's labels to apps with Advanced Access). Their state stays in
		// page.labels; a Page is ready for ad attribution once its messages are
		// current and every conversation is covered by AI analysis.
		page.Complete = page.Messages.Complete && page.Analysis.Complete
		pages = append(pages, page)
	}
	for _, pageID := range pageIDs {
		if !activePages[pageID] {
			pages = append(pages, journeyPageStatus{
				PageID: pageID, Messages: journeyMessageStatus{Status: "removed"},
				Labels: journeyLabelStatus{Status: "removed"}, Analysis: journeyAnalysisStatus{Status: "removed"},
			})
		}
	}
	sort.Slice(pages, func(i, j int) bool { return pages[i].PageID < pages[j].PageID })
	status, err := messengerjourney.Status(c.Request.Context(), db.DB, tenantID, pageIDs)
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "messenger_journey_unavailable"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"tenant_id": tenantID, "generated_at": now, "watermark": status.HighWatermark, "backlog": status.CoveragePending, "pages": pages})
}

func GetConversationJourney(cursorSecret string) gin.HandlerFunc {
	return func(c *gin.Context) {
		tenantID := middleware.GetTenantID(c)
		conversationID := c.Param("conversationId")
		limit, ok := journeyLimit(c)
		if !ok {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_limit"})
			return
		}
		var conversation models.Conversation
		if err := db.DB.WithContext(c.Request.Context()).Where("tenant_id = ? AND id = ?", tenantID, conversationID).First(&conversation).Error; err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "conversation_not_found"})
			return
		}
		scope := journeyScope(tenantID, []string{conversationID}, "conversation")
		cursor, ok := decodeJourneyCursor(c.Query("after"), scope, cursorSecret)
		if !ok || cursor.Watermark != 0 || cursor.Kind != "" || cursor.AfterKey != "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_cursor"})
			return
		}
		page, err := messengerjourney.ListConversation(c.Request.Context(), db.DB, tenantID, conversationID, cursor.Sequence, limit)
		if err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "messenger_journey_unavailable"})
			return
		}
		events := make([]journeyEventResponse, 0, len(page.Events))
		for _, event := range page.Events {
			if exportableJourneyEvent(event, legacyJourneyKinds) {
				events = append(events, journeyEvent(event))
			}
		}

		summary := gin.H{"labels": []string{}, "classification": nil, "classification_observed_at": nil, "first_referral": nil, "latest_referral": nil, "latest_order": nil, "last_observed_at": nil}
		var customer models.MessengerJourneyCustomer
		customerErr := db.DB.WithContext(c.Request.Context()).Where("tenant_id = ? AND conversation_id = ?", tenantID, conversationID).First(&customer).Error
		if customerErr != nil && !errors.Is(customerErr, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "messenger_journey_unavailable"})
			return
		}
		if customerErr == nil {
			if customer.Labels != nil {
				summary["labels"] = labelDisplayNames(*customer.Labels)
			}
			if customer.Classification != "" {
				summary["classification"] = customer.Classification
				summary["classification_observed_at"] = customer.ClassificationObservedAt
			}
			var firstReferral, latestReferral, latestOrder, lastEvent models.MessengerJourneyEvent
			eventQuery := func() *gorm.DB {
				return db.DB.WithContext(c.Request.Context()).Where("tenant_id = ? AND subject_id = ?", tenantID, customer.SubjectKey)
			}
			if err := eventQuery().Where("kind = ?", messengerjourney.KindReferral).Order("occurred_at ASC, sequence ASC").First(&firstReferral).Error; err == nil {
				summary["first_referral"] = journeyEvent(firstReferral)
			} else if !errors.Is(err, gorm.ErrRecordNotFound) {
				c.JSON(http.StatusServiceUnavailable, gin.H{"error": "messenger_journey_unavailable"})
				return
			}
			if err := eventQuery().Where("kind = ?", messengerjourney.KindReferral).Order("occurred_at DESC, sequence DESC").First(&latestReferral).Error; err == nil {
				summary["latest_referral"] = journeyEvent(latestReferral)
			} else if !errors.Is(err, gorm.ErrRecordNotFound) {
				c.JSON(http.StatusServiceUnavailable, gin.H{"error": "messenger_journey_unavailable"})
				return
			}
			if err := eventQuery().Where("kind = ?", messengerjourney.KindOrder).Order("observed_at DESC, sequence DESC").First(&latestOrder).Error; err == nil {
				summary["latest_order"] = journeyEvent(latestOrder)
			} else if !errors.Is(err, gorm.ErrRecordNotFound) {
				c.JSON(http.StatusServiceUnavailable, gin.H{"error": "messenger_journey_unavailable"})
				return
			}
			if err := eventQuery().Order("observed_at DESC, sequence DESC").First(&lastEvent).Error; err == nil {
				summary["last_observed_at"] = lastEvent.ObservedAt
			} else if !errors.Is(err, gorm.ErrRecordNotFound) {
				c.JSON(http.StatusServiceUnavailable, gin.H{"error": "messenger_journey_unavailable"})
				return
			}
		}
		next := encodeJourneyCursor(journeyCursor{Version: 1, Scope: scope, Sequence: page.NextSequence}, cursorSecret)
		c.JSON(http.StatusOK, gin.H{"summary": summary, "events": events, "next_cursor": next, "has_more": page.HasMore})
	}
}
