package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type NineRouterModel struct {
	ID     string `json:"id"`
	Vision bool   `json:"vision,omitempty"`
}

var nineRouterCatalogClient = &http.Client{
	Timeout: 8 * time.Second,
	CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	},
}

// FetchNineRouterModels reads the OpenAI-compatible chat model catalog.
func FetchNineRouterModels(ctx context.Context, baseURL, apiKey string) ([]NineRouterModel, error) {
	if err := ValidateNineRouterBaseURL(baseURL); err != nil {
		return nil, err
	}
	endpoint := strings.TrimRight(strings.TrimSpace(baseURL), "/") + "/models"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Accept", "application/json")
	resp, err := nineRouterCatalogClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("9router model catalog returned HTTP %d", resp.StatusCode)
	}

	const maxCatalogBytes = 2 << 20
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxCatalogBytes+1))
	if err != nil {
		return nil, err
	}
	if len(body) > maxCatalogBytes {
		return nil, fmt.Errorf("9router model catalog exceeds %d bytes", maxCatalogBytes)
	}
	var catalog struct {
		Data []struct {
			ID           string `json:"id"`
			Capabilities struct {
				Vision bool `json:"vision"`
			} `json:"capabilities"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &catalog); err != nil {
		return nil, err
	}
	if catalog.Data == nil {
		return nil, fmt.Errorf("9router model catalog is missing data")
	}
	models := make([]NineRouterModel, 0, len(catalog.Data))
	seen := make(map[string]bool, len(catalog.Data))
	for _, model := range catalog.Data {
		id := strings.TrimSpace(model.ID)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		models = append(models, NineRouterModel{ID: id, Vision: model.Capabilities.Vision})
	}
	return models, nil
}
