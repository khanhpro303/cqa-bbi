package handlers

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/vietbui/chat-quality-agent/config"
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

const erpLookupToolInstruction = " Khi lookup_outcome=no_visible_match, dùng customer_message theo nội dung khách hỏi và dừng tra mục đó. Không yêu cầu gửi lại mã hoặc ảnh, không tra lại qua search_knowledge. Kết quả này không xác nhận tồn kho hay sự tồn tại của sản phẩm."

var erpMCPTools = []erpMCPTool{
	newERPMCPTool("search_products", "Tìm dòng sản phẩm, giá, hoặc các dòng trùng tên. search là tên hoặc mã người dùng hỏi. intent là price hoặc stock. Tool này không trả tồn kho: hỏi tồn của cả dòng thì gọi tiếp inventory_by_line với line_name là web_name tìm được, không lấy parent_codes để gọi inventory_by_sku."+erpLookupToolInstruction, "products", []string{"search"}, "search", "limit", "intent", "exact_web_name", "color", "size", "brand", "parent_code"),
	newERPMCPTool("search_product_variants", "Tra màu, size hoặc giá biến thể của mã cha đã xác định. include_stock đính kèm tồn khả dụng 1.KHO-TONG khi tra được. Biến thể thiếu ton_kho chưa có kết quả tồn."+erpLookupToolInstruction, "product_variants", []string{"parent_code"}, "search", "parent_code", "limit", "color", "size", "brand", "include_stock"),
	newERPMCPTool("inventory_by_sku", "Tra tồn khả dụng 1.KHO-TONG của MỘT biến thể. Khi chỉ có tên dòng và thuộc tính màu/size, dùng search cùng color/size/brand để phân giải SKU. Hỏi tồn của cả dòng thì dùng inventory_by_line; nếu lỡ truyền mã cha, hệ thống trả tổng cả dòng."+erpLookupToolInstruction, "inventory", nil, "sku", "search", "limit", "parent_code", "exact_web_name", "color", "size", "brand"),
	newERPMCPTool("inventory_by_line", "Cộng tồn khả dụng 1.KHO-TONG của các SKU được phép xem thuộc dòng đã xác nhận (line_name là web_name chính xác, ví dụ LS2 FF818). Nếu tên còn mơ hồ, dùng search_products trước."+erpLookupToolInstruction, "inventory", []string{"line_name"}, "line_name"),
	newERPMCPTool("find_customer", "Tra thông tin khách hàng theo tên hoặc mã trong phạm vi được phép.", "customers", nil, "search", "limit", "partner_id"),
	newERPMCPTool("lookup_order", "Tra đơn hàng trong phạm vi được phép. Câu hỏi chung hoặc sắp giao dùng search “đơn hàng” để nhận các đơn tạo hôm nay theo giờ Việt Nam và trạng thái hiện tại. Ngày hẹn giao chỉ đi kèm những đơn có ngày hẹn. Dùng mã đơn khi tra một đơn hoặc 3/5/7 ngày gần đây khi xem lịch sử.", "orders", nil, "search", "limit", "partner_id"),
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
	preview := strings.HasPrefix(token, knowledgePreviewTokenPrefix)
	var agentType string
	if preview {
		if cfg, err := config.Load(); err == nil {
			agentType = resolveKnowledgePreviewAgent(cfg.EncryptionKey, tenantID, token)
		}
	} else if token != "" {
		agentType = resolveAgentType(tenantID, token)
	}
	if agentType == "" {
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
		if preview {
			reply(gin.H{"tools": []erpMCPTool{knowledgeMCPTool(tenantID)}})
			return
		}
		tools := append(append([]erpMCPTool{}, erpMCPTools...), listedOrderTools()...)
		tools = append(tools, knowledgeMCPTool(tenantID), complaintMCPTool())
		reply(gin.H{"tools": tools})
	case "tools/call":
		var params struct {
			Name      string                     `json:"name"`
			Arguments map[string]json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &params); err != nil {
			fail(-32602, "Invalid tool parameters")
			return
		}
		if params.Name == knowledgeMCPToolName {
			reply(handleKnowledgeTool(c.Request.Context(), tenantID, agentType, params.Arguments))
			return
		}
		if preview {
			fail(-32602, "Tool unavailable in knowledge preview")
			return
		}
		if params.Name == complaintMCPToolName {
			reply(handleComplaintTool(c.Request.Context(), tenantID, agentType, c.GetHeader("X-Permission-Token"), params.Arguments))
			return
		}
		if tool, ok := findCartTool(params.Name); ok {
			reply(handleCartTool(c.Request.Context(), tenantID, agentType, c.GetHeader("X-Permission-Token"), tool, params.Arguments))
			return
		}
		if tool, ok := findOrderTool(params.Name); ok {
			reply(handleOrderDraftTool(c.Request.Context(), tenantID, agentType, c.GetHeader("X-Permission-Token"), tool, params.Arguments))
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
			result := rec.Body.Bytes()
			if rec.Code == http.StatusOK {
				result = describeERPProductLookup(tool.resource, result)
			}
			reply(erpMCPResult(result, rec.Code >= 400))
			return
		}
		fail(-32602, "Unknown ERP tool")
	default:
		fail(-32601, "Method not found")
	}
}

// Label an empty scoped read without checking the unrestricted catalog. The
// caller gets the same outcome for an unknown product and an excluded one.
func describeERPProductLookup(resource string, body []byte) []byte {
	if resource != "products" && resource != "product_variants" && resource != "inventory" {
		return body
	}
	var result struct {
		Status          string            `json:"status"`
		Data            []json.RawMessage `json:"data"`
		Count           int               `json:"count"`
		RichInventory   bool              `json:"is_inventory_rich"`
		AvailableColors []string          `json:"available_colors"`
		AvailableSizes  []string          `json:"available_sizes"`
		AvailableBrands []string          `json:"available_brands"`
	}
	if json.Unmarshal(body, &result) != nil || result.Status != "success" || result.Data == nil ||
		len(result.Data) != 0 || result.Count != 0 || result.RichInventory ||
		len(result.AvailableColors)+len(result.AvailableSizes)+len(result.AvailableBrands) != 0 {
		return body
	}

	what := "thông tin sản phẩm"
	if resource == "inventory" {
		what = "tồn kho"
	}
	// Only pass fields independent of internal matches made before filtering.
	encoded, _ := json.Marshal(gin.H{
		"status": "success", "data": result.Data, "count": 0, "resource": resource,
		"lookup_outcome":   "no_visible_match",
		"customer_message": "Dạ em chưa xác nhận được " + what + " của mẫu này trong dữ liệu em có thể tra cứu. Anh/chị có thể hỏi nhân viên phụ trách để được kiểm tra thêm ạ.",
	})
	return encoded
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
