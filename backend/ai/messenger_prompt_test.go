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

func TestMessengerPromptAlwaysIncludesMandatorySenderRoleRules(t *testing.T) {
	config := `{"profile":"messenger_insights","rules":[]}`
	for _, override := range []string{"", "Prompt tùy chỉnh {{rules}}"} {
		prompt := BuildClassificationPrompt(config, override)
		for _, expected := range []string{
			"## Quy tắc vai trò bắt buộc",
			"(customer)",
			"(agent)",
			"không có dòng (customer)",
			"không có nghĩa khách đã cung cấp",
		} {
			if !strings.Contains(prompt, expected) {
				t.Errorf("prompt override %q missing mandatory role rule %q: %s", override, expected, prompt)
			}
		}
	}
}

func TestMessengerPromptAlwaysIncludesMandatoryProductNormalizationRules(t *testing.T) {
	config := `{"profile":"messenger_insights","rules":[]}`
	for _, override := range []string{"", "Prompt tùy chỉnh {{rules}}", "Prompt tùy chỉnh không có placeholder"} {
		prompt := BuildClassificationPrompt(config, override)
		for _, expected := range []string{
			mandatoryProductNormalizationHeading,
			"EGO, LS2, BULLDOG, YOHE, ZEUS",
			`"FF 818" phải chuẩn hóa thành "FF818"`,
			`"Mũ bảo hiểm E-24", "Mũ E-24" và "E-24"`,
			"evidence phải giữ nguyên trích dẫn chính xác",
			"Không suy đoán model hoặc tự tạo SKU",
		} {
			if !strings.Contains(prompt, expected) {
				t.Errorf("prompt override %q missing mandatory product rule %q: %s", override, expected, prompt)
			}
		}
	}
}

func TestMessengerPromptDefaultExposesEditableOrderOutcomeRules(t *testing.T) {
	prompt := DefaultMessengerInsightsPrompt()
	for _, expected := range []string{
		"## Quy tắc nhận diện đơn hàng",
		`"order_outcomes"`,
		"mua COD vẫn là confirmed",
		"gửi QR, báo giá, mã vận đơn",
		"TOÀN BỘ nội dung nguyên văn của đúng một tin nhắn (customer)",
		`status="review"`,
	} {
		if !strings.Contains(prompt, expected) {
			t.Errorf("editable default missing order rule %q", expected)
		}
	}
}

func TestMessengerPromptDoesNotInjectOrderOutcomeRulesIntoOverride(t *testing.T) {
	config := `{"profile":"messenger_insights","rules":[]}`
	override := "Prompt tùy chỉnh {{rules}}"
	prompt := BuildClassificationPrompt(config, override)
	for _, unexpected := range []string{"Quy tắc nhận diện đơn hàng", `"order_outcomes"`, "mua COD vẫn là confirmed"} {
		if strings.Contains(prompt, unexpected) {
			t.Errorf("runtime injected order rule %q into editable prompt: %s", unexpected, prompt)
		}
	}
}

func TestUpgradeMessengerInsightsPromptWithOrderOutcomes(t *testing.T) {
	legacy := "Prompt tùy chỉnh {{rules}}\nCHỈ trả về JSON."
	upgraded, changed := UpgradeMessengerInsightsPromptWithOrderOutcomes(legacy)
	if !changed || !strings.Contains(upgraded, messengerOrderOutcomePromptHeading) || !strings.Contains(upgraded, "mua COD vẫn là confirmed") {
		t.Fatalf("legacy prompt was not upgraded: %s", upgraded)
	}
	if strings.Index(upgraded, messengerOrderOutcomePromptHeading) > strings.Index(upgraded, "CHỈ trả về JSON") {
		t.Fatalf("order rules must be inserted before the final output instruction: %s", upgraded)
	}

	customized := strings.Replace(upgraded, "mua COD vẫn là confirmed", "quy tắc do tenant chỉnh", 1)
	unchanged, changed := UpgradeMessengerInsightsPromptWithOrderOutcomes(customized)
	if changed || unchanged != customized {
		t.Fatal("migration must not overwrite an existing editable order section")
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
	for _, expected := range []string{`"tags"`, `"summary"`, `"insights"`, `"intents"`, `"products"`, `"feedback"`, `"lead_quality"`, "JSON ARRAY chứa 2 phần tử", `"conversation_id"`, mandatoryProductNormalizationHeading, `"FF 818" phải chuẩn hóa thành "FF818"`} {
		if !strings.Contains(batch, expected) {
			t.Errorf("batch prompt missing %q", expected)
		}
	}
}

func TestMessengerProductGroupingPromptRequiresCompleteExactMapping(t *testing.T) {
	for _, expected := range []string{
		"Mỗi nhãn đầu vào phải xuất hiện đúng một lần",
		"EGO, LS2, BULLDOG, YOHE, ZEUS",
		`"FF 818" thành "FF818"`,
		`"E - 24" thành "E-24"`,
		"cho phép name là chuỗi rỗng",
		"assignments",
		"KHÔNG dùng ID số",
		"không bỏ sót, không thêm hoặc sửa key",
	} {
		if !strings.Contains(MessengerProductGroupingPrompt, expected) {
			t.Errorf("product grouping prompt missing %q", expected)
		}
	}
}
