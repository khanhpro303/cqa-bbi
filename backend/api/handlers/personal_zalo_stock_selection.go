package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/vietbui/chat-quality-agent/channels"
	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/engine"
)

const personalZaloStockMenuSuffix = ":stock_menu"
const personalZaloStockMenuTTL = 10 * time.Minute

// The ERP request writes a menu for this signed turn. Only the matching
// gateway response can move it to the permission-scoped chat session.
func recordPersonalZaloStockMenu(ctx context.Context, permissions *engine.GroupPermissionContext, options []channels.ZaloOAButton) {
	if db.RedisClient == nil || permissions.TurnID == "" || permissions.PersonalZaloSessionID == "" {
		return
	}
	raw, err := json.Marshal(options)
	if err == nil {
		db.RedisClient.Set(ctx, permissions.TurnID+personalZaloStockMenuSuffix, raw, personalZaloStockMenuTTL)
	}
}

func (g personalZaloDirectGrant) publishPersonalZaloStockMenu(ctx context.Context) string {
	if db.RedisClient == nil || g.turnID == "" {
		return ""
	}
	raw, err := db.RedisClient.GetDel(ctx, g.turnID+personalZaloStockMenuSuffix).Bytes()
	if err != nil {
		return ""
	}
	var options []channels.ZaloOAButton
	if json.Unmarshal(raw, &options) != nil || len(options) < 2 {
		return ""
	}
	reply := personalZaloStockMenuText(options)
	if personalZaloTextBoundary(reply, personalZaloTextLimit) < len(reply) {
		return ""
	}
	if db.RedisClient.Set(ctx, g.flowScope+personalZaloStockMenuSuffix, raw, personalZaloStockMenuTTL).Err() != nil {
		return ""
	}
	return reply
}

func personalZaloStockMenuText(options []channels.ZaloOAButton) string {
	return channels.BuildButtonOptionsAsText("Anh/chị nhắn số tương ứng với dòng cần tra tồn Kho Tổng giúp em nhé.", options)
}

func (g personalZaloDirectGrant) answerPersonalZaloStockPick(ctx context.Context, req personalZaloChatbotRequest) string {
	if db.RedisClient == nil || g.permission == "" {
		return ""
	}
	key := g.flowScope + personalZaloStockMenuSuffix
	raw, err := db.RedisClient.Get(ctx, key).Bytes()
	if err != nil {
		return ""
	}
	var options []channels.ZaloOAButton
	if json.Unmarshal(raw, &options) != nil {
		db.RedisClient.Del(ctx, key)
		return ""
	}
	postbacks := make([]string, len(options))
	for i, option := range options {
		postbacks[i] = option.Payload
	}
	postback, numeric, valid := engine.ResolveNumericSelection(req.Text, postbacks)
	if !numeric {
		db.RedisClient.Del(ctx, key)
		return ""
	}
	if !valid {
		return personalZaloStockMenuText(options)
	}
	intent, line := engine.ParseStockPickWeb(postback)
	if intent != engine.StockPickIntentStock || line == "" {
		db.RedisClient.Del(ctx, key)
		return ""
	}
	reply, confirmed := g.lookupPersonalZaloStockLine(ctx, req.TenantID, line)
	if confirmed {
		db.RedisClient.Del(ctx, key)
	}
	return reply
}

func (g personalZaloDirectGrant) lookupPersonalZaloStockLine(ctx context.Context, tenantID, line string) (string, bool) {
	body, _ := json.Marshal(map[string]string{"resource": "inventory", "search": line})
	rec := httptest.NewRecorder()
	query, _ := gin.CreateTestContext(rec)
	query.Params = gin.Params{{Key: "tenantId", Value: tenantID}}
	query.Request = httptest.NewRequest(http.MethodPost, "/erp/query", bytes.NewReader(body)).WithContext(ctx)
	query.Request.Header.Set("Content-Type", "application/json")
	query.Request.Header.Set("X-Agent-Token", g.agentToken)
	query.Request.Header.Set("X-Permission-Token", g.permission)
	query.Set(erpMCPToolContextKey, "inventory_by_line")
	ERPQuery(query)

	var result struct {
		Data []struct {
			Quantity *float64 `json:"ton_kho"`
			InStock  *bool    `json:"in_stock"`
		} `json:"data"`
	}
	const unavailable = "Dạ em chưa xác nhận được tồn khả dụng ở Kho Tổng cho dòng đã chọn. Anh/chị thử lại sau giúp em nhé."
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &result) != nil || len(result.Data) != 1 {
		return unavailable, false
	}
	stock := result.Data[0]
	if stock.Quantity != nil {
		return fmt.Sprintf("Dạ, tồn khả dụng của dòng %s tại Kho Tổng là %g chiếc ạ.", line, *stock.Quantity), true
	}
	if stock.InStock != nil {
		status := "hết hàng"
		if *stock.InStock {
			status = "còn hàng"
		}
		return fmt.Sprintf("Dạ, dòng %s hiện %s tại Kho Tổng ạ.", line, status), true
	}
	return unavailable, false
}
