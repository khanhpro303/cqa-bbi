package handlers

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/gin-gonic/gin"
)

type erpMCPTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
	resource    string
}

func newERPMCPTool(name, description, resource string, required []string, fields ...string) erpMCPTool {
	properties := make(map[string]any, len(fields))
	for _, field := range fields {
		typ := "string"
		switch field {
		case "limit":
			typ = "integer"
		case "exact_web_name", "include_stock":
			typ = "boolean"
		}
		properties[field] = gin.H{"type": typ}
	}
	schema := map[string]any{"type": "object", "properties": properties, "additionalProperties": false}
	if len(required) > 0 {
		schema["required"] = required
	}
	return erpMCPTool{Name: name, Description: description, resource: resource, InputSchema: schema}
}

var erpMCPTools = []erpMCPTool{
	newERPMCPTool("search_products", "Tìm dòng sản phẩm, giá, hoặc các dòng trùng tên. search là tên hoặc mã người dùng hỏi. intent là price hoặc stock.", "products", []string{"search"}, "search", "limit", "intent", "exact_web_name", "color", "size", "brand", "parent_code"),
	newERPMCPTool("search_product_variants", "Tra màu, size hoặc giá biến thể của mã cha đã xác định. include_stock đính kèm tồn 1.KHO-TONG.", "product_variants", []string{"parent_code"}, "search", "parent_code", "limit", "color", "size", "brand", "include_stock"),
	newERPMCPTool("inventory_by_sku", "Tra tồn 1.KHO-TONG của một SKU chính xác. Khi chỉ có tên dòng và thuộc tính màu/size, dùng search cùng color/size/brand để phân giải SKU.", "inventory", nil, "sku", "search", "limit", "parent_code", "exact_web_name", "color", "size", "brand"),
	newERPMCPTool("inventory_by_line", "Cộng tồn 1.KHO-TONG của toàn bộ SKU thuộc các mã cha của tên dòng đã xác nhận. Nếu tên còn mơ hồ, dùng search_products trước.", "inventory", []string{"line_name"}, "line_name"),
	newERPMCPTool("find_customer", "Tra thông tin khách hàng theo tên hoặc mã trong phạm vi được phép.", "customers", nil, "search", "limit", "partner_id"),
	newERPMCPTool("lookup_order", "Tra đơn hàng theo mã đơn hoặc khoảng thời gian trong phạm vi được phép.", "orders", nil, "search", "limit", "partner_id"),
	newERPMCPTool("lookup_debt", "Tra công nợ theo kỳ và khách hàng trong phạm vi được phép.", "debt", nil, "search", "limit", "partner_id"),
}

const erpMCPToolContextKey = "erp_mcp_tool"

func ERPMCP(c *gin.Context) {
	// This endpoint serves stateless JSON responses. An SSE GET stream is optional.
	if c.Request.Method == http.MethodGet {
		c.Header("Allow", http.MethodPost)
		c.Status(http.StatusMethodNotAllowed)
		return
	}

	tenantID := c.Param("tenantId")
	token := c.GetHeader("X-Agent-Token")
	if token == "" || resolveAgentType(tenantID, token) == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 64<<10)
	var req struct {
		Version string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id"`
		Method  string          `json:"method"`
		Params  json.RawMessage `json:"params"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"jsonrpc": "2.0", "id": nil, "error": gin.H{"code": -32700, "message": "Invalid JSON"}})
		return
	}

	fail := func(code int, message string) {
		c.JSON(http.StatusOK, gin.H{"jsonrpc": "2.0", "id": req.ID, "error": gin.H{"code": code, "message": message}})
	}
	if req.Version != "2.0" || req.Method == "" {
		fail(-32600, "Invalid JSON-RPC request")
		return
	}
	if len(req.ID) == 0 {
		c.Status(http.StatusAccepted)
		return
	}

	reply := func(result any) {
		c.JSON(http.StatusOK, gin.H{"jsonrpc": "2.0", "id": req.ID, "result": result})
	}
	switch req.Method {
	case "initialize":
		var params struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(req.Params, &params)
		version := params.ProtocolVersion
		switch version {
		case "2025-03-26", "2025-06-18", "2025-11-25":
		default:
			version = "2025-03-26"
		}
		reply(gin.H{"protocolVersion": version, "capabilities": gin.H{"tools": gin.H{}}, "serverInfo": gin.H{"name": "CQA ERP", "version": "1.0.0"}})
	case "ping":
		reply(gin.H{})
	case "tools/list":
		reply(gin.H{"tools": erpMCPTools})
	case "tools/call":
		var params struct {
			Name      string                     `json:"name"`
			Arguments map[string]json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &params); err != nil {
			fail(-32602, "Invalid tool parameters")
			return
		}
		for _, tool := range erpMCPTools {
			if tool.Name != params.Name {
				continue
			}
			body, err := erpMCPQuery(tool, params.Arguments)
			if err != nil {
				fail(-32602, err.Error())
				return
			}
			permissionToken := c.GetHeader("X-Permission-Token")
			if permissionToken == "" {
				reply(erpMCPResult([]byte(`{"error":"forbidden_token"}`), true))
				return
			}

			rec := httptest.NewRecorder()
			query, _ := gin.CreateTestContext(rec)
			query.Params = c.Params
			query.Request = c.Request.Clone(c.Request.Context())
			query.Request.Body = io.NopCloser(bytes.NewReader(body))
			query.Request.ContentLength = int64(len(body))
			query.Set(erpMCPToolContextKey, tool.Name)
			ERPQuery(query)
			if rec.Code >= 500 {
				reply(erpMCPResult([]byte(`{"error":"erp_data_unavailable","message":"Chưa thể lấy dữ liệu ERP. Vui lòng thử lại sau."}`), true))
				return
			}
			reply(erpMCPResult(rec.Body.Bytes(), rec.Code >= 400))
			return
		}
		fail(-32602, "Unknown ERP tool")
	default:
		fail(-32601, "Method not found")
	}
}

func erpMCPResult(body []byte, failed bool) gin.H {
	return gin.H{"isError": failed, "content": []gin.H{{"type": "text", "text": string(body)}}}
}

func erpMCPQuery(tool erpMCPTool, args map[string]json.RawMessage) ([]byte, error) {
	payload := map[string]any{"resource": tool.resource}
	properties := tool.InputSchema["properties"].(map[string]any)
	for key, value := range args {
		property, ok := properties[key]
		if !ok {
			return nil, &erpMCPArgumentError{key}
		}
		var typed any
		switch property.(gin.H)["type"] {
		case "boolean":
			typed = new(bool)
		case "integer":
			typed = new(int)
		default:
			typed = new(string)
		}
		if bytes.Equal(value, []byte("null")) || json.Unmarshal(value, typed) != nil {
			return nil, &erpMCPArgumentError{key}
		}
		switch key {
		case "sku", "line_name":
			payload["search"] = typed
		default:
			payload[key] = typed
		}
	}
	if required, ok := tool.InputSchema["required"].([]string); ok {
		for _, key := range required {
			var value string
			if json.Unmarshal(args[key], &value) != nil || strings.TrimSpace(value) == "" {
				return nil, &erpMCPArgumentError{key}
			}
		}
	}
	if tool.Name == "inventory_by_sku" {
		var sku string
		if raw, ok := args["sku"]; ok {
			_ = json.Unmarshal(raw, &sku)
			if strings.TrimSpace(sku) == "" || len(args) != 1 {
				return nil, &erpMCPArgumentError{"sku"}
			}
			payload["search"] = strings.TrimSpace(sku)
			payload["exact_sku"] = true
		} else if len(args["search"]) == 0 && len(args["parent_code"]) == 0 {
			return nil, &erpMCPArgumentError{"search"}
		}
	}
	return json.Marshal(payload)
}

type erpMCPArgumentError struct{ field string }

func (e *erpMCPArgumentError) Error() string { return "Invalid or missing argument: " + e.field }
