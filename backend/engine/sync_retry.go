package engine

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/vietbui/chat-quality-agent/channels"
)

const (
	// zaloConversationLimit caps the conversations one Zalo OA sync reads.
	zaloConversationLimit = 100
	// maxConversationSyncAttempts is how many runs in a row a conversation may
	// fail before the sync stops retrying it. With the usual 15-30 minute
	// interval that is 1-2 hours.
	maxConversationSyncAttempts = 4
)

// syncRetryEntry is one conversation whose last sync failed: the window to
// re-read and enough of the listing to sync it when it is no longer listed.
type syncRetryEntry struct {
	Since          time.Time              `json:"since"`
	Attempts       int                    `json:"attempts"`
	LastError      string                 `json:"last_error,omitempty"`
	ExternalUserID string                 `json:"external_user_id,omitempty"`
	CustomerName   string                 `json:"customer_name,omitempty"`
	LastMessageAt  time.Time              `json:"last_message_at"`
	Metadata       map[string]interface{} `json:"metadata,omitempty"`
}

// syncWorkItem is one conversation to sync in this run.
type syncWorkItem struct {
	conv     channels.SyncedConversation
	since    time.Time
	attempts int // failed runs before this one
}

type conversationSyncFailure struct {
	ExternalID string
	Err        error
}

// parseSyncRetryState reads channels.sync_retry_state. A missing or unreadable
// value means no conversation is waiting for a retry.
func parseSyncRetryState(raw *string) map[string]syncRetryEntry {
	retries := map[string]syncRetryEntry{}
	if raw == nil || strings.TrimSpace(*raw) == "" || *raw == "null" {
		return retries
	}
	if err := json.Unmarshal([]byte(*raw), &retries); err != nil {
		return map[string]syncRetryEntry{}
	}
	return retries
}

// encodeSyncRetryState returns the value to store; "" clears the column.
func encodeSyncRetryState(retries map[string]syncRetryEntry) *string {
	empty := ""
	if len(retries) == 0 {
		return &empty
	}
	out, err := json.Marshal(retries)
	if err != nil {
		return &empty
	}
	value := string(out)
	return &value
}

// planSyncWork lists this run's conversations: the listed ones read from
// `since`, or from their own older window when an earlier run failed them,
// followed by failed conversations that are no longer listed.
func planSyncWork(listed []channels.SyncedConversation, retries map[string]syncRetryEntry, since time.Time) []syncWorkItem {
	work := make([]syncWorkItem, 0, len(listed)+len(retries))
	seen := make(map[string]bool, len(listed))
	for _, conv := range listed {
		if seen[conv.ExternalID] {
			continue
		}
		seen[conv.ExternalID] = true
		item := syncWorkItem{conv: conv, since: since}
		if retry, ok := retries[conv.ExternalID]; ok {
			item.attempts = retry.Attempts
			if !retry.Since.IsZero() && retry.Since.Before(since) {
				item.since = retry.Since
			}
		}
		work = append(work, item)
	}

	ids := make([]string, 0, len(retries))
	for id := range retries {
		if !seen[id] && id != "" {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	for _, id := range ids {
		retry := retries[id]
		retrySince := retry.Since
		if retrySince.IsZero() {
			retrySince = since
		}
		work = append(work, syncWorkItem{
			conv: channels.SyncedConversation{
				ExternalID:     id,
				ExternalUserID: retry.ExternalUserID,
				CustomerName:   retry.CustomerName,
				LastMessageAt:  retry.LastMessageAt,
				Metadata:       retry.Metadata,
			},
			since:    retrySince,
			attempts: retry.Attempts,
		})
	}
	return work
}

var syncFailureDigits = regexp.MustCompile(`\b\d{6,}\b`)

// summarizeSyncFailures groups failures by error text, most frequent first,
// so the sync status shows the actual Zalo/Facebook error, e.g.
// `zalo api error -1: Error fetching messages: -2 ×1 [1052875424724271277]`.
func summarizeSyncFailures(failures []conversationSyncFailure) string {
	type group struct {
		message string
		ids     []string
	}
	var groups []*group
	byMessage := map[string]*group{}
	for _, f := range failures {
		msg := "unknown error"
		if f.Err != nil {
			// Long ids in the error text would split identical errors.
			msg = syncFailureDigits.ReplaceAllString(f.Err.Error(), "…")
		}
		g, ok := byMessage[msg]
		if !ok {
			g = &group{message: msg}
			byMessage[msg] = g
			groups = append(groups, g)
		}
		g.ids = append(g.ids, f.ExternalID)
	}
	sort.SliceStable(groups, func(i, j int) bool { return len(groups[i].ids) > len(groups[j].ids) })

	var parts []string
	for i, g := range groups {
		if i == 3 {
			parts = append(parts, fmt.Sprintf("+%d more error kinds", len(groups)-3))
			break
		}
		ids := g.ids
		suffix := ""
		if len(ids) > 3 {
			suffix = fmt.Sprintf(", +%d", len(ids)-3)
			ids = ids[:3]
		}
		parts = append(parts, fmt.Sprintf("%s ×%d [%s%s]", truncateRunes(g.message, 160), len(g.ids), strings.Join(ids, ", "), suffix))
	}
	return strings.Join(parts, "; ")
}
