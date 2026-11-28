package handlers

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"testing"

	"github.com/vietbui/chat-quality-agent/agents"
	"github.com/vietbui/chat-quality-agent/agents/native"
	"github.com/vietbui/chat-quality-agent/ai"
	"github.com/vietbui/chat-quality-agent/engine"
)

type stockModel struct {
	requests []ai.ToolChatRequest
}

func (m *stockModel) ChatWithTools(_ context.Context, req ai.ToolChatRequest) (ai.ToolChatResponse, error) {
	m.requests = append(m.requests, req)
	if len(m.requests) == 1 {
		calls := []ai.ToolCall{
			{ID: "c1", Name: "inventory_by_sku", Arguments: json.RawMessage(`{"sku":"SP457996"}`)},
			{ID: "c2", Name: "inventory_by_line", Arguments: json.RawMessage(`{"line_name":"Bulldog DOGO"}`)},
		}
		return ai.ToolChatResponse{ToolCalls: calls, Message: ai.ToolChatMessage{Role: ai.RoleAssistant, ToolCalls: calls}}, nil
	}
	return ai.ToolChatResponse{AIResponse: ai.AIResponse{Content: "Dạ còn 32 chiếc ạ."}}, nil
}

// The native runtime reaches the ERP MCP server in process with the turn's
// tokens: the server lists only the agent's tools, runs them with the
// signed data scope, and refuses a tool outside the agent even if the model
// asks for it.
func TestNativeRuntimeUsesERPMCPInProcess(t *testing.T) {
	_, calls := setupWarehouseFixture(t)
	permission, err := engine.SignPermissionToken(engine.GroupPermissionContext{
		TenantID: stockFixtureTenant, AgentType: "private", ZaloUserID: "fixture-zalo",
		AgentKey: "kho", AgentTools: []string{"inventory_by_sku", "search_knowledge"},
		Groups: []engine.GroupPermission{{GroupID: "private_bot", Resources: []engine.ResourcePermission{
			{Resource: "inventory", IsEnabled: true, ScopeType: "all", ProductGroups: []string{"mũ"}, Brands: []string{"bulldog"}},
		}}},
	}, stockFixtureSigningKey)
	if err != nil {
		t.Fatal(err)
	}
	client := native.MCPClient{Handler: ERPMCPHandler()}
	auth := native.Auth{AgentToken: "fixture-agent-token", PermissionToken: permission}

	listed, err := client.ListTools(context.Background(), stockFixtureTenant, auth)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tool := range listed {
		names = append(names, tool.Name)
		if tool.Parameters["type"] != "object" {
			t.Errorf("tool %s schema = %v", tool.Name, tool.Parameters)
		}
	}
	sort.Strings(names)
	if strings.Join(names, ",") != "inventory_by_sku,search_knowledge" {
		t.Fatalf("listed = %v, want the agent's tools", names)
	}
	if _, err := client.ListTools(context.Background(), stockFixtureTenant, native.Auth{AgentToken: "wrong"}); err == nil {
		t.Error("a wrong agent token must be refused")
	}

	model := &stockModel{}
	rt := &native.Runtime{
		Tools: client,
		Models: func(context.Context, string, string, string) (native.Model, error) {
			return native.Model{Client: model, Provider: "openai", Model: "gpt-5-mini"}, nil
		},
	}
	before := calls.Load()
	res, err := rt.Run(context.Background(), agents.RunInput{
		Agent: agents.Agent{
			TenantID: stockFixtureTenant, Key: "kho", Runtime: agents.RuntimeNative, Provider: "openai", Model: "gpt-5-mini",
			// The agent row lists inventory_by_line, but the signed token
			// does not: the server's list wins.
			Tools: []string{"inventory_by_line", "inventory_by_sku"},
		},
		Tier: agents.TierPrivate, Input: "DOGO còn không", AgentToken: "fixture-agent-token", PermissionToken: permission,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Text != "Dạ còn 32 chiếc ạ." {
		t.Fatalf("text = %q", res.Text)
	}
	if len(model.requests[0].Tools) != 1 || model.requests[0].Tools[0].Name != "inventory_by_sku" {
		t.Fatalf("offered tools = %+v", model.requests[0].Tools)
	}
	results := model.requests[1].Messages
	sku, line := results[len(results)-2], results[len(results)-1]
	if sku.IsError || !strings.Contains(sku.Text, `"ton_kho":32`) {
		t.Errorf("allowed tool result = %+v", sku)
	}
	if !line.IsError || !strings.Contains(line.Text, "tool_not_allowed") {
		t.Errorf("tool outside the token must be refused: %+v", line)
	}
	if got := calls.Load() - before; got != 1 {
		t.Errorf("warehouse requests = %d, want 1", got)
	}
}
