package ai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOpenAIAnalyzeJSONRequestsResponsesJSONObjectAndPreservesUsage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/responses" {
			t.Fatalf("request path = %q, want /responses", r.URL.Path)
		}
		var request map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		text, ok := request["text"].(map[string]interface{})
		if !ok {
			t.Fatalf("text format missing from JSON request: %#v", request)
		}
		format, ok := text["format"].(map[string]interface{})
		if !ok || format["type"] != "json_object" {
			t.Fatalf("text.format = %#v, want json_object", text["format"])
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"model":"gpt-4o-mini","output_text":"{\"groups\":[]}","usage":{"input_tokens":17,"output_tokens":9}}`))
	}))
	defer server.Close()

	provider := NewOpenAIProvider("test-key", "gpt-5-mini", server.URL)
	response, err := provider.AnalyzeJSON(context.Background(), "system", `{"product_names":[]}`)
	if err != nil {
		t.Fatal(err)
	}
	if response.Content != `{"groups":[]}` || response.InputTokens != 17 || response.OutputTokens != 9 || response.Model != "gpt-4o-mini" || response.Provider != "openai" {
		t.Fatalf("unexpected response: %#v", response)
	}
}

func TestOpenAIAnalyzeJSONFallbackRequestsChatCompletionsJSONObject(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/responses":
			http.Error(w, "unsupported", http.StatusNotFound)
		case "/chat/completions":
			var request map[string]interface{}
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Fatal(err)
			}
			format, ok := request["response_format"].(map[string]interface{})
			if !ok || format["type"] != "json_object" {
				t.Fatalf("response_format = %#v, want json_object", request["response_format"])
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"model":"gpt-4o-mini","choices":[{"message":{"content":"{\"groups\":[]}"}}],"usage":{"prompt_tokens":23,"completion_tokens":11}}`))
		default:
			t.Fatalf("unexpected request path %q", r.URL.Path)
		}
	}))
	defer server.Close()

	provider := NewOpenAIProvider("test-key", "gpt-5-mini", server.URL)
	response, err := provider.AnalyzeJSON(context.Background(), "system", `{"product_names":[]}`)
	if err != nil {
		t.Fatal(err)
	}
	if response.Content != `{"groups":[]}` || response.InputTokens != 23 || response.OutputTokens != 11 || response.Provider != "openai" {
		t.Fatalf("unexpected fallback response: %#v", response)
	}
}

func TestOpenAIAnalyzeChatDoesNotRequestJSONObjectMode(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		if _, exists := request["text"]; exists {
			t.Fatalf("normal chat unexpectedly requested JSON mode: %#v", request["text"])
		}
		if _, exists := request["response_format"]; exists {
			t.Fatalf("normal chat unexpectedly requested JSON mode: %#v", request["response_format"])
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"output_text":"plain text","usage":{"input_tokens":3,"output_tokens":2}}`))
	}))
	defer server.Close()

	provider := NewOpenAIProvider("test-key", "gpt-5-mini", server.URL)
	response, err := provider.AnalyzeChat(context.Background(), "system", "input")
	if err != nil {
		t.Fatal(err)
	}
	if response.Content != "plain text" || response.InputTokens != 3 || response.OutputTokens != 2 {
		t.Fatalf("unexpected normal response: %#v", response)
	}
}
