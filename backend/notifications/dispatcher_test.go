package notifications

import (
	"strings"
	"testing"
	"time"

	"github.com/vietbui/chat-quality-agent/db/models"
)

// buildNotificationBody must emit HTML <b> tags for telegram/email but plain
// text for zalo (group messages have no HTML rendering).
func TestBuildNotificationBody_BoldByOutputType(t *testing.T) {
	d := NewDispatcher()
	job := models.Job{Name: "Test Job"}
	results := []models.JobResult{
		{ResultType: "qc_violation", Severity: "NGHIEM_TRONG", RuleName: "R1", Evidence: "ev"},
	}

	tests := []struct {
		outputType string
		wantBold   bool
	}{
		{"telegram", true},
		{"email", true},
		{"zalo", false},
	}
	for _, tt := range tests {
		t.Run(tt.outputType, func(t *testing.T) {
			body := d.buildNotificationBody(job, results, tt.outputType)
			hasBold := strings.Contains(body, "<b>")
			if hasBold != tt.wantBold {
				t.Errorf("outputType=%s: <b> present=%v, want %v\nbody=%q", tt.outputType, hasBold, tt.wantBold, body)
			}
			if !strings.Contains(body, "Test Job") {
				t.Errorf("outputType=%s: body missing job name: %q", tt.outputType, body)
			}
		})
	}
}

// buildErrorBody must include the run's error message + job name, and respect the
// plain-text vs HTML rule per output type.
func TestBuildErrorBody(t *testing.T) {
	d := NewDispatcher()
	finished := time.Date(2026, 6, 5, 9, 30, 0, 0, time.UTC)
	job := models.Job{Name: "Job X"}
	run := models.JobRun{Status: "error", ErrorMessage: "ERP timeout", FinishedAt: &finished}

	t.Run("zalo plain", func(t *testing.T) {
		body := d.buildErrorBody(job, run, "zalo")
		if strings.Contains(body, "<b>") {
			t.Errorf("zalo body should be plain text: %q", body)
		}
		for _, want := range []string{"Job X", "ERP timeout"} {
			if !strings.Contains(body, want) {
				t.Errorf("zalo body missing %q: %q", want, body)
			}
		}
	})

	t.Run("telegram bold", func(t *testing.T) {
		body := d.buildErrorBody(job, run, "telegram")
		if !strings.Contains(body, "<b>") {
			t.Errorf("telegram body should contain <b>: %q", body)
		}
		if !strings.Contains(body, "ERP timeout") {
			t.Errorf("telegram body missing error message: %q", body)
		}
	})

	t.Run("empty error message fallback", func(t *testing.T) {
		body := d.buildErrorBody(job, models.JobRun{Status: "error"}, "zalo")
		if !strings.Contains(body, "Không có thông tin lỗi") {
			t.Errorf("expected fallback error text, got: %q", body)
		}
	})
}
