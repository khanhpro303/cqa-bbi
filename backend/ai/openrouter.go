package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

// openRouterBaseURL is OpenRouter's OpenAI-compatible API root.
const openRouterBaseURL = "https://openrouter.ai/api/v1"

// NewOpenRouterProvider builds a client for OpenRouter, which serves many
// vendors' models (including free ones, ids ending in ":free") behind the
// OpenAI Chat Completions API. Model ids are passed through unchanged.
func NewOpenRouterProvider(apiKey, model, baseURL string) *OpenAIProvider {
	if strings.TrimSpace(model) == "" {
		model = DefaultModel(ProviderOpenRouter)
	}
	if strings.TrimSpace(baseURL) == "" {
		baseURL = openRouterBaseURL
	}
	return &OpenAIProvider{
		apiKey:   apiKey,
		model:    model,
		baseURL:  strings.TrimRight(baseURL, "/"),
		name:     ProviderOpenRouter,
		chatOnly: true,
	}
}

// OpenRouterModel is one model offered by OpenRouter.
type OpenRouterModel struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	ContextLength int    `json:"context_length"`
	// Vision: the model accepts images (needed to read customer photos).
	Vision bool `json:"vision"`
}

const openRouterModelsTimeout = 15 * time.Second

var openRouterModelsURL = openRouterBaseURL + "/models"

// ListOpenRouterFreeModels returns OpenRouter's models that cost nothing per
// token. The model list is public, so no API key is needed.
func ListOpenRouterFreeModels(ctx context.Context) ([]OpenRouterModel, error) {
	ctx, cancel := context.WithTimeout(ctx, openRouterModelsTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, openRouterModelsURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := openAIHTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("openrouter models request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("openrouter models api error %d", resp.StatusCode)
	}
	return parseOpenRouterFreeModels(io.LimitReader(resp.Body, 16<<20))
}

func parseOpenRouterFreeModels(r io.Reader) ([]OpenRouterModel, error) {
	var payload struct {
		Data []struct {
			ID            string `json:"id"`
			Name          string `json:"name"`
			ContextLength int    `json:"context_length"`
			Pricing       struct {
				Prompt     string `json:"prompt"`
				Completion string `json:"completion"`
			} `json:"pricing"`
			Architecture struct {
				InputModalities []string `json:"input_modalities"`
			} `json:"architecture"`
		} `json:"data"`
	}
	if err := json.NewDecoder(r).Decode(&payload); err != nil {
		return nil, fmt.Errorf("openrouter models response is not valid JSON: %w", err)
	}
	out := make([]OpenRouterModel, 0)
	for _, m := range payload.Data {
		if m.ID == "" || !isZeroPrice(m.Pricing.Prompt) || !isZeroPrice(m.Pricing.Completion) {
			continue
		}
		vision := false
		for _, mod := range m.Architecture.InputModalities {
			if mod == "image" {
				vision = true
			}
		}
		out = append(out, OpenRouterModel{ID: m.ID, Name: m.Name, ContextLength: m.ContextLength, Vision: vision})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func isZeroPrice(s string) bool {
	v, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	return err == nil && v == 0
}
