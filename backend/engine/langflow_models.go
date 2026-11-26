package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

const maxLangflowFlowDefinitionSize = 8 << 20

var langflowFlowHTTPClient = &http.Client{
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

type LangflowModelOption struct {
	Name     string         `json:"name"`
	Provider string         `json:"provider"`
	Icon     string         `json:"icon,omitempty"`
	Category string         `json:"category,omitempty"`
	Metadata map[string]any `json:"metadata,omitempty"`
}

type LangflowFlowModel struct {
	FlowID       string                `json:"flow_id"`
	Name         string                `json:"name"`
	Model        string                `json:"model"`
	Provider     string                `json:"provider"`
	Metadata     map[string]any        `json:"metadata,omitempty"`
	Options      []LangflowModelOption `json:"options"`
	AgentNodeIDs []string              `json:"agent_node_ids,omitempty"`
	liveOpenAI   bool
}

type LangflowFlowModelError struct {
	Code    string
	Message string
}

func (e *LangflowFlowModelError) Error() string { return e.Message }

type langflowAgentModel struct {
	nodeID       string
	modelInput   map[string]any
	selected     map[string]any
	options      []map[string]any
	selectedName string
	selectedFrom string
}

// ParseLangflowFlowModel extracts the source-of-truth model selection from a
// Langflow GET /api/v1/flows/{id} response.
func ParseLangflowFlowModel(flowJSON []byte) (LangflowFlowModel, error) {
	document, agents, err := parseLangflowAgentModels(flowJSON)
	if err != nil {
		return LangflowFlowModel{}, err
	}
	result := LangflowFlowModel{
		FlowID:     stringValue(document["id"]),
		Name:       stringValue(document["name"]),
		Model:      agents[0].selectedName,
		Provider:   agents[0].selectedFrom,
		Metadata:   mapValue(agents[0].selected["metadata"]),
		liveOpenAI: true,
	}
	for _, agent := range agents {
		if agent.selectedName != result.Model || agent.selectedFrom != result.Provider {
			return LangflowFlowModel{}, &LangflowFlowModelError{Code: "inconsistent_agent_models", Message: "Các agent node trong flow đang dùng model khác nhau; hãy đồng nhất trong Langflow trước."}
		}
		result.AgentNodeIDs = append(result.AgentNodeIDs, agent.nodeID)
		result.liveOpenAI = result.liveOpenAI && agent.supportsLiveOpenAI()
	}
	result.Options = commonLangflowModelOptions(agents)
	return result, nil
}

func ReadLangflowFlowModel(ctx context.Context, apiURL, apiKey, flowID string) (LangflowFlowModel, error) {
	body, err := readLangflowFlow(ctx, langflowFlowHTTPClient, apiURL, apiKey, flowID)
	if err != nil {
		return LangflowFlowModel{}, err
	}
	model, err := ParseLangflowFlowModel(body)
	if err != nil {
		return LangflowFlowModel{}, err
	}
	if model.FlowID == "" {
		model.FlowID = flowID
	}
	if model.liveOpenAI {
		model.addOpenAICatalog(readLangflowOpenAICatalog(ctx, apiURL, apiKey, model.Provider))
	}
	return model, nil
}

// SyncLangflowFlowModel writes the exact option object advertised by Langflow,
// then reads the graph back to verify the source-of-truth value.
func SyncLangflowFlowModel(ctx context.Context, apiURL, apiKey, flowID, provider, modelName string) (LangflowFlowModel, error) {
	modelName = strings.TrimSpace(modelName)
	body, err := readLangflowFlow(ctx, langflowFlowHTTPClient, apiURL, apiKey, flowID)
	if err != nil {
		return LangflowFlowModel{}, err
	}
	payload, changed, selectedProvider, err := patchLangflowFlowModel(body, provider, modelName, nil)
	var catalog []LangflowModelOption
	if modelErr, ok := IsLangflowFlowModelError(err); ok && modelErr.Code == "model_not_available" {
		catalog = readLangflowOpenAICatalog(ctx, apiURL, apiKey, provider)
		if len(catalog) > 0 {
			payload, changed, selectedProvider, err = patchLangflowFlowModel(body, provider, modelName, catalog)
		}
	}
	if err != nil {
		return LangflowFlowModel{}, err
	}
	if changed {
		var revision struct {
			VersionToken string `json:"version_token"`
		}
		if err := json.Unmarshal(body, &revision); err != nil {
			return LangflowFlowModel{}, fmt.Errorf("read Langflow flow version: %w", err)
		}
		if err := patchLangflowFlow(ctx, langflowFlowHTTPClient, apiURL, apiKey, flowID, revision.VersionToken, payload); err != nil {
			return LangflowFlowModel{}, err
		}
		body, err = readLangflowFlow(ctx, langflowFlowHTTPClient, apiURL, apiKey, flowID)
		if err != nil {
			return LangflowFlowModel{}, fmt.Errorf("read back updated Langflow flow: %w", err)
		}
	}
	result, err := ParseLangflowFlowModel(body)
	if err != nil {
		return LangflowFlowModel{}, err
	}
	if result.FlowID == "" {
		result.FlowID = flowID
	}
	if result.Model != modelName || !strings.EqualFold(result.Provider, selectedProvider) {
		return LangflowFlowModel{}, &LangflowFlowModelError{Code: "model_sync_not_applied", Message: "Model đọc lại từ Langflow khác với lựa chọn đã gửi. Hãy tải lại flow."}
	}
	if result.liveOpenAI {
		result.addOpenAICatalog(catalog)
	}
	return result, nil
}

func patchLangflowFlowModel(flowJSON []byte, provider, modelName string, catalog []LangflowModelOption) ([]byte, bool, string, error) {
	document, agents, err := parseLangflowAgentModels(flowJSON)
	if err != nil {
		return nil, false, "", err
	}
	modelName, provider = strings.TrimSpace(modelName), strings.TrimSpace(provider)
	if modelName == "" {
		return nil, false, "", &LangflowFlowModelError{Code: "model_required", Message: "Model không được để trống."}
	}
	for _, agent := range agents {
		if !agent.supportsLiveOpenAI() {
			catalog = nil
			break
		}
	}
	selectedProvider := ""
	changed := false
	for _, agent := range agents {
		// The ToolCallingAgent has one configured API key. A different vendor
		// requires reconfiguring that credential inside Langflow.
		if provider == "" || !strings.EqualFold(agent.selectedFrom, provider) {
			return nil, false, "", &LangflowFlowModelError{Code: "flow_provider_change_requires_langflow", Message: "Đổi nhà cung cấp cần cấu hình khóa API của agent trong Langflow."}
		}
		if agent.supportsLiveOpenAI() && modelName != agent.selectedName && langflowOpenAIRequiresDifferentToolMode(modelName) {
			return nil, false, "", &LangflowFlowModelError{Code: "model_tool_mode_unsupported", Message: "Model này cần chế độ gọi công cụ khác với cấu hình Agent hiện tại."}
		}
		option, optionProvider, selectErr := selectLangflowModelOption(agent.options, provider, modelName)
		if modelErr, ok := IsLangflowFlowModelError(selectErr); ok && modelErr.Code == "model_not_available" {
			for _, candidate := range catalog {
				if candidate.Name == modelName && strings.EqualFold(candidate.Provider, agent.selectedFrom) {
					option = candidate.graphValue(agent.selectedFrom)
					optionProvider, selectErr = agent.selectedFrom, nil
					break
				}
			}
		}
		if selectErr != nil {
			return nil, false, "", selectErr
		}
		if selectedProvider == "" {
			selectedProvider = optionProvider
		} else if !strings.EqualFold(selectedProvider, optionProvider) {
			return nil, false, "", &LangflowFlowModelError{Code: "provider_mismatch", Message: "Các agent node không quảng bá cùng provider cho model đã chọn."}
		}
		if agent.selectedName != modelName || !strings.EqualFold(agent.selectedFrom, optionProvider) {
			agent.modelInput["value"] = []any{option}
			changed = true
		}
	}
	data, ok := document["data"].(map[string]any)
	if !ok {
		return nil, false, "", invalidLangflowModelGraph("Flow không có graph data hợp lệ.")
	}
	payload, err := json.Marshal(map[string]any{"data": data})
	if err != nil {
		return nil, false, "", fmt.Errorf("encode Langflow model update: %w", err)
	}
	return payload, changed, selectedProvider, nil
}

func parseLangflowAgentModels(flowJSON []byte) (map[string]any, []langflowAgentModel, error) {
	var document map[string]any
	decoder := json.NewDecoder(bytes.NewReader(flowJSON))
	decoder.UseNumber()
	if err := decoder.Decode(&document); err != nil {
		return nil, nil, invalidLangflowModelGraph("Langflow trả về flow JSON không hợp lệ.")
	}
	data, ok := document["data"].(map[string]any)
	if !ok {
		return nil, nil, invalidLangflowModelGraph("Flow không có graph data hợp lệ.")
	}
	nodes, ok := data["nodes"].([]any)
	if !ok {
		return nil, nil, invalidLangflowModelGraph("Flow không có danh sách node hợp lệ.")
	}
	agents := make([]langflowAgentModel, 0, 1)
	linkedModels := langflowLinkedModels(data["edges"])
	for _, rawNode := range nodes {
		node, ok := rawNode.(map[string]any)
		if !ok {
			continue
		}
		nodeData, _ := node["data"].(map[string]any)
		nodeDefinition, _ := nodeData["node"].(map[string]any)
		template, _ := nodeDefinition["template"].(map[string]any)
		if _, isAgent := template["system_prompt"]; !isAgent {
			continue
		}
		modelInput, ok := template["model"].(map[string]any)
		if !ok {
			continue
		}
		nodeID := stringValue(node["id"])
		if linkedModels[nodeID] {
			return nil, nil, &LangflowFlowModelError{Code: "agent_model_linked", Message: "Agent lấy model từ node khác. Hãy chỉnh model tại node nguồn trong Langflow."}
		}
		values, ok := modelInput["value"].([]any)
		if !ok || len(values) != 1 {
			return nil, nil, invalidLangflowModelGraph("Agent node phải có đúng một model được chọn.")
		}
		selected, ok := values[0].(map[string]any)
		if !ok || stringValue(selected["name"]) == "" || stringValue(selected["provider"]) == "" {
			return nil, nil, invalidLangflowModelGraph("Model đang chọn của agent node không hợp lệ.")
		}
		rawOptions, _ := modelInput["options"].([]any)
		options := make([]map[string]any, 0, len(rawOptions))
		for _, rawOption := range rawOptions {
			option, valid := rawOption.(map[string]any)
			if valid && stringValue(option["name"]) != "" && stringValue(option["provider"]) != "" {
				options = append(options, option)
			}
		}
		agents = append(agents, langflowAgentModel{nodeID: nodeID, modelInput: modelInput, selected: selected, options: options, selectedName: stringValue(selected["name"]), selectedFrom: stringValue(selected["provider"])})
	}
	if len(agents) == 0 {
		return nil, nil, &LangflowFlowModelError{Code: "agent_model_not_found", Message: "Không tìm thấy agent node có ModelInput trong flow."}
	}
	return document, agents, nil
}

func langflowLinkedModels(value any) map[string]bool {
	edges, _ := value.([]any)
	linked := make(map[string]bool)
	for _, edge := range edges {
		data := mapValue(edge)
		handle := mapValue(mapValue(data["data"])["targetHandle"])
		field := stringValue(handle["fieldName"])
		if field == "model" || field == "language_model" || field == "llm" {
			linked[stringValue(data["target"])] = true
		}
	}
	return linked
}

func commonLangflowModelOptions(agents []langflowAgentModel) []LangflowModelOption {
	common := make(map[string]bool, len(agents[0].options))
	for _, option := range agents[0].options {
		common[modelOptionKey(stringValue(option["provider"]), stringValue(option["name"]))] = true
	}
	for _, agent := range agents[1:] {
		available := make(map[string]bool, len(agent.options))
		for _, option := range agent.options {
			available[modelOptionKey(stringValue(option["provider"]), stringValue(option["name"]))] = true
		}
		for key := range common {
			if !available[key] {
				delete(common, key)
			}
		}
	}
	result := make([]LangflowModelOption, 0, len(common))
	for _, option := range agents[0].options {
		if common[modelOptionKey(stringValue(option["provider"]), stringValue(option["name"]))] {
			result = append(result, LangflowModelOption{Name: stringValue(option["name"]), Provider: stringValue(option["provider"]), Icon: stringValue(option["icon"]), Category: stringValue(option["category"]), Metadata: mapValue(option["metadata"])})
		}
	}
	return result
}

func selectLangflowModelOption(options []map[string]any, provider, modelName string) (map[string]any, string, error) {
	matches := make([]map[string]any, 0, 1)
	for _, option := range options {
		if stringValue(option["name"]) == modelName && (provider == "" || strings.EqualFold(stringValue(option["provider"]), provider)) {
			matches = append(matches, option)
		}
	}
	if len(matches) == 0 {
		return nil, "", &LangflowFlowModelError{Code: "model_not_available", Message: "Model/provider đã chọn không có trong options của agent node."}
	}
	if len(matches) > 1 {
		return nil, "", &LangflowFlowModelError{Code: "model_provider_ambiguous", Message: "Tên model tồn tại ở nhiều provider; hãy chọn provider cụ thể."}
	}
	return matches[0], stringValue(matches[0]["provider"]), nil
}

func readLangflowFlow(ctx context.Context, client *http.Client, apiURL, apiKey, flowID string) ([]byte, error) {
	return requestLangflowFlow(ctx, client, http.MethodGet, apiURL, apiKey, flowID, "", nil)
}

func patchLangflowFlow(ctx context.Context, client *http.Client, apiURL, apiKey, flowID, version string, payload []byte) error {
	_, err := requestLangflowFlow(ctx, client, http.MethodPatch, apiURL, apiKey, flowID, version, payload)
	return err
}

func requestLangflowFlow(ctx context.Context, client *http.Client, method, apiURL, apiKey, flowID, version string, payload []byte) ([]byte, error) {
	apiURL, flowID = strings.TrimRight(strings.TrimSpace(apiURL), "/"), strings.TrimSpace(flowID)
	if apiURL == "" || flowID == "" {
		return nil, &LangflowFlowModelError{Code: "flow_not_configured", Message: "Thiếu Langflow URL hoặc Flow ID."}
	}
	req, err := http.NewRequestWithContext(ctx, method, fmt.Sprintf("%s/api/v1/flows/%s", apiURL, url.PathEscape(flowID)), bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("create Langflow flow request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	if len(payload) > 0 {
		req.Header.Set("Content-Type", "application/json")
	}
	if version != "" {
		req.Header.Set("If-Match", strconv.Quote(version))
	}
	// /flows authenticates the Langflow API key via x-api-key. Sending it as
	// an Authorization bearer can cause the JWT middleware to reject it.
	if apiKey != "" {
		req.Header.Set("x-api-key", apiKey)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("Langflow flow request failed: %w", err)
	}
	defer resp.Body.Close()
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, maxLangflowFlowDefinitionSize+1))
	if readErr != nil {
		return nil, fmt.Errorf("read Langflow flow response: %w", readErr)
	}
	if len(body) > maxLangflowFlowDefinitionSize {
		return nil, &LangflowFlowModelError{Code: "flow_too_large", Message: "Langflow flow definition vượt quá giới hạn 8 MiB."}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &LangflowAPIError{prefix: "flow api error", StatusCode: resp.StatusCode, body: truncate(string(body), 500)}
	}
	return body, nil
}

func invalidLangflowModelGraph(message string) error {
	return &LangflowFlowModelError{Code: "invalid_flow_graph", Message: message}
}

func stringValue(value any) string {
	s, _ := value.(string)
	return strings.TrimSpace(s)
}

func mapValue(value any) map[string]any {
	m, _ := value.(map[string]any)
	return m
}

func modelOptionKey(provider, name string) string {
	return strings.ToLower(strings.TrimSpace(provider)) + "\x00" + strings.TrimSpace(name)
}

func IsLangflowFlowModelError(err error) (*LangflowFlowModelError, bool) {
	var target *LangflowFlowModelError
	ok := errors.As(err, &target)
	return target, ok
}
