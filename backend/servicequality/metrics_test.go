package servicequality

import (
	"testing"
	"time"

	"github.com/vietbui/chat-quality-agent/db/models"
)

func vnTime(t *testing.T, value string) time.Time {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339, value+":00+07:00")
	if err != nil {
		t.Fatalf("parse test time %q: %v", value, err)
	}
	return parsed
}

func customer(id, at, content string) models.Message {
	return models.Message{ID: id, SenderType: "customer", ContentType: "text", Content: content, SentAt: vnTimeForHelper(at)}
}

func sender(id, senderType, at, content string) models.Message {
	return models.Message{ID: id, SenderType: senderType, ContentType: "text", Content: content, SentAt: vnTimeForHelper(at)}
}

func vnTimeForHelper(value string) time.Time {
	parsed, err := time.Parse(time.RFC3339, value+":00+07:00")
	if err != nil {
		panic(err)
	}
	return parsed
}

func TestWorkingSecondsUsesDailyVietnamServiceHours(t *testing.T) {
	p := DefaultPolicy()
	tests := []struct {
		name       string
		start      string
		end        string
		wantSecond float64
	}{
		{name: "before opening", start: "2026-09-13T07:50", end: "2026-09-13T08:10", wantSecond: 10 * 60},
		{name: "after closing", start: "2026-09-13T21:50", end: "2026-09-13T22:30", wantSecond: 10 * 60},
		{name: "overnight excludes closed hours", start: "2026-09-13T21:50", end: "2026-09-14T08:10", wantSecond: 20 * 60},
		{name: "entirely outside hours", start: "2026-09-13T22:00", end: "2026-09-14T08:00", wantSecond: 0},
		{name: "all calendar days are staffed", start: "2026-09-13T21:00", end: "2026-09-15T09:00", wantSecond: 16 * 60 * 60},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := WorkingSeconds(vnTime(t, tt.start), vnTime(t, tt.end), p)
			if got != tt.wantSecond {
				t.Fatalf("WorkingSeconds() = %.0f; want %.0f", got, tt.wantSecond)
			}
		})
	}
}

func TestPolicyValidateRejectsMissingOrInvalidOperationalSettings(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Policy)
	}{
		{name: "blank timezone", mutate: func(p *Policy) { p.Timezone = "  " }},
		{name: "unknown timezone", mutate: func(p *Policy) { p.Timezone = "Moon/Base" }},
		{name: "closing before opening", mutate: func(p *Policy) { p.WorkEnd = "07:59" }},
		{name: "target below one minute", mutate: func(p *Policy) { p.TargetMinutes = 0 }},
		{name: "overdue below target", mutate: func(p *Policy) { p.OverdueMinutes = p.TargetMinutes - 1 }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := DefaultPolicy()
			tt.mutate(&p)
			if err := p.Validate(); err == nil {
				t.Fatal("Policy.Validate() error = nil; want invalid policy error")
			}
		})
	}
}

func TestCalculateGroupsConsecutiveCustomerMessagesFromFirstMessage(t *testing.T) {
	p := DefaultPolicy()
	messages := []models.Message{
		sender("reply", "agent", "2026-09-13T10:05", "Shop trả lời"),
		customer("follow-up", "2026-09-13T10:02", "Màu xanh còn không?"),
		customer("question", "2026-09-13T10:00", "Sản phẩm này còn không?"),
	}

	got := Calculate(messages, nil, vnTime(t, "2026-09-13T10:10"), p)
	if got.Status != "answered" || got.Pending != nil || len(got.Turns) != 1 {
		t.Fatalf("Calculate() status=%q pending=%v turns=%d; want one answered turn", got.Status, got.Pending != nil, len(got.Turns))
	}
	turn := got.Turns[0]
	if turn.StartedAt != vnTime(t, "2026-09-13T10:00") || turn.LastCustomerID != "follow-up" || turn.CustomerMessages != 2 {
		t.Fatalf("grouped turn = %+v; want start at first message and both customer messages", turn)
	}
	if turn.Seconds != 5*60 {
		t.Fatalf("response seconds = %.0f; want 300 from the first customer message", turn.Seconds)
	}
}

func TestCalculateSystemAndBotMessagesDoNotAnswerCustomer(t *testing.T) {
	p := DefaultPolicy()
	messages := []models.Message{
		customer("question", "2026-09-13T10:00", "Còn hàng không?"),
		sender("system", "system", "2026-09-13T10:01", "Conversation assigned"),
		sender("bot", "bot", "2026-09-13T10:02", "Tin nhắn tự động"),
	}

	got := Calculate(messages, nil, vnTime(t, "2026-09-13T10:20"), p)
	if got.Status != "overdue" || got.Pending == nil {
		t.Fatalf("Calculate() status=%q pending=%v; want overdue customer request", got.Status, got.Pending != nil)
	}
	if got.Pending.Seconds != 20*60 {
		t.Fatalf("pending seconds = %.0f; want 1200", got.Pending.Seconds)
	}
}

func TestCalculateIgnoresOnlyExactClosingMessagesAfterPageReply(t *testing.T) {
	p := DefaultPolicy()
	t.Run("exact closing is ignored", func(t *testing.T) {
		messages := []models.Message{
			customer("question", "2026-09-13T10:00", "Còn hàng không?"),
			sender("reply", "agent", "2026-09-13T10:01", "Dạ còn ạ"),
			customer("thanks", "2026-09-13T10:02", "Cảm ơn shop!!!"),
		}
		got := Calculate(messages, nil, vnTime(t, "2026-09-13T10:30"), p)
		if got.Status != "answered" || got.Pending != nil || len(got.Turns) != 1 {
			t.Fatalf("closing message opened a request: status=%q pending=%v turns=%d", got.Status, got.Pending != nil, len(got.Turns))
		}
	})

	t.Run("closing words with a question are retained", func(t *testing.T) {
		messages := []models.Message{
			customer("question", "2026-09-13T10:00", "Còn hàng không?"),
			sender("reply", "agent", "2026-09-13T10:01", "Dạ còn ạ"),
			customer("new-question", "2026-09-13T10:02", "Cảm ơn shop, màu xanh còn không?"),
		}
		got := Calculate(messages, nil, vnTime(t, "2026-09-13T10:30"), p)
		if got.Status != "overdue" || got.Pending == nil || got.Pending.LastCustomerID != "new-question" {
			t.Fatalf("customer question was dropped: status=%q pending=%+v", got.Status, got.Pending)
		}
	})
}

func TestCalculateClosingSuppressionIsLimitedToRecentAnsweredConversation(t *testing.T) {
	p := DefaultPolicy()
	t.Run("closing after thirty minute window opens a request", func(t *testing.T) {
		replyAt := vnTime(t, "2026-09-13T10:01")
		lateThanks := replyAt.Add(30*time.Minute + time.Second)
		messages := []models.Message{
			customer("question", "2026-09-13T10:00", "Còn hàng không?"),
			sender("reply", "agent", "2026-09-13T10:01", "Dạ còn ạ"),
			{ID: "late-thanks", SenderType: "customer", ContentType: "text", Content: "Cảm ơn shop", SentAt: lateThanks},
		}
		got := Calculate(messages, nil, vnTime(t, "2026-09-13T11:00"), p)
		if got.Pending == nil || got.Pending.LastCustomerID != "late-thanks" || got.Status != "overdue" {
			t.Fatalf("late closing message should be retained: status=%q pending=%+v", got.Status, got.Pending)
		}
	})

	t.Run("closing after manual resolution opens a request", func(t *testing.T) {
		through := vnTime(t, "2026-09-13T10:00")
		messages := []models.Message{
			customer("question", "2026-09-13T10:00", "Còn hàng không?"),
			customer("thanks", "2026-09-13T10:11", "Cảm ơn shop"),
		}
		resolutions := []models.ServiceResolution{{ThroughMessageID: "question", ThroughAt: through, ResolvedAt: vnTime(t, "2026-09-13T10:10")}}
		got := Calculate(messages, resolutions, vnTime(t, "2026-09-13T10:30"), p)
		if got.Pending == nil || got.Pending.LastCustomerID != "thanks" || got.Status != "overdue" {
			t.Fatalf("message after manual resolution should reopen queue: status=%q pending=%+v", got.Status, got.Pending)
		}
	})

	t.Run("standalone initial sticker does not create a request", func(t *testing.T) {
		messages := []models.Message{{
			ID: "sticker", SenderType: "customer", ContentType: "sticker", SentAt: vnTime(t, "2026-09-13T10:00"),
		}}
		got := Calculate(messages, nil, vnTime(t, "2026-09-13T10:30"), p)
		if got.Status != "no_request" || got.Pending != nil || len(got.Turns) != 0 {
			t.Fatalf("initial sticker opened a request: %+v", got)
		}
	})
}

func TestCalculateRetainsSingleUnansweredCustomerMessage(t *testing.T) {
	got := Calculate(
		[]models.Message{customer("only-message", "2026-09-13T21:55", "Shop ơi còn hàng không?")},
		nil,
		vnTime(t, "2026-09-14T08:05"),
		DefaultPolicy(),
	)
	if got.Pending == nil || len(got.Turns) != 1 || got.Pending.LastCustomerID != "only-message" {
		t.Fatalf("single customer message was not retained: %+v", got)
	}
	if got.Status != "waiting" || got.Pending.Seconds != 10*60 {
		t.Fatalf("single message status=%q seconds=%.0f; want waiting for 600 staffed seconds", got.Status, got.Pending.Seconds)
	}
}

func TestCalculateResolutionDoesNotCreateReplySampleAndLaterMessageReopens(t *testing.T) {
	p := DefaultPolicy()
	through := vnTime(t, "2026-09-13T10:00")
	resolvedAt := vnTime(t, "2026-09-13T10:10")
	messages := []models.Message{
		customer("old-question", "2026-09-13T10:00", "Còn hàng không?"),
		customer("new-question", "2026-09-13T10:20", "Shop kiểm tra giúp em"),
	}
	resolutions := []models.ServiceResolution{{
		ThroughMessageID: "old-question",
		ThroughAt:        through,
		ResolvedAt:       resolvedAt,
	}}

	got := Calculate(messages, resolutions, vnTime(t, "2026-09-13T10:40"), p)
	if len(got.Turns) != 2 || got.Turns[0].Status != "resolved" || got.Turns[0].RepliedAt != nil {
		t.Fatalf("resolution produced an invalid reply sample: %+v", got.Turns)
	}
	if got.Pending == nil || got.Pending.LastCustomerID != "new-question" || got.Status != "overdue" {
		t.Fatalf("message after resolution did not reopen queue: status=%q pending=%+v", got.Status, got.Pending)
	}
	summary := Summarize([]Timeline{got}, vnTime(t, "2026-09-13T00:00"), vnTime(t, "2026-09-14T00:00"), p)
	if summary.Answered != 0 || summary.OnTimePercent != nil || summary.Resolved != 1 {
		t.Fatalf("summary = %+v; resolution must not count as an answered SLA sample", summary)
	}
}

func TestCalculateOutdatedResolutionMarkerDoesNotResolveNewerMessageAtSameTimestamp(t *testing.T) {
	p := DefaultPolicy()
	at := vnTime(t, "2026-09-13T10:00")
	messages := []models.Message{
		customer("message-a", "2026-09-13T10:00", "Còn hàng không?"),
		customer("message-b", "2026-09-13T10:00", "Màu xanh còn không?"),
	}
	resolutions := []models.ServiceResolution{{
		ThroughMessageID: "message-a",
		ThroughAt:        at,
		ResolvedAt:       vnTime(t, "2026-09-13T10:05"),
	}}

	got := Calculate(messages, resolutions, vnTime(t, "2026-09-13T10:30"), p)
	if len(got.Turns) != 1 || got.Turns[0].Status != "overdue" || got.Turns[0].CustomerMessages != 2 {
		t.Fatalf("outdated marker should not close the grouped customer request; turns=%+v", got.Turns)
	}
	if got.Pending == nil || got.Pending.LastCustomerID != "message-b" || got.Status != "overdue" {
		t.Fatalf("newer same-timestamp message should remain pending: status=%q pending=%+v", got.Status, got.Pending)
	}
}

func TestCalculateRejectsZeroAndFutureTimestamps(t *testing.T) {
	now := vnTime(t, "2026-09-13T10:30")
	messages := []models.Message{
		{ID: "zero", SenderType: "customer", Content: "invalid"},
		customer("future", "2026-09-13T10:31", "future"),
		customer("valid", "2026-09-13T10:20", "valid"),
	}

	got := Calculate(messages, nil, now, DefaultPolicy())
	if got.InvalidTimestamps != 2 {
		t.Fatalf("InvalidTimestamps = %d; want 2", got.InvalidTimestamps)
	}
	if got.Pending == nil || got.Pending.LastCustomerID != "valid" || got.Pending.CustomerMessages != 1 {
		t.Fatalf("invalid timestamp messages entered timeline: %+v", got)
	}
}

func TestCalculateOrderingIsStableForUnsortedEqualTimestampEvents(t *testing.T) {
	for _, messages := range [][]models.Message{
		{
			sender("reply", "agent", "2026-09-13T10:00", "reply"),
			customer("message-b", "2026-09-13T10:00", "second"),
			customer("message-a", "2026-09-13T10:00", "first"),
		},
		{
			customer("message-a", "2026-09-13T10:00", "first"),
			customer("message-b", "2026-09-13T10:00", "second"),
			sender("reply", "agent", "2026-09-13T10:00", "reply"),
		},
	} {
		got := Calculate(messages, nil, vnTime(t, "2026-09-13T10:01"), DefaultPolicy())
		if len(got.Turns) != 1 || got.Turns[0].Status != "answered" || got.Turns[0].Seconds != 0 {
			t.Fatalf("equal timestamp events were not deterministically answered: %+v", got)
		}
		if got.Turns[0].CustomerMessages != 2 || got.Turns[0].LastCustomerID != "message-b" {
			t.Fatalf("equal timestamp customer ordering is unstable: %+v", got.Turns[0])
		}
	}
}

func TestSummarizeUsesAnsweredTurnsForSLAAndLiveStatusesForQueue(t *testing.T) {
	from := vnTime(t, "2026-09-13T00:00")
	to := vnTime(t, "2026-09-14T00:00")
	p := DefaultPolicy()
	timelines := []Timeline{
		{Status: "answered", Turns: []Turn{
			{StartedAt: vnTime(t, "2026-09-13T09:00"), Status: "answered", Seconds: 120, First: true},
			{StartedAt: vnTime(t, "2026-09-13T10:00"), Status: "answered", Seconds: 600},
		}},
		{Status: "waiting", Turns: []Turn{{StartedAt: vnTime(t, "2026-09-01T09:00"), Status: "waiting", Seconds: 10}}},
		{Status: "overdue", Turns: []Turn{{StartedAt: vnTime(t, "2026-09-01T10:00"), Status: "overdue", Seconds: 99999}}},
		{Status: "resolved", Turns: []Turn{{StartedAt: vnTime(t, "2026-09-13T11:00"), Status: "resolved", Seconds: 60, First: true}}},
	}

	got := Summarize(timelines, from, to, p)
	if got.Answered != 2 || got.OnTime != 1 || got.OnTimePercent == nil || *got.OnTimePercent != 50 {
		t.Fatalf("SLA summary = %+v; want 1/2 answered turns on time", got)
	}
	if got.Waiting != 1 || got.Overdue != 1 {
		t.Fatalf("live queue = waiting %d overdue %d; want 1 and 1 independent of report dates", got.Waiting, got.Overdue)
	}
	if got.Resolved != 1 {
		t.Fatalf("resolved = %d; want 1", got.Resolved)
	}
	if got.MedianSeconds == nil || *got.MedianSeconds != 120 || got.P90Seconds == nil || *got.P90Seconds != 600 {
		t.Fatalf("duration percentiles = median %v p90 %v; want 120 and 600", got.MedianSeconds, got.P90Seconds)
	}
	if got.FirstMedianSeconds == nil || *got.FirstMedianSeconds != 120 {
		t.Fatalf("first response median = %v; want 120", got.FirstMedianSeconds)
	}
}
