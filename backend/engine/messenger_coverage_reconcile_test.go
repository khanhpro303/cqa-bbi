package engine

import (
	"testing"
	"time"

	"github.com/vietbui/chat-quality-agent/messengerjourney"
)

func TestInsightProvesCursor(t *testing.T) {
	read := time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)
	saved := read.Add(5 * time.Minute)
	detail := `{"source_last_message_at":"2026-11-01T10:00:00Z"}`
	cursor := func(last, stored time.Time, count int64) messengerjourney.MessageCursor {
		return messengerjourney.MessageCursor{SourceLastMessageAt: &last, LastStoredAt: &stored, SourceLastMessageID: "m", SourceMessageCount: count}
	}
	cases := []struct {
		name    string
		detail  string
		current messengerjourney.MessageCursor
		want    bool
	}{
		{"insight read every stored message", detail, cursor(read, read.Add(time.Minute), 3), true},
		{"newer message after insight", detail, cursor(read.Add(time.Second), read.Add(time.Minute), 4), false},
		{"older message stored after insight", detail, cursor(read, saved.Add(time.Second), 4), false},
		{"insight without source cursor", `{}`, cursor(read, read, 3), false},
		{"broken detail", `not-json`, cursor(read, read, 3), false},
		{"no stored messages", detail, messengerjourney.MessageCursor{}, false},
	}
	for _, tc := range cases {
		if got := insightProvesCursor(tc.detail, saved, tc.current); got != tc.want {
			t.Errorf("%s: got %v want %v", tc.name, got, tc.want)
		}
	}
}
