package workers

import (
	"strings"
	"testing"
)

func TestSelectSystemPrompt(t *testing.T) {
	const (
		publicPrompt   = "PUBLIC_OWN_SCOPE"
		internalPrompt = "INTERNAL_ALL_SCOPE"
	)

	tests := []struct {
		name           string
		agentType      string
		publicPrompt   string
		internalPrompt string
		want           string
	}{
		{
			name:           "private agent uses internal prompt",
			agentType:      "private",
			publicPrompt:   publicPrompt,
			internalPrompt: internalPrompt,
			want:           internalPrompt,
		},
		{
			name:           "public agent uses public prompt",
			agentType:      "public",
			publicPrompt:   publicPrompt,
			internalPrompt: internalPrompt,
			want:           publicPrompt,
		},
		{
			name:           "private with empty internal returns empty (falls back to flow global, NOT public)",
			agentType:      "private",
			publicPrompt:   publicPrompt,
			internalPrompt: "",
			want:           "",
		},
		{
			name:           "public with empty public returns empty",
			agentType:      "public",
			publicPrompt:   "",
			internalPrompt: internalPrompt,
			want:           "",
		},
		{
			name:           "unknown agent type defaults to public prompt",
			agentType:      "",
			publicPrompt:   publicPrompt,
			internalPrompt: internalPrompt,
			want:           publicPrompt,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := selectSystemPrompt(tt.agentType, tt.publicPrompt, tt.internalPrompt)
			if got != tt.want {
				t.Fatalf("selectSystemPrompt(%q, %q, %q) = %q, want %q",
					tt.agentType, tt.publicPrompt, tt.internalPrompt, got, tt.want)
			}
		})
	}
}

func TestNormalizeLangflowReply(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{"No relevant supporting sources were found for that request.", botEmptyAnswerMessage},
		{"  NO RELEVANT SUPPORTING SOURCES WERE FOUND FOR THAT REQUEST\n", botEmptyAnswerMessage},
		{"Dạ mẫu này hiện hết hàng ạ.", "Dạ mẫu này hiện hết hàng ạ."},
		{"[RICH_MESSAGE_SENT]", "[RICH_MESSAGE_SENT]"},
		{"Here is the quote: No relevant supporting sources were found for that request.", "Here is the quote: No relevant supporting sources were found for that request."},
	} {
		if got := normalizeLangflowReply(tc.input); got != tc.want {
			t.Errorf("normalizeLangflowReply(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestPublicLineStockReply(t *testing.T) {
	inStock := publicLineStockReply("Bulldog TORII", 25)
	if !strings.Contains(inStock, "còn hàng") || strings.Contains(inStock, "25") {
		t.Errorf("public in-stock reply leaked count or missed status: %q", inStock)
	}
	outOfStock := publicLineStockReply("Bulldog TORII", 0)
	if !strings.Contains(outOfStock, "hết hàng") || strings.Contains(outOfStock, "0") {
		t.Errorf("public out-of-stock reply leaked count or missed status: %q", outOfStock)
	}
}

func TestPublicStockStatusInstructionRequiresStatusOnly(t *testing.T) {
	for _, want := range []string{"còn hàng", "hết hàng", "TUYỆT ĐỐI KHÔNG nêu số lượng tồn kho cụ thể"} {
		if !strings.Contains(publicStockStatusInstruction, want) {
			t.Errorf("publicStockStatusInstruction missing %q", want)
		}
	}
}
