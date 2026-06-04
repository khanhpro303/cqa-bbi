package engine

import (
	"reflect"
	"testing"
)

// TestPickContent covers the "tin nhắn nhắc lại" content-selection rule: the
// text-only reminder is used only when it is non-empty AND the segment already
// delivered successfully at least once; reminders never carry images. The
// surrounding DB lookup (segmentHasSuccessfulRun) is not exercised here — the
// project has no sqlite/mock harness — so the decision is kept pure and tested
// via its boolean input.
func TestPickContent(t *testing.T) {
	mainContent := "Tin chính"
	mainImgs := []string{"img-a", "img-b"}

	tests := []struct {
		name            string
		reminderContent string
		hasPriorSuccess bool
		wantContent     string
		wantAttachments []string
	}{
		{
			name:            "first send uses main content with images",
			reminderContent: "Nhắc lại",
			hasPriorSuccess: false,
			wantContent:     mainContent,
			wantAttachments: mainImgs,
		},
		{
			name:            "later send uses reminder without images",
			reminderContent: "Nhắc lại",
			hasPriorSuccess: true,
			wantContent:     "Nhắc lại",
			wantAttachments: nil,
		},
		{
			name:            "no reminder text always uses main content even after prior success",
			reminderContent: "",
			hasPriorSuccess: true,
			wantContent:     mainContent,
			wantAttachments: mainImgs,
		},
		{
			name:            "no reminder text on first send uses main content",
			reminderContent: "",
			hasPriorSuccess: false,
			wantContent:     mainContent,
			wantAttachments: mainImgs,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotContent, gotAttachments := pickContent(mainContent, mainImgs, tt.reminderContent, tt.hasPriorSuccess)
			if gotContent != tt.wantContent {
				t.Errorf("content = %q, want %q", gotContent, tt.wantContent)
			}
			if !reflect.DeepEqual(gotAttachments, tt.wantAttachments) {
				t.Errorf("attachments = %v, want %v", gotAttachments, tt.wantAttachments)
			}
		})
	}
}
