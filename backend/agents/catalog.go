// Package agents runs the chatbot as specialist agents behind a coordinator
// ("Bộ điều phối"). A channel points at a team; the team holds the agents
// that answer through it. Each agent has its own prompt, model, MCP tools and
// knowledge. Who is chatting still decides the security tier (public or
// private) and the data scope, which stay in the signed permission token: an
// agent only narrows the tools its tier already has.
package agents

import "sort"

// Tool groups shown in the configuration UI.
const (
	ToolGroupProducts   = "products"
	ToolGroupOrders     = "orders"
	ToolGroupDebt       = "debt"
	ToolGroupCustomers  = "customers"
	ToolGroupKnowledge  = "knowledge"
	ToolGroupComplaints = "complaints"
)

// ToolInfo describes one MCP tool of /mcp/erp/:tenantId for agent settings.
// Writes marks tools that change data or message the customer; a dry-run
// turn (the test console) may not call them.
type ToolInfo struct {
	Name   string   `json:"name"`
	Group  string   `json:"group"`
	Tiers  []string `json:"tiers"`
	Writes bool     `json:"writes"`
	// Hidden tools still answer but are not offered for new agents.
	Hidden bool `json:"hidden,omitempty"`
}

var bothTiers = []string{TierPublic, TierPrivate}

// toolCatalog lists every tool the ERP MCP server serves. A test in
// api/handlers keeps it in step with the server's tool list.
var toolCatalog = []ToolInfo{
	{Name: "search_products", Group: ToolGroupProducts, Tiers: bothTiers},
	{Name: "search_product_variants", Group: ToolGroupProducts, Tiers: bothTiers},
	{Name: "inventory_by_sku", Group: ToolGroupProducts, Tiers: bothTiers},
	{Name: "inventory_by_line", Group: ToolGroupProducts, Tiers: bothTiers},
	{Name: "find_customer", Group: ToolGroupCustomers, Tiers: bothTiers},
	{Name: "lookup_order", Group: ToolGroupOrders, Tiers: bothTiers},
	{Name: "lookup_debt", Group: ToolGroupDebt, Tiers: bothTiers},
	{Name: "order_cart_update", Group: ToolGroupOrders, Tiers: bothTiers, Writes: true},
	{Name: "order_cart_view", Group: ToolGroupOrders, Tiers: bothTiers},
	{Name: "order_cart_checkout", Group: ToolGroupOrders, Tiers: bothTiers, Writes: true},
	{Name: "order_cart_clear", Group: ToolGroupOrders, Tiers: bothTiers, Writes: true},
	{Name: "draft_order_create", Group: ToolGroupOrders, Tiers: bothTiers, Writes: true, Hidden: true},
	{Name: "draft_order_update", Group: ToolGroupOrders, Tiers: bothTiers, Writes: true},
	{Name: "draft_order_cancel", Group: ToolGroupOrders, Tiers: bothTiers, Writes: true},
	{Name: "search_knowledge", Group: ToolGroupKnowledge, Tiers: bothTiers},
	{Name: "complaint_record", Group: ToolGroupComplaints, Tiers: bothTiers, Writes: true},
}

// Tool names the prompt composer and the worker look for.
const (
	ToolComplaintRecord = "complaint_record"
	ToolLookupOrder     = "lookup_order"
	toolCartPrefix      = "order_cart_"
)

// Catalog returns a copy of the tool catalog.
func Catalog() []ToolInfo {
	out := make([]ToolInfo, len(toolCatalog))
	for i, t := range toolCatalog {
		t.Tiers = append([]string(nil), t.Tiers...)
		out[i] = t
	}
	return out
}

// LookupTool returns the catalog entry for name.
func LookupTool(name string) (ToolInfo, bool) {
	for _, t := range toolCatalog {
		if t.Name == name {
			return t, true
		}
	}
	return ToolInfo{}, false
}

// ToolWrites reports whether name changes data or messages the customer.
// Unknown names count as writes so a dry run never calls them.
func ToolWrites(name string) bool {
	t, ok := LookupTool(name)
	return !ok || t.Writes
}

// AllToolNames is every tool, hidden ones included: what the single bot of
// each tier could call before agents existed.
func AllToolNames() []string {
	names := make([]string, 0, len(toolCatalog))
	for _, t := range toolCatalog {
		names = append(names, t.Name)
	}
	sort.Strings(names)
	return names
}

func toolServesTier(t ToolInfo, tier string) bool {
	for _, v := range t.Tiers {
		if v == tier {
			return true
		}
	}
	return false
}
