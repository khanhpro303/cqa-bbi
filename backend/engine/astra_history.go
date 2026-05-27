package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/vietbui/chat-quality-agent/ai"
)

// SaveChatMessage persists a chat message into the AstraDB conversation
// history collection used by the Langflow flow's AstraDB-HistoryRetriever.
//
// The document shape mirrors what the Langflow MessageToDocument component
// produces (see BBI_RAG_Bot_Ext.json, nodes CustomComponent-sUNVR and
// MessageToDocument-yUtXJ) so rows written from Go are retrievable by the
// same retriever node that reads rows written by Langflow.
//
// If embedder is non-nil the function calls it to compute a $vector field in
// the same embedding space as the EmbeddingModel-OnvoJ node. An embedder
// failure is logged and the row is still inserted (degraded mode) so chat
// history is never silently dropped.
//
// Returns nil silently when apiEndpoint, token, or collection are empty so
// callers in unconfigured environments do not need extra guards.
func SaveChatMessage(ctx context.Context, apiEndpoint, token, keyspace, collection, zaloUserID, sessionID, role, content string, embedder *ai.EmbeddingsClient) error {
	if apiEndpoint == "" || token == "" || collection == "" {
		return nil
	}
	if keyspace == "" {
		keyspace = "default_keyspace"
	}

	url := fmt.Sprintf("%s/api/json/v1/%s/%s", apiEndpoint, keyspace, collection)

	document := map[string]interface{}{
		"zalo_user_id": zaloUserID,
		"session_id":   sessionID,
		"role":         role,
		"content":      content,
		"text":         content,
		"created_at":   time.Now().Unix(),
	}

	if embedder != nil {
		vec, err := embedder.Embed(ctx, content)
		if err != nil {
			log.Printf("[astra] embed failed for session %s role %s (degraded write, no $vector): %v", sessionID, role, err)
		} else {
			document["$vector"] = vec
		}
	}

	payload := map[string]interface{}{
		"insertOne": map[string]interface{}{
			"document": document,
		},
	}

	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal astra db payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewBuffer(bodyBytes))
	if err != nil {
		return fmt.Errorf("create astra db request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Token", token)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("astra db request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return fmt.Errorf("astra db api error (status %d)", resp.StatusCode)
	}
	return nil
}
