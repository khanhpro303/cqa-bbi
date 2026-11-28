package native

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"

	"github.com/vietbui/chat-quality-agent/ai"
)

// Auth is what a turn presents to the ERP MCP server: the agent token for
// the turn's tier, and the signed permission token (data scope, the agent's
// tool list).
type Auth struct {
	AgentToken      string
	PermissionToken string
}

// ToolResult is a tool's answer as the model reads it.
type ToolResult struct {
	Text    string
	IsError bool
}

// ToolServer is the ERP MCP server as the native runtime uses it.
type ToolServer interface {
	ListTools(ctx context.Context, tenantID string, auth Auth) ([]ai.ToolSpec, error)
	CallTool(ctx context.Context, tenantID string, auth Auth, name string, args json.RawMessage) (ToolResult, error)
}

// MCPClient speaks JSON-RPC to the ERP MCP server (/mcp/erp/:tenantId).
// Handler serves that route in this process, so a native turn's tool calls
// go through exactly the checks Langflow's calls go through (agent token,
// permission token, the agent's tool list, dry runs) without leaving the
// process or needing the public URL.
type MCPClient struct {
	Handler http.Handler
}

var mcpRequestID atomic.Int64

// ListTools returns the tools the server lists for auth.
func (c MCPClient) ListTools(ctx context.Context, tenantID string, auth Auth) ([]ai.ToolSpec, error) {
	raw, err := c.rpc(ctx, tenantID, auth, "tools/list", map[string]any{})
	if err != nil {
		return nil, err
	}
	var result struct {
		Tools []struct {
			Name        string         `json:"name"`
			Description string         `json:"description"`
			InputSchema map[string]any `json:"inputSchema"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("mcp tools/list: %w", err)
	}
	out := make([]ai.ToolSpec, 0, len(result.Tools))
	for _, t := range result.Tools {
		if t.Name == "" {
			continue
		}
		out = append(out, ai.ToolSpec{Name: t.Name, Description: t.Description, Parameters: t.InputSchema})
	}
	return out, nil
}

// CallTool runs one tool. A JSON-RPC error (unknown tool, bad arguments)
// comes back as an error result for the model to read, not as a failure.
func (c MCPClient) CallTool(ctx context.Context, tenantID string, auth Auth, name string, args json.RawMessage) (ToolResult, error) {
	if len(bytes.TrimSpace(args)) == 0 || bytes.Equal(bytes.TrimSpace(args), []byte("null")) {
		args = json.RawMessage(`{}`)
	}
	raw, err := c.rpc(ctx, tenantID, auth, "tools/call", map[string]any{"name": name, "arguments": args})
	var rpcErr *rpcError
	if asRPCError(err, &rpcErr) {
		b, _ := json.Marshal(map[string]any{"error": "tool_error", "message": rpcErr.Message})
		return ToolResult{Text: string(b), IsError: true}, nil
	}
	if err != nil {
		return ToolResult{}, err
	}
	var result struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return ToolResult{}, fmt.Errorf("mcp tools/call %s: %w", name, err)
	}
	var text []string
	for _, part := range result.Content {
		if part.Type == "text" || part.Type == "" {
			text = append(text, part.Text)
		}
	}
	return ToolResult{Text: strings.Join(text, "\n"), IsError: result.IsError}, nil
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *rpcError) Error() string { return fmt.Sprintf("mcp error %d: %s", e.Code, e.Message) }

func asRPCError(err error, target **rpcError) bool {
	e, ok := err.(*rpcError)
	if ok {
		*target = e
	}
	return ok
}

func (c MCPClient) rpc(ctx context.Context, tenantID string, auth Auth, method string, params any) (json.RawMessage, error) {
	if c.Handler == nil {
		return nil, fmt.Errorf("mcp server not configured")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	body, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": mcpRequestID.Add(1), "method": method, "params": params,
	})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "/mcp/erp/"+url.PathEscape(tenantID), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if auth.AgentToken != "" {
		req.Header.Set("X-Agent-Token", auth.AgentToken)
	}
	if auth.PermissionToken != "" {
		req.Header.Set("X-Permission-Token", auth.PermissionToken)
	}
	rec := httptest.NewRecorder()
	c.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		return nil, fmt.Errorf("mcp %s: http %d: %s", method, rec.Code, strings.TrimSpace(capText(rec.Body.String(), 300)))
	}
	var resp struct {
		Result json.RawMessage `json:"result"`
		Error  *rpcError       `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		return nil, fmt.Errorf("mcp %s: %w", method, err)
	}
	if resp.Error != nil {
		return nil, resp.Error
	}
	return resp.Result, nil
}
