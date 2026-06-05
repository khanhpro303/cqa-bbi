package notifications

import (
	"strings"
	"testing"

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
