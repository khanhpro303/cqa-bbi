package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/vietbui/chat-quality-agent/config"
)

// fallbackAgentNodeIDs are the agent node IDs we tweak when live node discovery
// (resolveAgentNodeIDs) fails — e.g. the GET /flows/{id} call errors (a 401 from
// a stale Langflow token is the usual cause) or returns an unexpected shape.
// Each Langflow flow has its own randomly-suffixed node ID, taken from the
// flow's exported definition:
//
//   - ToolCallingAgent-zznkZ — private/internal RAG flow (BBI_RAG_Bot_Ext.json)
//   - ToolCallingAgent-biiBD — public/group flow (37593e48-052f-4be3-abcc-94a21465b3aa)
//
// On the happy path we don't use these at all; the node IDs are read straight
// from the running flow's definition so re-imports that change node IDs keep
// working. Langflow ignores tweaks for node IDs absent from the running flow,
// so sending both as a fallback is safe. Keep this list in sync with the agent
// node IDs in the exported flows — a mismatch makes the system-prompt override
// silently miss that flow's agent (it then runs with an empty system prompt).
var fallbackAgentNodeIDs = []string{
	"ToolCallingAgent-zznkZ",
	"ToolCallingAgent-biiBD",
}

const (
	// agentNodeCacheTTL is how long a successful node-ID discovery is cached
	// per flow. Flow structure changes rarely, so this keeps the extra
	// GET /flows/{id} call to roughly once per flow per interval.
	agentNodeCacheTTL = 10 * time.Minute
	// agentNodeCacheNegativeTTL caches the fallback briefly after a failed
	// discovery so a transient Langflow hiccup doesn't trigger a GET on every
	// single message, while still self-healing within seconds.
	agentNodeCacheNegativeTTL = 30 * time.Second
)

type agentNodeCacheEntry struct {
	nodeIDs   []string
	expiresAt time.Time
}

// LangflowClient handles communication with the Langflow API.
type LangflowClient struct {
	cfg    *config.Config
	client *http.Client

	nodeCacheMu sync.RWMutex
	nodeCache   map[string]agentNodeCacheEntry // keyed by flow ID
}

func NewLangflowClient(cfg *config.Config) *LangflowClient {
	return &LangflowClient{
		cfg: cfg,
		client: &http.Client{
			Timeout: 60 * time.Second, // AI operations can take time
		},
		nodeCache: make(map[string]agentNodeCacheEntry),
	}
}

// applySystemPromptTweak overrides the agent's SYSTEM_PROMPT global variable for
// this request by tweaking the system_prompt field of every agent node in the
// running flow. The node IDs are discovered from the flow definition (cached per
// flow ID), so the override lands whether the private or the public flow handles
// the message. A no-op when systemPrompt is empty ("use the Langflow default").
func (l *LangflowClient) applySystemPromptTweak(ctx context.Context, tweaks map[string]interface{}, apiURL, apiKey, flowID, systemPrompt string) {
	if systemPrompt == "" {
		return
	}
	for _, nodeID := range l.resolveAgentNodeIDs(ctx, apiURL, apiKey, flowID) {
		tweaks[nodeID] = map[string]interface{}{
			"system_prompt": systemPrompt,
		}
	}
}

// resolveAgentNodeIDs returns the IDs of nodes in the given flow whose template
// exposes a system_prompt field (the agent nodes we can override). Results are
// cached per flow ID. On any error it falls back to fallbackAgentNodeIDs so a
// system prompt still ships even when discovery is unavailable.
func (l *LangflowClient) resolveAgentNodeIDs(ctx context.Context, apiURL, apiKey, flowID string) []string {
	l.nodeCacheMu.RLock()
	entry, ok := l.nodeCache[flowID]
	l.nodeCacheMu.RUnlock()
	if ok && time.Now().Before(entry.expiresAt) {
		return entry.nodeIDs
	}

	nodeIDs, err := l.fetchAgentNodeIDs(ctx, apiURL, apiKey, flowID)
	ttl := agentNodeCacheTTL
	if err != nil || len(nodeIDs) == 0 {
		log.Printf("[langflow] agent node discovery for flow %s failed (err=%v, found=%d); using fallback node IDs", flowID, err, len(nodeIDs))
		nodeIDs = fallbackAgentNodeIDs
		ttl = agentNodeCacheNegativeTTL
	}

	l.nodeCacheMu.Lock()
	l.nodeCache[flowID] = agentNodeCacheEntry{nodeIDs: nodeIDs, expiresAt: time.Now().Add(ttl)}
	l.nodeCacheMu.Unlock()
	return nodeIDs
}

// fetchAgentNodeIDs GETs the flow definition and extracts agent node IDs.
func (l *LangflowClient) fetchAgentNodeIDs(ctx context.Context, apiURL, apiKey, flowID string) ([]string, error) {
	if apiURL == "" || flowID == "" {
		return nil, fmt.Errorf("langflow integration is not configured")
	}

	url := fmt.Sprintf("%s/api/v1/flows/%s", apiURL, flowID)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("create flow request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	// Authenticate with x-api-key ONLY. The /api/v1/flows/{id} endpoint runs the
	// Bearer/JWT auth dependency first and rejects a Langflow API key ("sk-...")
	// sent as "Authorization: Bearer" with 401 "Invalid token" — even when a valid
	// x-api-key is also present. (The /run endpoint uses api-key auth, which is why
	// it tolerated both headers.) Sending only x-api-key lets discovery succeed.
	if apiKey != "" {
		req.Header.Set("x-api-key", apiKey)
	}

	resp, err := l.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("flow request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read flow response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("flow api error (status %d): %s", resp.StatusCode, string(body))
	}

	return extractAgentNodeIDs(body)
}

// extractAgentNodeIDs parses a Langflow flow definition and returns the IDs of
// nodes whose template exposes a "system_prompt" field — the agent nodes we can
// override per request. Shape (GET /api/v1/flows/{id}):
//
//	{ "data": { "nodes": [ { "id": "...",
//	    "data": { "node": { "template": { "system_prompt": {...} } } } } ] } }
func extractAgentNodeIDs(flowJSON []byte) ([]string, error) {
	var flow struct {
		Data struct {
			Nodes []struct {
				ID   string `json:"id"`
				Data struct {
					Node struct {
						Template map[string]json.RawMessage `json:"template"`
					} `json:"node"`
				} `json:"data"`
			} `json:"nodes"`
		} `json:"data"`
	}
	if err := json.Unmarshal(flowJSON, &flow); err != nil {
		return nil, fmt.Errorf("unmarshal flow definition: %w", err)
	}

	var ids []string
	for _, n := range flow.Data.Nodes {
		if n.ID == "" {
			continue
		}
		if _, ok := n.Data.Node.Template["system_prompt"]; ok {
			ids = append(ids, n.ID)
		}
	}
	return ids, nil
}

// buildHistoryFilter returns the metadata filter for the AstraDB
// HistoryRetriever. Scope:
//
//   - metadata.zalo_user_id == <uid>
//       Strict per-user isolation, no cross-user leak.
//
//   - metadata.is_disambiguation != true
//       Excludes ephemeral option-list pushes (see engine.ChatMessage
//       IsDisambiguation). Two reasons:
//         1. Cross-context within a session: if user asks Q1 then Q2 and
//            both end with "1./2./3." option lists, embedding "1" matches
//            both equally — leaving them in the recall pool would poison
//            the prompt with stale options. They're still retrievable via
//            Memory-zEYL8 (sequential, time-ordered) for the immediate
//            follow-up, which is the only place they're useful.
//         2. Long-term recall stays meaningful: only real Q&A turns
//            survive into semantic memory.
//
// Note: no `session_id` filter — that would kill cross-session memory
// ("the customer from last week asked about FF901..."). Sequential
// short-term memory is the job of Memory-zEYL8, not this retriever.
//
// Dotted keys (`metadata.<field>`) because Langflow's AstraDB Vectorstore
// nests custom fields inside the `metadata` sub-document; the same shape
// is mirrored by engine.SaveChatMessage.
func buildHistoryFilter(zaloUserID string) map[string]interface{} {
	filter := map[string]interface{}{
		"metadata.is_disambiguation": map[string]interface{}{"$ne": true},
	}
	if zaloUserID != "" {
		filter["metadata.zalo_user_id"] = zaloUserID
	}
	return filter
}

// RunFlow sends a message to a Langflow flow using global config.
func (l *LangflowClient) RunFlow(ctx context.Context, sessionID, zaloUserID, message string) (string, error) {
	return l.RunFlowWithOverrides(ctx, sessionID, zaloUserID, message, l.cfg.LangflowAPIURL, l.cfg.LangflowAPIKey, l.cfg.LangflowFlowID, "")
}

// RunFlowWithOverrides allows passing specific API URL, Key, and Flow ID.
// systemPrompt, when non-empty, overrides the agent's SYSTEM_PROMPT global
// variable for this request via a tweak; empty means "use the Langflow default".
func (l *LangflowClient) RunFlowWithOverrides(ctx context.Context, sessionID, zaloUserID, message, apiURL, apiKey, flowID, systemPrompt string) (string, error) {
	if apiURL == "" || flowID == "" {
		return "", fmt.Errorf("langflow integration is not configured")
	}

	url := fmt.Sprintf("%s/api/v1/run/%s", apiURL, flowID)

	tweaks := map[string]interface{}{
		// Pass sessionID to memory components if they expose session_id tweak
		"session_id": sessionID,
	}

	if zaloUserID != "" {
		// HistoryRetriever scope is per-user only — see buildHistoryFilter
		// for the rationale (cross-session recall preserved; option-list
		// bleed killed via the is_disambiguation exclusion). Sent under both
		// search_filter and advanced_search_filter so it works regardless of
		// which key the installed Langflow AstraDB component version reads.
		historyFilter := buildHistoryFilter(zaloUserID)
		tweaks["AstraDB-HistoryRetriever"] = map[string]interface{}{
			"search_filter":          historyFilter,
			"advanced_search_filter": historyFilter,
		}
		// Tag every chat document with zalo_user_id at ingest time so the
		// filter above can isolate semantic memory per user.
		tweaks["MsgToDoc-User"] = map[string]interface{}{
			"zalo_user_id": zaloUserID,
		}
		tweaks["MsgToDoc-Assistant"] = map[string]interface{}{
			"zalo_user_id": zaloUserID,
		}
	}

	l.applySystemPromptTweak(ctx, tweaks, apiURL, apiKey, flowID, systemPrompt)

	payload := map[string]interface{}{
		"input_value": message,
		"input_type":  "chat",
		"output_type": "chat",
		"session_id":  sessionID,
		"tweaks":      tweaks,
	}

	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal langflow payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(bodyBytes))
	if err != nil {
		return "", fmt.Errorf("create langflow request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
		req.Header.Set("x-api-key", apiKey)
	}

	resp, err := l.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("langflow request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read langflow response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("langflow api error (status %d): %s", resp.StatusCode, string(respBody))
	}

	var result map[string]interface{}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", fmt.Errorf("unmarshal langflow response: %w", err)
	}

	// Langflow response structure is typically nested:
	// outputs -> [0] -> outputs -> [0] -> results -> message -> text
	outputs, ok := result["outputs"].([]interface{})
	if !ok || len(outputs) == 0 {
		return "", fmt.Errorf("langflow response missing outputs")
	}

	firstOutput, ok := outputs[0].(map[string]interface{})
	if !ok {
		return "", fmt.Errorf("langflow response invalid output format")
	}

	innerOutputs, ok := firstOutput["outputs"].([]interface{})
	if !ok || len(innerOutputs) == 0 {
		return "", fmt.Errorf("langflow response missing inner outputs")
	}

	innerFirst, ok := innerOutputs[0].(map[string]interface{})
	if !ok {
		return "", fmt.Errorf("langflow response invalid inner output format")
	}

	results, ok := innerFirst["results"].(map[string]interface{})
	if !ok {
		// Sometimes Langflow returns text directly under 'outputs[0].outputs[0].text' depending on the version
		if text, ok := innerFirst["text"].(string); ok {
			return text, nil
		}
		// Also try messages
		if msgs, ok := innerFirst["messages"].([]interface{}); ok && len(msgs) > 0 {
			if msgObj, ok := msgs[0].(map[string]interface{}); ok {
				if text, ok := msgObj["message"].(string); ok {
					return text, nil
				}
				if text, ok := msgObj["text"].(string); ok {
					return text, nil
				}
			}
		}

		return "", fmt.Errorf("langflow response missing results: %v", innerFirst)
	}

	msgObj, ok := results["message"].(map[string]interface{})
	if !ok {
		// Depending on component, it might just be 'text'
		if textObj, ok := results["text"].(string); ok {
			return textObj, nil
		}
		return "", fmt.Errorf("langflow response missing message in results")
	}

	text, ok := msgObj["text"].(string)
	if !ok {
		return "", fmt.Errorf("langflow response missing text in message")
	}

	return text, nil
}

// LangflowResult carries the chatbot reply text plus best-effort token usage
// parsed from the Langflow response. HasUsage is true only when real usage
// figures were found; callers should fall back to estimation otherwise.
type LangflowResult struct {
	Text         string
	InputTokens  int
	OutputTokens int
	Model        string
	Provider     string
	HasUsage     bool
}

// RunFlowWithCustomer keeps the original string-only contract for callers that
// don't need usage telemetry; it delegates to RunFlowWithCustomerUsage.
func (l *LangflowClient) RunFlowWithCustomer(ctx context.Context, sessionID, zaloUserID, message, apiURL, apiKey, flowID, customerCode, permissionToken, systemPrompt string) (string, error) {
	res, err := l.RunFlowWithCustomerUsage(ctx, sessionID, zaloUserID, message, apiURL, apiKey, flowID, customerCode, permissionToken, systemPrompt)
	return res.Text, err
}

// RunFlowWithCustomerUsage allows passing specific API URL, Key, Flow ID, Customer Code, and Permission Token.
// systemPrompt, when non-empty, overrides the agent's SYSTEM_PROMPT global
// variable for this request via a tweak; empty means "use the Langflow default".
// It additionally parses token usage from the Langflow response when present.
func (l *LangflowClient) RunFlowWithCustomerUsage(ctx context.Context, sessionID, zaloUserID, message, apiURL, apiKey, flowID, customerCode, permissionToken, systemPrompt string) (LangflowResult, error) {
	if apiURL == "" || flowID == "" {
		return LangflowResult{}, fmt.Errorf("langflow integration is not configured")
	}

	url := fmt.Sprintf("%s/api/v1/run/%s", apiURL, flowID)

	tweaks := map[string]interface{}{
		"session_id": sessionID,
	}

	if permissionToken != "" {
		tweaks["permission_token"] = permissionToken
	}

	customComponentTweaks := map[string]interface{}{}

	if permissionToken != "" {
		customComponentTweaks["permission_token"] = permissionToken
	}

	if zaloUserID != "" {
		tweaks["zalo_user_id"] = zaloUserID
		// HistoryRetriever scope is per-user only (no session_id) — see
		// buildHistoryFilter for the rationale. Cross-session recall is
		// preserved; option-list bleed is killed by the is_disambiguation
		// exclusion in the filter.
		historyFilter := buildHistoryFilter(zaloUserID)
		tweaks["AstraDB-HistoryRetriever"] = map[string]interface{}{
			"search_filter":          historyFilter,
			"advanced_search_filter": historyFilter,
		}
		// Tag every chat document with zalo_user_id at ingest time so the
		// filter above can isolate semantic memory per user.
		tweaks["MsgToDoc-User"] = map[string]interface{}{
			"zalo_user_id": zaloUserID,
		}
		tweaks["MsgToDoc-Assistant"] = map[string]interface{}{
			"zalo_user_id": zaloUserID,
		}
		customComponentTweaks["zalo_user_id"] = zaloUserID
	}

	if customerCode != "" {
		tweaks["customer_code"] = customerCode
		customComponentTweaks["customer_code"] = customerCode
	}

	if len(customComponentTweaks) > 0 {
		tweaks["CustomComponent"] = customComponentTweaks
	}

	l.applySystemPromptTweak(ctx, tweaks, apiURL, apiKey, flowID, systemPrompt)

	payload := map[string]interface{}{
		"input_value": message,
		"input_type":  "chat",
		"output_type": "chat",
		"session_id":  sessionID,
		"tweaks":      tweaks,
	}

	return l.sendAndParse(ctx, url, apiKey, payload)
}

// sendAndParse marshals the payload, performs the Langflow HTTP call, and parses
// the reply text plus best-effort token usage from the response.
func (l *LangflowClient) sendAndParse(ctx context.Context, url, apiKey string, payload map[string]interface{}) (LangflowResult, error) {
	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return LangflowResult{}, fmt.Errorf("marshal langflow payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(bodyBytes))
	if err != nil {
		return LangflowResult{}, fmt.Errorf("create langflow request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
		req.Header.Set("x-api-key", apiKey)
	}

	resp, err := l.client.Do(req)
	if err != nil {
		return LangflowResult{}, fmt.Errorf("langflow request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return LangflowResult{}, fmt.Errorf("read langflow response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return LangflowResult{}, fmt.Errorf("langflow api error (status %d): %s", resp.StatusCode, string(respBody))
	}

	return parseLangflowResult(respBody)
}

// parseLangflowResult extracts the reply text from the nested Langflow response
// shape and, on a best-effort basis, any token usage metadata it carries.
func parseLangflowResult(respBody []byte) (LangflowResult, error) {
	var result map[string]interface{}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return LangflowResult{}, fmt.Errorf("unmarshal langflow response: %w", err)
	}

	res := LangflowResult{}
	// Best-effort: scan the whole response for token usage metadata, which
	// different Langflow component versions place in different spots.
	if in, out, model, found := findTokenUsage(result); found {
		res.InputTokens, res.OutputTokens, res.Model, res.HasUsage = in, out, model, true
	}

	outputs, ok := result["outputs"].([]interface{})
	if !ok || len(outputs) == 0 {
		return res, fmt.Errorf("langflow response missing outputs")
	}

	firstOutput, ok := outputs[0].(map[string]interface{})
	if !ok {
		return res, fmt.Errorf("langflow response invalid output format")
	}

	innerOutputs, ok := firstOutput["outputs"].([]interface{})
	if !ok || len(innerOutputs) == 0 {
		return res, fmt.Errorf("langflow response missing inner outputs")
	}

	innerFirst, ok := innerOutputs[0].(map[string]interface{})
	if !ok {
		return res, fmt.Errorf("langflow response invalid inner output format")
	}

	results, ok := innerFirst["results"].(map[string]interface{})
	if !ok {
		if text, ok := innerFirst["text"].(string); ok {
			res.Text = text
			return res, nil
		}
		if msgs, ok := innerFirst["messages"].([]interface{}); ok && len(msgs) > 0 {
			if msgObj, ok := msgs[0].(map[string]interface{}); ok {
				if text, ok := msgObj["message"].(string); ok {
					res.Text = text
					return res, nil
				}
				if text, ok := msgObj["text"].(string); ok {
					res.Text = text
					return res, nil
				}
			}
		}
		return res, fmt.Errorf("langflow response missing results: %v", innerFirst)
	}

	msgObj, ok := results["message"].(map[string]interface{})
	if !ok {
		if textObj, ok := results["text"].(string); ok {
			res.Text = textObj
			return res, nil
		}
		return res, fmt.Errorf("langflow response missing message in results")
	}

	text, ok := msgObj["text"].(string)
	if !ok {
		return res, fmt.Errorf("langflow response missing text in message")
	}

	res.Text = text
	return res, nil
}

// findTokenUsage walks an arbitrary decoded JSON value looking for a map that
// carries LLM token usage. It accepts the common key spellings used by OpenAI,
// Anthropic and Langflow components, returning found=true on the first match
// that yields a non-zero input or output token count.
func findTokenUsage(v interface{}) (inTok, outTok int, model string, found bool) {
	switch node := v.(type) {
	case map[string]interface{}:
		in := firstIntKey(node, "input_tokens", "prompt_tokens")
		out := firstIntKey(node, "output_tokens", "completion_tokens")
		if in > 0 || out > 0 {
			m, _ := node["model"].(string)
			if m == "" {
				m, _ = node["model_name"].(string)
			}
			return in, out, m, true
		}
		for _, child := range node {
			if i, o, m, ok := findTokenUsage(child); ok {
				return i, o, m, ok
			}
		}
	case []interface{}:
		for _, child := range node {
			if i, o, m, ok := findTokenUsage(child); ok {
				return i, o, m, ok
			}
		}
	}
	return 0, 0, "", false
}

// firstIntKey returns the first integer value found among the given keys.
func firstIntKey(m map[string]interface{}, keys ...string) int {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			switch n := v.(type) {
			case float64:
				return int(n)
			case int:
				return n
			}
		}
	}
	return 0
}
