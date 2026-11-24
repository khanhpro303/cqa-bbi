package knowledge

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/vietbui/chat-quality-agent/ai"
	"github.com/vietbui/chat-quality-agent/db/models"
)

func TestSummarizePassagesCoversLongDocument(t *testing.T) {
	markers := []string{"ĐẦU TÀI LIỆU", "GIỮA TÀI LIỆU", "CUỐI TÀI LIỆU"}
	var sections []string
	for _, marker := range markers {
		sections = append(sections, marker+"\n"+strings.Repeat("ủ", summaryInputRunes-64))
	}
	var calls atomic.Int32
	call := func(_ context.Context, system, input string) (ai.AIResponse, error) {
		calls.Add(1)
		var data map[string]string
		if err := json.Unmarshal([]byte(input), &data); err != nil {
			return ai.AIResponse{}, err
		}
		content := data["content"]
		if utf8.RuneCountInString(content) > summaryInputRunes || data["title"] != "Sổ tay BBI" {
			return ai.AIResponse{}, fmt.Errorf("invalid summary input")
		}
		var found []string
		for _, marker := range markers {
			if strings.Contains(content, marker) {
				found = append(found, marker)
			}
		}
		if strings.Contains(system, "Viết một đoạn tổng quan") && len(found) != len(markers) {
			return ai.AIResponse{}, fmt.Errorf("final pass is missing sections: %v", found)
		}
		return ai.AIResponse{Content: strings.Join(found, " | ")}, nil
	}
	got, err := summarizePassages(context.Background(), call, "Sổ tay BBI", sections)
	if err != nil || got != strings.Join(markers, " | ") || calls.Load() != 4 {
		t.Fatalf("summary = %q, calls = %d, err = %v", got, calls.Load(), err)
	}
}

func TestReadDocumentSummaryIsIndependentOfIndex(t *testing.T) {
	now := time.Now()
	expired := now.Add(-summaryJobTimeout - time.Minute)
	for _, tt := range []struct {
		doc  models.KnowledgeDocument
		want string
	}{
		{models.KnowledgeDocument{Status: StatusProcessing}, "missing"},
		{models.KnowledgeDocument{Status: StatusFailed, SummaryAttempted: true, SummaryStartedAt: &now}, StatusProcessing},
		{models.KnowledgeDocument{Status: StatusReady, SummaryAttempted: true, SummaryStartedAt: &expired}, StatusFailed},
		{models.KnowledgeDocument{Status: StatusFailed, Summary: "Bảo hành 12 tháng."}, StatusReady},
	} {
		if got := ReadDocumentSummary(&tt.doc); got.Status != tt.want || got.Text != tt.doc.Summary {
			t.Errorf("summary = %+v, want %s and %q", got, tt.want, tt.doc.Summary)
		}
	}
}

func TestDocumentIndexLockHoldsThroughMutation(t *testing.T) {
	ctx := context.Background()
	release, ok := LockDocumentIndex(ctx, "tenant-a", "doc-a", false)
	if !ok {
		t.Fatal("initial lock was denied")
	}
	if _, ok := LockDocumentIndex(ctx, "tenant-a", "doc-a", false); ok {
		t.Fatal("mutation entered while index was writing")
	}
	unrelated, ok := LockDocumentIndex(ctx, "tenant-b", "doc-a", false)
	if !ok {
		t.Fatal("another tenant was blocked")
	}
	unrelated()

	waitCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	locked := make(chan bool, 1)
	go func() {
		release, ok := LockDocumentIndex(waitCtx, "tenant-a", "doc-a", true)
		if ok {
			release()
		}
		locked <- ok
	}()
	release()
	if !<-locked {
		t.Fatal("queued index did not acquire the released lock")
	}
}
