package servicequality

import (
	"testing"
	"time"
)

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
