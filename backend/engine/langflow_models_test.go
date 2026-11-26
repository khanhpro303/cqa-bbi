package engine

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const modelFlowFixture = `{
  "name": "Internal bot", "version_token": "version-1",
  "data": {
    "nodes": [
      {"id":"ToolCallingAgent-example","data":{"node":{"template":{
        "system_prompt":{"value":"preserved"},
        "api_key":{"value":"LANGFLOW_KEY","load_from_db":true},
        "model":{"_input_type":"ModelInput","value":[{"name":"gpt-5-mini","provider":"OpenAI"}],"options":[
          {"name":"gpt-5-mini","provider":"OpenAI"},
          {"name":"gpt-4o-mini","provider":"OpenAI","metadata":{"tool_calling":true}},
          {"name":"claude-haiku-4-5","provider":"Anthropic"}
        ]}
      }}}},
      {"id":"ChatOutput-keep","data":{"node":{"template":{"text":{"value":""}}}}}
    ],
    "edges":[{"source":"ToolCallingAgent-example","target":"ChatOutput-keep"}],
    "viewport":{"zoom":1.2}
  }
}`

func TestSyncLangflowFlowModelPreservesGraphAndConfirmsReadback(t *testing.T) {
	body := modelFlowFixture
	gets, patches := 0, 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/flows/flow" || r.Header.Get("x-api-key") != "test-token" || r.Header.Get("Authorization") != "" {
			t.Errorf("unexpected Langflow request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.Method {
		case http.MethodGet:
			gets++
			_, _ = io.WriteString(w, body)
		case http.MethodPatch:
			patches++
			if got := r.Header.Get("If-Match"); got != `"version-1"` {
				t.Errorf("If-Match = %q", got)
			}
			var patch map[string]json.RawMessage
			if err := json.NewDecoder(r.Body).Decode(&patch); err != nil || len(patch) != 1 {
				t.Errorf("invalid graph-only patch: %v", err)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			graph := string(patch["data"])
			for _, want := range []string{`"tool_calling":true`, `"value":"LANGFLOW_KEY"`, `"value":"preserved"`, `"edges"`, `"zoom":1.2`} {
				if !strings.Contains(graph, want) {
					t.Errorf("PATCH graph missing %s", want)
				}
			}
			body = `{"name":"Internal bot","data":` + graph + `}`
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
	defer server.Close()
	result, err := SyncLangflowFlowModel(context.Background(), server.URL, "test-token", "flow", "OpenAI", "gpt-4o-mini")
	if err != nil || result.Provider != "OpenAI" || result.Model != "gpt-4o-mini" || gets != 2 || patches != 1 {
		t.Fatalf("sync result = %+v, error = %v, GETs = %d, PATCHes = %d", result, err, gets, patches)
	}
	if result.Metadata["tool_calling"] != true || len(result.AgentNodeIDs) != 1 {
		t.Fatalf("lost model metadata: %+v", result)
	}
	_, err = SyncLangflowFlowModel(context.Background(), server.URL, "test-token", "flow", "OpenAI", "gpt-4o-mini")
	if err != nil || patches != 1 {
		t.Fatalf("repeating current model should be a no-op, patches = %d, error = %v", patches, err)
	}
}

func TestSyncLangflowFlowModelRejectsUnsafeChoices(t *testing.T) {
	for _, tc := range []struct{ provider, model, code string }{
		{"OpenAI", "missing-model", "model_not_available"},
		{"Anthropic", "claude-haiku-4-5", "flow_provider_change_requires_langflow"},
	} {
		t.Run(tc.model, func(t *testing.T) {
			_, _, _, err := patchLangflowFlowModel([]byte(modelFlowFixture), tc.provider, tc.model)
			got, ok := IsLangflowFlowModelError(err)
			if !ok || got.Code != tc.code {
				t.Fatalf("error = %v, want %s", err, tc.code)
			}
		})
	}
}

func TestSyncLangflowFlowModelFailsIfPatchIsNotApplied(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, modelFlowFixture)
	}))
	defer server.Close()
	_, err := SyncLangflowFlowModel(context.Background(), server.URL, "", "flow", "OpenAI", "gpt-4o-mini")
	got, ok := IsLangflowFlowModelError(err)
	if !ok || got.Code != "model_sync_not_applied" {
		t.Fatalf("error = %v, want readback mismatch", err)
	}
}
