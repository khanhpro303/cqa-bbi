package engine

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/vietbui/chat-quality-agent/ai"
	"github.com/vietbui/chat-quality-agent/db/models"
)

func TestTranscriptSinceForJob(t *testing.T) {
	since := time.Date(2026, 9, 13, 8, 0, 0, 0, time.UTC)

	legacy := models.Job{JobType: "classification", RulesConfig: `[{"name":"Hỏi giá","description":"Khách hỏi giá"}]`}
	if got := transcriptSinceForJob(legacy, since); !got.Equal(since) {
		t.Fatalf("legacy classification should retain incremental transcript cutoff: %v", got)
	}

	messenger := models.Job{JobType: "classification", RulesConfig: `{"profile":"messenger_insights","rules":[]}`}
	if got := transcriptSinceForJob(messenger, since); !got.IsZero() {
		t.Fatalf("Messenger insights should load full conversation history: %v", got)
	}

	qc := models.Job{JobType: "qc_analysis", RulesConfig: `{"profile":"messenger_insights","rules":[]}`}
	if got := transcriptSinceForJob(qc, since); !got.Equal(since) {
		t.Fatalf("non-classification jobs should retain cutoff: %v", got)
	}
}

func TestMessengerInsightsGuardSkipsAgentOnlyConversation(t *testing.T) {
	job := models.Job{
		JobType:     "classification",
		RulesConfig: `{"profile":"messenger_insights","rules":[{"name":"Hỏi giá","description":"Khách hỏi giá"}]}`,
	}
	messages := []models.Message{
		{SenderType: "agent", SenderName: "LS2 Helmets Vietnam", Content: "Áo LS2 Bolton Air giá 3.590.000đ. Cho em xin chiều cao và cân nặng để tư vấn size."},
		{SenderType: "agent", SenderName: "LS2 Helmets Vietnam", Content: "Bạn đang phản hồi bình luận của người dùng về bài viết trên Trang của mình."},
	}

	raw, guarded := messengerInsightsGuardResponse(job, messages)
	if !guarded {
		t.Fatal("agent-only Messenger conversation must be handled without calling AI")
	}
	response, err := ai.ParseClassificationResponse(raw)
	if err != nil {
		t.Fatalf("guard response must be valid classification JSON: %v", err)
	}
	if len(response.Tags) != 0 {
		t.Fatalf("agent-only conversation must not receive customer-intent tags: %#v", response.Tags)
	}
	if !strings.Contains(response.Summary, "Chưa ghi nhận tin nhắn từ khách hàng") || !strings.Contains(response.Summary, "2 tin nhắn từ Fanpage") {
		t.Fatalf("unexpected safe summary: %q", response.Summary)
	}
	if response.Insights == nil || len(response.Insights.Intents) != 0 || len(response.Insights.Products) != 0 || len(response.Insights.Feedback) != 0 {
		t.Fatalf("agent-only conversation must have empty customer insights: %#v", response.Insights)
	}
	if response.Insights.LeadQuality.Level != "unknown" || response.Insights.LeadQuality.Evidence != "" {
		t.Fatalf("agent-only lead quality must be unknown without evidence: %#v", response.Insights.LeadQuality)
	}
}

func TestMessengerInsightsGuardLeavesCustomerAndOtherJobsForAnalysis(t *testing.T) {
	messenger := models.Job{JobType: "classification", RulesConfig: `{"profile":"messenger_insights","rules":[]}`}
	withCustomer := []models.Message{{SenderType: "customer", Content: "Áo này giá bao nhiêu?"}}
	if raw, guarded := messengerInsightsGuardResponse(messenger, withCustomer); guarded || raw != "" {
		t.Fatalf("conversation with a customer message must continue to AI: guarded=%v raw=%q", guarded, raw)
	}

	legacy := models.Job{JobType: "classification", RulesConfig: `[]`}
	qc := models.Job{JobType: "qc_analysis", RulesConfig: `{"profile":"messenger_insights","rules":[]}`}
	agentOnly := []models.Message{{SenderType: "agent", Content: "Xin chào"}}
	for _, job := range []models.Job{legacy, qc} {
		if raw, guarded := messengerInsightsGuardResponse(job, agentOnly); guarded || raw != "" {
			t.Fatalf("non-Messenger-insights job must keep existing behavior: type=%s guarded=%v raw=%q", job.JobType, guarded, raw)
		}
	}
}

func TestBuildConversationInsightDetailIncludesSourceTimestamp(t *testing.T) {
	source := time.Date(2026, 9, 13, 15, 4, 5, 123000000, time.FixedZone("ICT", 7*60*60))
	response := ai.ClassificationResponse{
		Summary: "Khách hỏi Serum A.",
		Insights: &ai.ConversationInsights{
			Intents:  []string{"Hỏi hàng"},
			Products: []ai.ProductInsight{{Name: "Serum A", Evidence: "Serum A còn hàng không?"}},
			Feedback: []ai.FeedbackInsight{},
			LeadQuality: ai.LeadQualityInsight{
				Level:    "high",
				Evidence: "mình đặt 2 chai",
				Reason:   "Khách xác nhận số lượng",
			},
		},
	}

	detailJSON, err := buildConversationInsightDetail(response, &source)
	if err != nil {
		t.Fatalf("unexpected marshal error: %v", err)
	}
	var detail map[string]interface{}
	if err := json.Unmarshal(detailJSON, &detail); err != nil {
		t.Fatalf("unexpected detail JSON: %v", err)
	}
	if detail["source_last_message_at"] != "2026-09-13T08:04:05.123Z" {
		t.Fatalf("unexpected source timestamp: %v", detail["source_last_message_at"])
	}
	if detail["summary"] != response.Summary {
		t.Fatalf("missing summary in insight detail: %#v", detail)
	}
}

func TestPendingInsightConversationIDsIncludesNeverAnalysedAndStale(t *testing.T) {
	latest := time.Date(2026, 9, 16, 8, 30, 0, 0, time.UTC)
	candidates := []staleInsightCandidate{
		{ID: "never"},
		{ID: "fresh", InsightID: "result-fresh", InsightJobID: "job-a", LastMessageAt: &latest, Detail: `{"source_last_message_at":"2026-09-16T08:30:00Z"}`},
		{ID: "new-message", InsightID: "result-stale", InsightJobID: "job-a", LastMessageAt: &latest, Detail: `{"source_last_message_at":"2026-09-16T08:00:00Z"}`},
		{ID: "legacy", InsightID: "result-legacy", InsightJobID: "job-b", LastMessageAt: &latest, Detail: `{}`},
	}
	got := pendingInsightConversationIDs(candidates, "", 0)
	if len(got) != 3 || got[0] != "never" || got[1] != "new-message" || got[2] != "legacy" {
		t.Fatalf("pendingInsightConversationIDs() = %#v; want never-analysed and stale conversations", got)
	}
	targeted := pendingInsightConversationIDs(candidates, "job-a", 0)
	if len(targeted) != 1 || targeted[0] != "new-message" {
		t.Fatalf("targeted pending IDs = %#v; want only stale insight from requested job", targeted)
	}
	limited := pendingInsightConversationIDs(candidates, "", 2)
	if len(limited) != 2 || limited[0] != "never" || limited[1] != "new-message" {
		t.Fatalf("limited pending IDs = %#v", limited)
	}
}

func TestMapBatchResultsRejectsUnknownDuplicateAndCountsMissing(t *testing.T) {
	results := []json.RawMessage{
		json.RawMessage(`{"conversation_id":"conv-b","summary":"B"}`),
		json.RawMessage(`{"conversation_id":"outside","summary":"unknown"}`),
		json.RawMessage(`{"conversation_id":"conv-b","summary":"duplicate"}`),
	}

	mapped, missing := mapBatchResults([]string{"conv-a", "conv-b", "conv-c"}, results)
	if len(mapped) != 1 || mapped[0].ConversationIndex != 1 {
		t.Fatalf("unexpected mappings: %#v", mapped)
	}
	if missing != 2 {
		t.Fatalf("expected two missing conversations, got %d", missing)
	}
}

func TestMapBatchResultsUsesSafePositionFallback(t *testing.T) {
	results := []json.RawMessage{
		json.RawMessage(`{"summary":"A"}`),
		json.RawMessage(`{"summary":"B"}`),
	}

	mapped, missing := mapBatchResults([]string{"conv-a", "conv-b", "conv-c"}, results)
	if len(mapped) != 2 || mapped[0].ConversationIndex != 0 || mapped[1].ConversationIndex != 1 {
		t.Fatalf("unexpected positional mappings: %#v", mapped)
	}
	if missing != 1 {
		t.Fatalf("expected one missing conversation, got %d", missing)
	}
}

// MockAIProvider returns a predefined response without calling any API.
type MockAIProvider struct {
	Response ai.AIResponse
	Err      error
}

func (m *MockAIProvider) AnalyzeChat(ctx context.Context, systemPrompt string, chatTranscript string) (ai.AIResponse, error) {
	if m.Err != nil {
		return ai.AIResponse{}, m.Err
	}
	return m.Response, nil
}

func TestMockAIProviderQCPass(t *testing.T) {
	passResponse := map[string]interface{}{
		"verdict":    "PASS",
		"score":      90,
		"review":     "Cuộc chat tốt, nhân viên lịch sự và giải đáp đầy đủ.",
		"violations": []interface{}{},
		"summary":    "Khách hàng hỏi về sản phẩm, nhân viên trả lời chi tiết.",
	}
	respJSON, _ := json.Marshal(passResponse)

	mock := &MockAIProvider{
		Response: ai.AIResponse{
			Content:      string(respJSON),
			InputTokens:  150,
			OutputTokens: 80,
			Model:        "claude-sonnet-4-6",
			Provider:     "claude",
		},
	}

	resp, err := mock.AnalyzeChat(context.Background(), "test prompt", "test transcript")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.InputTokens != 150 {
		t.Errorf("expected 150 input tokens, got %d", resp.InputTokens)
	}
	if resp.OutputTokens != 80 {
		t.Errorf("expected 80 output tokens, got %d", resp.OutputTokens)
	}
	if resp.Provider != "claude" {
		t.Errorf("expected provider claude, got %s", resp.Provider)
	}

	// Verify we can parse the response
	var qcResult struct {
		Verdict    string        `json:"verdict"`
		Score      int           `json:"score"`
		Violations []interface{} `json:"violations"`
	}
	if err := json.Unmarshal([]byte(resp.Content), &qcResult); err != nil {
		t.Fatalf("failed to parse QC response: %v", err)
	}
	if qcResult.Verdict != "PASS" {
		t.Errorf("expected PASS, got %s", qcResult.Verdict)
	}
	if qcResult.Score != 90 {
		t.Errorf("expected score 90, got %d", qcResult.Score)
	}
}

func TestMockAIProviderQCFail(t *testing.T) {
	failResponse := map[string]interface{}{
		"verdict": "FAIL",
		"score":   30,
		"review":  "Nhân viên không chào hỏi, trả lời cộc lốc.",
		"violations": []map[string]interface{}{
			{
				"severity":    "NGHIEM_TRONG",
				"rule":        "Chào hỏi lịch sự",
				"evidence":    "Khách: Xin chào. NV: Gì?",
				"explanation": "Nhân viên không chào hỏi lại, trả lời thô lỗ.",
				"suggestion":  "Nên bắt đầu bằng lời chào thân thiện.",
			},
		},
		"summary": "Cuộc chat cần cải thiện.",
	}
	respJSON, _ := json.Marshal(failResponse)

	mock := &MockAIProvider{
		Response: ai.AIResponse{
			Content:      string(respJSON),
			InputTokens:  200,
			OutputTokens: 120,
			Model:        "gemini-2.0-flash",
			Provider:     "gemini",
		},
	}

	resp, err := mock.AnalyzeChat(context.Background(), "test prompt", "test transcript")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var qcResult struct {
		Verdict    string `json:"verdict"`
		Violations []struct {
			Severity string `json:"severity"`
			Rule     string `json:"rule"`
		} `json:"violations"`
	}
	if err := json.Unmarshal([]byte(resp.Content), &qcResult); err != nil {
		t.Fatalf("failed to parse QC response: %v", err)
	}
	if qcResult.Verdict != "FAIL" {
		t.Errorf("expected FAIL, got %s", qcResult.Verdict)
	}
	if len(qcResult.Violations) != 1 {
		t.Errorf("expected 1 violation, got %d", len(qcResult.Violations))
	}
	if qcResult.Violations[0].Severity != "NGHIEM_TRONG" {
		t.Errorf("expected NGHIEM_TRONG, got %s", qcResult.Violations[0].Severity)
	}
}

func TestCalculateCostUSD(t *testing.T) {
	tests := []struct {
		name     string
		provider string
		model    string
		input    int
		output   int
		minCost  float64
		maxCost  float64
	}{
		{"claude sonnet small", "claude", "claude-sonnet-4-6", 1000, 500, 0.01, 0.02},
		{"claude haiku cheap", "claude", "claude-haiku-4-5-20251001", 1000, 500, 0.001, 0.005},
		{"gemini flash very cheap", "gemini", "gemini-2.0-flash", 1000, 500, 0.0001, 0.001},
		{"openai gpt-5-mini", "openai", "gpt-5-mini", 1000, 500, 0.001, 0.01},
		{"zero tokens", "claude", "claude-sonnet-4-6", 0, 0, 0, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cost := ai.CalculateCostUSD(tt.provider, tt.model, tt.input, tt.output)
			if cost < tt.minCost || cost > tt.maxCost {
				t.Errorf("cost %f out of range [%f, %f]", cost, tt.minCost, tt.maxCost)
			}
		})
	}
}
