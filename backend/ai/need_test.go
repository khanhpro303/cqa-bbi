package ai

import "testing"

func TestNeedFromIntents(t *testing.T) {
	cases := []struct {
		intents []string
		want    string
	}{
		{nil, NeedUnknown},
		{[]string{"  "}, NeedUnknown},
		{[]string{"Hỏi giá mũ E-24"}, NeedPrice},
		{[]string{"Hỏi giá", "Hỏi phí ship về Hà Nội"}, NeedPrice},
		{[]string{"Hỏi thời gian giao hàng"}, NeedOrder},
		{[]string{"Tư vấn size theo vòng đầu"}, NeedAdvice},
		{[]string{"Hỏi còn màu đen không"}, NeedStock},
		{[]string{"Hỏi mẫu nào phù hợp đi phượt"}, NeedAdvice},
		{[]string{"Muốn đặt hàng", "Hỏi giá"}, NeedOrder},
		{[]string{"Hỏi chính sách bảo hành", "Hỏi giá"}, NeedWarranty},
		{[]string{"Phàn nàn giao chậm", "Muốn đổi hàng"}, NeedComplaint},
		{[]string{"Xin số điện thoại cửa hàng"}, NeedOther},
		{[]string{"hoi gia"}, NeedPrice},
		{[]string{"Hỏi về giao diện website"}, NeedOther}, // "giao" is not "giá" or "giao hàng"
		{[]string{"Trả lời tin nhắn"}, NeedOther},         // "lời" is not "lỗi"
	}
	for _, tc := range cases {
		if got := NeedFromIntents(tc.intents); got != tc.want {
			t.Errorf("NeedFromIntents(%q) = %q, want %q", tc.intents, got, tc.want)
		}
	}
}

func TestResolveNeedPrefersValidAICode(t *testing.T) {
	if need, source := ResolveNeed(" Advice ", []string{"Hỏi giá"}); need != NeedAdvice || source != "ai" {
		t.Fatalf("got %s/%s", need, source)
	}
	if need, source := ResolveNeed("hỏi giá", []string{"Hỏi giá"}); need != NeedPrice || source != "keyword" {
		t.Fatalf("invalid AI text must fall back to keywords, got %s/%s", need, source)
	}
	if need, source := ResolveNeed("", nil); need != NeedUnknown || source != "keyword" {
		t.Fatalf("got %s/%s", need, source)
	}
}

func TestParseClassificationResponseKeepsOnlyNeedCodes(t *testing.T) {
	response, err := ParseClassificationResponse(`{"tags":[],"summary":"x","insights":{"intents":["Hỏi giá"],"need":"PRICE","products":[],"feedback":[],"lead_quality":{"level":"unknown"}}}`)
	if err != nil || response.Insights.Need != NeedPrice {
		t.Fatalf("need = %q err=%v", response.Insights.Need, err)
	}
	response, _ = ParseClassificationResponse(`{"tags":[],"summary":"x","insights":{"intents":[],"need":"khách hỏi giá","products":[],"feedback":[],"lead_quality":{"level":"unknown"}}}`)
	if response.Insights.Need != "" {
		t.Fatalf("free text need must be dropped, got %q", response.Insights.Need)
	}
}

func TestMessengerPromptsAlwaysAskForNeed(t *testing.T) {
	config := `{"profile":"messenger_insights","rules":[]}`
	for name, prompt := range map[string]string{
		"default":  BuildClassificationPrompt(config),
		"override": BuildClassificationPrompt(config, "Prompt riêng {{rules}}\nCHỈ trả về JSON"),
	} {
		if count := countSubstring(prompt, mandatoryNeedHeading); count != 1 {
			t.Fatalf("%s prompt has %d need sections", name, count)
		}
	}
	if count := countSubstring(BuildClassificationPrompt(config, DefaultMessengerInsightsPrompt()), mandatoryNeedHeading); count != 1 {
		t.Fatalf("saved default prompt duplicated the need section %d times", count)
	}
}

func countSubstring(s, sub string) int {
	count := 0
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			count++
		}
	}
	return count
}
