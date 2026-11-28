package native

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/vietbui/chat-quality-agent/agents"
	"github.com/vietbui/chat-quality-agent/ai"
	"github.com/vietbui/chat-quality-agent/chatmemory"
)

// scriptedModel answers each request with the next scripted reply and keeps
// the requests it saw.
type scriptedModel struct {
	replies  []ai.ToolChatResponse
	requests []ai.ToolChatRequest
	err      error
}

func (m *scriptedModel) ChatWithTools(_ context.Context, req ai.ToolChatRequest) (ai.ToolChatResponse, error) {
	req.Messages = append([]ai.ToolChatMessage(nil), req.Messages...)
	m.requests = append(m.requests, req)
	if m.err != nil {
		return ai.ToolChatResponse{}, m.err
	}
	if len(m.replies) == 0 {
		return ai.ToolChatResponse{AIResponse: ai.AIResponse{Content: "hết kịch bản"}}, nil
	}
	r := m.replies[0]
	m.replies = m.replies[1:]
	r.Message = ai.ToolChatMessage{Role: ai.RoleAssistant, Text: r.Content, ToolCalls: r.ToolCalls}
	return r, nil
}

type fakeTools struct {
	listed []ai.ToolSpec
	auths  []Auth
	calls  []string
	lists  int
}

func (f *fakeTools) ListTools(_ context.Context, _ string, auth Auth) ([]ai.ToolSpec, error) {
	f.lists++
	f.auths = append(f.auths, auth)
	return f.listed, nil
}

func (f *fakeTools) CallTool(_ context.Context, _ string, auth Auth, name string, args json.RawMessage) (ToolResult, error) {
	f.auths = append(f.auths, auth)
	f.calls = append(f.calls, name+" "+string(args))
	return ToolResult{Text: `{"tool":"` + name + `","ok":true}`}, nil
}

func reply(text string, usage int, calls ...ai.ToolCall) ai.ToolChatResponse {
	return ai.ToolChatResponse{
		AIResponse: ai.AIResponse{Content: text, InputTokens: usage, OutputTokens: usage / 10, Provider: "openai", Model: "gpt-5-mini-2025"},
		ToolCalls:  calls,
	}
}

func call(id, name, args string) ai.ToolCall {
	return ai.ToolCall{ID: id, Name: name, Arguments: json.RawMessage(args)}
}

func newRuntime(model *scriptedModel, tools *fakeTools) *Runtime {
	return &Runtime{
		Tools: tools,
		Models: func(_ context.Context, tenantID, provider, modelID string) (Model, error) {
			if tenantID != "t1" || provider != "openai" || modelID != "gpt-5-mini" {
				return Model{}, fmt.Errorf("%w: unexpected model %s %s/%s", agents.ErrPermanent, tenantID, provider, modelID)
			}
			return Model{Client: model, Provider: provider, Model: modelID}, nil
		},
	}
}

func debtAgent() agents.Agent {
	temp := 0.2
	return agents.Agent{
		TenantID: "t1", Key: "cong-no", Runtime: agents.RuntimeNative, Provider: "openai", Model: "gpt-5-mini",
		Tools: []string{"lookup_debt", "search_knowledge"}, Options: agents.Options{Temperature: &temp},
	}
}

var catalog = []ai.ToolSpec{
	{Name: "lookup_debt", Description: "Tra công nợ", Parameters: map[string]any{"type": "object"}},
	{Name: "lookup_order", Description: "Tra đơn"},
	{Name: "search_knowledge", Description: "Tài liệu"},
}

func TestRunCallsAgentToolsAndAnswers(t *testing.T) {
	model := &scriptedModel{replies: []ai.ToolChatResponse{
		reply("", 1000, call("c1", "lookup_debt", `{"search":"tháng này"}`)),
		reply("Dạ, công nợ tháng này là 0đ ạ.", 1500),
	}}
	tools := &fakeTools{listed: catalog}
	res, err := newRuntime(model, tools).Run(context.Background(), agents.RunInput{
		Agent: debtAgent(), SystemPrompt: "Bạn là bot công nợ.", Input: "nợ bao nhiêu",
		History:    []chatmemory.RecentTurn{{Speaker: "Khách", Text: "chào"}, {Speaker: "Bot", Text: "Dạ em chào anh"}},
		AgentToken: "agent-public", PermissionToken: "perm-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Text != "Dạ, công nợ tháng này là 0đ ạ." {
		t.Fatalf("text = %q", res.Text)
	}
	if res.Usage.InputTokens != 2500 || res.Usage.OutputTokens != 250 || !res.Usage.HasUsage ||
		res.Usage.Provider != "openai" || res.Usage.Model != "gpt-5-mini-2025" {
		t.Errorf("usage = %+v", res.Usage)
	}
	if len(tools.calls) != 1 || tools.calls[0] != `lookup_debt {"search":"tháng này"}` {
		t.Errorf("tool calls = %v", tools.calls)
	}
	for _, a := range tools.auths {
		if a != (Auth{AgentToken: "agent-public", PermissionToken: "perm-1"}) {
			t.Errorf("MCP auth = %+v, want the turn's tokens", a)
		}
	}

	first := model.requests[0]
	if first.System != "Bạn là bot công nợ." || first.Temperature == nil || *first.Temperature != 0.2 || first.ToolChoice != "" {
		t.Errorf("first request = %+v", first)
	}
	var offered []string
	for _, tool := range first.Tools {
		offered = append(offered, tool.Name)
	}
	if strings.Join(offered, ",") != "lookup_debt,search_knowledge" {
		t.Errorf("offered tools = %v, want only the agent's", offered)
	}
	if len(first.Messages) != 3 || first.Messages[0].Text != "chào" || first.Messages[1].Role != ai.RoleAssistant ||
		first.Messages[2].Text != "nợ bao nhiêu" {
		t.Errorf("conversation = %+v", first.Messages)
	}
	second := model.requests[1]
	last := second.Messages[len(second.Messages)-1]
	if last.Role != ai.RoleTool || last.ToolCallID != "c1" || last.ToolName != "lookup_debt" || !strings.Contains(last.Text, `"ok":true`) {
		t.Errorf("tool result message = %+v", last)
	}
}

func TestRunStopsAtToolCallBudget(t *testing.T) {
	agent := debtAgent()
	agent.Options.MaxToolCalls = 1
	model := &scriptedModel{replies: []ai.ToolChatResponse{
		reply("", 10, call("c1", "lookup_debt", `{}`), call("c2", "lookup_debt", `{"search":"quý 2"}`)),
		reply("Dạ em gửi kết quả đã tra.", 10),
	}}
	tools := &fakeTools{listed: catalog}
	res, err := newRuntime(model, tools).Run(context.Background(), agents.RunInput{Agent: agent, Input: "nợ", AgentToken: "a"})
	if err != nil {
		t.Fatal(err)
	}
	if len(tools.calls) != 1 {
		t.Fatalf("tool calls = %v, want 1 (budget)", tools.calls)
	}
	if res.Text != "Dạ em gửi kết quả đã tra." || len(model.requests) != 2 {
		t.Fatalf("text %q after %d requests", res.Text, len(model.requests))
	}
	final := model.requests[1]
	if final.ToolChoice != ai.ToolChoiceNone || len(final.Tools) == 0 {
		t.Errorf("after the budget the model must answer (tool_choice none, tools still defined): %+v", final.ToolChoice)
	}
	refused := final.Messages[len(final.Messages)-1]
	if refused.ToolCallID != "c2" || !refused.IsError || !strings.Contains(refused.Text, "tool_call_limit") {
		t.Errorf("over-budget call must get an error result: %+v", refused)
	}
}

func TestRunRefusesToolsOutsideTheAgent(t *testing.T) {
	model := &scriptedModel{replies: []ai.ToolChatResponse{
		reply("", 10, call("c1", "lookup_order", `{}`), call("c2", "lookup_debt", `not json`)),
		reply("Dạ em chỉ hỗ trợ công nợ.", 10),
	}}
	tools := &fakeTools{listed: catalog}
	res, err := newRuntime(model, tools).Run(context.Background(), agents.RunInput{Agent: debtAgent(), Input: "đơn đâu", AgentToken: "a"})
	if err != nil {
		t.Fatal(err)
	}
	if len(tools.calls) != 0 {
		t.Fatalf("tool server reached for refused calls: %v", tools.calls)
	}
	msgs := model.requests[1].Messages
	if !strings.Contains(msgs[len(msgs)-2].Text, "tool_not_allowed") || !strings.Contains(msgs[len(msgs)-1].Text, "invalid_arguments") {
		t.Errorf("refusals = %+v", msgs[len(msgs)-2:])
	}
	if res.Text != "Dạ em chỉ hỗ trợ công nợ." {
		t.Errorf("text = %q", res.Text)
	}
}

func TestRunWithoutAgentTokenOffersNoTools(t *testing.T) {
	model := &scriptedModel{replies: []ai.ToolChatResponse{reply("Dạ.", 10, call("c1", "lookup_debt", `{}`))}}
	tools := &fakeTools{listed: catalog}
	res, err := newRuntime(model, tools).Run(context.Background(), agents.RunInput{Agent: debtAgent(), Input: "hi"})
	if err != nil {
		t.Fatal(err)
	}
	if tools.lists != 0 || len(tools.calls) != 0 || len(model.requests[0].Tools) != 0 {
		t.Fatalf("lists=%d calls=%v tools=%v", tools.lists, tools.calls, model.requests[0].Tools)
	}
	if res.Text != "Dạ." {
		t.Errorf("text = %q", res.Text)
	}
}

func TestRunAgentWithoutToolsSkipsTheServer(t *testing.T) {
	agent := debtAgent()
	agent.Tools = nil
	model := &scriptedModel{replies: []ai.ToolChatResponse{reply("Dạ.", 10)}}
	tools := &fakeTools{listed: catalog}
	if _, err := newRuntime(model, tools).Run(context.Background(), agents.RunInput{Agent: agent, Input: "hi", AgentToken: "a"}); err != nil {
		t.Fatal(err)
	}
	if tools.lists != 0 || len(model.requests[0].Tools) != 0 {
		t.Fatalf("an agent without tools must get none (lists=%d)", tools.lists)
	}
}

func TestRunErrorsArePermanentWhenRetryingCannotHelp(t *testing.T) {
	agent := debtAgent()
	agent.Model = "khong-co"
	_, err := newRuntime(&scriptedModel{}, &fakeTools{}).Run(context.Background(), agents.RunInput{Agent: agent, Input: "hi"})
	if !errors.Is(err, agents.ErrPermanent) {
		t.Fatalf("missing model/key: err = %v, want permanent", err)
	}

	refused := &scriptedModel{err: errors.New("openai api error 401: invalid key")}
	_, err = newRuntime(refused, &fakeTools{}).Run(context.Background(), agents.RunInput{Agent: debtAgent(), Input: "hi"})
	if !errors.Is(err, agents.ErrPermanent) {
		t.Fatalf("refused key: err = %v, want permanent", err)
	}
	busy := &scriptedModel{err: errors.New("openai api error 503: overloaded")}
	_, err = newRuntime(busy, &fakeTools{}).Run(context.Background(), agents.RunInput{Agent: debtAgent(), Input: "hi"})
	if err == nil || errors.Is(err, agents.ErrPermanent) {
		t.Fatalf("overloaded: err = %v, want a retryable error", err)
	}
}

func TestRunSumsReportedCostOnlyWhenEveryRequestHasOne(t *testing.T) {
	cost := 0.001
	withCost := func(r ai.ToolChatResponse) ai.ToolChatResponse { r.ReportedCostUSD = &cost; return r }
	model := &scriptedModel{replies: []ai.ToolChatResponse{
		withCost(reply("", 10, call("c1", "lookup_debt", `{}`))),
		withCost(reply("ok", 10)),
	}}
	res, err := newRuntime(model, &fakeTools{listed: catalog}).Run(context.Background(), agents.RunInput{Agent: debtAgent(), Input: "nợ", AgentToken: "a"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Usage.ReportedCostUSD == nil || *res.Usage.ReportedCostUSD != 0.002 {
		t.Fatalf("reported cost = %v", res.Usage.ReportedCostUSD)
	}

	model = &scriptedModel{replies: []ai.ToolChatResponse{
		withCost(reply("", 10, call("c1", "lookup_debt", `{}`))),
		reply("ok", 10),
	}}
	res, _ = newRuntime(model, &fakeTools{listed: catalog}).Run(context.Background(), agents.RunInput{Agent: debtAgent(), Input: "nợ", AgentToken: "a"})
	if res.Usage.ReportedCostUSD != nil {
		t.Fatalf("a request without a reported cost makes the total unknown, got %v", *res.Usage.ReportedCostUSD)
	}
}

func TestConversation(t *testing.T) {
	got := conversation([]chatmemory.RecentTurn{
		{Speaker: "Bot", Text: "Đơn DH1 đang giao"},
		{Speaker: "Nhân viên", Text: "Em gửi ảnh nhé"},
		{Speaker: "Khách", Text: "ok"},
		{Speaker: "Khách", Text: "khi nào tới"},
		{Speaker: "Bot", Text: "Dạ chiều nay"},
		{Speaker: "Nhân viên", Text: "Em xác nhận"},
		{Speaker: "Khách", Text: "  "},
	}, "cảm ơn")
	want := []ai.ToolChatMessage{
		{Role: ai.RoleUser, Text: "Bot: Đơn DH1 đang giao\nNhân viên: Em gửi ảnh nhé\nKhách: ok\nkhi nào tới"},
		{Role: ai.RoleAssistant, Text: "Dạ chiều nay\nNhân viên: Em xác nhận"},
		{Role: ai.RoleUser, Text: "cảm ơn"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %+v", got)
	}
	for i := range want {
		if got[i].Role != want[i].Role || got[i].Text != want[i].Text {
			t.Errorf("message %d = %+v, want %+v", i, got[i], want[i])
		}
	}

	only := conversation([]chatmemory.RecentTurn{{Speaker: "Bot", Text: "Anh cần màu nào?"}}, "đỏ")
	if len(only) != 1 || only[0].Role != ai.RoleUser || only[0].Text != "Bot: Anh cần màu nào?\nKhách: đỏ" {
		t.Errorf("bot question then answer = %+v", only)
	}
}
