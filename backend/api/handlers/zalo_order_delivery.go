package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/vietbui/chat-quality-agent/config"
	"github.com/vietbui/chat-quality-agent/engine"
)

const zaloOrderDeliveryAccessDenied = "Dạ, tài khoản của anh/chị hiện chưa có quyền tra cứu đơn hàng. Anh/chị có thể liên hệ nhân viên phụ trách để được hỗ trợ ạ."

// LookupCurrentZaloOrderDelivery runs the existing signed public ERP lookup
// in-process, so the Zalo worker can answer before invoking a language model.
func LookupCurrentZaloOrderDelivery(ctx context.Context, tenantID, agentToken, permissionToken string, days int) (string, bool, error) {
	search := "đơn hàng"
	switch days {
	case 0:
	case 3, 5, 7:
		search = fmt.Sprintf("%d ngày gần đây", days)
	default:
		return "", false, fmt.Errorf("unsupported Zalo order lookup period: %d", days)
	}
	if tenantID == "" || agentToken == "" || permissionToken == "" {
		return "", false, fmt.Errorf("Zalo order lookup is missing its signed credentials")
	}
	cfg, err := config.Load()
	if err != nil {
		return "", false, fmt.Errorf("load Zalo order lookup config: %w", err)
	}
	// The ordinary ERP route also supports private staff lookups. Guard this
	// entry point so it cannot trigger a private date or customer picker.
	permissions, err := engine.VerifyPermissionToken(permissionToken, cfg.EncryptionKey)
	if err != nil || permissions.AgentType != "public" || permissions.TenantID != tenantID {
		return "", false, fmt.Errorf("Zalo order lookup requires a signed public permission token for the tenant")
	}
	if allowed, scope, _ := permissions.IsResourceAllowed("orders"); allowed && scope != "own" {
		return zaloOrderDeliveryAccessDenied, false, nil
	}

	rec := httptest.NewRecorder()
	query, _ := gin.CreateTestContext(rec)
	query.Params = gin.Params{{Key: "tenantId", Value: tenantID}}
	query.Request = httptest.NewRequestWithContext(ctx, http.MethodPost, "/erp/query",
		strings.NewReader(fmt.Sprintf(`{"resource":"orders","search":%q}`, search)))
	query.Request.RemoteAddr = "127.0.0.1:0"
	query.Request.Header.Set("Content-Type", "application/json")
	query.Request.Header.Set("X-Agent-Token", agentToken)
	query.Request.Header.Set("X-Permission-Token", permissionToken)
	query.Set(erpMCPToolContextKey, "lookup_order")
	ERPQuery(query)

	return renderZaloOrderDeliveryResponse(rec.Code, rec.Body.Bytes(), days)
}

type zaloOrderDeliveryResponse struct {
	Status    string         `json:"status"`
	Error     string         `json:"error"`
	Resource  string         `json:"resource"`
	Scope     string         `json:"scope"`
	Source    string         `json:"source"`
	DateBasis string         `json:"date_basis"`
	RangeDays *int           `json:"range_days"`
	From      string         `json:"from"`
	To        string         `json:"to"`
	Summary   *OrdersSummary `json:"orders_summary"`
	Orders    []struct {
		ID          string `json:"order_id"`
		Status      string `json:"status"`
		Appointment string `json:"scheduled_delivery_date"`
	} `json:"orders"`
}

func renderZaloOrderDeliveryResponse(status int, body []byte, days int) (string, bool, error) {
	var result zaloOrderDeliveryResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return "", false, fmt.Errorf("decode scoped ERP order response (HTTP %d): %w", status, err)
	}
	if status == http.StatusForbidden && result.Error == "forbidden_scope" {
		return zaloOrderDeliveryAccessDenied, false, nil
	}
	if status != http.StatusOK {
		return "", false, fmt.Errorf("scoped ERP order lookup failed (HTTP %d)", status)
	}
	if result.Status != "success" || result.Resource != "orders" || result.Scope != "own" ||
		result.Source != "cloudify_live" || result.DateBasis != "order_created" ||
		result.RangeDays == nil || *result.RangeDays != days || result.Summary == nil {
		return "", false, fmt.Errorf("ERP did not return scoped orders for the requested period")
	}

	from, fromErr := time.Parse("02/01/2006", result.From)
	to, toErr := time.Parse("02/01/2006", result.To)
	if fromErr != nil || toErr != nil || !from.AddDate(0, 0, days).Equal(to) {
		return "", false, fmt.Errorf("ERP did not return the expected order date bounds")
	}

	const waiting, delivering = "4", "5"
	var waitingCount, deliveringCount, unknownCount, counted int
	for _, bucket := range result.Summary.ByStatus {
		if bucket.Count < 0 {
			return "", false, fmt.Errorf("ERP returned an invalid order status count")
		}
		counted += bucket.Count
		switch normalizeOrderLookupStatus(bucket.Status) {
		case waiting:
			waitingCount += bucket.Count
		case delivering:
			deliveringCount += bucket.Count
		case "0", "1", "2", "3":
		default:
			unknownCount += bucket.Count
		}
	}
	if counted != result.Summary.TotalOrders {
		return "", false, fmt.Errorf("ERP returned incomplete order status counts")
	}

	period := "hôm nay"
	if days > 0 {
		period = fmt.Sprintf("trong %d ngày gần đây (từ %s đến %s)", days, result.From, result.To)
	}
	var reply strings.Builder
	switch {
	case result.Summary.TotalOrders == 0:
		fmt.Fprintf(&reply, "Dạ, em không thấy đơn hàng nào của anh/chị phát sinh %s.", period)
	case waitingCount == 0 && deliveringCount == 0:
		fmt.Fprintf(&reply, "Dạ, trong các đơn phát sinh %s, em chưa thấy đơn nào có trạng thái Chờ giao hoặc Đang giao.", period)
	default:
		fmt.Fprintf(&reply, "Dạ, trong các đơn phát sinh %s, có %d đơn Chờ giao và %d đơn Đang giao.", period, waitingCount, deliveringCount)
	}
	if unknownCount > 0 {
		fmt.Fprintf(&reply, " Có %d đơn chưa xác định được trạng thái.", unknownCount)
	}

	dates := 0
	for _, order := range result.Orders {
		orderStatus := normalizeOrderLookupStatus(order.Status)
		if (orderStatus != waiting && orderStatus != delivering) || strings.TrimSpace(order.ID) == "" {
			continue
		}
		if _, err := time.Parse("02/01/2006", order.Appointment); err != nil {
			continue
		}
		fmt.Fprintf(&reply, "\n- Đơn %s có ngày hẹn giao %s.", order.ID, order.Appointment)
		dates++
		if dates == 5 {
			break
		}
	}
	if days == 0 {
		reply.WriteString("\nAnh/chị muốn em xem thêm đơn hàng trong 3, 5 hoặc 7 ngày gần đây không ạ?")
	}
	return reply.String(), unknownCount > 0, nil
}
