package messengerlabels

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/vietbui/chat-quality-agent/channels"
	"github.com/vietbui/chat-quality-agent/db/models"
)

func TestClassifyRequiresCompleteFreshRead(t *testing.T) {
	now := time.Date(2026, 9, 13, 8, 0, 0, 0, time.UTC)
	fresh := now.Add(-time.Minute)
	stale := now.Add(-31 * time.Minute)
	future := now.Add(time.Minute)
	rules := []Rule{{"11", "qualified"}, {"12", "potential"}, {"13", "unqualified"}, {"14", "qualified"}}
	for _, tc := range []struct {
		name    string
		labels  []channels.FacebookLabel
		status  string
		checked *time.Time
		ready   bool
		want    string
	}{
		{"empty fresh is unclassified", []channels.FacebookLabel{}, "success", &fresh, true, "unclassified"},
		{"irrelevant label is unclassified", []channels.FacebookLabel{{ID: "99", Name: "VIP"}}, "success", &fresh, true, "unclassified"},
		{"qualified", []channels.FacebookLabel{{ID: "11", Name: "Renamed"}}, "success", &fresh, true, "qualified"},
		{"unqualified", []channels.FacebookLabel{{ID: "13", Name: "Không phù hợp"}}, "success", &fresh, true, "unqualified"},
		{"potential", []channels.FacebookLabel{{ID: "12", Name: "Tiềm năng"}}, "success", &fresh, true, "potential"},
		{"two categories conflict", []channels.FacebookLabel{{ID: "11"}, {ID: "12"}}, "success", &fresh, true, "conflict"},
		{"two same category labels", []channels.FacebookLabel{{ID: "11"}, {ID: "14"}}, "success", &fresh, true, "qualified"},
		{"unread", nil, "", nil, true, "unknown"},
		{"error never means empty", nil, "error", &fresh, true, "unknown"},
		{"stale empty", nil, "success", &stale, true, "unknown"},
		{"future", nil, "success", &future, true, "unknown"},
		{"disabled or invalid config", nil, "success", &fresh, false, "unknown"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := Classify(tc.labels, tc.status, tc.checked, rules, tc.ready, now); got != tc.want {
				t.Fatalf("got %s want %s", got, tc.want)
			}
		})
	}
}

func TestPolicyUsesUniqueKnownIDs(t *testing.T) {
	catalog := []channels.FacebookLabel{{ID: "11", Name: "Phù hợp"}, {ID: "12", Name: "Tiềm năng"}}
	for _, tc := range []struct {
		policy Policy
		valid  bool
	}{
		{Policy{Enabled: false}, true},
		{Policy{Enabled: true}, false},
		{Policy{true, []Rule{{"11", "qualified"}, {"12", "potential"}}}, true},
		{Policy{true, []Rule{{"missing", "qualified"}}}, false},
		{Policy{true, []Rule{{"99", "qualified"}}}, false},
		{Policy{true, []Rule{{"11", "wrong"}}}, false},
		{Policy{true, []Rule{{"11", "qualified"}, {"11", "potential"}}}, false},
		{Policy{true, []Rule{{"11", "qualified"}, {"11", "qualified"}}}, false},
		{Policy{false, []Rule{{"99", "qualified"}}}, true},
	} {
		if got := tc.policy.Validate(catalog) == nil; got != tc.valid {
			t.Fatalf("policy %+v valid=%v", tc.policy, got)
		}
	}
}

func TestParticipantPSIDIgnoresLegacyConversationUserID(t *testing.T) {
	for _, tc := range []struct {
		metadata string
		want     string
	}{
		{`{"participants":{"data":[{"id":"100"},{"id":"200"}]}}`, "200"},
		{`{"participants":{"data":[{"id":"100"},{"id":"200"},{"id":"200"}]}}`, "200"},
		{`{"participants":{"data":[{"id":"200"}],"paging":{"cursors":{"after":"terminal"}}}}`, "200"},
		{`{"participants":{"data":[{"id":"200"}],"paging":{"next":"more"}}}`, ""},
		{`{"participants":{"data":[{"id":"200"},{"id":"300"}]}}`, ""},
		{`{"participants":{"data":[{"id":"100"}]}}`, ""},
		{`{"participants":{"data":[{"id":""}]}}`, ""},
		{`invalid`, ""},
	} {
		got := ParticipantPSID(models.Conversation{ExternalUserID: "incorrect-conversation-id", Metadata: tc.metadata}, "100")
		if got != tc.want {
			t.Fatalf("got %q for %s", got, tc.metadata)
		}
	}
}

type fakeReader struct {
	mu       sync.Mutex
	calls    []string
	labels   map[string][]channels.FacebookLabel
	errors   map[string]error
	resolved string
}

func (f *fakeReader) FetchPageLabels(context.Context) ([]channels.FacebookLabel, error) {
	return []channels.FacebookLabel{}, nil
}
func (f *fakeReader) FetchUserLabels(_ context.Context, id string) ([]channels.FacebookLabel, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, id)
	return f.labels[id], f.errors[id]
}
func (f *fakeReader) ResolveConversationPSID(context.Context, string) (string, error) {
	return f.resolved, nil
}

func TestObserveReadsOldConversationAndHandlesRemovedLabels(t *testing.T) {
	old := time.Now().AddDate(-1, 0, 0)
	convs := []models.Conversation{{ID: "conv", TenantID: "tenant", ChannelID: "channel", ExternalConversationID: "thread", LastMessageAt: &old, Metadata: `{"participants":{"data":[{"id":"100"},{"id":"200"}]}}`}}
	fake := &fakeReader{labels: map[string][]channels.FacebookLabel{"200": {}}, errors: map[string]error{}}
	var saved models.MessengerLabelSnapshot
	good, bad, err := Observe(context.Background(), fake, convs, "100", func(s models.MessengerLabelSnapshot) error { saved = s; return nil })
	if err != nil || good != 1 || bad != 0 || saved.Labels != "[]" || saved.Status != "success" || saved.CheckedAt == nil || saved.PSID != "200" {
		t.Fatalf("result %d %d %v, snapshot %+v", good, bad, err, saved)
	}
	if len(fake.calls) != 1 || fake.calls[0] != "200" {
		t.Fatalf("must read PSID even without new messages: %+v", fake.calls)
	}
}

func TestObserveDoesNotConvertFailuresToUnclassified(t *testing.T) {
	for _, kind := range []string{"unsupported", "permission", "rate_limit", "auth"} {
		t.Run(kind, func(t *testing.T) {
			fake := &fakeReader{resolved: "200", errors: map[string]error{"200": &channels.LabelAPIError{Kind: kind}}}
			var saved models.MessengerLabelSnapshot
			good, bad, err := Observe(context.Background(), fake, []models.Conversation{{ID: "conv", TenantID: "tenant", ChannelID: "channel", ExternalConversationID: "thread"}}, "100", func(s models.MessengerLabelSnapshot) error { saved = s; return nil })
			if good != 0 || bad != 1 || saved.Status != "error" || saved.CheckedAt != nil || saved.ErrorKind != kind {
				t.Fatalf("%d %d %v %+v", good, bad, err, saved)
			}
			if (err != nil) != (kind != "unsupported") {
				t.Fatalf("fatal status wrong: %v", err)
			}
		})
	}
}

func TestObservePropagatesStorageFailure(t *testing.T) {
	fake := &fakeReader{resolved: "200", labels: map[string][]channels.FacebookLabel{"200": {}}, errors: map[string]error{}}
	want := errors.New("database failed")
	_, _, err := Observe(context.Background(), fake, []models.Conversation{{ID: "conv"}}, "100", func(models.MessengerLabelSnapshot) error { return want })
	if !errors.Is(err, want) {
		t.Fatalf("got %v", err)
	}
}

func TestCountsAreDisjoint(t *testing.T) {
	counts := Counts{}
	for _, s := range []string{"unclassified", "qualified", "unqualified", "potential", "conflict", "unknown", "unknown"} {
		counts.Add(s)
	}
	if counts.Total != 7 || counts.Unknown != 2 || counts.Total != counts.Unclassified+counts.Qualified+counts.Unqualified+counts.Potential+counts.Conflict+counts.Unknown {
		t.Fatalf("invalid counts %+v", counts)
	}
}
