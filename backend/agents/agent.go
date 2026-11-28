package agents

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/vietbui/chat-quality-agent/db/models"
	"github.com/vietbui/chat-quality-agent/engine"
	"github.com/vietbui/chat-quality-agent/knowledge"
)

// Security tiers of a chat turn, as signed in the permission token.
const (
	TierPublic  = "public"
	TierPrivate = "private"
)

// Who an agent serves.
const (
	AudienceCustomer = "customer"
	AudienceStaff    = "staff"
	AudienceAll      = "all"
)

// Agent runtimes.
const (
	RuntimeLangflow = "langflow"
	RuntimeNative   = "native"
)

// Limits on editable fields.
const (
	MaxPromptChars      = 120_000
	MaxDescriptionChars = 2_000
	maxRevisionsKept    = 30
)

// AudienceForTier maps the turn's security tier to the audience whose
// agents may answer it: the private tier is staff in a 1:1 chat, everything
// else (customers, every group chat) is the customer audience.
func AudienceForTier(tier string) string {
	if tier == TierPrivate {
		return AudienceStaff
	}
	return AudienceCustomer
}

// Options are per-agent knobs stored as JSON.
type Options struct {
	// MaxToolCalls bounds the native runtime's tool loop (0 = default).
	MaxToolCalls int `json:"max_tool_calls,omitempty"`
	// Temperature for the native runtime; nil = provider default.
	Temperature *float64 `json:"temperature,omitempty"`
	// LegacySource names the AI Engine setting the prompt was copied from
	// when the tenant moved to agents (see MigrateLegacy).
	LegacySource string `json:"legacy_source,omitempty"`
}

// Agent is a decoded models.AIAgent.
type Agent struct {
	ID             string   `json:"id"`
	TenantID       string   `json:"tenant_id"`
	Key            string   `json:"key"`
	Name           string   `json:"name"`
	Description    string   `json:"description"`
	Audience       string   `json:"audience"`
	Runtime        string   `json:"runtime"`
	LangflowFlowID string   `json:"langflow_flow_id"`
	Provider       string   `json:"provider"`
	Model          string   `json:"model"`
	SystemPrompt   string   `json:"system_prompt,omitempty"`
	Tools          []string `json:"tools"`
	KnowledgeTags  []string `json:"knowledge_tags"`
	Options        Options  `json:"options"`
	IsActive       bool     `json:"is_active"`
}

// Serves reports whether the agent may answer someone of audience.
func (a Agent) Serves(audience string) bool {
	return a.Audience == AudienceAll || a.Audience == audience
}

// HasTool reports whether the agent may call the MCP tool name.
func (a Agent) HasTool(name string) bool {
	for _, t := range a.Tools {
		if t == name {
			return true
		}
	}
	return false
}

// HasToolGroup reports whether the agent has any tool of a catalog group.
func (a Agent) HasToolGroup(group string) bool {
	for _, t := range a.Tools {
		if info, ok := LookupTool(t); ok && info.Group == group {
			return true
		}
	}
	return false
}

// HasCartTools reports whether the agent takes orders through the chat cart.
func (a Agent) HasCartTools() bool {
	for _, t := range a.Tools {
		if strings.HasPrefix(t, toolCartPrefix) {
			return true
		}
	}
	return false
}

// FromModel decodes a stored agent. Malformed JSON columns decode as empty,
// which for Tools means no tools: an agent never gains tools from bad data.
func FromModel(m models.AIAgent) Agent {
	a := Agent{
		ID: m.ID, TenantID: m.TenantID, Key: m.Key, Name: m.Name,
		Description: m.Description, Audience: m.Audience, Runtime: m.Runtime,
		LangflowFlowID: m.LangflowFlowID, Provider: m.Provider, Model: m.Model,
		SystemPrompt: m.SystemPrompt, IsActive: m.IsActive,
		Tools: []string{}, KnowledgeTags: []string{},
	}
	if m.Tools != "" {
		_ = json.Unmarshal([]byte(m.Tools), &a.Tools)
	}
	if m.KnowledgeTags != "" {
		_ = json.Unmarshal([]byte(m.KnowledgeTags), &a.KnowledgeTags)
	}
	if m.Options != "" {
		_ = json.Unmarshal([]byte(m.Options), &a.Options)
	}
	if a.Tools == nil {
		a.Tools = []string{}
	}
	if a.KnowledgeTags == nil {
		a.KnowledgeTags = []string{}
	}
	return a
}

// ApplyTo writes the editable fields onto a stored row.
func (a Agent) ApplyTo(m *models.AIAgent) {
	m.Key = a.Key
	m.Name = a.Name
	m.Description = a.Description
	m.Audience = a.Audience
	m.Runtime = a.Runtime
	m.LangflowFlowID = a.LangflowFlowID
	m.Provider = a.Provider
	m.Model = a.Model
	m.SystemPrompt = a.SystemPrompt
	m.IsActive = a.IsActive
	m.Tools = mustJSON(a.Tools)
	m.KnowledgeTags = mustJSON(a.KnowledgeTags)
	m.Options = mustJSON(a.Options)
}

func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "null"
	}
	return string(b)
}

var (
	agentKeyPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)
	flowIDPattern   = regexp.MustCompile(`^[A-Za-z0-9-]{0,64}$`)
)

// ValidationError is a field-level problem the UI shows next to the field.
type ValidationError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

func (e *ValidationError) Error() string { return e.Field + ": " + e.Message }

func invalid(field, format string, args ...any) error {
	return &ValidationError{Field: field, Message: fmt.Sprintf(format, args...)}
}

// Normalize trims and validates an agent before it is saved. It returns the
// cleaned agent: tools deduplicated and sorted, unknown values refused.
func Normalize(a Agent) (Agent, error) {
	a.Key = strings.TrimSpace(strings.ToLower(a.Key))
	a.Name = strings.TrimSpace(a.Name)
	a.Description = strings.TrimSpace(a.Description)
	a.Audience = strings.TrimSpace(a.Audience)
	a.Runtime = strings.TrimSpace(a.Runtime)
	a.LangflowFlowID = strings.TrimSpace(a.LangflowFlowID)
	a.Provider = strings.ToLower(strings.TrimSpace(a.Provider))
	a.Model = strings.TrimSpace(a.Model)

	if !agentKeyPattern.MatchString(a.Key) {
		return a, invalid("key", "chỉ dùng chữ thường không dấu, số và dấu gạch ngang (tối đa 63 ký tự)")
	}
	if a.Name == "" || len([]rune(a.Name)) > 120 {
		return a, invalid("name", "tên từ 1 đến 120 ký tự")
	}
	if len([]rune(a.Description)) > MaxDescriptionChars {
		return a, invalid("description", "mô tả tối đa %d ký tự", MaxDescriptionChars)
	}
	if len([]rune(a.SystemPrompt)) > MaxPromptChars {
		return a, invalid("system_prompt", "prompt tối đa %d ký tự", MaxPromptChars)
	}
	switch a.Audience {
	case AudienceCustomer, AudienceStaff, AudienceAll:
	default:
		return a, invalid("audience", "đối tượng phải là customer, staff hoặc all")
	}
	switch a.Runtime {
	case "":
		a.Runtime = RuntimeLangflow
	case RuntimeLangflow, RuntimeNative:
	default:
		return a, invalid("runtime", "runtime phải là langflow hoặc native")
	}
	if !flowIDPattern.MatchString(a.LangflowFlowID) {
		return a, invalid("langflow_flow_id", "mã flow không hợp lệ")
	}
	if a.Runtime == RuntimeNative {
		// The same providers and model ids as the AI task settings, so an
		// agent model resolves exactly like a task override does.
		if a.Provider == "" {
			return a, invalid("provider", "runtime native cần chọn nhà cung cấp AI")
		}
		if a.Model == "" {
			return a, invalid("model", "runtime native cần mã model")
		}
		if _, err := engine.NormalizeAITaskModel(engine.AITaskModel{Provider: a.Provider, Model: a.Model}); err != nil {
			field := "model"
			if strings.Contains(err.Error(), "nhà cung cấp") {
				field = "provider"
			}
			return a, invalid(field, "%s", err.Error())
		}
		// A native agent has no flow whose own prompt could stand in.
		if strings.TrimSpace(a.SystemPrompt) == "" {
			return a, invalid("system_prompt", "agent chạy native cần system prompt")
		}
	} else if a.Provider != "" || a.Model != "" {
		// A Langflow agent's model lives in its flow; a stray value here
		// would suggest a model that is not used.
		a.Provider, a.Model = "", ""
	}

	tools, err := normalizeTools(a.Tools, a.Audience)
	if err != nil {
		return a, err
	}
	a.Tools = tools

	// Same normal form as document tags, so they match exactly.
	tags, err := knowledge.NormalizeTags(a.KnowledgeTags)
	if err != nil {
		var bad knowledge.ErrInvalidTag
		errors.As(err, &bad)
		return a, invalid("knowledge_tags", "nhãn %q không hợp lệ", bad.Tag)
	}
	a.KnowledgeTags = tags

	if a.Options.MaxToolCalls < 0 || a.Options.MaxToolCalls > 20 {
		return a, invalid("options.max_tool_calls", "từ 0 đến 20")
	}
	if t := a.Options.Temperature; t != nil && (*t < 0 || *t > 2) {
		return a, invalid("options.temperature", "từ 0 đến 2")
	}
	return a, nil
}

// normalizeTools keeps known tools that every tier of the audience may use.
func normalizeTools(in []string, audience string) ([]string, error) {
	tiers := []string{TierPublic}
	switch audience {
	case AudienceStaff:
		tiers = []string{TierPrivate}
	case AudienceAll:
		tiers = []string{TierPublic, TierPrivate}
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, name := range in {
		name = strings.TrimSpace(name)
		if name == "" || seen[name] {
			continue
		}
		info, ok := LookupTool(name)
		if !ok {
			return nil, invalid("tools", "công cụ %q không tồn tại", name)
		}
		for _, tier := range tiers {
			if !toolServesTier(info, tier) {
				return nil, invalid("tools", "công cụ %q không dùng được cho đối tượng này", name)
			}
		}
		seen[name] = true
		out = append(out, name)
	}
	sort.Strings(out)
	return out, nil
}
