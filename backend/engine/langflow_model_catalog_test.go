package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
)

func TestLangflowFlowModelUsesCurrentCatalogWithoutChangingSelectedModel(t *testing.T) {
	flow := strings.ReplaceAll(modelFlowFixture, "gpt-5-mini", "gpt-5.4")
	newModel := "gpt-6.2-sol"
	catalogStatus := http.StatusOK
	gets, patches := 0, 0
	var mu sync.Mutex
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.Header.Get("x-api-key") != "catalog-test-token" || r.Header.Get("Authorization") != "" {
			t.Error("expected Langflow's normal API key header")
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/models":
			if r.URL.Query().Get("tool_calling") != "true" || r.URL.Query().Get("provider") != "OpenAI" {
				t.Errorf("unexpected catalog query: %s", r.URL.RawQuery)
			}
			gets++
			w.WriteHeader(catalogStatus)
			fmt.Fprintf(w, `[{"provider":"OpenAI","icon":"OpenAI","models":[
				{"model_name":%q,"metadata":{"model_type":"llm","tool_calling":true,"reasoning":true,"context_window":1050000}},
				{"model_name":"gpt-6.2-pro","metadata":{"model_type":"llm","tool_calling":true}},
				{"model_name":"gpt-6.2-mini","metadata":{"model_type":"llm","tool_calling":false}},
				{"model_name":"gpt-6.2-astra","metadata":{"model_type":"llm","tool_calling":true}},
				{"model_name":"gpt-5.4","metadata":{"model_type":"llm","tool_calling":true}}
			]}]`, newModel)
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/models/enabled_models":
			fmt.Fprintf(w, `{"enabled_models":{"OpenAI":{%q:true,"gpt-6.2-pro":true,"gpt-6.2-mini":true,"gpt-6.2-astra":false,"gpt-5.4":true}}}`, newModel)
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/api/v1/flows/"):
			_, _ = io.WriteString(w, flow)
		case r.Method == http.MethodPatch && r.URL.Path == "/api/v1/flows/internal":
			patches++
			var patch map[string]json.RawMessage
			if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
				t.Errorf("decode patch: %v", err)
			}
			flow = `{"name":"Internal bot","data":` + string(patch["data"]) + `}`
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	ctx := context.Background()
	for _, id := range []string{"internal", "public"} {
		model, err := ReadLangflowFlowModel(ctx, server.URL, "catalog-test-token", id)
		if err != nil || model.Model != "gpt-5.4" || model.Provider != "OpenAI" {
			t.Fatalf("read %s = %+v, %v", id, model, err)
		}
		var models []string
		for _, option := range model.Options {
			if option.Provider == "OpenAI" {
				models = append(models, option.Name)
			}
		}
		if !slices.Equal(models, []string{"gpt-5.4", "gpt-4o-mini", newModel}) {
			t.Fatalf("%s options = %v", id, models)
		}
	}
	mu.Lock()
	if gets != 2 || patches != 0 {
		mu.Unlock()
		t.Fatalf("catalog refresh changed a flow or failed to refresh both: GETs=%d PATCHes=%d", gets, patches)
	}

	// A model first appearing after deploy is pulled live, with no CQA
	// release, and materialized only on an explicit sync.
	newModel = "gpt-7.1-sol"
	mu.Unlock()
	model, err := SyncLangflowFlowModel(ctx, server.URL, "catalog-test-token", "internal", "OpenAI", newModel)
	mu.Lock()
	patchCount := patches
	mu.Unlock()
	if err != nil || model.Model != newModel || patchCount != 1 {
		t.Fatalf("new catalog model sync = %+v, %v, patches=%d", model, err, patchCount)
	}
	if names, ok := model.Metadata["reasoning_models"].([]any); !ok || len(names) != 1 || names[0] != newModel {
		t.Fatalf("reasoning compatibility metadata = %+v", model.Metadata)
	}
	mu.Lock()
	catalogStatus = http.StatusServiceUnavailable
	mu.Unlock()
	model, err = ReadLangflowFlowModel(ctx, server.URL, "catalog-test-token", "internal")
	if err != nil || model.Model != newModel || len(model.Options) != 3 {
		t.Fatalf("catalog failure should keep the saved graph usable: %+v, %v", model, err)
	}
	_, err = SyncLangflowFlowModel(ctx, server.URL, "catalog-test-token", "internal", "OpenAI", "gpt-7.2-sol")
	mu.Lock()
	patchCount = patches
	mu.Unlock()
	if modelErr, ok := IsLangflowFlowModelError(err); !ok || modelErr.Code != "model_not_available" || patchCount != 1 {
		t.Fatalf("unlisted model must remain blocked when the live catalog is unavailable: %v, patches=%d", err, patchCount)
	}
}
