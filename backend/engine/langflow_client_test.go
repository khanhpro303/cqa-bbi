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
	filter, ok := retrieverTweaks["advanced_search_filter"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected advanced_search_filter object, got %#v", retrieverTweaks["advanced_search_filter"])
	}
	if got := filter["zalo_user_id"]; got != "zalo-user-1" {
		t.Fatalf("expected AstraDB-HistoryRetriever filter zalo_user_id, got %#v", got)
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
	filter, ok := retrieverTweaks["advanced_search_filter"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected advanced_search_filter object, got %#v", retrieverTweaks["advanced_search_filter"])
	}
	if got := filter["zalo_user_id"]; got != "zalo-user-9" {
		t.Fatalf("expected AstraDB-HistoryRetriever filter zalo_user_id, got %#v", got)
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
