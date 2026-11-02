package handlers

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestExportOrderEventsReturnsSanitizedStableContract(t *testing.T) {
	created := time.Date(2026, 11, 2, 4, 5, 6, 0, time.UTC)
	rows := []orderEventRow{{
		ResultID: "result-1", CreatedAt: created, PageID: "page-1", PSID: "psid-1",
		Detail: `{"summary":"sensitive transcript omitted","order_outcomes":[{"order_id":"cqa_order_1","order_ref":"DH-01","value_vnd":450000,"status":"confirmed","ordered_at":"2026-11-02T03:00:00Z","evidence_message_ids":["msg-1","msg-2"],"confidence":0.96}]}`,
	}}
	events, _, _ := paginateOrderEvents(rows, orderEventCursor{Index: -1}, 50, time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC), false)
	if len(events) != 1 {
		t.Fatalf("expected one event: %#v", events)
	}
	event := events[0]
	if event.SourceEventID == "" || event.OrderID != "cqa_order_1" || event.OrderRef != "DH-01" || event.PageID != "page-1" || event.PSID != "psid-1" || event.ObservedAt != created.Format(time.RFC3339Nano) {
		t.Fatalf("unexpected export event: %#v", event)
	}
	payload, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"sensitive transcript omitted", "summary", "content", "phone", "customer_name"} {
		if strings.Contains(string(payload), forbidden) {
			t.Fatalf("sanitized event leaked %q: %s", forbidden, payload)
		}
	}
}

func TestOrderEventCursorRoundTripAndInvalidInput(t *testing.T) {
	row := orderEventRow{ResultID: "result-9", CreatedAt: time.Date(2026, 11, 2, 4, 5, 6, 123, time.UTC)}
	cursor := encodeOrderEventCursor(row, 7)
	decoded, createdAt, ok := decodeOrderEventCursor(cursor)
	if !ok || decoded.ID != row.ResultID || decoded.Index != 7 || !createdAt.Equal(row.CreatedAt) {
		t.Fatalf("cursor round trip failed: cursor=%#v time=%v ok=%v", decoded, createdAt, ok)
	}
	if _, _, ok := decodeOrderEventCursor("not-base64!"); ok {
		t.Fatal("malformed cursor must fail closed")
	}
}

func TestParseOrderEventPageIDsRequiresBoundedExactIDs(t *testing.T) {
	got, ok := parseOrderEventPageIDs("123,456")
	if !ok || len(got) != 2 || got[0] != "123" || got[1] != "456" {
		t.Fatalf("unexpected page IDs: %#v ok=%v", got, ok)
	}
	for _, invalid := range []string{"", "123,123", "abc", "0", strings.Repeat("1,", 20) + "1"} {
		if got, ok := parseOrderEventPageIDs(invalid); ok {
			t.Fatalf("invalid page IDs %q accepted: %#v", invalid, got)
		}
	}
}

func TestPaginateOrderEventsCapsExpandedOutcomesWithoutSkipping(t *testing.T) {
	outcomes := make([]storedOrderOutcome, 0, 25)
	for index := 0; index < 25; index++ {
		outcomes = append(outcomes, storedOrderOutcome{
			OrderID: "order-" + strconv.Itoa(index), OrderRef: "DH-" + strconv.Itoa(index),
			Status: "review", OrderedAt: "2026-11-02T03:00:00Z",
			EvidenceMessageIDs: []string{"message-" + strconv.Itoa(index)}, Confidence: .8,
		})
	}
	detail, _ := json.Marshal(storedInsightDetail{OrderOutcomes: outcomes})
	row := orderEventRow{ResultID: "result-many", CreatedAt: time.Date(2026, 11, 2, 4, 0, 0, 0, time.UTC), PageID: "123", PSID: "456", Detail: string(detail)}
	first, cursorRaw, more := paginateOrderEvents([]orderEventRow{row}, orderEventCursor{Index: -1}, 20, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), false)
	if len(first) != 20 || !more || cursorRaw == "" {
		t.Fatalf("first page mismatch: events=%d more=%v cursor=%q", len(first), more, cursorRaw)
	}
	cursor, _, ok := decodeOrderEventCursor(cursorRaw)
	if !ok || cursor.Index != 19 {
		t.Fatalf("unexpected expanded cursor: %#v ok=%v", cursor, ok)
	}
	second, _, more := paginateOrderEvents([]orderEventRow{row}, cursor, 20, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), false)
	if len(second) != 5 || more || second[0].OrderID != "order-20" {
		t.Fatalf("second page skipped or duplicated outcomes: %#v more=%v", second, more)
	}
}

func TestOrderEventRetentionUsesOriginalOrderedAt(t *testing.T) {
	row := orderEventRow{ResultID: "new-analysis", CreatedAt: time.Date(2026, 11, 2, 4, 0, 0, 0, time.UTC), PageID: "123", PSID: "456", Detail: `{"order_outcomes":[{"order_id":"old","order_ref":"old","status":"review","ordered_at":"2025-01-01T00:00:00Z","evidence_message_ids":["old-msg"],"confidence":0}]}`}
	events, _, _ := paginateOrderEvents([]orderEventRow{row}, orderEventCursor{Index: -1}, 20, time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC), false)
	if len(events) != 0 {
		t.Fatalf("new reanalysis must not re-export a PSID whose order evidence is past retention: %#v", events)
	}
}
