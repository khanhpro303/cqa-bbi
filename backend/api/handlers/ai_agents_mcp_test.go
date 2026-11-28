package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/vietbui/chat-quality-agent/agents"
	"github.com/vietbui/chat-quality-agent/engine"
)

// The agents catalog is what the settings UI offers and what migrated agents
// get; it must name exactly the tools this server can serve.
func TestAgentCatalogMatchesMCPTools(t *testing.T) {
	served := map[string]bool{knowledgeMCPToolName: true, complaintMCPTool().Name: true}
	for _, tool := range erpMCPTools {
		served[tool.Name] = true
	}
	for _, tool := range erpMCPCartTools {
		served[tool.Name] = true
	}
	for _, tool := range erpMCPOrderTools {
		served[tool.Name] = true
	}
	listed := map[string]bool{}
	for _, agentType := range []string{"public", "private"} {
		tools := append(erpMCPToolsForAgent(agentType), listedOrderTools()...)
		for _, tool := range append(tools, knowledgeMCPTool("fixture"), complaintMCPTool()) {
			listed[tool.Name] = true
		}
	}
	names := make([]string, 0, len(served))
	for name := range served {
		names = append(names, name)
		info, ok := agents.LookupTool(name)
		if !ok {
			t.Errorf("MCP tool %s is missing from the agents catalog", name)
			continue
		}
		if info.Hidden == listed[name] {
			t.Errorf("catalog hidden=%t for %s, but the server lists it=%t", info.Hidden, name, listed[name])
		}
	}
	sort.Strings(names)
	if got := agents.AllToolNames(); strings.Join(got, ",") != strings.Join(names, ",") {
		t.Errorf("catalog %v, server %v", got, names)
	}
}

func TestERPMCPAgentToolAllowlist(t *testing.T) {
	_, calls := setupWarehouseFixture(t)
	router := gin.New()
	router.POST("/mcp/erp/:tenantId", ERPMCP)
	signed := func(agentKey string, tools []string, dryRun bool) string {
		token, err := engine.SignPermissionToken(engine.GroupPermissionContext{
			TenantID: stockFixtureTenant, AgentType: "private", ZaloUserID: "fixture-zalo",
			AgentKey: agentKey, AgentTools: tools, DryRun: dryRun,
			Groups: []engine.GroupPermission{{GroupID: "private_bot", Resources: []engine.ResourcePermission{
				{Resource: "inventory", IsEnabled: true, ScopeType: "all", ProductGroups: []string{"mũ"}, Brands: []string{"bulldog"}},
			}}},
		}, stockFixtureSigningKey)
		if err != nil {
			t.Fatal(err)
		}
		return token
	}
	request := func(permission, body string) string {
		req := httptest.NewRequest(http.MethodPost, "/mcp/erp/"+stockFixtureTenant, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Agent-Token", "fixture-agent-token")
		req.Header.Set("X-Permission-Token", permission)
		res := httptest.NewRecorder()
		router.ServeHTTP(res, req)
		if res.Code != http.StatusOK {
			t.Fatalf("status %d: %s", res.Code, res.Body.String())
		}
		return res.Body.String()
	}
	listNames := func(permission string) []string {
		var data struct {
			Result struct {
				Tools []struct{ Name string }
			}
		}
		body := request(permission, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
		if err := json.Unmarshal([]byte(body), &data); err != nil {
			t.Fatal(err)
		}
		names := make([]string, 0, len(data.Result.Tools))
		for _, tool := range data.Result.Tools {
			names = append(names, tool.Name)
		}
		sort.Strings(names)
		return names
	}
	const sku = `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"inventory_by_sku","arguments":{"sku":"SP457996"}}}`
	const line = `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"inventory_by_line","arguments":{"line_name":"Bulldog DOGO"}}}`
	const cart = `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"order_cart_update","arguments":{}}}`

	stock := signed("kho", []string{"inventory_by_sku", "order_cart_update"}, false)
	if got := listNames(stock); strings.Join(got, ",") != "inventory_by_sku,order_cart_update" {
		t.Errorf("agent tool list = %v", got)
	}
	before := calls.Load()
	if body := request(stock, sku); !strings.Contains(body, `\"ton_kho\":32`) {
		t.Errorf("allowed tool failed: %s", body)
	}
	if body := request(stock, line); !strings.Contains(body, "tool_not_allowed") || !strings.Contains(body, `"isError":true`) {
		t.Errorf("tool outside the agent ran: %s", body)
	}
	if got := calls.Load() - before; got != 1 {
		t.Errorf("warehouse requests = %d, want 1 (the refused call must not reach the ERP)", got)
	}

	dry := signed("kho", []string{"inventory_by_sku", "order_cart_update"}, true)
	if got := listNames(dry); strings.Join(got, ",") != "inventory_by_sku" {
		t.Errorf("dry-run tool list = %v", got)
	}
	if body := request(dry, cart); !strings.Contains(body, "dry_run") {
		t.Errorf("dry run called a write tool: %s", body)
	}

	// No agent in the token: every tool of the tier, as before agents.
	legacy := listNames(signed("", nil, false))
	if len(legacy) < len(agents.AllToolNames())-1 {
		t.Errorf("legacy tool list shrank: %v", legacy)
	}
	// An agent with no tools gets none, not all.
	if got := listNames(signed("rong", nil, false)); len(got) != 0 {
		t.Errorf("tool list for an agent without tools = %v", got)
	}
	// A token for another tenant is ignored by the filter (and refused by
	// every data tool itself).
	other, _ := engine.SignPermissionToken(engine.GroupPermissionContext{
		TenantID: "other-tenant", AgentType: "private", AgentKey: "kho", AgentTools: []string{"inventory_by_sku"},
	}, stockFixtureSigningKey)
	if got := listNames(other); len(got) != len(legacy) {
		t.Errorf("foreign token filtered the list: %v", got)
	}
}
