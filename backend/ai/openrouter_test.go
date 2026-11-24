package ai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOpenRouterProviderUsesChatCompletionsOnly(t *testing.T) {
	var paths []string
	var gotModel, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		gotAuth = r.Header.Get("Authorization")
		var body map[string]interface{}
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		gotModel, _ = body["model"].(string)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"model":"meta-llama/llama-3.3-70b-instruct:free","choices":[{"message":{"content":"{\"ok\":true}"}}],"usage":{"prompt_tokens":7,"completion_tokens":3}}`))
	}))
	defer srv.Close()

	p := NewOpenRouterProvider("or-key", "meta-llama/llama-3.3-70b-instruct:free", srv.URL+"/api/v1")
	resp, err := p.AnalyzeJSON(context.Background(), "sys", "hi")
	if err != nil {
		t.Fatalf("AnalyzeJSON: %v", err)
	}
	if len(paths) != 1 || paths[0] != "/api/v1/chat/completions" {
		t.Fatalf("expected one call to /api/v1/chat/completions, got %v", paths)
	}
	if gotModel != "meta-llama/llama-3.3-70b-instruct:free" {
		t.Errorf("model was rewritten: %q", gotModel)
	}
	if gotAuth != "Bearer or-key" {
		t.Errorf("auth header: %q", gotAuth)
	}
	if resp.Provider != ProviderOpenRouter || resp.Content != `{"ok":true}` || resp.InputTokens != 7 {
		t.Errorf("unexpected response: %+v", resp)
	}
}

func TestOpenRouterProviderDefaultsToOpenRouterURL(t *testing.T) {
	p := NewOpenRouterProvider("k", "", "")
	if got := p.chatCompletionsURL(); got != "https://openrouter.ai/api/v1/chat/completions" {
		t.Errorf("default URL: %s", got)
	}
	if p.model != DefaultModel(ProviderOpenRouter) {
		t.Errorf("default model: %s", p.model)
	}
}

func TestNewProviderClientSupportsEveryProvider(t *testing.T) {
	for _, name := range SupportedProviders {
		client, err := NewProviderClient(name, "key", "", "", 1024)
		if err != nil || client == nil {
			t.Errorf("%s: client=%v err=%v", name, client, err)
		}
	}
	if _, err := NewProviderClient("bogus", "key", "", "", 1024); err == nil {
		t.Error("unknown provider accepted")
	}
	if !IsSupportedProvider(ProviderOpenRouter) || IsSupportedProvider("bogus") {
		t.Error("IsSupportedProvider")
	}
}

func TestOpenRouterAPIKeySettingKey(t *testing.T) {
	if got := ProviderAPIKeySettingKey(ProviderOpenRouter); got != "ai_api_key_openrouter" {
		t.Errorf("got %s", got)
	}
}

func TestParseOpenRouterFreeModels(t *testing.T) {
	body := `{"data":[
		{"id":"meta-llama/llama-3.3-70b-instruct:free","name":"Llama 3.3 70B (free)","context_length":131072,"pricing":{"prompt":"0","completion":"0"},"architecture":{"input_modalities":["text"]}},
		{"id":"google/gemma-3-27b-it:free","name":"Gemma 3 27B (free)","context_length":96000,"pricing":{"prompt":"0","completion":"0"},"architecture":{"input_modalities":["text","image"]}},
		{"id":"openai/gpt-5-mini","name":"GPT-5 Mini","context_length":400000,"pricing":{"prompt":"0.00000025","completion":"0.000002"}},
		{"id":"weird/half-free","name":"Half","pricing":{"prompt":"0","completion":"0.000001"}}
	]}`
	got, err := parseOpenRouterFreeModels(strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 free models, got %+v", got)
	}
	if got[0].ID != "google/gemma-3-27b-it:free" || !got[0].Vision {
		t.Errorf("models should be sorted by id with vision flag: %+v", got)
	}
	if got[1].Vision || got[1].ContextLength != 131072 {
		t.Errorf("second model: %+v", got[1])
	}
}
