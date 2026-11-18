package workers

import (
	"encoding/json"
	"testing"
)

func TestParseClickPayload(t *testing.T) {
	payloadStr := `{"MA_CHA":"FF800","WEB_NAME":"Áo gió"}`
	var parsed struct {
		MaCha   string `json:"MA_CHA"`
		WebName string `json:"WEB_NAME"`
	}
	err := json.Unmarshal([]byte(payloadStr), &parsed)
	if err != nil {
		t.Fatalf("Failed to parse: %v", err)
	}
	if parsed.MaCha != "FF800" {
		t.Errorf("Expected MA_CHA FF800, got %s", parsed.MaCha)
	}
	if parsed.WebName != "Áo gió" {
		t.Errorf("Expected WEB_NAME Áo gió, got %s", parsed.WebName)
	}
}

func TestMockInventoryAggregation(t *testing.T) {
	// Simple test to ensure mapping and key retrieval logic works
	item := map[string]interface{}{
		"code": "FF800-R-L",
		"ton":  15.0,
	}

	code := getMapString(item, "code", "ma_hang", "ma")
	stock := getMapFloat(item, "stock", "ton", "ton_kho")

	if code != "FF800-R-L" {
		t.Errorf("getMapString failed, got: %s", code)
	}
	if stock != 15.0 {
		t.Errorf("getMapFloat failed, got: %f", stock)
	}
}

func TestSessionTimeoutTaskCreation(t *testing.T) {
	payload := SessionTimeoutPayload{
		SessionKey:     "zalo_session:ch-1:user-1",
		SessionID:      "session-1",
		TenantID:       "tenant-1",
		ChannelID:      "ch-1",
		ZaloUserID:     "user-1",
		ZaloGroupID:    "group-1",
		TimeoutMessage: "Nếu bạn không cần...",
	}

	task, err := NewSessionTimeoutTask(payload)
	if err != nil {
		t.Fatalf("failed to create session timeout task: %v", err)
	}

	if task.Type() != TypeSessionTimeout {
		t.Errorf("expected task type %s, got %s", TypeSessionTimeout, task.Type())
	}

	var parsed SessionTimeoutPayload
	if err := json.Unmarshal(task.Payload(), &parsed); err != nil {
		t.Fatalf("failed to unmarshal task payload: %v", err)
	}

	if parsed.SessionID != payload.SessionID {
		t.Errorf("expected session ID %s, got %s", payload.SessionID, parsed.SessionID)
	}
}

func TestIsDisambiguationReply(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		// Should match — bypasses CASUAL classifier
		{"1", true},
		{"5", true},
		{"9", true},
		{" 1 ", true},
		{"\t2\n", true},
		{"SP458484", true},
		{"SP458495_TL", true},
		{"SP123456ab", true},

		// Should NOT match — must still go through intent classifier
		{"0", false},       // leading zero is not a valid menu pick
		{"10", false},      // two-digit reply is ambiguous; let LLM decide
		{"", false},        // empty
		{"ok", false},      // genuine CASUAL acknowledgment
		{"dạ", false},      // genuine CASUAL
		{"FF901", false},   // not an SP code — should hit normal classifier
		{"SP12345", false}, // SP must have 6 digits per agent rule
		{"hello", false},
		{"cảm ơn", false},
		{"1 cái áo size L", false},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			if got := isDisambiguationReply(tc.in); got != tc.want {
				t.Errorf("isDisambiguationReply(%q) = %v; want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestParseMessageIntent(t *testing.T) {
	cases := []struct {
		name, response, message, want string
	}{
		{"out of scope", `{"label":"OUT_OF_SCOPE"}`, "mai có mưa không", "OUT_OF_SCOPE"},
		{"fenced response", "```json\n{\"label\":\"out_of_scope\"}\n```", "giải bài toán giúp em", "OUT_OF_SCOPE"},
		{"acknowledgment", `{"label":"CASUAL"}`, "dạ, cảm ơn", "CASUAL"},
		{"question mislabelled casual", `{"label":"CASUAL"}`, "Mẫu này thì sao?", "IN_SCOPE"},
		{"helmet question mislabelled casual", `{"label":"CASUAL"}`, "bên mình có mũ nguyên đầu không", "IN_SCOPE"},
		{"helmet question mislabelled out of scope", `{"label":"OUT_OF_SCOPE"}`, "có loại mũ nửa đầu nào", "IN_SCOPE"},
		{"explicit handover", `{"label":"HANDOVER"}`, "mũ nguyên đầu bị lỗi, cho em gặp nhân viên", "HANDOVER"},
		{"bare label", " OUT_OF_SCOPE ", "mai có mưa không", "OUT_OF_SCOPE"},
		{"ambiguous prose", "This is not HANDOVER or CASUAL", "có mẫu nguyên đầu không", "IN_SCOPE"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := parseMessageIntent(tc.response, tc.message); got != tc.want {
				t.Errorf("parseMessageIntent() = %q; want %q", got, tc.want)
			}
		})
	}
}
