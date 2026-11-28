package ai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

var testTools = []ToolSpec{{
	Name:        "lookup_debt",
	Description: "Tra công nợ",
	Parameters: map[string]any{
		"type":                 "object",
		"properties":           map[string]any{"search": map[string]any{"type": "string"}},
		"required":             []any{"search"},
		"additionalProperties": false,
	},
}}

// recorder keeps the JSON bodies a fake provider received.
type recorder struct {
	mu     sync.Mutex
	paths  []string
	bodies []map[string]any
}

func (r *recorder) add(req *http.Request) map[string]any {
	body, _ := io.ReadAll(req.Body)
	var m map[string]any
	_ = json.Unmarshal(body, &m)
	r.mu.Lock()
	defer r.mu.Unlock()
	r.paths = append(r.paths, req.URL.Path)
	r.bodies = append(r.bodies, m)
	return m
}

func TestOpenAIChatWithToolsRoundTrip(t *testing.T) {
	rec := &recorder{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := rec.add(r)
		msgs, _ := body["messages"].([]any)
		if len(msgs) <= 2 {
			_, _ = w.Write([]byte(`{"model":"gpt-5-mini-2025","choices":[{"message":{"content":null,"tool_calls":[{"id":"call_a","type":"function","function":{"name":"lookup_debt","arguments":"{\"search\":\"tháng này\"}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":100,"completion_tokens":7}}`))
			return
		}
		_, _ = w.Write([]byte(`{"model":"gpt-5-mini-2025","choices":[{"message":{"content":"Dạ, công nợ là 0đ."},"finish_reason":"stop"}],"usage":{"prompt_tokens":150,"completion_tokens":9}}`))
	}))
	defer srv.Close()

	p := NewOpenAIProvider("sk-test", "gpt-5-mini", srv.URL+"/v1")
	temp := 0.3
	req := ToolChatRequest{System: "sys", Messages: []ToolChatMessage{{Role: RoleUser, Text: "nợ bao nhiêu"}}, Tools: testTools, Temperature: &temp}
	first, err := p.ChatWithTools(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.ToolCalls) != 1 || first.ToolCalls[0].ID != "call_a" || first.ToolCalls[0].Name != "lookup_debt" ||
		string(first.ToolCalls[0].Arguments) != `{"search":"tháng này"}` {
		t.Fatalf("tool calls = %+v", first.ToolCalls)
	}
	if first.Provider != "openai" || first.InputTokens != 100 || first.OutputTokens != 7 {
		t.Errorf("usage = %+v", first.AIResponse)
	}
	body := rec.bodies[0]
	if body["tool_choice"] != "auto" || body["temperature"] != 0.3 || rec.paths[0] != "/v1/chat/completions" {
		t.Errorf("first request = %v at %s", body, rec.paths[0])
	}
	tools, _ := body["tools"].([]any)
	if len(tools) != 1 || !strings.Contains(mustMarshal(tools[0]), `"name":"lookup_debt"`) {
		t.Errorf("tools = %v", tools)
	}

	req.Messages = append(req.Messages, first.Message, ToolChatMessage{Role: RoleTool, ToolCallID: "call_a", ToolName: "lookup_debt", Text: `{"total":0}`})
	req.ToolChoice = ToolChoiceNone
	second, err := p.ChatWithTools(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if second.Content != "Dạ, công nợ là 0đ." || len(second.ToolCalls) != 0 {
		t.Fatalf("second = %+v", second)
	}
	msgs := mustMarshal(rec.bodies[1]["messages"])
	for _, want := range []string{`"role":"system"`, `"tool_calls":[{`, `"id":"call_a"`, `"role":"tool"`, `"tool_call_id":"call_a"`, `"content":"{\"total\":0}"`} {
		if !strings.Contains(msgs, want) {
			t.Errorf("second messages missing %s: %s", want, msgs)
		}
	}
	if rec.bodies[1]["tool_choice"] != "none" {
		t.Errorf("tool_choice = %v, want none", rec.bodies[1]["tool_choice"])
	}
}

func TestOpenAIChatWithToolsDropsRefusedTemperature(t *testing.T) {
	rec := &recorder{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := rec.add(r)
		if _, ok := body["temperature"]; ok {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"message":"Unsupported value: 'temperature' does not support 0.2 with this model. Only the default (1) value is supported."}}`))
			return
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}]}`))
	}))
	defer srv.Close()

	temp := 0.2
	resp, err := NewOpenRouterProvider("k", "openai/gpt-5-mini", srv.URL).ChatWithTools(context.Background(),
		ToolChatRequest{Messages: []ToolChatMessage{{Role: RoleUser, Text: "hi"}}, Temperature: &temp})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Content != "ok" || resp.Provider != "openrouter" || len(rec.bodies) != 2 {
		t.Fatalf("resp = %+v after %d requests", resp, len(rec.bodies))
	}
	if _, ok := rec.bodies[0]["tools"]; ok {
		t.Error("no tools: the request must not send tools or tool_choice")
	}
}

func TestOpenAIChatWithToolsFillsMissingCallIDs(t *testing.T) {
	resp, err := parseToolChatCompletion([]byte(`{"choices":[{"message":{"tool_calls":[{"type":"function","function":{"name":"a","arguments":""}},{"type":"function","function":{"name":"b","arguments":"{}"}}]}}]}`), "m")
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.ToolCalls) != 2 || resp.ToolCalls[0].ID == resp.ToolCalls[1].ID || resp.ToolCalls[0].ID == "" ||
		string(resp.ToolCalls[0].Arguments) != "{}" {
		t.Fatalf("calls = %+v", resp.ToolCalls)
	}
}

func TestClaudeChatWithToolsRoundTrip(t *testing.T) {
	rec := &recorder{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.add(r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"msg_1","type":"message","role":"assistant","model":"claude-haiku-4-5","stop_reason":"tool_use",
			"content":[{"type":"text","text":"Em tra nhé."},{"type":"tool_use","id":"toolu_1","name":"lookup_debt","input":{"search":"tháng này"}}],
			"usage":{"input_tokens":200,"output_tokens":20}}`))
	}))
	defer srv.Close()

	p := NewClaudeProvider("sk-ant", "claude-haiku-4-5", 1024, srv.URL)
	temp := 1.5
	resp, err := p.ChatWithTools(context.Background(), ToolChatRequest{
		System: "sys",
		Messages: []ToolChatMessage{
			{Role: RoleUser, Text: "nợ bao nhiêu"},
			{Role: RoleAssistant, ToolCalls: []ToolCall{{ID: "toolu_0", Name: "lookup_debt", Arguments: json.RawMessage(`{"search":"x"}`)}, {ID: "toolu_00", Name: "lookup_debt", Arguments: json.RawMessage(`{}`)}}},
			{Role: RoleTool, ToolCallID: "toolu_0", ToolName: "lookup_debt", Text: `{"total":1}`},
			{Role: RoleTool, ToolCallID: "toolu_00", ToolName: "lookup_debt", Text: `{"error":"x"}`, IsError: true},
		},
		Tools: testTools, ToolChoice: ToolChoiceNone, Temperature: &temp,
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Content != "Em tra nhé." || len(resp.ToolCalls) != 1 || resp.ToolCalls[0].ID != "toolu_1" ||
		string(resp.ToolCalls[0].Arguments) != `{"search":"tháng này"}` {
		t.Fatalf("resp = %+v", resp)
	}
	if resp.InputTokens != 200 || resp.OutputTokens != 20 || resp.Provider != "claude" {
		t.Errorf("usage = %+v", resp.AIResponse)
	}
	body := rec.bodies[0]
	if rec.paths[0] != "/v1/messages" || body["temperature"] != 1.0 {
		t.Errorf("path %s temperature %v (Claude takes at most 1)", rec.paths[0], body["temperature"])
	}
	if mustMarshal(body["tool_choice"]) != `{"type":"none"}` {
		t.Errorf("tool_choice = %s", mustMarshal(body["tool_choice"]))
	}
	tools := mustMarshal(body["tools"])
	for _, want := range []string{`"name":"lookup_debt"`, `"input_schema":{`, `"required":["search"]`, `"additionalProperties":false`, `"type":"object"`} {
		if !strings.Contains(tools, want) {
			t.Errorf("tools missing %s: %s", want, tools)
		}
	}
	msgs, _ := body["messages"].([]any)
	if len(msgs) != 3 {
		t.Fatalf("messages = %s, want user, assistant, one user with both results", mustMarshal(msgs))
	}
	results := mustMarshal(msgs[2])
	if strings.Count(results, `"type":"tool_result"`) != 2 || !strings.Contains(results, `"is_error":true`) {
		t.Errorf("tool results = %s", results)
	}
}

func TestGeminiChatWithToolsReplaysModelContent(t *testing.T) {
	rec := &recorder{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := rec.add(r)
		w.Header().Set("Content-Type", "application/json")
		if contents, _ := body["contents"].([]any); len(contents) == 1 {
			_, _ = w.Write([]byte(`{"modelVersion":"gemini-2.5-flash","candidates":[{"content":{"role":"model","parts":[{"functionCall":{"name":"lookup_debt","args":{"search":"tháng này"}},"thoughtSignature":"c2ln"}]}}],"usageMetadata":{"promptTokenCount":50,"candidatesTokenCount":5,"thoughtsTokenCount":10}}`))
			return
		}
		_, _ = w.Write([]byte(`{"modelVersion":"gemini-2.5-flash","candidates":[{"content":{"role":"model","parts":[{"text":"nghĩ","thought":true},{"text":"Dạ 0đ."}]}}],"usageMetadata":{"promptTokenCount":80,"candidatesTokenCount":4}}`))
	}))
	defer srv.Close()

	p := NewGeminiProvider("g-key", "gemini-2.5-flash", srv.URL)
	req := ToolChatRequest{System: "sys", Messages: []ToolChatMessage{{Role: RoleUser, Text: "nợ"}}, Tools: testTools}
	first, err := p.ChatWithTools(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.ToolCalls) != 1 || first.ToolCalls[0].Name != "lookup_debt" || !strings.HasPrefix(first.ToolCalls[0].ID, generatedToolCallID) {
		t.Fatalf("first = %+v", first)
	}
	if first.InputTokens != 50 || first.OutputTokens != 15 {
		t.Errorf("usage = %+v (thinking tokens count as output)", first.AIResponse)
	}
	decls := mustMarshal(rec.bodies[0]["tools"])
	if !strings.Contains(decls, `"parametersJsonSchema":{`) || !strings.Contains(decls, `"name":"lookup_debt"`) {
		t.Errorf("tools = %s", decls)
	}

	req.Messages = append(req.Messages, first.Message, ToolChatMessage{Role: RoleTool, ToolCallID: first.ToolCalls[0].ID, ToolName: "lookup_debt", Text: `{"total":0}`})
	second, err := p.ChatWithTools(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if second.Content != "Dạ 0đ." {
		t.Errorf("content = %q (thought parts are not the answer)", second.Content)
	}
	contents := mustMarshal(rec.bodies[1]["contents"])
	if !strings.Contains(contents, `"thoughtSignature":"c2ln"`) {
		t.Errorf("model content must be replayed with its thought signature: %s", contents)
	}
	if !strings.Contains(contents, `"functionResponse":{"name":"lookup_debt","response":{"output":"{\"total\":0}"}}`) {
		t.Errorf("function response (without a made-up id) missing: %s", contents)
	}
}

func mustMarshal(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
