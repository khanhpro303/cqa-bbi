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
	for _, nodeID := range fallbackAgentNodeIDs {
		if _, present := tweaks[nodeID]; present {
			t.Fatalf("expected %q tweak to be absent when systemPrompt is empty, got %#v", nodeID, tweaks[nodeID])
		}
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
	// The prompt must reach every known agent node so it applies whether the
	// private or the public flow handles the request.
	for _, nodeID := range fallbackAgentNodeIDs {
		agentTweaks, ok := tweaks[nodeID].(map[string]interface{})
		if !ok {
			t.Fatalf("expected %q tweaks object, got %#v", nodeID, tweaks[nodeID])
		}
		if got := agentTweaks["system_prompt"]; got != wantPrompt {
			t.Fatalf("system_prompt for %q = %#v, want %q", nodeID, got, wantPrompt)
		}
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
	for _, nodeID := range fallbackAgentNodeIDs {
		agentTweaks, ok := tweaks[nodeID].(map[string]interface{})
		if !ok {
			t.Fatalf("expected %q tweaks object, got %#v", nodeID, tweaks[nodeID])
		}
		if got := agentTweaks["system_prompt"]; got != wantPrompt {
			t.Fatalf("system_prompt for %q = %#v, want %q", nodeID, got, wantPrompt)
		}
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

// flowDiscoveryRoundTripper serves a flow definition on GET /api/v1/flows/{id}
// and captures the POST /api/v1/run/{id} payload, so tests can exercise the live
// agent-node discovery path end to end.
type flowDiscoveryRoundTripper struct {
	flowBody string
	payload  map[string]interface{}
	getCount int
}

func (f *flowDiscoveryRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) {
	respond := func(body string) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(bytes.NewBufferString(body)),
			Request:    r,
		}, nil
	}

	if r.Method == http.MethodGet {
		f.getCount++
		return respond(f.flowBody)
	}

	if err := json.NewDecoder(r.Body).Decode(&f.payload); err != nil {
		return nil, err
	}
	return respond(`{"outputs":[{"outputs":[{"results":{"message":{"text":"ok"}}}]}]}`)
}

const discoveryFlowBody = `{"data":{"nodes":[
	{"id":"ToolCallingAgent-DISCOVER","data":{"node":{"template":{"system_prompt":{"value":"SYSTEM_PROMPT","load_from_db":true}}}}},
	{"id":"ChatInput-irrelevant","data":{"node":{"template":{"input_value":{"value":""}}}}}
]}}`

func TestRunFlowDiscoversAgentNodeFromFlowDefinition(t *testing.T) {
	transport := &flowDiscoveryRoundTripper{flowBody: discoveryFlowBody}
	client := NewLangflowClient(&config.Config{})
	client.client = &http.Client{Transport: transport}

	const wantPrompt = "Bạn là trợ lý công khai của BBI."
	if _, err := client.RunFlowWithCustomer(
		context.Background(),
		"session-disc",
		"",
		"hello",
		"https://langflow.example",
		"langflow-key",
		"public-flow-xyz",
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

	// The prompt must land on the node discovered from THIS flow's definition,
	// not on a hardcoded fallback ID.
	agentTweaks, ok := tweaks["ToolCallingAgent-DISCOVER"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected discovered node tweak, got %#v", tweaks["ToolCallingAgent-DISCOVER"])
	}
	if got := agentTweaks["system_prompt"]; got != wantPrompt {
		t.Fatalf("system_prompt = %#v, want %q", got, wantPrompt)
	}
	// Nodes without a system_prompt field must not be tweaked.
	if _, present := tweaks["ChatInput-irrelevant"]; present {
		t.Errorf("non-agent node must not receive a system_prompt tweak")
	}
	// Hardcoded fallbacks must not leak in once discovery succeeds.
	for _, fb := range fallbackAgentNodeIDs {
		if _, present := tweaks[fb]; present {
			t.Errorf("fallback node %q must not be tweaked when discovery succeeds", fb)
		}
	}
}

func TestResolveAgentNodeIDsCachesPerFlow(t *testing.T) {
	transport := &flowDiscoveryRoundTripper{flowBody: discoveryFlowBody}
	client := NewLangflowClient(&config.Config{})
	client.client = &http.Client{Transport: transport}

	for i := 0; i < 3; i++ {
		got := client.resolveAgentNodeIDs(context.Background(), "https://langflow.example", "key", "flow-cache")
		if len(got) != 1 || got[0] != "ToolCallingAgent-DISCOVER" {
			t.Fatalf("resolveAgentNodeIDs = %#v, want [ToolCallingAgent-DISCOVER]", got)
		}
	}
	if transport.getCount != 1 {
		t.Fatalf("expected flow definition fetched once (cached), got %d GETs", transport.getCount)
	}
}

func TestExtractAgentNodeIDs(t *testing.T) {
	t.Run("picks only nodes with system_prompt", func(t *testing.T) {
		ids, err := extractAgentNodeIDs([]byte(discoveryFlowBody))
		if err != nil {
			t.Fatalf("extractAgentNodeIDs error: %v", err)
		}
		if len(ids) != 1 || ids[0] != "ToolCallingAgent-DISCOVER" {
			t.Fatalf("ids = %#v, want [ToolCallingAgent-DISCOVER]", ids)
		}
	})

	t.Run("multiple agent nodes", func(t *testing.T) {
		body := `{"data":{"nodes":[
			{"id":"A","data":{"node":{"template":{"system_prompt":{}}}}},
			{"id":"B","data":{"node":{"template":{"system_prompt":{}}}}}
		]}}`
		ids, err := extractAgentNodeIDs([]byte(body))
		if err != nil {
			t.Fatalf("extractAgentNodeIDs error: %v", err)
		}
		if len(ids) != 2 {
			t.Fatalf("ids = %#v, want 2 entries", ids)
		}
	})

	t.Run("no agent nodes yields empty", func(t *testing.T) {
		body := `{"data":{"nodes":[{"id":"X","data":{"node":{"template":{"input_value":{}}}}}]}}`
		ids, err := extractAgentNodeIDs([]byte(body))
		if err != nil {
			t.Fatalf("extractAgentNodeIDs error: %v", err)
		}
		if len(ids) != 0 {
			t.Fatalf("ids = %#v, want empty", ids)
		}
	})

	t.Run("malformed json errors", func(t *testing.T) {
		if _, err := extractAgentNodeIDs([]byte("not json")); err == nil {
			t.Fatalf("expected error for malformed json")
		}
	})
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
