package pkg

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestMainWarehouseAvailableStock_UsesExactSKUAndAvailableQuantity(t *testing.T) {
	var requestBody map[string]interface{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/rest_api/private/"+InventoryTotalStockEndpoint {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if got := r.Header.Get("Content-Type"); got != "application/json" {
			t.Fatalf("unexpected content type: %q", got)
		}
		if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":200,"message":"ok","count":2,"data":[` +
			`{"MA_HANG":"OTHER","TON_KHO_CHI_TIET":[{"TEN_KHO":"KHO-TONG","SO_LUONG_TON_KHA_DUNG":100}]},` +
			`{"MA_HANG":"SKU-1","SO_LUONG_TON_KHA_DUNG":999,"SO_LUONG_TON_TONG":888,"TON_KHO_CHI_TIET":[` +
			`{"TEN_KHO":"KHO-TONG","SO_LUONG_TON":50,"SO_LUONG_TON_KHA_DUNG":7},` +
			`{"TEN_KHO":"1.KHO-TONG","SO_LUONG_TON":40,"SO_LUONG_TON_KHA_DUNG":"3"},` +
			`{"TEN_KHO":"Kho Chi Nhánh","SO_LUONG_TON_KHA_DUNG":9}]}]}`))
	}))
	defer srv.Close()

	c := &CloudifyClient{BaseURL: srv.URL, DB: "db", Login: "u", Password: "p"}
	seedSession(c)

	got, err := c.MainWarehouseAvailableStock(context.Background(), "SKU-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != 10 {
		t.Fatalf("expected 10 available units from main warehouse rows, got %v", got)
	}
	if len(requestBody) != 1 || requestBody["MA_HANG"] != "SKU-1" {
		t.Fatalf("expected exact body {MA_HANG: SKU-1}, got %#v", requestBody)
	}
}

func TestMainWarehouseAvailableStock_ExplicitZeroIsValid(t *testing.T) {
	items := []map[string]interface{}{{
		"MA_HANG": "SKU-0",
		"TON_KHO_CHI_TIET": []interface{}{
			map[string]interface{}{"TEN_KHO": "Kho Tổng", "SO_LUONG_TON_KHA_DUNG": 0.0},
		},
	}}

	got, err := mainWarehouseAvailableStockFromInventoryItems(items, "SKU-0")
	if err != nil {
		t.Fatalf("zero must be valid, got error: %v", err)
	}
	if got != 0 {
		t.Fatalf("expected zero, got %v", got)
	}
}

func TestMainWarehouseAvailableStock_EmptyWarehouseDetails(t *testing.T) {
	tests := []struct {
		name, data string
		wantZero   bool
	}{
		{"both totals explicitly zero", `[{"MA_HANG":"SP457697","SO_LUONG_TON_TONG":0,"SO_LUONG_TON_KHA_DUNG":0,"TON_KHO_CHI_TIET":[]}]`, true},
		{"available total missing", `[{"MA_HANG":"SP457697","SO_LUONG_TON_TONG":0,"TON_KHO_CHI_TIET":[]}]`, false},
		{"physical total missing", `[{"MA_HANG":"SP457697","SO_LUONG_TON_KHA_DUNG":0,"TON_KHO_CHI_TIET":[]}]`, false},
		{"physical stock exists", `[{"MA_HANG":"SP457697","SO_LUONG_TON_TONG":1,"SO_LUONG_TON_KHA_DUNG":0,"TON_KHO_CHI_TIET":[]}]`, false},
		{"available total nonzero", `[{"MA_HANG":"SP457697","SO_LUONG_TON_TONG":0,"SO_LUONG_TON_KHA_DUNG":1,"TON_KHO_CHI_TIET":[]}]`, false},
		{"warehouse list null", `[{"MA_HANG":"SP457697","SO_LUONG_TON_TONG":0,"SO_LUONG_TON_KHA_DUNG":0,"TON_KHO_CHI_TIET":null}]`, false},
		{"other warehouses account for all stock", `[{"MA_HANG":"SP457697","SO_LUONG_TON_TONG":4,"SO_LUONG_TON_KHA_DUNG":3,"TON_KHO_CHI_TIET":[{"TEN_KHO":"Kho NG","SO_LUONG_TON":1,"SO_LUONG_TON_KHA_DUNG":1},{"TEN_KHO":"Kho MKT","SO_LUONG_TON":"3","SO_LUONG_TON_KHA_DUNG":"2"}]}]`, true},
		{"other warehouse physical quantity missing", `[{"MA_HANG":"SP457697","SO_LUONG_TON_TONG":0,"SO_LUONG_TON_KHA_DUNG":0,"TON_KHO_CHI_TIET":[{"TEN_KHO":"Kho NG","SO_LUONG_TON_KHA_DUNG":0}]}]`, false},
		{"physical stock remains unexplained", `[{"MA_HANG":"SP457697","SO_LUONG_TON_TONG":2,"SO_LUONG_TON_KHA_DUNG":1,"TON_KHO_CHI_TIET":[{"TEN_KHO":"Kho NG","SO_LUONG_TON":1,"SO_LUONG_TON_KHA_DUNG":1}]}]`, false},
		{"available stock remains unexplained", `[{"MA_HANG":"SP457697","SO_LUONG_TON_TONG":3,"SO_LUONG_TON_KHA_DUNG":2,"TON_KHO_CHI_TIET":[{"TEN_KHO":"Kho NG","SO_LUONG_TON":3,"SO_LUONG_TON_KHA_DUNG":1}]}]`, false},
		{"negative other warehouse quantities", `[{"MA_HANG":"SP457697","SO_LUONG_TON_TONG":1,"SO_LUONG_TON_KHA_DUNG":1,"TON_KHO_CHI_TIET":[{"TEN_KHO":"Kho NG","SO_LUONG_TON":-1,"SO_LUONG_TON_KHA_DUNG":-1},{"TEN_KHO":"Kho MKT","SO_LUONG_TON":2,"SO_LUONG_TON_KHA_DUNG":2}]}]`, false},
		{"conflicting duplicate SKU", `[{"MA_HANG":"SP457697","SO_LUONG_TON_TONG":0,"SO_LUONG_TON_KHA_DUNG":0,"TON_KHO_CHI_TIET":[]},{"MA_HANG":"SP457697","SO_LUONG_TON_TONG":1,"SO_LUONG_TON_KHA_DUNG":1,"TON_KHO_CHI_TIET":[]}]`, false},
		{"different SKU has zero", `[{"MA_HANG":"OTHER","SO_LUONG_TON_TONG":0,"SO_LUONG_TON_KHA_DUNG":0,"TON_KHO_CHI_TIET":[]}]`, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var items []map[string]interface{}
			if err := json.Unmarshal([]byte(tt.data), &items); err != nil {
				t.Fatal(err)
			}
			got, err := mainWarehouseAvailableStockFromInventoryItems(items, "SP457697")
			if tt.wantZero && (err != nil || got != 0) {
				t.Fatalf("expected confirmed zero, got %v, %v", got, err)
			}
			if !tt.wantZero && err == nil {
				t.Fatalf("expected unconfirmed stock, got %v", got)
			}
		})
	}
}

func TestMainWarehouseAvailableStock_FailsClosed(t *testing.T) {
	tests := []struct {
		name  string
		items []map[string]interface{}
		want  string
	}{
		{name: "empty response", want: "no product rows"},
		{name: "wrong SKU", items: []map[string]interface{}{{"MA_HANG": "OTHER", "TON_KHO_CHI_TIET": []interface{}{}}}, want: "no exact SKU match"},
		{name: "missing details", items: []map[string]interface{}{{"MA_HANG": "SKU"}}, want: "TON_KHO_CHI_TIET"},
		{name: "no main warehouse", items: []map[string]interface{}{{"MA_HANG": "SKU", "TON_KHO_CHI_TIET": []interface{}{map[string]interface{}{"TEN_KHO": "Kho CN", "SO_LUONG_TON_KHA_DUNG": 12.0}}}}, want: "no main warehouse row"},
		{name: "physical stock is not fallback", items: []map[string]interface{}{{"MA_HANG": "SKU", "TON_KHO_CHI_TIET": []interface{}{map[string]interface{}{"TEN_KHO": "KHO-TONG", "SO_LUONG_TON": 12.0}}}}, want: "SO_LUONG_TON_KHA_DUNG"},
		{name: "invalid available quantity", items: []map[string]interface{}{{"MA_HANG": "SKU", "TON_KHO_CHI_TIET": []interface{}{map[string]interface{}{"TEN_KHO": "KHO-TONG", "SO_LUONG_TON_KHA_DUNG": "unknown"}}}}, want: "SO_LUONG_TON_KHA_DUNG"},
		{name: "overflowing total", items: []map[string]interface{}{{"MA_HANG": "SKU", "TON_KHO_CHI_TIET": []interface{}{map[string]interface{}{"TEN_KHO": "KHO-TONG", "SO_LUONG_TON_KHA_DUNG": 1.7e308}, map[string]interface{}{"TEN_KHO": "1.KHO-TONG", "SO_LUONG_TON_KHA_DUNG": 1.7e308}}}}, want: "not finite"},
		{name: "unknown warehouse row", items: []map[string]interface{}{{"MA_HANG": "SKU", "TON_KHO_CHI_TIET": []interface{}{map[string]interface{}{"SO_LUONG_TON_KHA_DUNG": 4.0}}}}, want: "TEN_KHO"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := mainWarehouseAvailableStockFromInventoryItems(tt.items, "SKU")
			if err == nil {
				t.Fatalf("expected fail-closed error, got stock %v", got)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("expected error containing %q, got %v", tt.want, err)
			}
		})
	}
}

func TestMainWarehouseAvailableStock_ContextCancellation(t *testing.T) {
	requestStarted := make(chan struct{})
	releaseHandler := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(requestStarted)
		<-releaseHandler
	}))
	defer func() {
		close(releaseHandler)
		srv.Close()
	}()

	c := &CloudifyClient{BaseURL: srv.URL, DB: "db", Login: "u", Password: "p"}
	seedSession(c)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := c.MainWarehouseAvailableStock(ctx, "SKU")
		done <- err
	}()

	select {
	case <-requestStarted:
	case <-time.After(time.Second):
		t.Fatal("request did not start")
	}
	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context cancellation, got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("request did not stop after context cancellation")
	}
}

func TestMainWarehouseAvailableStock_RejectsAuthorizationAfterRetry(t *testing.T) {
	dataRequests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/web/session/authenticate":
			http.SetCookie(w, &http.Cookie{Name: "session_id", Value: "refreshed-session"})
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":null,"result":{"uid":1,"session_id":"refreshed-session"}}`))
		case "/rest_api/private/" + InventoryTotalStockEndpoint:
			dataRequests++
			w.WriteHeader(http.StatusUnauthorized)
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	c := &CloudifyClient{BaseURL: srv.URL, DB: "db", Login: "u", Password: "p"}
	seedSession(c)
	_, err := c.MainWarehouseAvailableStock(context.Background(), "SKU")
	if err == nil || !strings.Contains(err.Error(), "after re-authentication") {
		t.Fatalf("expected final authorization error, got %v", err)
	}
	if dataRequests != 2 {
		t.Fatalf("expected one retry, got %d data requests", dataRequests)
	}
}
