package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/vietbui/chat-quality-agent/config"
	"github.com/vietbui/chat-quality-agent/engine"
)

func fakeLangflowServer(t *testing.T, goodKey string, flows ...string) *httptest.Server {
	t.Helper()
	known := map[string]bool{}
	for _, f := range flows {
		known["/api/v1/flows/"+f] = true
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-api-key") != goodKey {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"detail":"Invalid API key"}`))
			return
		}
		if !known[r.URL.Path] {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"detail":"Flow not found"}`))
			return
		}
		_, _ = w.Write([]byte(`{"data":{"nodes":[]}}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestRunLangflowFlowChecks_ReportsEachFlow(t *testing.T) {
	srv := fakeLangflowServer(t, "sk-good", "flow-private")
	client := engine.NewLangflowClient(&config.Config{})
	lfCfg := engine.LangflowConfig{APIURL: srv.URL, APIKey: "sk-good", PrivateFlowID: "flow-private", PublicFlowID: "flow-deleted"}

	checks := runLangflowFlowChecks(context.Background(), client, lfCfg)

	if len(checks) != 2 {
		t.Fatalf("expected private + public checks, got %+v", checks)
	}
	if !checks[0].OK || checks[0].Role != "private" {
		t.Fatalf("private flow should pass, got %+v", checks[0])
	}
	if checks[1].OK || !strings.Contains(checks[1].Error, "không tìm thấy flow") {
		t.Fatalf("deleted public flow should fail with a clear message, got %+v", checks[1])
	}
	failed := failedFlowChecks(checks)
	if len(failed) != 1 || !strings.Contains(failed[0], "flow-deleted") {
		t.Fatalf("expected one failure naming the flow, got %v", failed)
	}
}

func TestRunLangflowFlowChecks_BadKey(t *testing.T) {
	srv := fakeLangflowServer(t, "sk-good", "flow-private")
	client := engine.NewLangflowClient(&config.Config{})
	lfCfg := engine.LangflowConfig{APIURL: srv.URL, APIKey: "sk-stale", PrivateFlowID: "flow-private"}

	checks := runLangflowFlowChecks(context.Background(), client, lfCfg)

	if len(checks) != 1 || checks[0].OK || !strings.Contains(checks[0].Error, "API key") {
		t.Fatalf("stale key should fail with an API key message, got %+v", checks)
	}
}

func TestApplyLangflowFormOverrides(t *testing.T) {
	lfCfg := engine.LangflowConfig{APIKey: "sk-saved", PrivateFlowID: "saved", Sources: map[string]string{}}

	applyLangflowFormOverrides(&lfCfg, langflowTestRequest{
		LangflowFlowID:       " new-private ",
		LangflowPublicFlowID: "new-public",
		LangflowToken:        "••••••••",
	})

	if lfCfg.PrivateFlowID != "new-private" || lfCfg.PublicFlowID != "new-public" {
		t.Fatalf("form flow IDs should override, got %+v", lfCfg)
	}
	if lfCfg.APIKey != "sk-saved" {
		t.Fatalf("masked token must keep the saved key, got %q", lfCfg.APIKey)
	}
	if lfCfg.Sources["flow_id"] != "form" {
		t.Fatalf("source should be marked as form, got %v", lfCfg.Sources)
	}
}
