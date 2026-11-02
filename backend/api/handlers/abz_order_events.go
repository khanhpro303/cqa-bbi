package handlers

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/vietbui/chat-quality-agent/api/middleware"
	"github.com/vietbui/chat-quality-agent/db"
)

var pageIDPattern = regexp.MustCompile(`^[1-9][0-9]{0,63}$`)

func parseOrderEventPageIDs(raw string) ([]string, bool) {
	pageIDs := make([]string, 0, 20)
	seenPages := make(map[string]bool)
	for _, rawPageID := range strings.Split(raw, ",") {
		pageID := strings.TrimSpace(rawPageID)
		if pageID == "" {
			continue
		}
		if !pageIDPattern.MatchString(pageID) || seenPages[pageID] || len(pageIDs) >= 20 {
			return nil, false
		}
		seenPages[pageID] = true
		pageIDs = append(pageIDs, pageID)
	}
	return pageIDs, len(pageIDs) > 0
}

const (
	defaultOrderEventLimit = 50
	maxOrderEventLimit     = 100
	psidRetentionDays      = 180
)

type orderEventCursor struct {
	CreatedAt string `json:"t"`
	ID        string `json:"id"`
	Index     int    `json:"i"`
}

type storedOrderOutcome struct {
	OrderID            string   `json:"order_id"`
	OrderRef           string   `json:"order_ref"`
	ValueVND           *int64   `json:"value_vnd"`
	Status             string   `json:"status"`
	OrderedAt          string   `json:"ordered_at"`
	EvidenceMessageIDs []string `json:"evidence_message_ids"`
	Confidence         float64  `json:"confidence"`
}

type storedInsightDetail struct {
	OrderOutcomes []storedOrderOutcome `json:"order_outcomes"`
}

type orderEventRow struct {
	ResultID  string
	Detail    string
	CreatedAt time.Time
	PageID    string
	PSID      string
}

type abzOrderEvent struct {
	SourceEventID      string   `json:"source_event_id"`
	OrderID            string   `json:"order_id"`
	OrderRef           string   `json:"order_ref"`
	PageID             string   `json:"page_id"`
	PSID               string   `json:"psid"`
	ValueVND           *int64   `json:"value_vnd"`
	Status             string   `json:"status"`
	OrderedAt          string   `json:"ordered_at"`
	ObservedAt         string   `json:"observed_at"`
	EvidenceMessageIDs []string `json:"evidence_message_ids"`
	Confidence         float64  `json:"confidence"`
}

func encodeOrderEventCursor(row orderEventRow, index int) string {
	payload, _ := json.Marshal(orderEventCursor{CreatedAt: row.CreatedAt.UTC().Format(time.RFC3339Nano), ID: row.ResultID, Index: index})
	return base64.RawURLEncoding.EncodeToString(payload)
}

func decodeOrderEventCursor(raw string) (orderEventCursor, time.Time, bool) {
	if raw == "" {
		return orderEventCursor{Index: -1}, time.Time{}, true
	}
	payload, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return orderEventCursor{}, time.Time{}, false
	}
	var cursor orderEventCursor
	if err := json.Unmarshal(payload, &cursor); err != nil || cursor.ID == "" || cursor.Index < -1 {
		return orderEventCursor{}, time.Time{}, false
	}
	createdAt, err := time.Parse(time.RFC3339Nano, cursor.CreatedAt)
	return cursor, createdAt, err == nil
}

func sourceOrderEventID(resultID, orderID string) string {
	digest := sha256.Sum256([]byte(resultID + "\x00" + orderID))
	return "cqa_evt_" + base64.RawURLEncoding.EncodeToString(digest[:18])
}

func orderEventFromOutcome(row orderEventRow, outcome storedOrderOutcome, cutoff time.Time) (abzOrderEvent, bool) {
	validStatus := map[string]bool{"confirmed": true, "cancelled": true, "refunded": true, "review": true}
	orderedAt, err := time.Parse(time.RFC3339Nano, outcome.OrderedAt)
	if err != nil || orderedAt.Before(cutoff) || row.PageID == "" || row.PSID == "" || outcome.OrderID == "" || outcome.OrderRef == "" || !validStatus[outcome.Status] || len(outcome.EvidenceMessageIDs) == 0 || len(outcome.EvidenceMessageIDs) > 2 {
		return abzOrderEvent{}, false
	}
	return abzOrderEvent{
		SourceEventID: sourceOrderEventID(row.ResultID, outcome.OrderID),
		OrderID:       outcome.OrderID, OrderRef: outcome.OrderRef,
		PageID: row.PageID, PSID: row.PSID, ValueVND: outcome.ValueVND,
		Status: outcome.Status, OrderedAt: outcome.OrderedAt,
		ObservedAt:         row.CreatedAt.UTC().Format(time.RFC3339Nano),
		EvidenceMessageIDs: append([]string(nil), outcome.EvidenceMessageIDs...),
		Confidence:         outcome.Confidence,
	}, true
}

func paginateOrderEvents(rows []orderEventRow, cursor orderEventCursor, limit int, cutoff time.Time, fetchedExtraRow bool) ([]abzOrderEvent, string, bool) {
	events := make([]abzOrderEvent, 0, limit)
	nextCursor := ""
	hasMore := fetchedExtraRow
	for _, row := range rows {
		var detail storedInsightDetail
		if json.Unmarshal([]byte(row.Detail), &detail) != nil {
			nextCursor = encodeOrderEventCursor(row, -1)
			continue
		}
		start := 0
		if row.ResultID == cursor.ID && row.CreatedAt.UTC().Format(time.RFC3339Nano) == cursor.CreatedAt {
			start = cursor.Index + 1
		}
		for outcomeIndex := start; outcomeIndex < len(detail.OrderOutcomes); outcomeIndex++ {
			if len(events) >= limit {
				hasMore = true
				return events, nextCursor, hasMore
			}
			nextCursor = encodeOrderEventCursor(row, outcomeIndex)
			if event, ok := orderEventFromOutcome(row, detail.OrderOutcomes[outcomeIndex], cutoff); ok {
				events = append(events, event)
			}
		}
		if len(detail.OrderOutcomes) == 0 {
			nextCursor = encodeOrderEventCursor(row, -1)
		}
	}
	return events, nextCursor, hasMore
}

// ListABZOrderEvents exports only compact, validated order outcomes. It never
// returns transcript text, customer names, phone numbers or addresses.
func ListABZOrderEvents(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	tenantID := middleware.GetTenantID(c)
	cursor, after, ok := decodeOrderEventCursor(c.Query("after"))
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_cursor"})
		return
	}
	limit, err := strconv.Atoi(c.DefaultQuery("limit", strconv.Itoa(defaultOrderEventLimit)))
	if err != nil || limit < 1 || limit > maxOrderEventLimit {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_limit"})
		return
	}
	pageIDs, validPageIDs := parseOrderEventPageIDs(c.Query("page_ids"))
	if !validPageIDs {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_page_ids"})
		return
	}

	query := db.DB.Table("job_results AS jr").
		Select(`jr.id AS result_id,jr.detail,jr.created_at,
			COALESCE(mia.page_id,ch.external_id) AS page_id,
			COALESCE(mia.psid,c.external_user_id) AS psid`).
		Joins("JOIN conversations AS c ON c.id=jr.conversation_id AND c.tenant_id=jr.tenant_id").
		Joins("JOIN channels AS ch ON ch.id=c.channel_id AND ch.tenant_id=c.tenant_id AND ch.channel_type='facebook' AND ch.is_active=TRUE").
		Joins("LEFT JOIN messenger_intake_attributions AS mia ON mia.conversation_id=c.id AND mia.tenant_id=c.tenant_id").
		Where("jr.tenant_id=? AND jr.result_type='conversation_insight' AND c.last_message_at>=?", tenantID, time.Now().UTC().AddDate(0, 0, -psidRetentionDays)).
		Where("COALESCE(mia.page_id,ch.external_id) IN ?", pageIDs)
	if !after.IsZero() {
		query = query.Where("(jr.created_at>? OR (jr.created_at=? AND jr.id>=?))", after, after, cursor.ID)
	}
	var rows []orderEventRow
	if err := query.Order("jr.created_at ASC,jr.id ASC").Limit(limit + 1).Scan(&rows).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "order_events_unavailable"})
		return
	}
	fetchedExtraRow := len(rows) > limit
	if fetchedExtraRow {
		rows = rows[:limit]
	}
	events, nextCursor, hasMore := paginateOrderEvents(rows, cursor, limit, time.Now().UTC().AddDate(0, 0, -psidRetentionDays), fetchedExtraRow)
	c.JSON(http.StatusOK, gin.H{
		"events": events, "next_cursor": nextCursor, "has_more": hasMore,
	})
}
