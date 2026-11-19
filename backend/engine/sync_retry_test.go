package engine

import (
	"fmt"
	"testing"
	"time"

	"github.com/vietbui/chat-quality-agent/channels"
)

func TestPlanSyncWorkUsesRetryWindowAndCarriesUnlistedConversations(t *testing.T) {
	since := time.Now().Add(-time.Hour)
	older := since.Add(-5 * time.Hour)
	retries := map[string]syncRetryEntry{
		"listed":   {Since: older, Attempts: 2},
		"unlisted": {Since: older, Attempts: 1, CustomerName: "Khach"},
	}
	work := planSyncWork([]channels.SyncedConversation{{ExternalID: "new"}, {ExternalID: "listed"}, {ExternalID: "new"}}, retries, since)
	if len(work) != 3 {
		t.Fatalf("work=%+v", work)
	}
	if work[0].conv.ExternalID != "new" || !work[0].since.Equal(since) || work[0].attempts != 0 {
		t.Fatalf("new conversation: %+v", work[0])
	}
	if !work[1].since.Equal(older) || work[1].attempts != 2 {
		t.Fatalf("listed retry should keep its older window: %+v", work[1])
	}
	if work[2].conv.ExternalID != "unlisted" || work[2].conv.CustomerName != "Khach" || !work[2].since.Equal(older) {
		t.Fatalf("unlisted retry: %+v", work[2])
	}
}

func TestSummarizeSyncFailuresGroupsByError(t *testing.T) {
	var failures []conversationSyncFailure
	for i := 0; i < 5; i++ {
		failures = append(failures, conversationSyncFailure{ExternalID: fmt.Sprintf("c%d", i), Err: &channels.ZaloAPIError{Code: -32, Message: "Your OA reached limit call api"}})
	}
	failures = append(failures, conversationSyncFailure{ExternalID: "1052875424724271277", Err: fmt.Errorf("fetch messages: %w", &channels.ZaloAPIError{Code: -1, Message: "Error fetching messages: -2"})})
	got := summarizeSyncFailures(failures)
	want := "zalo api error -32: Your OA reached limit call api ×5 [c0, c1, c2, +2]; fetch messages: zalo api error -1: Error fetching messages: -2 ×1 [1052875424724271277]"
	if got != want {
		t.Fatalf("got  %q\nwant %q", got, want)
	}
}

func TestEncodeSyncRetryStateRoundTrip(t *testing.T) {
	if v := encodeSyncRetryState(nil); v == nil || *v != "" {
		t.Fatal("empty state should clear the column")
	}
	state := encodeSyncRetryState(map[string]syncRetryEntry{"a": {Attempts: 3, LastError: "x"}})
	if back := parseSyncRetryState(state); back["a"].Attempts != 3 {
		t.Fatalf("round trip lost data: %+v", back)
	}
	bad := "{not json"
	if len(parseSyncRetryState(&bad)) != 0 || len(parseSyncRetryState(nil)) != 0 {
		t.Fatal("unreadable state should mean no retries")
	}
}
