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

// RunFlow sends a message to a Langflow flow using global config.
func (l *LangflowClient) RunFlow(ctx context.Context, sessionID, message string) (string, error) {
	return l.RunFlowWithOverrides(ctx, sessionID, message, l.cfg.LangflowAPIURL, l.cfg.LangflowAPIKey, l.cfg.LangflowFlowID)
}

// RunFlowWithOverrides allows passing specific API URL, Key, and Flow ID.
func (l *LangflowClient) RunFlowWithOverrides(ctx context.Context, sessionID, message, apiURL, apiKey, flowID string) (string, error) {
	if apiURL == "" || flowID == "" {
		return "", fmt.Errorf("langflow integration is not configured")
	}

	url := fmt.Sprintf("%s/api/v1/run/%s", apiURL, flowID)

	payload := map[string]interface{}{
		"input_value": message,
		"input_type":  "chat",
		"output_type": "chat",
		"tweaks": map[string]interface{}{
			// Pass sessionID to memory components if they expose session_id tweak
			"session_id": sessionID,
		},
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
