package ai

import (
	"strings"
	"testing"
)

func TestBuildQCPrompt(t *testing.T) {
	rules := "## Phải chào hỏi lịch sự"
	prompt := BuildQCPrompt(rules, "")

	if !strings.Contains(prompt, rules) {
		t.Error("QC prompt should contain the rules content")
	}
	if !strings.Contains(prompt, "violations") {
		t.Error("QC prompt should mention violations in output format")
	}
	if !strings.Contains(prompt, "JSON") {
		t.Error("QC prompt should request JSON output")
	}
}

func TestBuildClassificationPrompt(t *testing.T) {
	rules := `[{"name":"complaint","description":"Customer complaint"}]`
	prompt := BuildClassificationPrompt(rules)

	if !strings.Contains(prompt, rules) {
		t.Error("Classification prompt should contain rules config")
	}
	if !strings.Contains(prompt, "tags") {
		t.Error("Classification prompt should mention tags in output format")
	}
	if strings.Contains(prompt, `"insights"`) {
		t.Error("Legacy classification config should not enable structured insights")
	}
}

func TestBuildClassificationPromptMessengerInsightsProfile(t *testing.T) {
	config := `{"profile":"messenger_insights","rules":[{"name":"Hỏi sản phẩm","description":"Khách hỏi sản phẩm"}]}`
	prompt := BuildClassificationPrompt(config)

	for _, expected := range []string{`"insights"`, `"products"`, `"lead_quality"`, "không suy đoán hoặc tự tạo SKU", "không đồng nghĩa khách hàng chất lượng thấp"} {
		if !strings.Contains(prompt, expected) {
			t.Errorf("Messenger insights prompt should contain %q", expected)
		}
	}
}

func TestFormatChatTranscript(t *testing.T) {
	messages := []ChatMessage{
		{SenderType: "customer", SenderName: "Nguyen Van A", Content: "Xin chào", SentAt: "2026-09-13T09:00:00+07:00"},
		{SenderType: "agent", SenderName: "CSKH", Content: "Dạ, em chào anh", SentAt: "2026-09-13T09:01:00+07:00"},
	}

	transcript := FormatChatTranscript(messages)

	if !strings.Contains(transcript, "Nguyen Van A") {
		t.Error("Transcript should contain sender name")
	}
	if !strings.Contains(transcript, "Xin chào") {
		t.Error("Transcript should contain message content")
	}
	if !strings.Contains(transcript, "2026-09-13T09:00:00+07:00") {
		t.Error("Transcript should contain timestamp")
	}
	if !strings.Contains(transcript, "2026-09-13T09:01:00+07:00") {
		t.Error("Transcript should contain all timestamps")
	}
}

func TestFormatChatTranscriptFallbackSenderType(t *testing.T) {
	messages := []ChatMessage{
		{SenderType: "customer", SenderName: "", Content: "Hello", SentAt: "10:00"},
	}

	transcript := FormatChatTranscript(messages)

	if !strings.Contains(transcript, "customer") {
		t.Error("Should fallback to sender_type when name is empty")
	}
}

func TestTranscriptRetainsRolesWithNamedSenders(t *testing.T) {
	transcript := FormatChatTranscript([]ChatMessage{
		{SenderType: "customer", SenderName: "Minh", Content: "Giá bao nhiêu?"},
		{SenderType: "agent", SenderName: "Minh", Content: "200.000đ"},
	})
	for _, role := range []string{"Minh (customer):", "Minh (agent):"} {
		if !strings.Contains(transcript, role) {
			t.Errorf("transcript lost role %q for named sender: %s", role, transcript)
		}
	}
}
