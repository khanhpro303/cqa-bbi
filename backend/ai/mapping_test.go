package ai

import "testing"

func TestModelMappingOpenAI(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"gpt-5.4-mini", "gpt-4o-mini"},
		{"gpt-5-mini", "gpt-4o-mini"},
		{"gpt-5-nano", "gpt-4o-mini"},
		{"gpt-5", "gpt-4o"},
		{"gpt-5.3-codex", "gpt-4o"},
		{"gpt-5.3-chat-latest", "gpt-4o"},
		{"gpt-4o-mini", "gpt-4o-mini"}, // normal/unmapped stays the same
		{"", "gpt-4o-mini"}, // default case
	}

	for _, tt := range tests {
		p := NewOpenAIProvider("test_key", tt.input, "")
		if p.model != tt.expected {
			t.Errorf("NewOpenAIProvider model mapping failed for %q: expected %q, got %q", tt.input, tt.expected, p.model)
		}
	}
}

func TestModelMappingClaude(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"claude-sonnet-4-6", "claude-3-5-sonnet-latest"},
		{"claude-sonnet-4-5-20250929", "claude-3-5-sonnet-latest"},
		{"claude-sonnet-4-5", "claude-3-5-sonnet-latest"},
		{"claude-haiku-4-5-20251001", "claude-3-5-haiku-latest"},
		{"claude-haiku-4-5", "claude-3-5-haiku-latest"},
		{"claude-opus-4", "claude-3-opus-latest"},
		{"claude-opus-4-6", "claude-3-opus-latest"},
		{"claude-3-5-sonnet-latest", "claude-3-5-sonnet-latest"}, // normal/unmapped stays the same
		{"", "claude-3-5-sonnet-latest"}, // default case
	}

	for _, tt := range tests {
		p := NewClaudeProvider("test_key", tt.input, 0, "")
		if p.model != tt.expected {
			t.Errorf("NewClaudeProvider model mapping failed for %q: expected %q, got %q", tt.input, tt.expected, p.model)
		}
	}
}
