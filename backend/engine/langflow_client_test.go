package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/vietbui/chat-quality-agent/config"
)

type captureRoundTripper struct {
	payload map[string]interface{}
}

func (c *captureRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.Method != http.MethodPost {
		return nil, &methodError{method: r.Method}
	}

	if err := json.NewDecoder(r.Body).Decode(&c.payload); err != nil {
		return nil, err
	}

	body := `{"outputs":[{"outputs":[{"results":{"message":{"text":"ok"}}}]}]}`
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(bytes.NewBufferString(body)),
		Request:    r,
	}, nil
}

type methodError struct {
	method string
}

func (m *methodError) Error() string {
	return "unexpected method " + m.method
}

func TestRunFlowWithCustomerPassesPermissionTokenToCustomComponent(t *testing.T) {
	transport := &captureRoundTripper{}
	client := NewLangflowClient(&config.Config{})
	client.client = &http.Client{Transport: transport}

	reply, err := client.RunFlowWithCustomer(
		context.Background(),
		"session-1",
		"zalo-user-1",
		"FF800 ton may cai",
		"https://langflow.example",
		"langflow-key",
		"flow-1",
		"S084 - Cho Bao Ho",
		"permission-token-1",
		"",
	)
	if err != nil {
		t.Fatalf("RunFlowWithCustomer returned error: %v", err)
	}
	if reply != "ok" {
		t.Fatalf("expected reply ok, got %q", reply)
	}

	tweaks, ok := transport.payload["tweaks"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected tweaks object, got %#v", transport.payload["tweaks"])
	}

	if got := tweaks["permission_token"]; got != "permission-token-1" {
		t.Fatalf("expected top-level permission_token tweak, got %#v", got)
	}

	customTweaks, ok := tweaks["CustomComponent"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected CustomComponent tweaks object, got %#v", tweaks["CustomComponent"])
	}
	if got := customTweaks["permission_token"]; got != "permission-token-1" {
		t.Fatalf("expected CustomComponent permission_token, got %#v", got)
	}
	if got := customTweaks["customer_code"]; got != "S084 - Cho Bao Ho" {
		t.Fatalf("expected CustomComponent customer_code, got %#v", got)
	}
	if got := customTweaks["zalo_user_id"]; got != "zalo-user-1" {
		t.Fatalf("expected CustomComponent zalo_user_id, got %#v", got)
	}

	retrieverTweaks, ok := tweaks["AstraDB-HistoryRetriever"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected AstraDB-HistoryRetriever tweaks object, got %#v", tweaks["AstraDB-HistoryRetriever"])
	}
	for _, key := range []string{"search_filter", "advanced_search_filter"} {
		filter, ok := retrieverTweaks[key].(map[string]interface{})
		if !ok {
			t.Fatalf("expected %s object, got %#v", key, retrieverTweaks[key])
		}
		if got := filter["metadata.zalo_user_id"]; got != "zalo-user-1" {
			t.Errorf("%s[metadata.zalo_user_id] = %#v, want zalo-user-1", key, got)
		}
		// session_id must NOT be in the filter — that would kill cross-session
		// memory recall. Sequential short-term context is Memory-zEYL8's job.
		if _, present := filter["metadata.session_id"]; present {
			t.Errorf("%s must not contain metadata.session_id (breaks long-term memory)", key)
		}
		// Disambiguation docs (Go option-list pushes) must be excluded so two
		// option lists in the same session don't blur into the same "1" reply.
		excl, ok := filter["metadata.is_disambiguation"].(map[string]interface{})
		if !ok {
			t.Errorf("%s missing metadata.is_disambiguation exclusion, got %#v", key, filter["metadata.is_disambiguation"])
			continue
		}
		if excl["$ne"] != true {
			t.Errorf("%s[metadata.is_disambiguation] = %#v, want {$ne: true}", key, excl)
		}
	}

	for _, node := range []string{"MsgToDoc-User", "MsgToDoc-Assistant"} {
		nodeTweaks, ok := tweaks[node].(map[string]interface{})
		if !ok {
			t.Fatalf("expected %s tweaks object, got %#v", node, tweaks[node])
		}
		if got := nodeTweaks["zalo_user_id"]; got != "zalo-user-1" {
			t.Fatalf("expected %s zalo_user_id, got %#v", node, got)
		}
	}
}

func TestRunFlowWithCustomerOmitsZaloUserIDTweaksWhenEmpty(t *testing.T) {
	transport := &captureRoundTripper{}
	client := NewLangflowClient(&config.Config{})
	client.client = &http.Client{Transport: transport}

	if _, err := client.RunFlowWithCustomer(
		context.Background(),
		"session-2",
		"",
		"hello",
		"https://langflow.example",
		"langflow-key",
		"flow-2",
		"",
		"",
		"",
	); err != nil {
		t.Fatalf("RunFlowWithCustomer returned error: %v", err)
	}

	tweaks, ok := transport.payload["tweaks"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected tweaks object, got %#v", transport.payload["tweaks"])
	}

	for _, key := range []string{"zalo_user_id", "AstraDB-HistoryRetriever", "MsgToDoc-User", "MsgToDoc-Assistant"} {
		if _, present := tweaks[key]; present {
			t.Fatalf("expected tweak %q to be absent when zaloUserID is empty, got %#v", key, tweaks[key])
		}
	}
	// An empty systemPrompt must not emit an agent tweak — the flow then falls
	// back to its SYSTEM_PROMPT global variable.
	if _, present := tweaks[langflowAgentNodeID]; present {
		t.Fatalf("expected %q tweak to be absent when systemPrompt is empty, got %#v", langflowAgentNodeID, tweaks[langflowAgentNodeID])
	}
}

func TestRunFlowWithCustomerPassesSystemPromptTweak(t *testing.T) {
	transport := &captureRoundTripper{}
	client := NewLangflowClient(&config.Config{})
	client.client = &http.Client{Transport: transport}

	const wantPrompt = "Bạn là trợ lý bán hàng của BBI."
	if _, err := client.RunFlowWithCustomer(
		context.Background(),
		"session-sp",
		"",
		"hello",
		"https://langflow.example",
		"langflow-key",
		"flow-sp",
		"",
		"",
		wantPrompt,
	); err != nil {
		t.Fatalf("RunFlowWithCustomer returned error: %v", err)
	}

	tweaks, ok := transport.payload["tweaks"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected tweaks object, got %#v", transport.payload["tweaks"])
	}
	agentTweaks, ok := tweaks[langflowAgentNodeID].(map[string]interface{})
	if !ok {
		t.Fatalf("expected %q tweaks object, got %#v", langflowAgentNodeID, tweaks[langflowAgentNodeID])
	}
	if got := agentTweaks["system_prompt"]; got != wantPrompt {
		t.Fatalf("system_prompt = %#v, want %q", got, wantPrompt)
	}
}

func TestRunFlowWithOverridesPassesSystemPromptTweak(t *testing.T) {
	transport := &captureRoundTripper{}
	client := NewLangflowClient(&config.Config{})
	client.client = &http.Client{Transport: transport}

	const wantPrompt = "You are a helpful assistant."
	if _, err := client.RunFlowWithOverrides(
		context.Background(),
		"session-sp2",
		"",
		"ping",
		"https://langflow.example",
		"langflow-key",
		"flow-sp2",
		wantPrompt,
	); err != nil {
		t.Fatalf("RunFlowWithOverrides returned error: %v", err)
	}

	tweaks, ok := transport.payload["tweaks"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected tweaks object, got %#v", transport.payload["tweaks"])
	}
	agentTweaks, ok := tweaks[langflowAgentNodeID].(map[string]interface{})
	if !ok {
		t.Fatalf("expected %q tweaks object, got %#v", langflowAgentNodeID, tweaks[langflowAgentNodeID])
	}
	if got := agentTweaks["system_prompt"]; got != wantPrompt {
		t.Fatalf("system_prompt = %#v, want %q", got, wantPrompt)
	}
}

func TestRunFlowWithOverridesPassesZaloUserIDToHistoryNodes(t *testing.T) {
	transport := &captureRoundTripper{}
	client := NewLangflowClient(&config.Config{})
	client.client = &http.Client{Transport: transport}

	if _, err := client.RunFlowWithOverrides(
		context.Background(),
		"session-3",
		"zalo-user-9",
		"ping",
		"https://langflow.example",
		"langflow-key",
		"flow-3",
		"",
	); err != nil {
		t.Fatalf("RunFlowWithOverrides returned error: %v", err)
	}

	tweaks, ok := transport.payload["tweaks"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected tweaks object, got %#v", transport.payload["tweaks"])
	}

	retrieverTweaks, ok := tweaks["AstraDB-HistoryRetriever"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected AstraDB-HistoryRetriever tweaks object, got %#v", tweaks["AstraDB-HistoryRetriever"])
	}
	for _, key := range []string{"search_filter", "advanced_search_filter"} {
		filter, ok := retrieverTweaks[key].(map[string]interface{})
		if !ok {
			t.Fatalf("expected %s object, got %#v", key, retrieverTweaks[key])
		}
		if got := filter["metadata.zalo_user_id"]; got != "zalo-user-9" {
			t.Errorf("%s[metadata.zalo_user_id] = %#v, want zalo-user-9", key, got)
		}
		if _, present := filter["metadata.session_id"]; present {
			t.Errorf("%s must not contain metadata.session_id", key)
		}
		if _, present := filter["zalo_user_id"]; present {
			t.Errorf("%s contains un-prefixed zalo_user_id (must use metadata. prefix)", key)
		}
	}

	for _, node := range []string{"MsgToDoc-User", "MsgToDoc-Assistant"} {
		nodeTweaks, ok := tweaks[node].(map[string]interface{})
		if !ok {
			t.Fatalf("expected %s tweaks object, got %#v", node, tweaks[node])
		}
		if got := nodeTweaks["zalo_user_id"]; got != "zalo-user-9" {
			t.Fatalf("expected %s zalo_user_id, got %#v", node, got)
		}
	}
}

func TestBuildHistoryFilter(t *testing.T) {
	excludeDisambig := map[string]interface{}{"$ne": true}

	t.Run("with user id", func(t *testing.T) {
		got := buildHistoryFilter("u1")
		if got["metadata.zalo_user_id"] != "u1" {
			t.Errorf("metadata.zalo_user_id = %#v, want u1", got["metadata.zalo_user_id"])
		}
		excl, ok := got["metadata.is_disambiguation"].(map[string]interface{})
		if !ok || excl["$ne"] != excludeDisambig["$ne"] {
			t.Errorf("metadata.is_disambiguation = %#v, want %#v", got["metadata.is_disambiguation"], excludeDisambig)
		}
		if _, present := got["metadata.session_id"]; present {
			t.Errorf("filter must not contain metadata.session_id (cross-session memory)")
		}
	})

	t.Run("empty user keeps disambiguation exclusion", func(t *testing.T) {
		got := buildHistoryFilter("")
		if _, present := got["metadata.zalo_user_id"]; present {
			t.Errorf("empty user must skip metadata.zalo_user_id entry")
		}
		if _, present := got["metadata.is_disambiguation"]; !present {
			t.Errorf("disambiguation exclusion must apply unconditionally")
		}
	})
}
