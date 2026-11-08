package messengerintake

import (
	"testing"
	"time"
)

func TestValidCapturedAtRejectsImplausibleTimes(t *testing.T) {
	now := time.Date(2026, 11, 8, 2, 0, 0, 0, time.UTC)
	good := now.Add(-time.Minute)
	for name, tc := range map[string]struct{ in, want time.Time }{
		"zero":           {time.Time{}, now},
		"seconds as ms":  {time.UnixMilli(good.Unix()), now},
		"far future":     {now.Add(time.Hour), now},
		"plausible time": {good, good},
	} {
		if got := validCapturedAt(tc.in, now); !got.Equal(tc.want) {
			t.Fatalf("%s: got %s want %s", name, got, tc.want)
		}
	}
}
