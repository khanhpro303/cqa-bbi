package engine

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/vietbui/chat-quality-agent/db/models"
	"github.com/vietbui/chat-quality-agent/servicequality"
)

// AdBazooka rejects a whole journey page when one insight breaks its bounds,
// so the export must drop or clamp values it would refuse.
func TestInsightPayloadStaysInsideAdBazookaBounds(t *testing.T) {
	emojiName := strings.Repeat("a", 59) + "🙂" // 60 runes, 61 UTF-16 units
	detail, _ := json.Marshal(map[string]any{
		"intents": []string{"hỏi giá"},
		"products": []map[string]any{
			{"name": emojiName}, {"name": "\uFEFF"}, {"name": "  \uFEFFLS2 FF353\uFEFF "}, {"name": strings.Repeat("ữ", 60)},
		},
		"lead_quality": map[string]any{"level": "high"},
	})
	start := time.Date(2026, 1, 5, 2, 0, 0, 0, time.UTC) // 09:00 Asia/Ho_Chi_Minh
	messages := []models.Message{
		{ID: "m1", SenderType: "customer", Content: "giá bao nhiêu", SentAt: start},
		{ID: "m2", SenderType: "agent", Content: "dạ", SentAt: start.Add(400 * 24 * time.Hour)},
	}
	raw, err := messengerInsightPayload(string(detail), messages, servicequality.DefaultPolicy())
	if err != nil {
		t.Fatal(err)
	}
	var payload struct {
		Products []string `json:"products"`
		Seconds  *int64   `json:"first_response_seconds"`
		Status   string   `json:"first_reply_status"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatal(err)
	}
	if strings.Join(payload.Products, "|") != "LS2 FF353|"+strings.Repeat("ữ", 60) {
		t.Fatalf("products = %q", payload.Products)
	}
	if payload.Status != "answered" || payload.Seconds == nil || *payload.Seconds != maxInsightResponseSeconds {
		t.Fatalf("first reply = %s %v, want answered clamped to %d", payload.Status, payload.Seconds, maxInsightResponseSeconds)
	}
}

func insightPayloadFields(t *testing.T, detail string) map[string]any {
	t.Helper()
	raw, err := messengerInsightPayload(detail, nil, servicequality.DefaultPolicy())
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatal(err)
	}
	return payload
}

// The analyzer saves a keyword-resolved need in "need" with need_source
// "keyword". The export must keep that source, not relabel it as AI.
func TestInsightPayloadKeepsStoredNeedSource(t *testing.T) {
	for _, tc := range []struct{ detail, need, source string }{
		{`{"intents":["hỏi giá"],"need":"price","need_source":"keyword"}`, "price", "keyword"},
		{`{"intents":["hỏi giá"],"need":"stock","need_source":"ai"}`, "stock", "ai"},
		{`{"intents":["hỏi giá"]}`, "price", "keyword"},
		{`{"intents":["hỏi giá"],"need":"bogus","need_source":"ai"}`, "price", "keyword"},
		{`{"intents":["hỏi giá"],"need":"advice"}`, "advice", "ai"},
	} {
		payload := insightPayloadFields(t, tc.detail)
		if payload["need"] != tc.need || payload["need_source"] != tc.source {
			t.Fatalf("%s: need=%v source=%v, want %s %s", tc.detail, payload["need"], payload["need_source"], tc.need, tc.source)
		}
	}
}

// Old insights with another field shape still export the readable fields.
func TestInsightPayloadReadsOldShapesFieldByField(t *testing.T) {
	payload := insightPayloadFields(t, `{"intents":"hỏi giá","lead_quality":"high","products":[{"name":"LS2 FF353"},"Yohe 851",{"name":7}],"feedback":{"category":"giá"},"need":["price"],"source_last_message_at":"not a time"}`)
	if payload["lead_level"] != "unknown" || payload["need"] != "unknown" || payload["need_source"] != "keyword" {
		t.Fatalf("payload = %v", payload)
	}
	if products, _ := payload["products"].([]any); len(products) != 1 || products[0] != "LS2 FF353" {
		t.Fatalf("products = %v", payload["products"])
	}
	for _, detail := range []string{`not json`, `null`, `["a"]`, `"text"`} {
		if _, err := messengerInsightPayload(detail, nil, servicequality.DefaultPolicy()); err == nil {
			t.Fatalf("%s: want errInsightDetail", detail)
		}
	}
}
