package ai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func TestOpenAIAnalyzeJSONSchemaUsesStrictSchemaInBothAPIs(t *testing.T) {
	schema := map[string]interface{}{"type": "object", "properties": map[string]interface{}{"assignments": map[string]interface{}{"type": "object"}}, "required": []interface{}{"assignments"}, "additionalProperties": false}
	for _, fallback := range []bool{false, true} {
		t.Run(map[bool]string{false: "responses", true: "chat fallback"}[fallback], func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if fallback && r.URL.Path == "/responses" {
					http.Error(w, "unsupported", http.StatusNotFound)
					return
				}
				var request map[string]interface{}
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Fatal(err)
				}
				var format, config map[string]interface{}
				if fallback {
					format = request["response_format"].(map[string]interface{})
					config = format["json_schema"].(map[string]interface{})
				} else {
					format = request["text"].(map[string]interface{})["format"].(map[string]interface{})
					config = format
				}
				if format["type"] != "json_schema" || config["strict"] != true || !reflect.DeepEqual(config["schema"], schema) {
					t.Errorf("strict schema missing: %#v", format)
				}
				w.Header().Set("Content-Type", "application/json")
				if fallback {
					_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"assignments\":{\"1\":\"E-24\"}}"}}]}`))
				} else {
					_, _ = w.Write([]byte(`{"output_text":"{\"assignments\":{\"1\":\"E-24\"}}"}`))
				}
			}))
			defer server.Close()
			response, err := NewOpenAIProvider("test-key", "gpt-4o-mini", server.URL).AnalyzeJSONSchema(context.Background(), "Return JSON", `{"product_names":["Mũ E-24"]}`, schema)
			if err != nil || response.Content != `{"assignments":{"1":"E-24"}}` {
				t.Fatalf("schema request failed: %#v %v", response, err)
			}
		})
	}
}

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
