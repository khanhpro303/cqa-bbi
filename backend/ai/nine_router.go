package ai

import (
	"fmt"
	"net/url"
	"strings"
)

// ValidateNineRouterBaseURL requires an explicit 9router API endpoint.
func ValidateNineRouterBaseURL(baseURL string) error {
	baseURL = strings.TrimSpace(baseURL)
	endpoint, err := url.Parse(baseURL)
	if err != nil || endpoint.Hostname() == "" || (endpoint.Scheme != "http" && endpoint.Scheme != "https") ||
		endpoint.User != nil || endpoint.ForceQuery || endpoint.RawQuery != "" || strings.Contains(baseURL, "#") ||
		!strings.HasSuffix(strings.TrimRight(endpoint.Path, "/"), "/v1") {
		return fmt.Errorf("9router Base URL must be an HTTP(S) API URL ending in /v1")
	}
	return nil
}

func newNineRouterProvider(apiKey, model, baseURL string) (*OpenAIProvider, error) {
	if err := ValidateNineRouterBaseURL(baseURL); err != nil {
		return nil, err
	}
	model = strings.TrimSpace(model)
	if model == "" {
		return nil, fmt.Errorf("9router requires a model ID")
	}
	return &OpenAIProvider{
		apiKey:   apiKey,
		model:    model,
		baseURL:  strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		name:     "9router",
		chatOnly: true,
	}, nil
}
