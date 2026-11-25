package pkg

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const orderAppointmentPath = "/web/dataset/call_kw/account.ex.don.dat.hang/search_read"

// SearchOrderAppointments reads appointment dates for orders already selected
// and authorized by the caller. Cloudify's fulfillment report searches by
// order date and saorders/search omits the appointment field.
func (c *CloudifyClient) SearchOrderAppointments(ctx context.Context, orderCodes, customerCodes []string) ([]map[string]any, error) {
	if len(orderCodes) == 0 || len(customerCodes) == 0 {
		return nil, fmt.Errorf("Cloudify appointment lookup requires order and customer codes")
	}
	// The main order lookup already authenticated. If its session is gone,
	// skip the optional lookup within the same chat turn.
	cloudifySessionCacheMu.Lock()
	entry := cloudifySessionCache[c.cacheKey()]
	cloudifySessionCacheMu.Unlock()
	if entry == nil || !time.Now().Before(entry.ExpiresAt) {
		return nil, fmt.Errorf("Cloudify appointment lookup has no active session")
	}
	payload, err := json.Marshal(cloudifyJSONRPCRequest{
		JSONRPC: "2.0",
		Method:  "call",
		Params: map[string]any{
			"model":  "account.ex.don.dat.hang",
			"method": "search_read",
			"args": []any{[][]any{
				{"SO_DON_HANG", "in", orderCodes},
				{"KHACH_HANG_ID.MA", "in", customerCodes},
			}},
			"kwargs": map[string]any{
				"fields": []string{"SO_DON_HANG", "KHACH_HANG_ID", "NGAY_GIAO_HANG"},
			},
		},
	})
	if err != nil {
		return nil, err
	}
	url := strings.TrimRight(c.BaseURL, "/") + orderAppointmentPath
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Cookie", entry.Cookie)

	client := &http.Client{
		Timeout: 4 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Cloudify appointment lookup returned HTTP %d", resp.StatusCode)
	}
	const maxResponse = 1 << 20
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponse+1))
	if err != nil {
		return nil, err
	}
	if len(body) > maxResponse {
		return nil, fmt.Errorf("Cloudify appointment response exceeds size limit")
	}
	var reply struct {
		Result []map[string]any `json:"result"`
		Error  json.RawMessage  `json:"error"`
	}
	if err := json.Unmarshal(body, &reply); err != nil {
		return nil, fmt.Errorf("decode Cloudify appointment response: %w", err)
	}
	if reply.Result == nil || (len(reply.Error) > 0 && string(reply.Error) != "null") {
		return nil, fmt.Errorf("Cloudify appointment lookup did not return an order list")
	}
	return reply.Result, nil
}
