package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/vietbui/chat-quality-agent/config"
)

// langflowAgentNodeID is the ToolCallingAgent node in BBI_RAG_Bot_Ext.json.
// Tweaking system_prompt on this node overrides the Langflow global variable
// SYSTEM_PROMPT on a per-request basis. Only the flow containing this node
// (the private flow) is affected; Langflow ignores tweaks for unknown node IDs.
const langflowAgentNodeID = "ToolCallingAgent-zznkZ"

// LangflowClient handles communication with the Langflow API.
type LangflowClient struct {
	cfg    *config.Config
	client *http.Client
}

func NewLangflowClient(cfg *config.Config) *LangflowClient {
	return &LangflowClient{
		cfg: cfg,
		client: &http.Client{
			Timeout: 60 * time.Second, // AI operations can take time
		},
	}
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

	if systemPrompt != "" {
		// Override the agent's SYSTEM_PROMPT global variable for this request.
		tweaks[langflowAgentNodeID] = map[string]interface{}{
			"system_prompt": systemPrompt,
		}
	}

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

// RunFlowWithCustomer allows passing specific API URL, Key, Flow ID, Customer Code, and Permission Token.
// systemPrompt, when non-empty, overrides the agent's SYSTEM_PROMPT global
// variable for this request via a tweak; empty means "use the Langflow default".
func (l *LangflowClient) RunFlowWithCustomer(ctx context.Context, sessionID, zaloUserID, message, apiURL, apiKey, flowID, customerCode, permissionToken, systemPrompt string) (string, error) {
	if apiURL == "" || flowID == "" {
		return "", fmt.Errorf("langflow integration is not configured")
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

	if systemPrompt != "" {
		// Override the agent's SYSTEM_PROMPT global variable for this request.
		tweaks[langflowAgentNodeID] = map[string]interface{}{
			"system_prompt": systemPrompt,
		}
	}

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
		if text, ok := innerFirst["text"].(string); ok {
			return text, nil
		}
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
