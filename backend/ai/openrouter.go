package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

// OpenRouter (openrouter.ai) serves many vendors' models, some of them free,
// behind an OpenAI-compatible Chat Completions API. It reuses
// OpenAIProvider: same request and response shapes, Chat Completions only.
const (
	OpenRouterBaseURL = "https://openrouter.ai/api/v1"
	// OpenRouterDefaultModel is used when no model is set. Free models come
	// and go: the Settings page lists the current free ones.
	OpenRouterDefaultModel = "meta-llama/llama-3.3-70b-instruct:free"
)

// NewOpenRouterProvider builds an OpenRouter client. model is an OpenRouter
// model id such as "deepseek/deepseek-chat-v3-0324:free" and is sent as is.
func NewOpenRouterProvider(apiKey, model, baseURL string) *OpenAIProvider {
	if strings.TrimSpace(model) == "" {
		model = OpenRouterDefaultModel
	}
	if strings.TrimSpace(baseURL) == "" {
		baseURL = OpenRouterBaseURL
	}
	return &OpenAIProvider{
		apiKey:   apiKey,
		model:    strings.TrimSpace(model),
		baseURL:  strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		name:     "openrouter",
		chatOnly: true,
		// Optional OpenRouter attribution headers.
		headers: map[string]string{"X-Title": "CQA"},
	}
}

// Providers lists the supported provider names.
var Providers = []string{"claude", "gemini", "openai", "openrouter", "9router"}

// ValidProvider reports whether name is a supported provider.
func ValidProvider(name string) bool {
	for _, p := range Providers {
		if p == name {
			return true
		}
	}
	return false
}

// DefaultModel is the model used for a provider when none is set.
func DefaultModel(provider string) string {
	switch provider {
	case "gemini":
		return "gemini-2.0-flash"
	case "openai":
		return "gpt-5-mini"
	case "openrouter":
		return OpenRouterDefaultModel
	case "9router":
		return ""
	}
	return "claude-haiku-4-5"
}

// NewProvider builds the client for a provider name.
func NewProvider(provider, apiKey, model, baseURL string, maxTokens int) (AIProvider, error) {
	switch provider {
	case "claude":
		return NewClaudeProvider(apiKey, model, maxTokens, baseURL), nil
	case "gemini":
		return NewGeminiProvider(apiKey, model, baseURL), nil
	case "openai":
		return NewOpenAIProvider(apiKey, model, baseURL), nil
	case "openrouter":
		return NewOpenRouterProvider(apiKey, model, baseURL), nil
	case "9router":
		return newNineRouterProvider(apiKey, model, baseURL)
	}
	return nil, fmt.Errorf("unsupported AI provider: %s", provider)
}

// OpenRouterModel is one model in OpenRouter's catalogue.
type OpenRouterModel struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	ContextLength int    `json:"context_length"`
	// Vision: the model reads images (needed to read customers' photos).
	Vision bool `json:"vision"`
	// JSONMode: the model accepts response_format (JSON output).
	JSONMode bool `json:"json_mode"`
}

var (
	openRouterModelsMu    sync.Mutex
	openRouterModelsCache []OpenRouterModel
	openRouterModelsAt    time.Time
	openRouterModelsURL   = OpenRouterBaseURL + "/models"
)

const openRouterModelsTTL = 10 * time.Minute

// ResetOpenRouterFreeModelsCache makes the next OpenRouterFreeModels call
// fetch the catalogue again (the "refresh model lists" button).
func ResetOpenRouterFreeModelsCache() {
	openRouterModelsMu.Lock()
	defer openRouterModelsMu.Unlock()
	openRouterModelsCache = nil
	openRouterModelsAt = time.Time{}
}

// OpenRouterFreeModels lists OpenRouter's free models (cached 10 minutes).
// The catalogue is public: no API key is needed.
func OpenRouterFreeModels(ctx context.Context) ([]OpenRouterModel, error) {
	openRouterModelsMu.Lock()
	defer openRouterModelsMu.Unlock()
	if openRouterModelsCache != nil && time.Since(openRouterModelsAt) < openRouterModelsTTL {
		return openRouterModelsCache, nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, openRouterModelsURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := openAIHTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("openrouter models request failed: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 20<<20))
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("openrouter models error %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	models, err := ParseOpenRouterFreeModels(body)
	if err != nil {
		return nil, err
	}
	openRouterModelsCache, openRouterModelsAt = models, time.Now()
	return models, nil
}

// ParseOpenRouterFreeModels keeps the free models of a /models response,
// sorted by name.
func ParseOpenRouterFreeModels(body []byte) ([]OpenRouterModel, error) {
	var parsed struct {
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
			SupportedParameters []string `json:"supported_parameters"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("openrouter models parse failed: %w", err)
	}
	isZero := func(p string) bool {
		p = strings.TrimSpace(p)
		return p == "0" || p == "0.0" || p == "0.00"
	}
	out := []OpenRouterModel{}
	for _, m := range parsed.Data {
		free := strings.HasSuffix(m.ID, ":free") || (isZero(m.Pricing.Prompt) && isZero(m.Pricing.Completion))
		if !free || m.ID == "" || m.ID == "openrouter/auto" {
			continue
		}
		om := OpenRouterModel{ID: m.ID, Name: m.Name, ContextLength: m.ContextLength}
		for _, mod := range m.Architecture.InputModalities {
			if mod == "image" {
				om.Vision = true
			}
		}
		for _, p := range m.SupportedParameters {
			if p == "response_format" || p == "structured_outputs" {
				om.JSONMode = true
			}
		}
		if om.Name == "" {
			om.Name = om.ID
		}
		out = append(out, om)
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name) })
	return out, nil
}
