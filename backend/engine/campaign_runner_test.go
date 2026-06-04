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

// TestMentionTokens checks the Zalo "[@id]" token builder: blanks dropped, cap
// honoured, empty input → nil.
func TestMentionTokens(t *testing.T) {
	tests := []struct {
		name    string
		userIDs []string
		cap     int
		want    []string
	}{
		{name: "empty input", userIDs: nil, cap: 50, want: nil},
		{name: "blanks dropped", userIDs: []string{" ", "", "123"}, cap: 50, want: []string{"[@123]"}},
		{name: "trims and wraps", userIDs: []string{" 123 ", "456"}, cap: 50, want: []string{"[@123]", "[@456]"}},
		{name: "cap honoured", userIDs: []string{"1", "2", "3"}, cap: 2, want: []string{"[@1]", "[@2]"}},
		{name: "all blanks → nil", userIDs: []string{"", "  "}, cap: 50, want: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := mentionTokens(tt.userIDs, tt.cap); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("mentionTokens(%v, %d) = %v, want %v", tt.userIDs, tt.cap, got, tt.want)
			}
		})
	}
}

// TestApplyMentions covers the WHERE-to-place logic: prefix greeting vs inline
// {tag} replacement, the inline→prefix fallback when the body has no token,
// greeting defaulting, and placeholder stripping when there is no one to tag.
func TestApplyMentions(t *testing.T) {
	tags := []string{"[@123]", "[@456]"}

	tests := []struct {
		name      string
		content   string
		tags      []string
		placement string
		greeting  string
		want      string
	}{
		{
			name:      "no tags strips placeholder",
			content:   "Xem ngay {tag} nhé",
			tags:      nil,
			placement: "inline",
			want:      "Xem ngay  nhé",
		},
		{
			name:      "no tags leaves plain body unchanged",
			content:   "Khuyến mãi hôm nay",
			tags:      nil,
			placement: "prefix",
			want:      "Khuyến mãi hôm nay",
		},
		{
			name:      "selected + prefix with custom greeting",
			content:   "Khuyến mãi hôm nay",
			tags:      tags,
			placement: "prefix",
			greeting:  "Chào cả nhà",
			want:      "Chào cả nhà [@123] [@456],\nKhuyến mãi hôm nay",
		},
		{
			name:      "prefix empty greeting falls back to default",
			content:   "Khuyến mãi hôm nay",
			tags:      tags,
			placement: "prefix",
			greeting:  "  ",
			want:      "Xin chào [@123] [@456],\nKhuyến mãi hôm nay",
		},
		{
			name:      "inline replaces token in place",
			content:   "Xin chào {tag}, vui lòng kiểm tra nhé!",
			tags:      tags,
			placement: "inline",
			want:      "Xin chào [@123] [@456], vui lòng kiểm tra nhé!",
		},
		{
			name:      "inline without token falls back to prefix",
			content:   "Nội dung không có token",
			tags:      tags,
			placement: "inline",
			greeting:  "Xin chào",
			want:      "Xin chào [@123] [@456],\nNội dung không có token",
		},
		{
			name:      "prefix with empty body returns greeting line only",
			content:   "",
			tags:      tags,
			placement: "prefix",
			greeting:  "Xin chào",
			want:      "Xin chào [@123] [@456],",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := applyMentions(tt.content, tt.tags, tt.placement, tt.greeting); got != tt.want {
				t.Errorf("applyMentions() = %q, want %q", got, tt.want)
			}
		})
	}
}
