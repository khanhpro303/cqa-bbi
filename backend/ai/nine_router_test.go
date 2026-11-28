package ai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNineRouterChatCompletions(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Model  string `json:"model"`
			Format struct {
				Type string `json:"type"`
			} `json:"response_format"`
		}
		if r.Method != http.MethodPost || r.URL.Path != "/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer router-key" ||
			json.NewDecoder(r.Body).Decode(&body) != nil || body.Model != "cx/gpt-5.6-luna" || body.Format.Type != "json_object" {
			t.Error("unexpected 9router request")
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"{\"ok\":true}"}}],"usage":{"prompt_tokens":3,"completion_tokens":4}}`)
	}))
	defer server.Close()

	client, err := NewProvider("9router", "router-key", "cx/gpt-5.6-luna", server.URL+"/v1/", 100)
	if err != nil {
		t.Fatal(err)
	}
	reply, err := client.(JSONProvider).AnalyzeJSON(context.Background(), "Return JSON.", "Ping")
	if err != nil || reply.Provider != "9router" || reply.Model != "cx/gpt-5.6-luna" || reply.Content != `{"ok":true}` || reply.InputTokens != 3 || reply.OutputTokens != 4 {
		t.Fatalf("reply = %+v, %v", reply, err)
	}
	keys := ProviderAPIKeySettingKeys("9router")
	if len(keys) != 1 || keys[0] != "ai_api_key_9router" {
		t.Fatalf("9router key resolution = %v", keys)
	}
}

func TestNineRouterRequiresExplicitEndpointAndModel(t *testing.T) {
	for _, invalidURL := range []string{"", "https://router.example.com", "https://user@router.example.com/v1", "https://router.example.com/v1#", "https://router.example.com/v1?x=y"} {
		if _, err := NewProvider("9router", "router-key", "cx/model", invalidURL, 100); err == nil {
			t.Errorf("accepted invalid 9router endpoint %q", invalidURL)
		}
	}
	if _, err := NewProvider("9router", "router-key", " ", "https://router.example.com/v1", 100); err == nil {
		t.Fatal("accepted an empty model")
	}
}
