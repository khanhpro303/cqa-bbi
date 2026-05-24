package pkg

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// ---------------------------------------------------------------------------
// Session cache — in-memory, keyed by "baseURL|login"
// ---------------------------------------------------------------------------

var (
	cloudifySessionCache   = make(map[string]*cloudifySessionEntry)
	cloudifySessionCacheMu sync.Mutex
)

type cloudifySessionEntry struct {
	Cookie    string // raw "session_id=<value>" cookie header
	ExpiresAt time.Time
}

// ---------------------------------------------------------------------------
// CloudifyClient
// ---------------------------------------------------------------------------

// CloudifyClient performs authenticated calls to the Cloudify ERP REST API.
// Authentication uses JSON-RPC session cookies (Odoo-compatible).
// Session is cached for 30 minutes to avoid repeated logins.
type CloudifyClient struct {
	BaseURL  string // e.g., "https://bbiapi.cloudify.vn"
	DB       string // Cloudify database name, e.g., "demobienla"
	Login    string // e.g., "bien.la@cloudify.vn"
	Password string
}

func (c *CloudifyClient) cacheKey() string {
	return c.BaseURL + "|" + c.Login
}

// getSession returns a valid session cookie string, re-authenticating if expired.
func (c *CloudifyClient) getSession() (string, error) {
	key := c.cacheKey()

	cloudifySessionCacheMu.Lock()
	entry, ok := cloudifySessionCache[key]
	cloudifySessionCacheMu.Unlock()

	if ok && time.Now().Before(entry.ExpiresAt) {
		return entry.Cookie, nil
	}

	cookie, err := c.authenticate()
	if err != nil {
		return "", err
	}

	cloudifySessionCacheMu.Lock()
	cloudifySessionCache[key] = &cloudifySessionEntry{
		Cookie:    cookie,
		ExpiresAt: time.Now().Add(30 * time.Minute),
	}
	cloudifySessionCacheMu.Unlock()

	return cookie, nil
}

// InvalidateSession removes the cached session for this client (forces re-auth on next call).
func (c *CloudifyClient) InvalidateSession() {
	cloudifySessionCacheMu.Lock()
	delete(cloudifySessionCache, c.cacheKey())
	cloudifySessionCacheMu.Unlock()
}

// ---------------------------------------------------------------------------
// Authentication
// ---------------------------------------------------------------------------

type cloudifyJSONRPCRequest struct {
	JSONRPC string                 `json:"jsonrpc"`
	Method  string                 `json:"method"`
	Params  map[string]interface{} `json:"params"`
}

type cloudifyJSONRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      interface{}     `json:"id"`
	Result  json.RawMessage `json:"result"`
	Error   *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Data    struct {
			Message string `json:"message"`
		} `json:"data"`
	} `json:"error"`
}

// authenticate performs JSON-RPC POST to /web/session/authenticate and returns
// the "session_id=<value>" cookie string.
func (c *CloudifyClient) authenticate() (string, error) {
	authURL := strings.TrimRight(c.BaseURL, "/") + "/web/session/authenticate"

	payload := cloudifyJSONRPCRequest{
		JSONRPC: "2.0",
		Method:  "call",
		Params: map[string]interface{}{
			"db":       c.DB,
			"login":    c.Login,
			"password": c.Password,
		},
	}

	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal auth request: %w", err)
	}

	req, err := http.NewRequest("POST", authURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return "", fmt.Errorf("create auth request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("auth HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	var rpcResp cloudifyJSONRPCResponse
	if err := json.NewDecoder(resp.Body).Decode(&rpcResp); err != nil {
		return "", fmt.Errorf("decode auth response: %w", err)
	}

	if rpcResp.Error != nil {
		msg := rpcResp.Error.Message
		if rpcResp.Error.Data.Message != "" {
			msg = rpcResp.Error.Data.Message
		}
		return "", fmt.Errorf("cloudify auth error: %s", msg)
	}

	// uid=false means wrong credentials in Odoo-based systems
	var result struct {
		UID      interface{} `json:"uid"`
		SessionID string     `json:"session_id"`
	}
	if err := json.Unmarshal(rpcResp.Result, &result); err != nil {
		return "", fmt.Errorf("decode auth result: %w", err)
	}

	if b, isBool := result.UID.(bool); isBool && !b {
		return "", fmt.Errorf("cloudify authentication failed: invalid credentials (uid=false)")
	}
	if result.UID == nil {
		return "", fmt.Errorf("cloudify authentication failed: null uid in response")
	}

	// Prefer Set-Cookie header (authoritative)
	for _, cookie := range resp.Cookies() {
		if cookie.Name == "session_id" {
			return "session_id=" + cookie.Value, nil
		}
	}

	// Fallback: session_id from JSON body
	if result.SessionID != "" {
		return "session_id=" + result.SessionID, nil
	}

	return "", fmt.Errorf("no session_id found in Cloudify auth response")
}

// TestConnection authenticates and immediately returns — useful for validating credentials.
func (c *CloudifyClient) TestConnection() error {
	_, err := c.authenticate()
	return err
}

// ---------------------------------------------------------------------------
// REST API helpers
// ---------------------------------------------------------------------------

// restSearch performs GET /rest_api/private/<endpoint> with the session cookie.
// It retries once if the session has expired (401/403).
func (c *CloudifyClient) restSearch(endpoint string, params map[string]string) ([]map[string]interface{}, error) {
	session, err := c.getSession()
	if err != nil {
		return nil, fmt.Errorf("get session: %w", err)
	}

	data, statusCode, err := c.doRESTGet(endpoint, params, session)
	if err != nil {
		return nil, err
	}

	// Session expired → invalidate cache and retry once
	if statusCode == http.StatusUnauthorized || statusCode == http.StatusForbidden {
		c.InvalidateSession()
		session, err = c.getSession()
		if err != nil {
			return nil, fmt.Errorf("re-authenticate after session expiry: %w", err)
		}
		data, _, err = c.doRESTGet(endpoint, params, session)
		if err != nil {
			return nil, err
		}
	}

	return data, nil
}

func (c *CloudifyClient) doRESTGet(endpoint string, params map[string]string, session string) ([]map[string]interface{}, int, error) {
	baseURL := strings.TrimRight(c.BaseURL, "/")
	apiURL := fmt.Sprintf("%s/rest_api/private/%s", baseURL, endpoint)

	req, err := http.NewRequest("GET", apiURL, nil)
	if err != nil {
		return nil, 0, fmt.Errorf("create request: %w", err)
	}

	q := req.URL.Query()
	if c.DB != "" {
		q.Set("db", c.DB)
	}
	if session != "" {
		sessionID := strings.TrimPrefix(session, "session_id=")
		q.Set("session_id", sessionID)
	}
	for k, v := range params {
		if v != "" {
			q.Set(k, v)
		}
	}
	req.URL.RawQuery = q.Encode()
	req.Header.Set("Cookie", session)
	req.Header.Set("Content-Type", "application/json")

	httpClient := &http.Client{Timeout: 15 * time.Second}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("Cloudify API request to %s failed: %w", endpoint, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return nil, resp.StatusCode, nil // caller handles retry
	}

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("read response body: %w", err)
	}

	if resp.StatusCode >= 400 {
		preview := string(bodyBytes)
		if len(preview) > 300 {
			preview = preview[:300]
		}
		return nil, resp.StatusCode, fmt.Errorf("Cloudify returned HTTP %d: %s", resp.StatusCode, preview)
	}

	// Try parsing as JSON array directly
	var result []map[string]interface{}
	if err := json.Unmarshal(bodyBytes, &result); err == nil {
		return result, resp.StatusCode, nil
	}

	// Try wrapped object: {"data": [...]} or {"result": [...]} or {"records": [...]}
	var wrapped struct {
		Data    []map[string]interface{} `json:"data"`
		Result  []map[string]interface{} `json:"result"`
		Records []map[string]interface{} `json:"records"`
	}
	if err := json.Unmarshal(bodyBytes, &wrapped); err == nil {
		if wrapped.Data != nil {
			return wrapped.Data, resp.StatusCode, nil
		}
		if wrapped.Result != nil {
			return wrapped.Result, resp.StatusCode, nil
		}
		if wrapped.Records != nil {
			return wrapped.Records, resp.StatusCode, nil
		}
	}

	preview := string(bodyBytes)
	if len(preview) > 200 {
		preview = preview[:200]
	}
	return nil, resp.StatusCode, fmt.Errorf("unexpected Cloudify response format: %s", preview)
}

// ---------------------------------------------------------------------------
// Public search methods
// ---------------------------------------------------------------------------

// SearchProducts queries /rest_api/private/product/search
func (c *CloudifyClient) SearchProducts(search string, limit int) ([]map[string]interface{}, error) {
	return c.restSearch("product/search", map[string]string{
		"keyword": search,
		"limit":   fmt.Sprintf("%d", limit),
	})
}

// SearchInventory queries /rest_api/private/inventory_receipt/search
func (c *CloudifyClient) SearchInventory(search string, limit int) ([]map[string]interface{}, error) {
	return c.restSearch("inventory_receipt/search", map[string]string{
		"keyword": search,
		"limit":   fmt.Sprintf("%d", limit),
	})
}

// SearchSaleDocuments queries /rest_api/private/sale_document/search
// partnerID optionally scopes results to a specific customer.
func (c *CloudifyClient) SearchSaleDocuments(partnerID, search string, limit int) ([]map[string]interface{}, error) {
	return c.restSearch("sale_document/search", map[string]string{
		"partner_id": partnerID,
		"keyword":    search,
		"limit":      fmt.Sprintf("%d", limit),
	})
}

// SearchPartners queries /rest_api/private/partner/search
func (c *CloudifyClient) SearchPartners(search string, limit int) ([]map[string]interface{}, error) {
	return c.restSearch("partner/search", map[string]string{
		"keyword": search,
		"limit":   fmt.Sprintf("%d", limit),
	})
}

// SearchPartnerLedger queries /rest_api/private/partner_ledger/search
// partnerID optionally scopes debt data to a specific customer.
func (c *CloudifyClient) SearchPartnerLedger(partnerID, search string, limit int) ([]map[string]interface{}, error) {
	return c.restSearch("partner_ledger/search", map[string]string{
		"partner_id": partnerID,
		"keyword":    search,
		"limit":      fmt.Sprintf("%d", limit),
	})
}

// SearchPurchaseDocuments queries /rest_api/private/purchase_document/search
func (c *CloudifyClient) SearchPurchaseDocuments(search string, limit int) ([]map[string]interface{}, error) {
	return c.restSearch("purchase_document/search", map[string]string{
		"keyword": search,
		"limit":   fmt.Sprintf("%d", limit),
	})
}

// SearchCustomEndpoint queries a custom endpoint under /rest_api/private/
func (c *CloudifyClient) SearchCustomEndpoint(endpoint string, params map[string]string) ([]map[string]interface{}, error) {
	return c.restSearch(endpoint, params)
}

