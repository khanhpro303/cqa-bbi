package engine

import (
	"strings"
	"testing"

	"github.com/vietbui/chat-quality-agent/ai"
	"github.com/vietbui/chat-quality-agent/config"
	"github.com/vietbui/chat-quality-agent/db/models"
)

func plainSettings(kv map[string]string) map[string]models.AppSetting {
	out := make(map[string]models.AppSetting, len(kv))
	for k, v := range kv {
		out[k] = models.AppSetting{SettingKey: k, ValuePlain: v}
	}
	return out
}

func TestBuildTenantAIProviderForAgentModel(t *testing.T) {
	cfg := &config.Config{LangflowAPIKey: "legacy-openai"}
	settings := plainSettings(map[string]string{
		"ai_provider":       "claude",
		"ai_base_url":       "https://gateway.example/claude",
		"ai_api_key_claude": "sk-ant",
		"ai_api_key_openai": "sk-openai",
	})

	got, err := buildTenantAIProvider(settings, cfg, "agent model", AITaskModel{Provider: "openai", Model: "gpt-5-mini"}, AITaskModel{Provider: "claude"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if got.Provider != "openai" || got.Model != "gpt-5-mini" {
		t.Fatalf("got %+v", got)
	}
	if _, ok := got.Client.(*ai.OpenAIProvider); !ok {
		t.Fatalf("client = %T, want OpenAI", got.Client)
	}

	got, err = buildTenantAIProvider(settings, cfg, "agent model", AITaskModel{Provider: "claude", Model: "claude-haiku-4-5"}, AITaskModel{Provider: "claude"}, false)
	if err != nil || got.Client == nil {
		t.Fatalf("claude agent model: %+v, %v", got, err)
	}

	// No key for the agent's provider: an error, never another vendor's key
	// or the legacy Langflow key.
	_, err = buildTenantAIProvider(settings, cfg, "agent model", AITaskModel{Provider: "gemini", Model: "gemini-2.5-flash"}, AITaskModel{Provider: "claude"}, false)
	if err == nil || !strings.Contains(err.Error(), "agent model (gemini)") {
		t.Fatalf("missing gemini key: err = %v", err)
	}
	_, err = buildTenantAIProvider(plainSettings(map[string]string{"ai_provider": "openai"}), cfg, "agent model",
		AITaskModel{Provider: "openai", Model: "gpt-5-mini"}, AITaskModel{Provider: "claude"}, false)
	if err == nil {
		t.Fatal("the legacy Langflow key must not serve an agent model")
	}
}
