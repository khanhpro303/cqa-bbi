package pkg

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// seedSession pre-populates the session cache so getSession() skips the live
// authenticate() round-trip and the test only needs to mock the data endpoint.
func seedSession(c *CloudifyClient) {
	cloudifySessionCacheMu.Lock()
	cloudifySessionCache[c.cacheKey()] = &cloudifySessionEntry{
		Cookie:    "session_id=test-session",
		ExpiresAt: time.Now().Add(30 * time.Minute),
	}
	cloudifySessionCacheMu.Unlock()
}

func TestTotalStockFromInventoryItems_KhoTongOnly(t *testing.T) {
	items := []map[string]interface{}{
		// Nested warehouse rows: only "Kho Tổng" counts; branch + aggregate ignored.
		{
			"MA_HANG":           "FF901-RED-L",
			"SO_LUONG_TON_TONG": 999.0, // aggregate must be ignored
			"TON_KHO_CHI_TIET": []interface{}{
				map[string]interface{}{"TEN_KHO": "Kho Tổng", "SO_LUONG_TON": 12.0},
				map[string]interface{}{"TEN_KHO": "Kho Chi Nhánh 1", "SO_LUONG_TON": 7.0},
			},
		},
		// Flat shape, case- and space-insensitive warehouse match.
		{"TEN_KHO": " kho  tổng ", "SO_LUONG_TON": 3.0},
		// String-encoded stock is coerced.
		{"TEN_KHO": "KHO TỔNG", "SO_LUONG_TON": "5"},
		// Non-matching warehouse ignored.
		{"TEN_KHO": "Kho Lỗi", "SO_LUONG_TON": 50.0},
	}

	got := TotalStockFromInventoryItems(items)
	if got != 20.0 {
		t.Fatalf("expected 20.0 (12 + 3 + 5), got %v", got)
	}
}

// TestTotalStockFromInventoryItems_AsciiHyphenWarehouse is the regression guard
// for the real BBI ERP response: the total warehouse is returned as the ASCII,
// hyphenated "KHO-TONG" (no diacritics), not the configured "Kho Tổng". Warehouse
// matching must fold diacritics and drop separators, otherwise live stock (here
// 6 units of SP458495) is silently reported as 0 — read by the customer as out
// of stock.
func TestTotalStockFromInventoryItems_AsciiHyphenWarehouse(t *testing.T) {
	items := []map[string]interface{}{
		{
			"MA_HANG":           "SP458495",
			"SO_LUONG_TON_TONG": 6.0, // aggregate still ignored
			"TON_KHO_CHI_TIET": []interface{}{
				map[string]interface{}{"TEN_CHI_NHANH": "CN NỘI BỘ", "TEN_KHO": "KHO-TONG", "SO_LUONG_TON": 6.0},
			},
		},
	}
	if got := TotalStockFromInventoryItems(items); got != 6.0 {
		t.Fatalf("expected 6.0 from KHO-TONG (real BBI shape), got %v", got)
	}
}

func TestTotalStockFromInventoryItems_NoMatchReturnsZero(t *testing.T) {
	items := []map[string]interface{}{
		{"TEN_KHO": "Kho Chi Nhánh", "SO_LUONG_TON": 99.0},
	}
	if got := TotalStockFromInventoryItems(items); got != 0 {
		t.Fatalf("expected 0 when no Kho Tổng row, got %v", got)
	}
}

func TestTotalStockForSKU_SumsKhoTong(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"MA_HANG":"SP458484","TON_KHO_CHI_TIET":[` +
			`{"TEN_KHO":"Kho Tổng","SO_LUONG_TON":20},` +
			`{"TEN_KHO":"Kho CN","SO_LUONG_TON":5}]}]`))
	}))
	defer srv.Close()

	c := &CloudifyClient{BaseURL: srv.URL, DB: "db", Login: "u", Password: "p"}
	seedSession(c)

	stock, err := c.InventoryStock(InventoryTotalStockEndpoint, true, "SP458484")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if stock != 20.0 {
		t.Fatalf("expected 20.0 (Kho Tổng only), got %v", stock)
	}
}

func TestInventoryStock_CustomEndpointSumsRows(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"ma_hang":"X","ton":4},{"ma_hang":"X","so_luong_ton_tong":6}]`))
	}))
	defer srv.Close()

	c := &CloudifyClient{BaseURL: srv.URL, DB: "db", Login: "u", Password: "p"}
	seedSession(c)

	// A custom (non-official) endpoint sums stock across rows rather than reading
	// only "Kho Tổng".
	stock, err := c.InventoryStock("danhmucvattuhanghoa/custom_ton", true, "X")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if stock != 10.0 {
		t.Fatalf("expected 10.0 (4 + 6), got %v", stock)
	}
}

// TestTotalStockForSKU_HTTP500PropagatesError is the regression guard for the
// reported bug: an ERP HTTP 500 must surface as an error, never collapse into a
// silent "0 stock" that the customer reads as out of stock.
func TestTotalStockForSKU_HTTP500PropagatesError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("Internal Server Error"))
	}))
	defer srv.Close()

	c := &CloudifyClient{BaseURL: srv.URL, DB: "db", Login: "u", Password: "p"}
	seedSession(c)

	stock, err := c.InventoryStock(InventoryTotalStockEndpoint, true, "SP458484")
	if err == nil {
		t.Fatal("expected error on HTTP 500, got nil (silent-zero regression)")
	}
	if stock != 0 {
		t.Fatalf("expected 0 stock alongside the error, got %v", stock)
	}
}
