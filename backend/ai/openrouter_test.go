package ai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOpenRouterUsesChatCompletionsOnly(t *testing.T) {
	var paths []string
	var gotModel, gotAuth, gotTitle string
	var gotFormat interface{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		gotAuth, gotTitle = r.Header.Get("Authorization"), r.Header.Get("X-Title")
		body, _ := io.ReadAll(r.Body)
		var req map[string]interface{}
		_ = json.Unmarshal(body, &req)
		gotModel, _ = req["model"].(string)
		gotFormat = req["response_format"]
		_, _ = w.Write([]byte(`{"model":"deepseek/deepseek-chat-v3-0324:free","choices":[{"message":{"content":"{\"ok\":true}"}}],"usage":{"prompt_tokens":12,"completion_tokens":3}}`))
	}))
	defer srv.Close()

	p := NewOpenRouterProvider("sk-or-test", "deepseek/deepseek-chat-v3-0324:free", srv.URL+"/api/v1")
	resp, err := p.AnalyzeJSON(context.Background(), "sys", "hi")
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 1 || paths[0] != "/api/v1/chat/completions" {
		t.Fatalf("paths = %v, want only chat completions", paths)
	}
	if gotAuth != "Bearer sk-or-test" || gotTitle != "CQA" {
		t.Errorf("headers auth=%q title=%q", gotAuth, gotTitle)
	}
	if gotModel != "deepseek/deepseek-chat-v3-0324:free" {
		t.Errorf("model sent = %q (must not be remapped)", gotModel)
	}
	if gotFormat == nil {
		t.Error("JSON mode should send response_format")
	}
	if resp.Provider != "openrouter" || resp.InputTokens != 12 || resp.OutputTokens != 3 {
		t.Errorf("resp = %+v", resp)
	}
	if CalculateCostUSD("openrouter", resp.Model, 1000, 1000) != 0 {
		t.Error("free OpenRouter models cost nothing")
	}
}

func TestNewProviderAndKeys(t *testing.T) {
	p, err := NewProvider("openrouter", "k", "", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	op := p.(*OpenAIProvider)
	if op.model != OpenRouterDefaultModel || op.chatCompletionsURL() != OpenRouterBaseURL+"/chat/completions" {
		t.Errorf("defaults: model=%q url=%q", op.model, op.chatCompletionsURL())
	}
	if _, err := NewProvider("nope", "k", "", "", 0); err == nil {
		t.Error("unknown provider accepted")
	}
	if keys := ProviderAPIKeySettingKeys("openrouter"); len(keys) != 1 || keys[0] != "ai_api_key_openrouter" {
		t.Errorf("openrouter keys = %v (must not fall back to the legacy key)", keys)
	}
	if o := NewOpenAIProvider("k", "gpt-5", ""); o.providerName() != "openai" || o.chatOnly {
		t.Error("OpenAI provider changed")
	}
}

func TestParseOpenRouterFreeModels(t *testing.T) {
	body := []byte(`{"data":[
		{"id":"openai/gpt-4o","name":"GPT-4o","pricing":{"prompt":"0.0000025","completion":"0.00001"}},
		{"id":"meta-llama/llama-3.3-70b-instruct:free","name":"Llama 3.3 70B (free)","context_length":131072,"pricing":{"prompt":"0","completion":"0"},"architecture":{"input_modalities":["text"]},"supported_parameters":["response_format","temperature"]},
		{"id":"google/gemma-3-27b-it:free","name":"Gemma 3 27B (free)","pricing":{"prompt":"0","completion":"0"},"architecture":{"input_modalities":["text","image"]}},
		{"id":"openrouter/auto","name":"Auto","pricing":{"prompt":"-1","completion":"-1"}}
	]}`)
	got, err := ParseOpenRouterFreeModels(body)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ID != "google/gemma-3-27b-it:free" || got[1].ID != "meta-llama/llama-3.3-70b-instruct:free" {
		t.Fatalf("free models = %+v", got)
	}
	if !got[0].Vision || got[0].JSONMode || got[1].Vision || !got[1].JSONMode || got[1].ContextLength != 131072 {
		t.Errorf("flags = %+v", got)
	}
}
