package ai

import (
	"strings"
	"testing"
)

func TestMessengerPromptDefaultPreservesExistingOutput(t *testing.T) {
	config := `{"profile":"messenger_insights","rules":[{"name":"Hỏi giá","description":"Khách hỏi giá sản phẩm"}]}`
	if got, want := BuildClassificationPrompt(config, DefaultMessengerInsightsPrompt()), BuildClassificationPrompt(config); got != want {
		t.Fatalf("editable default changed existing prompt:\n%s", got)
	}
	for _, override := range []string{"", " \n\t"} {
		if got, want := BuildClassificationPrompt(config, override), BuildClassificationPrompt(config); got != want {
			t.Fatal("empty override should retain the built-in prompt")
		}
	}
}

func TestMessengerPromptOverrideRetainsJobRules(t *testing.T) {
	config := `{"profile":"messenger_insights","rules":[{"name":"Hỏi giá","description":"Khách hỏi giá"}]}`
	for _, override := range []string{"Chỉ phân tích lời khách.\n{{rules}}", "Chỉ phân tích lời khách."} {
		prompt := BuildClassificationPrompt(config, override)
		if !strings.Contains(prompt, "Chỉ phân tích lời khách.") || !strings.Contains(prompt, `"name":"Hỏi giá"`) || strings.Contains(prompt, "{{rules}}") {
			t.Fatalf("override or job rules missing: %s", prompt)
		}
	}
}

func TestMessengerPromptDoesNotOverrideLegacyClassification(t *testing.T) {
	config := `[{"name":"Hỏi giá","description":"Khách hỏi giá"}]`
	if got, want := BuildClassificationPrompt(config, "Messenger override {{rules}}"), BuildClassificationPrompt(config); got != want {
		t.Fatal("Messenger override affected legacy classification")
	}
}

func TestMessengerPromptBatchRetainsStructuredOutput(t *testing.T) {
	prompt := BuildClassificationPrompt(`{"profile":"messenger_insights","rules":[]}`, DefaultMessengerInsightsPrompt())
	batch := WrapBatchPrompt(prompt, 2)
	for _, expected := range []string{`"tags"`, `"summary"`, `"insights"`, `"intents"`, `"products"`, `"feedback"`, `"lead_quality"`, "JSON ARRAY chứa 2 phần tử", `"conversation_id"`} {
		if !strings.Contains(batch, expected) {
			t.Errorf("batch prompt missing %q", expected)
		}
	}
}
