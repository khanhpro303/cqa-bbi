package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// DefaultEmbeddingModel must match the model configured on the Langflow
// EmbeddingModel-OnvoJ node in BBI_RAG_Bot_Ext.json so vectors produced by Go
// land in the same space as vectors produced by the Langflow flow.
const DefaultEmbeddingModel = "text-embedding-ada-002"

const openAIEmbeddingsPath = "/v1/embeddings"

// EmbeddingsClient calls OpenAI-compatible /v1/embeddings endpoints.
//
// It is intentionally tiny: one method, no provider switching. Other providers
// can be added later behind an interface in caller code.
type EmbeddingsClient struct {
	apiKey  string
	model   string
	baseURL string
	http    *http.Client
}

// NewEmbeddingsClient returns a client targeting OpenAI by default. Pass an
// empty baseURL to use https://api.openai.com; pass an empty model to use
// DefaultEmbeddingModel.
func NewEmbeddingsClient(apiKey, model, baseURL string) *EmbeddingsClient {
	if model == "" {
		model = DefaultEmbeddingModel
	}
	if baseURL == "" {
		baseURL = "https://api.openai.com"
	}
	return &EmbeddingsClient{
		apiKey:  apiKey,
		model:   model,
		baseURL: strings.TrimRight(baseURL, "/"),
		http:    NewHTTPClientWithTimeout(),
	}
}

type embeddingsRequest struct {
	Model string `json:"model"`
	Input string `json:"input"`
}

type embeddingsResponse struct {
	Data []struct {
		Embedding []float32 `json:"embedding"`
	} `json:"data"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error,omitempty"`
}

// Embed returns the embedding vector for text. An empty text or missing api
// key returns (nil, error) — callers should treat embed failures as degraded
// mode and still persist the row without $vector.
func (c *EmbeddingsClient) Embed(ctx context.Context, text string) ([]float32, error) {
	if c == nil {
		return nil, fmt.Errorf("embed text: nil client")
	}
	if c.apiKey == "" {
		return nil, fmt.Errorf("embed text: missing api key")
	}
	if strings.TrimSpace(text) == "" {
		return nil, fmt.Errorf("embed text: empty input")
	}

	body, err := json.Marshal(embeddingsRequest{Model: c.model, Input: text})
	if err != nil {
		return nil, fmt.Errorf("embed text: marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+openAIEmbeddingsPath, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("embed text: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("embed text: do request: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("embed text: read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("embed text: status %d: %s", resp.StatusCode, truncateForLog(raw))
	}

	var parsed embeddingsResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, fmt.Errorf("embed text: decode response: %w", err)
	}
	if parsed.Error != nil {
		return nil, fmt.Errorf("embed text: api error %s: %s", parsed.Error.Type, parsed.Error.Message)
	}
	if len(parsed.Data) == 0 || len(parsed.Data[0].Embedding) == 0 {
		return nil, fmt.Errorf("embed text: empty embedding in response")
	}
	return parsed.Data[0].Embedding, nil
}

// Model returns the model identifier this client was constructed with.
func (c *EmbeddingsClient) Model() string {
	if c == nil {
		return ""
	}
	return c.model
}

func truncateForLog(b []byte) string {
	const max = 200
	if len(b) <= max {
		return string(b)
	}
	return string(b[:max]) + "..."
}
