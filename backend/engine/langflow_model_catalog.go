package engine

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"
)

// Use Langflow's own refreshed catalog rather than a list bundled with CQA.
// Restrict discovery to stable general-purpose GPT models for the Agent's
// ChatOpenAI integration, which needs Chat Completions and tool calling.
var langflowGPTModelID = regexp.MustCompile(`^gpt-[0-9]+(\.[0-9]+)?(-[a-z][a-z0-9]*)?$`)

func langflowOpenAIRequiresDifferentToolMode(name string) bool {
	switch name {
	// These need reasoning_effort=none for Chat Completions with tools,
	// which Langflow's native Agent does not set.
	case "gpt-6-sol", "gpt-6-luna":
		return true
	// Use the Responses API for these models.
	case "gpt-5.4-pro", "gpt-5.5-pro":
		return true
	}
	return false
}

func (agent langflowAgentModel) supportsLiveOpenAI() bool {
	if !strings.EqualFold(agent.selectedFrom, "OpenAI") ||
		!strings.HasPrefix(agent.nodeID, "ToolCallingAgent-") ||
		agent.modelInput["_input_type"] != "ModelInput" {
		return false
	}
	for field, value := range mapValue(agent.modelInput["filters"]) {
		if field != "tool_calling" || value != true {
			return false
		}
	}
	return true
}

func readLangflowOpenAICatalog(ctx context.Context, apiURL, apiKey, provider string) []LangflowModelOption {
	if !strings.EqualFold(provider, "OpenAI") || apiKey == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	const endpoint = "/api/v1/models?provider=OpenAI&model_type=llm&tool_calling=true&purpose=configure"
	candidates := parseLangflowOpenAICatalog(langflowCatalogGET(ctx, apiURL, apiKey, endpoint))
	if len(candidates) == 0 {
		return nil
	}
	// The catalog also lists models the account has disabled. Only advertise
	// new options Langflow currently enables for this API key's user.
	var statuses struct {
		Enabled map[string]map[string]bool `json:"enabled_models"`
	}
	body := langflowCatalogGET(ctx, apiURL, apiKey, "/api/v1/models/enabled_models?purpose=configure")
	if json.Unmarshal(body, &statuses) != nil {
		return nil
	}
	enabled := statuses.Enabled["OpenAI"]
	options := make([]LangflowModelOption, 0, len(candidates))
	for _, option := range candidates {
		if enabled[option.Name] {
			options = append(options, option)
		}
	}
	return options
}

func langflowCatalogGET(ctx context.Context, apiURL, apiKey, path string) []byte {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(apiURL, "/")+path, nil)
	if err != nil {
		return nil
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("x-api-key", apiKey)
	resp, err := langflowFlowHTTPClient.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxLangflowFlowDefinitionSize+1))
	if err != nil || len(body) > maxLangflowFlowDefinitionSize {
		return nil
	}
	return body
}

func parseLangflowOpenAICatalog(body []byte) []LangflowModelOption {
	var providers []struct {
		Provider string `json:"provider"`
		Icon     string `json:"icon"`
		Models   []struct {
			Name     string `json:"model_name"`
			Metadata struct {
				ModelType    string `json:"model_type"`
				ToolCalling  bool   `json:"tool_calling"`
				Reasoning    bool   `json:"reasoning"`
				Deprecated   bool   `json:"deprecated"`
				NotSupported bool   `json:"not_supported"`
				Preview      bool   `json:"preview"`
				Search       bool   `json:"search"`
				Context      int    `json:"context_window"`
				Created      int64  `json:"created"`
			} `json:"metadata"`
		} `json:"models"`
	}
	if err := json.Unmarshal(body, &providers); err != nil {
		return nil
	}
	type candidate struct {
		option  LangflowModelOption
		created int64
	}
	var candidates []candidate
	for _, group := range providers {
		if !strings.EqualFold(group.Provider, "OpenAI") {
			continue
		}
		for _, model := range group.Models {
			info := model.Metadata
			if !info.ToolCalling || info.ModelType != "llm" || info.Deprecated || info.NotSupported || info.Preview || info.Search ||
				!langflowGPTModelID.MatchString(model.Name) || langflowOpenAIRequiresDifferentToolMode(model.Name) {
				continue
			}
			switch model.Name[strings.LastIndex(model.Name, "-")+1:] {
			case "pro", "cyber", "rosalind":
				continue
			}
			// Langflow 1.10 uses reasoning_models to omit temperature. Only
			// copy capability fields from the catalog, never client wiring.
			metadata := map[string]any{
				"model_class": "ChatOpenAI", "model_name_param": "model", "api_key_param": "api_key",
				"tool_calling": true, "reasoning": info.Reasoning,
			}
			if info.Reasoning {
				metadata["reasoning_models"] = []string{model.Name}
			}
			if info.Context > 0 {
				metadata["context_length"] = info.Context
			}
			candidates = append(candidates, candidate{LangflowModelOption{
				Name: model.Name, Provider: "OpenAI", Icon: group.Icon, Category: "OpenAI", Metadata: metadata,
			}, info.Created})
		}
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		return candidates[i].created > candidates[j].created
	})
	options := make([]LangflowModelOption, 0, len(candidates))
	seen := make(map[string]bool, len(candidates))
	for _, candidate := range candidates {
		if !seen[candidate.option.Name] {
			options = append(options, candidate.option)
			seen[candidate.option.Name] = true
		}
	}
	return options
}

func (option LangflowModelOption) graphValue(provider string) map[string]any {
	return map[string]any{
		"name": option.Name, "provider": provider, "icon": option.Icon,
		"category": option.Category, "metadata": option.Metadata,
	}
}

func (flow *LangflowFlowModel) addOpenAICatalog(catalog []LangflowModelOption) {
	flow.Options = slices.DeleteFunc(flow.Options, func(option LangflowModelOption) bool {
		return strings.EqualFold(option.Provider, "OpenAI") && langflowOpenAIRequiresDifferentToolMode(option.Name)
	})
	existing := make(map[string]bool, len(flow.Options))
	for _, option := range flow.Options {
		existing[modelOptionKey(option.Provider, option.Name)] = true
	}
	for _, option := range catalog {
		if !existing[modelOptionKey(option.Provider, option.Name)] {
			option.Provider = flow.Provider
			flow.Options = append(flow.Options, option)
		}
	}
}
