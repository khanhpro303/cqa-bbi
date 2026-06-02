package workers

import "testing"

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
