package engine

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
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
	if got := transcriptSinceForJob(qc, since); !got.IsZero() {
		t.Fatalf("CSKH quality analysis should load full conversation history: %v", got)
	}
}

func TestNewAnalysisSnapshotMetadataCapturesExactInput(t *testing.T) {
	older := time.Date(2026, 9, 17, 8, 0, 0, 0, time.UTC)
	latest := older.Add(15 * time.Minute)
	messages := []models.Message{
		{ID: "message-b", SentAt: latest},
		{ID: "message-a", SentAt: latest},
		{ID: "message-c", SentAt: older},
	}

	metadata := newAnalysisSnapshotMetadata(messages, "full transcript", "quality prompt")
	if metadata.SourceMessageCount != 3 {
		t.Fatalf("unexpected source message count: %d", metadata.SourceMessageCount)
	}
	if metadata.SourceLastMessageAt == nil || !metadata.SourceLastMessageAt.Equal(latest) {
		t.Fatalf("unexpected source timestamp: %#v", metadata.SourceLastMessageAt)
	}
	if metadata.SourceLastMessageID != "message-b" {
		t.Fatalf("equal timestamps must use the greatest stable message ID: %#v", metadata)
	}
	transcriptHash := fmt.Sprintf("%x", sha256.Sum256([]byte("full transcript")))
	promptHash := fmt.Sprintf("%x", sha256.Sum256([]byte("quality prompt")))
	if metadata.TranscriptSHA256 != transcriptHash || metadata.PromptSHA256 != promptHash {
		t.Fatalf("unexpected input hashes: %#v", metadata)
	}
}

func TestBuildQCEvaluationDetailIncludesSnapshotMetadata(t *testing.T) {
	source := time.Date(2026, 9, 17, 8, 15, 0, 0, time.UTC)
	detailJSON, err := buildQCEvaluationDetail("Cần cải thiện", 62, "Có ba vấn đề", analysisSnapshotMetadata{
		SourceLastMessageAt: &source,
		SourceLastMessageID: "message-24",
		SourceMessageCount:  24,
		TranscriptSHA256:    "transcript-hash",
		PromptSHA256:        "prompt-hash",
		AIProvider:          "openai",
		AIModel:             "gpt-5.6",
	})
	if err != nil {
		t.Fatalf("build detail: %v", err)
	}
	var detail map[string]interface{}
	if err := json.Unmarshal(detailJSON, &detail); err != nil {
		t.Fatalf("invalid detail JSON: %v", err)
	}
	if detail["snapshot_version"] != float64(1) || detail["source_message_count"] != float64(24) {
		t.Fatalf("missing snapshot metadata: %#v", detail)
	}
	if detail["source_last_message_at"] != "2026-09-17T08:15:00Z" || detail["transcript_sha256"] != "transcript-hash" || detail["prompt_sha256"] != "prompt-hash" {
		t.Fatalf("unexpected source metadata: %#v", detail)
	}
	if detail["source_last_message_id"] != "message-24" {
		t.Fatalf("missing stable source cursor: %#v", detail)
	}
	if detail["ai_provider"] != "openai" || detail["ai_model"] != "gpt-5.6" {
		t.Fatalf("unexpected AI metadata: %#v", detail)
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

func TestValidateOrderOutcomesRequiresExactCustomerConfirmation(t *testing.T) {
	confirmedAt := time.Date(2026, 11, 2, 3, 4, 5, 0, time.UTC)
	messages := []models.Message{
		{ID: "customer-amount", SenderType: "customer", Content: "Tổng đơn của mình là 450.000đ đúng không?", SentAt: confirmedAt.Add(-time.Minute)},
		{ID: "customer-confirm", SenderType: "customer", Content: "Dạ mình lấy COD nhé", SentAt: confirmedAt},
	}
	value := int64(450000)
	proposals := []ai.OrderOutcomeInsight{
		{Status: "confirmed", OrderRef: "DH-01", ValueVND: &value, ConfirmationMessageID: "customer-confirm", ConfirmationEvidence: "Dạ mình lấy COD nhé", AmountMessageID: "customer-amount", AmountEvidence: "Tổng đơn của mình là 450.000đ đúng không?", Confidence: .96},
		{Status: "confirmed", ConfirmationMessageID: "customer-confirm", ConfirmationEvidence: "Fanpage tự nói đã chốt", Confidence: .9},
		{Status: "confirmed", ConfirmationMessageID: "missing-message", ConfirmationEvidence: "Dạ mình lấy COD nhé", Confidence: .9},
	}
	got := validateOrderOutcomes("conv-1", proposals, messages, nil)
	if len(got) != 1 {
		t.Fatalf("agent-only confirmation must be rejected: %#v", got)
	}
	if got[0].Status != "confirmed" || got[0].ValueVND == nil || *got[0].ValueVND != value || got[0].OrderedAt != confirmedAt.Format(time.RFC3339Nano) {
		t.Fatalf("unexpected validated outcome: %#v", got[0])
	}
	if len(got[0].EvidenceMessageIDs) != 2 || got[0].EvidenceMessageIDs[0] != "customer-confirm" || got[0].EvidenceMessageIDs[1] != "customer-amount" {
		t.Fatalf("evidence IDs must resolve to exact messages: %#v", got[0].EvidenceMessageIDs)
	}
}

func TestValidateOrderOutcomesReviewsMissingAmountAndKeepsStableOrder(t *testing.T) {
	messages := []models.Message{
		{ID: "confirm", SenderType: "customer", Content: "Mình chốt đơn này", SentAt: time.Unix(10, 0)},
		{ID: "cancel", SenderType: "customer", Content: "Mình hủy đơn nhé", SentAt: time.Unix(20, 0)},
	}
	confirmed := validateOrderOutcomes("conv-2", []ai.OrderOutcomeInsight{{Status: "confirmed", ConfirmationMessageID: "confirm", ConfirmationEvidence: "Mình chốt đơn này", Confidence: .8}}, messages, nil)
	cancelled := validateOrderOutcomes("conv-2", []ai.OrderOutcomeInsight{{Status: "cancelled", ConfirmationMessageID: "cancel", ConfirmationEvidence: "Mình hủy đơn nhé", Confidence: .9}}, messages, confirmed)
	if len(confirmed) != 1 || confirmed[0].Status != "review" || confirmed[0].ValueVND != nil {
		t.Fatalf("confirmed order without amount must require review: %#v", confirmed)
	}
	if len(cancelled) != 1 || cancelled[0].Status != "cancelled" || confirmed[0].OrderID != cancelled[0].OrderID || confirmed[0].OrderRef != cancelled[0].OrderRef {
		t.Fatalf("later cancellation must update the stable conversation order: confirmed=%#v cancelled=%#v", confirmed, cancelled)
	}
}

func TestValidateOrderOutcomesRejectsInventedAmountAndLateQuote(t *testing.T) {
	confirmedAt := time.Date(2026, 11, 2, 10, 0, 0, 0, time.UTC)
	messages := []models.Message{
		{ID: "quote", SenderType: "agent", Content: "Tổng đơn 450.000đ", SentAt: confirmedAt.Add(-time.Hour)},
		{ID: "confirm", SenderType: "customer", Content: "Dạ mình chốt COD", SentAt: confirmedAt},
		{ID: "late", SenderType: "agent", Content: "Tổng đơn 999.999đ", SentAt: confirmedAt.Add(time.Minute)},
	}
	invented := int64(999999)
	quoted := int64(450000)
	accepted := validateOrderOutcomes("conv-amount", []ai.OrderOutcomeInsight{{Status: "confirmed", ValueVND: &quoted, ConfirmationMessageID: "confirm", ConfirmationEvidence: "Dạ mình chốt COD", AmountMessageID: "quote", AmountEvidence: "Tổng đơn 450.000đ", Confidence: .9}}, messages, nil)
	if len(accepted) != 1 || accepted[0].Status != "confirmed" || accepted[0].ValueVND == nil || *accepted[0].ValueVND != quoted {
		t.Fatalf("matching agent quote before customer acceptance should be accepted: %#v", accepted)
	}
	got := validateOrderOutcomes("conv-amount", []ai.OrderOutcomeInsight{{Status: "confirmed", ValueVND: &invented, ConfirmationMessageID: "confirm", ConfirmationEvidence: "Dạ mình chốt COD", AmountMessageID: "quote", AmountEvidence: "Tổng đơn 450.000đ", Confidence: .9}}, messages, nil)
	if len(got) != 1 || got[0].Status != "review" || got[0].ValueVND != nil {
		t.Fatalf("invented amount must be rejected: %#v", got)
	}
	got = validateOrderOutcomes("conv-amount", []ai.OrderOutcomeInsight{{Status: "confirmed", ValueVND: &quoted, ConfirmationMessageID: "confirm", ConfirmationEvidence: "Dạ mình chốt COD", AmountMessageID: "missing-message", AmountEvidence: "Tổng đơn 450.000đ", Confidence: .9}}, messages, nil)
	if len(got) != 1 || got[0].Status != "review" || got[0].ValueVND != nil {
		t.Fatalf("amount text without its exact message ID must be rejected: %#v", got)
	}
	got = validateOrderOutcomes("conv-amount", []ai.OrderOutcomeInsight{{Status: "confirmed", ValueVND: &invented, ConfirmationMessageID: "confirm", ConfirmationEvidence: "Dạ mình chốt COD", AmountMessageID: "late", AmountEvidence: "Tổng đơn 999.999đ", Confidence: .9}}, messages, nil)
	if len(got) != 1 || got[0].ValueVND != nil {
		t.Fatalf("quote after confirmation must be rejected: %#v", got)
	}
}

func TestValidateOrderOutcomesUsesConfirmationMessageIdentityAndRetractsOmissionToReview(t *testing.T) {
	messages := []models.Message{
		{ID: "confirm-1", SenderType: "customer", Content: "Mình chốt đơn DH-01", SentAt: time.Unix(10, 0)},
		{ID: "confirm-2", SenderType: "customer", Content: "Mình chốt thêm đơn DH-02", SentAt: time.Unix(20, 0)},
	}
	proposals := []ai.OrderOutcomeInsight{
		{Status: "confirmed", OrderRef: "DH-01", ConfirmationMessageID: "confirm-1", ConfirmationEvidence: "Mình chốt đơn DH-01", Confidence: .9},
		{Status: "confirmed", OrderRef: "DH-02", ConfirmationMessageID: "confirm-2", ConfirmationEvidence: "Mình chốt thêm đơn DH-02", Confidence: .9},
	}
	got := validateOrderOutcomes("conv-multi", proposals, messages, nil)
	if len(got) != 2 || got[0].OrderID == got[1].OrderID || got[0].OrderRef != "DH-01" || got[1].OrderRef != "DH-02" {
		t.Fatalf("separate confirmations need separate verified identities: %#v", got)
	}
	retracted := validateOrderOutcomes("conv-multi", nil, messages, got)
	if len(retracted) != 2 || retracted[0].Status != "review" || retracted[1].Status != "review" || retracted[0].OrderID != got[0].OrderID {
		t.Fatalf("omitted full-snapshot outcomes must emit review revisions: %#v", retracted)
	}
}

func TestValidateOrderOutcomesRejectsHallucinatedRefAndPreservesVerifiedCancellationIdentity(t *testing.T) {
	messages := []models.Message{
		{ID: "confirm", SenderType: "customer", Content: "Mình chốt đơn DH-77", SentAt: time.Unix(10, 0)},
		{ID: "cancel", SenderType: "customer", Content: "Mình hủy DH-77 nhé", SentAt: time.Unix(20, 0)},
		{ID: "confirm-prefix", SenderType: "customer", Content: "Mình chốt đơn DH-10", SentAt: time.Unix(30, 0)},
	}
	confirmed := validateOrderOutcomes("conv-ref", []ai.OrderOutcomeInsight{{Status: "confirmed", OrderRef: "DH-77", ConfirmationMessageID: "confirm", ConfirmationEvidence: "Mình chốt đơn DH-77", Confidence: .9}}, messages, nil)
	cancelled := validateOrderOutcomes("conv-ref", []ai.OrderOutcomeInsight{{Status: "cancelled", OrderRef: "DH-77", ConfirmationMessageID: "cancel", ConfirmationEvidence: "Mình hủy DH-77 nhé", Confidence: .9}}, messages, confirmed)
	if len(cancelled) != 1 || cancelled[0].Status != "cancelled" || cancelled[0].OrderID != confirmed[0].OrderID || cancelled[0].OrderedAt != confirmed[0].OrderedAt {
		t.Fatalf("verified cancellation must preserve purchase identity/date: confirmed=%#v cancelled=%#v", confirmed, cancelled)
	}
	hallucinated := validateOrderOutcomes("conv-ref", []ai.OrderOutcomeInsight{{Status: "confirmed", OrderRef: "MADE-UP", ConfirmationMessageID: "confirm", ConfirmationEvidence: "Mình chốt đơn DH-77", Confidence: .9}}, messages, nil)
	if len(hallucinated) != 1 || hallucinated[0].OrderRef == "MADE-UP" {
		t.Fatalf("order ref absent from evidence must not be trusted: %#v", hallucinated)
	}
	prefix := validateOrderOutcomes("conv-ref-prefix", []ai.OrderOutcomeInsight{{Status: "confirmed", OrderRef: "DH-1", ConfirmationMessageID: "confirm-prefix", ConfirmationEvidence: "Mình chốt đơn DH-10", Confidence: .9}}, messages, nil)
	if len(prefix) != 1 || prefix[0].OrderRef == "DH-1" {
		t.Fatalf("partial reference token must not verify against a longer reference: %#v", prefix)
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

func TestPendingQCConversationIDsIncludesNeverAnalysedAndStale(t *testing.T) {
	latest := time.Date(2026, 9, 16, 8, 30, 0, 0, time.UTC)
	evaluatedAt := latest.Add(10 * time.Minute)
	candidates := []staleQCCandidate{
		{ID: "never"},
		{ID: "fresh", EvaluationID: "result-fresh", EvaluatedAt: evaluatedAt, LastMessageAt: &latest, Detail: `{"source_last_message_at":"2026-09-16T08:30:00Z"}`},
		{ID: "new-message", EvaluationID: "result-stale", EvaluatedAt: evaluatedAt, LastMessageAt: &latest, Detail: `{"source_last_message_at":"2026-09-16T08:00:00Z"}`},
		{ID: "legacy", EvaluationID: "result-legacy", EvaluatedAt: latest.Add(-time.Minute), LastMessageAt: &latest, Detail: `{}`},
	}
	got := pendingQCConversationIDs(candidates, 0)
	if len(got) != 3 || got[0] != "never" || got[1] != "new-message" || got[2] != "legacy" {
		t.Fatalf("pendingQCConversationIDs() = %#v; want never-analysed and stale conversations", got)
	}
	limited := pendingQCConversationIDs(candidates, 2)
	if len(limited) != 2 || limited[0] != "never" || limited[1] != "new-message" {
		t.Fatalf("limited pending QC IDs = %#v", limited)
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
