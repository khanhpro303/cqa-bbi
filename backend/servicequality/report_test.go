package servicequality

import (
	"testing"
	"time"

	"github.com/vietbui/chat-quality-agent/db/models"
)

func TestQualityAnalysesForRunsBuildsLatestQCDetailsOnly(t *testing.T) {
	evaluatedAt := time.Date(2026, 9, 17, 8, 0, 0, 0, time.UTC)
	evaluations := []qualityEvaluationRecord{
		{JobResult: models.JobResult{ConversationID: "conversation-1", JobRunID: "qc-run", Severity: "FAIL", Evidence: "Cần cải thiện", Detail: `{"score":20}`, CreatedAt: evaluatedAt}, JobName: "Chất lượng CSKH"},
		{JobResult: models.JobResult{ConversationID: "conversation-2", JobRunID: "qc-run-2", Severity: "PASS", Evidence: "Đạt yêu cầu", Detail: `{"score":100}`, CreatedAt: evaluatedAt}, JobName: "Chất lượng CSKH"},
	}
	results := []models.JobResult{
		{ConversationID: "conversation-1", JobRunID: "qc-run", Severity: "CAN_CAI_THIEN", RuleName: "Chất lượng nội dung", Evidence: "Chúng tôi sẽ sớm trả lời.", Detail: `{"explanation":"Không trả lời trọng tâm.","suggestion":"Cung cấp giá cụ thể."}`},
		{ConversationID: "conversation-1", JobRunID: "old-run", RuleName: "Kết quả cũ", Detail: `{}`},
		{ConversationID: "conversation-2", JobRunID: "qc-run", RuleName: "Sai hội thoại", Detail: `{}`},
	}

	got := qualityAnalysesForRuns(evaluations, results)
	analysis := got["conversation-1"]
	if analysis == nil || analysis.Score == nil || *analysis.Score != 20 || analysis.Verdict != "FAIL" || analysis.JobName != "Chất lượng CSKH" {
		t.Fatalf("unexpected quality analysis: %#v", analysis)
	}
	if len(analysis.Violations) != 1 {
		t.Fatalf("violations = %d; want 1", len(analysis.Violations))
	}
	violation := analysis.Violations[0]
	if violation.RuleName != "Chất lượng nội dung" || violation.Explanation != "Không trả lời trọng tâm." || violation.Suggestion != "Cung cấp giá cụ thể." {
		t.Fatalf("unexpected violation: %#v", violation)
	}
	if len(got["conversation-2"].Violations) != 0 {
		t.Fatalf("cross-conversation violation leaked into analysis: %#v", got["conversation-2"].Violations)
	}
}

func TestQualityAnalysisFreshnessUsesSnapshotThenEvaluationTimeFallback(t *testing.T) {
	evaluatedAt := time.Date(2026, 9, 17, 8, 0, 0, 0, time.UTC)
	analysis := &QualityAnalysis{EvaluatedAt: evaluatedAt}
	before := evaluatedAt.Add(-time.Second)
	after := evaluatedAt.Add(time.Second)

	if isQualityAnalysisStale(nil, &after) {
		t.Fatal("missing analysis must not be marked stale")
	}
	if isQualityAnalysisStale(analysis, &before) {
		t.Fatal("analysis newer than the last message must be fresh")
	}
	if !isQualityAnalysisStale(analysis, &after) {
		t.Fatal("legacy analysis must be stale when a newer message exists")
	}

	snapshotAt := evaluatedAt.Add(-10 * time.Minute)
	analysis.Detail = `{"source_last_message_at":"2026-09-17T07:50:00Z"}`
	if !isQualityAnalysisStale(analysis, &evaluatedAt) {
		t.Fatal("analysis must be stale when a message arrived after its AI snapshot")
	}
	if isQualityAnalysisStale(analysis, &snapshotAt) {
		t.Fatal("analysis whose snapshot includes the latest message must be fresh")
	}
}

func TestIsInsightStale(t *testing.T) {
	last := time.Date(2026, 9, 16, 8, 30, 0, 0, time.UTC)
	tests := []struct {
		name   string
		detail string
		last   *time.Time
		want   bool
	}{
		{name: "legacy result has no source timestamp", detail: `{}`, last: &last, want: true},
		{name: "invalid result is stale", detail: `{`, last: &last, want: true},
		{name: "new message after analysis", detail: `{"source_last_message_at":"2026-09-16T08:00:00Z"}`, last: &last, want: true},
		{name: "source matches latest message", detail: `{"source_last_message_at":"2026-09-16T08:30:00Z"}`, last: &last, want: false},
		{name: "conversation has no latest message", detail: `{"source_last_message_at":"2026-09-16T08:30:00Z"}`, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsInsightStale(tt.detail, tt.last); got != tt.want {
				t.Fatalf("IsInsightStale() = %v; want %v", got, tt.want)
			}
		})
	}
}

func TestDateWindowDefaultsToLastSevenVietnamCalendarDays(t *testing.T) {
	now := time.Date(2026, 9, 12, 17, 30, 0, 0, time.UTC) // 00:30 on Sep 13 in Vietnam.
	from, to, err := DateWindow("", "", now, DefaultPolicy())
	if err != nil {
		t.Fatalf("DateWindow() error = %v", err)
	}
	if got, want := from.Format(time.RFC3339), "2026-09-07T00:00:00+07:00"; got != want {
		t.Fatalf("default from = %s; want %s", got, want)
	}
	if got, want := to.Format(time.RFC3339), "2026-09-14T00:00:00+07:00"; got != want {
		t.Fatalf("default to = %s; want %s", got, want)
	}
	if got, want := from.UTC().Format(time.RFC3339), "2026-09-06T17:00:00Z"; got != want {
		t.Fatalf("default UTC boundary = %s; want %s", got, want)
	}
}

func TestDateWindowExplicitDatesAreInclusiveLocalCalendarDates(t *testing.T) {
	now := vnTime(t, "2026-09-13T12:00")
	from, to, err := DateWindow("2026-09-12", "2026-09-12", now, DefaultPolicy())
	if err != nil {
		t.Fatalf("DateWindow() error = %v", err)
	}
	if got, want := from.Format(time.RFC3339), "2026-09-12T00:00:00+07:00"; got != want {
		t.Fatalf("from = %s; want %s", got, want)
	}
	if got, want := to.Format(time.RFC3339), "2026-09-13T00:00:00+07:00"; got != want {
		t.Fatalf("exclusive to = %s; want %s", got, want)
	}
}

func TestDateWindowRejectsInvalidRanges(t *testing.T) {
	now := vnTime(t, "2026-09-13T12:00")
	tests := []struct {
		name string
		from string
		to   string
	}{
		{name: "invalid start", from: "13-09-2026", to: "2026-09-13"},
		{name: "invalid end", from: "2026-09-13", to: "tomorrow"},
		{name: "reversed", from: "2026-09-13", to: "2026-09-12"},
		{name: "future start", from: "2026-09-14", to: "2026-09-14"},
		{name: "more than 90 inclusive days", from: "2026-01-01", to: "2026-04-01"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, _, err := DateWindow(tt.from, tt.to, now, DefaultPolicy()); err == nil {
				t.Fatal("DateWindow() error = nil; want invalid range error")
			}
		})
	}
}
