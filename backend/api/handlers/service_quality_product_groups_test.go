package handlers

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/vietbui/chat-quality-agent/ai"
	"github.com/vietbui/chat-quality-agent/db/models"
	"github.com/vietbui/chat-quality-agent/servicequality"
)

func TestParseProductGroupsAcceptsWrappedAIJSONWithoutChangingMapping(t *testing.T) {
	content := `{"groups":[{"name":"E-24","member_ids":[1,2]}]}`
	encoded, _ := json.Marshal(content)
	for _, wrapped := range []string{content, "```json\n" + content + "\n```", "Kết quả tổng hợp:\n```json\n" + content + "\n```\nĐã gom đầy đủ.", string(encoded)} {
		groups, err := parseAndValidateProductGroups(wrapped, []string{"Mũ E-24", "E-24"})
		if err != nil || len(groups) != 1 || groups[0].Name != "E-24" || !reflect.DeepEqual(groups[0].Members, []string{"Mũ E-24", "E-24"}) {
			t.Fatalf("wrapped response failed: groups=%#v err=%v", groups, err)
		}
	}
	for _, invalid := range []string{"Kết quả: " + `{"groups":[{"name":"E-24","member_ids":[1]}]}`, content + "\n" + content, `{"groups":[{"name":"E-24","member_ids":["1",2]}]}`} {
		if _, err := parseAndValidateProductGroups(invalid, []string{"Mũ E-24", "E-24"}); !errors.Is(err, errInvalidProductGrouping) {
			t.Fatalf("invalid mapping was accepted: %v", err)
		}
	}
}

type productGroupingMockProvider struct {
	response ai.AIResponse
	err      error
	calls    int
	prompt   string
	input    string
	timeout  time.Duration
}

type schemaProductGroupingProvider struct {
	productGroupingMockProvider
	schema map[string]interface{}
}

func (p *schemaProductGroupingProvider) AnalyzeJSONSchema(_ context.Context, _, _ string, schema map[string]interface{}) (ai.AIResponse, error) {
	p.schema = schema
	return p.response, nil
}

func TestResolveProductGroupsUsesRequiredAssignmentsSchema(t *testing.T) {
	provider := &schemaProductGroupingProvider{productGroupingMockProvider: productGroupingMockProvider{response: ai.AIResponse{Content: `{"assignments":{"Mũ E-24":"E-24","E-24":"E-24","LS2":""}}`}}}
	deps := productGroupingDependencies{
		loadCache: func(context.Context, string, string) ([]serviceQualityProductGroup, bool, error) {
			return nil, false, nil
		},
		saveCache: func(context.Context, string, string, []serviceQualityProductGroup) error { return nil },
		aiClient:  func(context.Context, string) (ai.AIProvider, error) { return provider, nil },
		logUsage:  func(context.Context, string, ai.AIResponse) {},
	}
	groups, err := resolveProductGroups(context.Background(), "tenant-a", []string{"Mũ E-24", "E-24", "LS2"}, deps)
	if err != nil || len(groups) != 2 || provider.schema == nil || provider.calls != 0 || !reflect.DeepEqual(groups[0].Members, []string{"Mũ E-24", "E-24"}) {
		t.Fatalf("schema provider: groups=%#v err=%v schema=%#v", groups, err, provider.schema)
	}
	assignments := provider.schema["properties"].(map[string]interface{})["assignments"].(map[string]interface{})
	if !reflect.DeepEqual(assignments["required"], []string{"Mũ E-24", "E-24", "LS2"}) || assignments["additionalProperties"] != false {
		t.Fatalf("schema does not require exactly each ID: %#v", assignments)
	}
	properties := assignments["properties"].(map[string]interface{})
	if properties["Mũ E-24"].(map[string]interface{})["minLength"] != 1 || properties["E-24"].(map[string]interface{})["minLength"] != 1 || properties["LS2"].(map[string]interface{})["minLength"] != nil {
		t.Fatalf("schema permits discarding models or prohibits brand-only omission: %#v", properties)
	}
	if properties["Mũ E-24"].(map[string]interface{})["description"] != "Tên chuẩn của đúng nhãn gốc: Mũ E-24" {
		t.Fatal("schema does not bind the original label to its assignment ID")
	}
}

func TestParseProductGroupAssignmentsRejectsMissingDuplicateAndUnknownIDs(t *testing.T) {
	for _, content := range []string{
		`{"assignments":{"1":"E-24"}}`,
		`{"assignments":{"1":"E-24","1":"FF818","2":"E-24"}}`,
		`{"assignments":{"1":"E-24","3":"E-24"}}`,
		`{"assignments":{"1":null,"2":"E-24"}}`,
		`{"assignments":["E-24","E-24"]}`,
		`{"assignments":{"1":"E-24","2":"E-24"},"groups":[{"name":"FF818","member_ids":[1,2]}]}`,
	} {
		if _, err := parseAndValidateProductGroups(content, []string{"Mũ E-24", "E-24"}); !errors.Is(err, errInvalidProductGrouping) {
			t.Fatalf("invalid assignments accepted: %s err=%v", content, err)
		}
	}
}

func TestProductGroupingAssignmentsBindExactSourceNames(t *testing.T) {
	names := []string{"mũ bảo hiểm nửa đầu EGO E-24", "Bulldog Beagle", "EGO E-24", "chốt kính của nón LS2"}
	groups, err := parseAndValidateProductGroups(`{"assignments":{"Bulldog Beagle":"Beagle","chốt kính của nón LS2":"chốt kính","EGO E-24":"E-24","mũ bảo hiểm nửa đầu EGO E-24":"E-24"}}`, names)
	if err != nil || len(groups) != 3 || groups[0].Name != "E-24" || !reflect.DeepEqual(groups[0].Members, []string{names[0], names[2]}) || groups[1].Name != "Beagle" {
		t.Fatalf("source-key mapping changed product identity: groups=%#v err=%v", groups, err)
	}
	for _, content := range []string{
		`{"assignments":{"EGO E-24":"E-24","foreign":"E-24"}}`,
		`{"assignments":{"EGO E-24":"E-24","EGO E-24":"Beagle","Bulldog Beagle":"Beagle"}}`,
		`{"assignments":{"EGO E-24":"Beagle","Bulldog Beagle":"E-24"}}`,
	} {
		if _, err := parseAndValidateProductGroups(content, []string{"EGO E-24", "Bulldog Beagle"}); !errors.Is(err, errInvalidProductGrouping) {
			t.Fatalf("invalid exact source mapping accepted: %s err=%v", content, err)
		}
	}
}

type jsonProductGroupingProvider struct {
	productGroupingMockProvider
	jsonCalls int
}

func (p *jsonProductGroupingProvider) AnalyzeJSON(_ context.Context, _, _ string) (ai.AIResponse, error) {
	p.jsonCalls++
	return p.response, p.err
}

func TestResolveProductGroupsRequestsJSONFromSupportingProvider(t *testing.T) {
	provider := &jsonProductGroupingProvider{productGroupingMockProvider: productGroupingMockProvider{response: ai.AIResponse{Content: `{"groups":[{"name":"E-24","member_ids":[1]}]}`}}}
	deps := productGroupingDependencies{
		loadCache: func(context.Context, string, string) ([]serviceQualityProductGroup, bool, error) {
			return nil, false, nil
		},
		saveCache: func(context.Context, string, string, []serviceQualityProductGroup) error { return nil },
		aiClient:  func(context.Context, string) (ai.AIProvider, error) { return provider, nil },
		logUsage:  func(context.Context, string, ai.AIResponse) {},
	}
	groups, err := resolveProductGroups(context.Background(), "tenant-a", []string{"Mũ E-24"}, deps)
	if err != nil || len(groups) != 1 || provider.jsonCalls != 1 || provider.calls != 0 {
		t.Fatalf("JSON provider selection: groups=%#v err=%v json=%d chat=%d", groups, err, provider.jsonCalls, provider.calls)
	}
}

type productGroupingEchoProvider struct {
	calls int
}

func (p *productGroupingEchoProvider) AnalyzeChat(_ context.Context, _ string, input string) (ai.AIResponse, error) {
	p.calls++
	var request struct {
		Names []string `json:"product_names"`
	}
	if err := json.Unmarshal([]byte(input), &request); err != nil {
		return ai.AIResponse{}, err
	}
	groups := make([]serviceQualityProductGroup, 0, len(request.Names))
	for _, name := range request.Names {
		groups = append(groups, serviceQualityProductGroup{Name: name, Members: []string{name}})
	}
	content, _ := json.Marshal(serviceQualityProductGroupsResponse{Groups: groups})
	return ai.AIResponse{Content: string(content), Provider: "openai", Model: "gpt-5-mini"}, nil
}

func (p *productGroupingEchoProvider) AnalyzeChatBatch(context.Context, string, []ai.BatchItem) (ai.AIResponse, error) {
	return ai.AIResponse{}, errors.New("unexpected batch call")
}

func (m *productGroupingMockProvider) AnalyzeChat(ctx context.Context, prompt, input string) (ai.AIResponse, error) {
	m.calls++
	m.prompt = prompt
	m.input = input
	if deadline, ok := ctx.Deadline(); ok {
		m.timeout = time.Until(deadline)
	}
	return m.response, m.err
}

func (m *productGroupingMockProvider) AnalyzeChatBatch(context.Context, string, []ai.BatchItem) (ai.AIResponse, error) {
	return ai.AIResponse{}, errors.New("unexpected batch call")
}

func TestProductNamesForGroupingUsesOnlyFreshInWindowQualifiedLeads(t *testing.T) {
	insight := func(level string, names ...string) json.RawMessage {
		products := make([]map[string]string, 0, len(names))
		for _, name := range names {
			products = append(products, map[string]string{"name": name})
		}
		data, _ := json.Marshal(map[string]interface{}{
			"products":     products,
			"lead_quality": map[string]string{"level": level},
		})
		return data
	}
	from := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	inWindow := time.Date(2026, 9, 15, 0, 0, 0, 0, time.UTC)
	outWindow := time.Date(2026, 8, 31, 0, 0, 0, 0, time.UTC)
	report := &servicequality.Report{From: from, To: to, Rows: []servicequality.Row{
		{InsightAt: &inWindow, Insight: insight("high", "LS2 FF 818", "LS2 FF 818")},
		{InsightAt: &inWindow, Insight: insight("medium", "Mũ E-24")},
		{InsightAt: &inWindow, Insight: insight("low", "E-24")},
		{InsightAt: &inWindow, Insight: insight("high", "stale"), InsightStale: true},
		{InsightAt: &outWindow, Insight: insight("high", "outside-window")},
		{Insight: insight("high", "missing-timestamp")},
		{InsightAt: &inWindow, Insight: insight("spam", "spam-product")},
		{InsightAt: &inWindow, Insight: insight("unknown", "unknown-product")},
		{InsightAt: &inWindow, Insight: json.RawMessage(`{"products":`)},
	}}

	want := []string{"E-24", "LS2 FF 818", "Mũ E-24"}
	if got := productNamesForGrouping(report); !reflect.DeepEqual(got, want) {
		t.Fatalf("productNamesForGrouping() = %#v, want %#v", got, want)
	}
}

func TestValidateProductGroupingSnapshotDetectsReportChanges(t *testing.T) {
	current := []string{"E-24", "FF818"}
	for _, snapshot := range [][]string{nil, {"FF818", "E-24"}} {
		if err := validateProductGroupingSnapshot(snapshot, current); err != nil {
			t.Fatalf("valid snapshot %#v rejected: %v", snapshot, err)
		}
	}
	for _, snapshot := range [][]string{{"E-24"}, {"E-24", "forged"}, {"E-24", "E-24"}, {}} {
		if err := validateProductGroupingSnapshot(snapshot, current); !errors.Is(err, errProductGroupingSnapshotConflict) {
			t.Fatalf("changed snapshot %#v error = %v, want conflict", snapshot, err)
		}
	}
	if err := validateProductGroupingSnapshot([]string{}, []string{}); err != nil {
		t.Fatalf("explicit empty snapshot should match empty report: %v", err)
	}
}

func TestResolveProductGroupsUsesTenantScopedCache(t *testing.T) {
	names := []string{"E-24", "Mũ E-24"}
	cached := []serviceQualityProductGroup{{Name: "E-24", Members: names}}
	provider := &productGroupingMockProvider{response: ai.AIResponse{
		Content:  `{"groups":[{"name":"E-24","members":["E-24","Mũ E-24"]}]}`,
		Provider: "openai", Model: "gpt-5-mini",
	}}
	var loadedTenants, savedTenants []string
	deps := productGroupingDependencies{
		loadCache: func(_ context.Context, tenantID, _ string) ([]serviceQualityProductGroup, bool, error) {
			loadedTenants = append(loadedTenants, tenantID)
			if tenantID == "tenant-a" {
				return cached, true, nil
			}
			return nil, false, nil
		},
		saveCache: func(_ context.Context, tenantID, _ string, _ []serviceQualityProductGroup) error {
			savedTenants = append(savedTenants, tenantID)
			return nil
		},
		aiClient: func(_ context.Context, tenantID string) (ai.AIProvider, error) {
			if tenantID != "tenant-b" {
				t.Fatalf("unexpected AI tenant %q", tenantID)
			}
			return provider, nil
		},
		logUsage: func(context.Context, string, ai.AIResponse) {},
	}

	groupsA, err := resolveProductGroups(context.Background(), "tenant-a", names, deps)
	if err != nil || !reflect.DeepEqual(groupsA, cached) || provider.calls != 0 {
		t.Fatalf("tenant-a cache was not used: groups=%#v calls=%d err=%v", groupsA, provider.calls, err)
	}
	groupsB, err := resolveProductGroups(context.Background(), "tenant-b", names, deps)
	if err != nil || !reflect.DeepEqual(groupsB, cached) || provider.calls != 1 {
		t.Fatalf("tenant-b AI resolution failed: groups=%#v calls=%d err=%v", groupsB, provider.calls, err)
	}
	if !reflect.DeepEqual(loadedTenants, []string{"tenant-a", "tenant-b"}) || !reflect.DeepEqual(savedTenants, []string{"tenant-b"}) {
		t.Fatalf("cache tenant scope wrong: loaded=%v saved=%v", loadedTenants, savedTenants)
	}
	if provider.prompt != ai.MessengerProductGroupingPrompt || provider.input != `{"product_names":["E-24","Mũ E-24"]}` {
		t.Fatalf("unexpected AI request: prompt=%q input=%s", provider.prompt, provider.input)
	}
	if provider.timeout < 50*time.Second || provider.timeout > productGroupingAITimeout {
		t.Fatalf("AI timeout = %s, want bounded near %s", provider.timeout, productGroupingAITimeout)
	}
}

func TestResolveProductGroupsCachesAlternatingHashesPerTenant(t *testing.T) {
	provider := &productGroupingEchoProvider{}
	cache := make(map[string][]serviceQualityProductGroup)
	key := func(tenantID, hash string) string { return tenantID + "/" + productGroupingCacheSettingKey(hash) }
	deps := productGroupingDependencies{
		loadCache: func(_ context.Context, tenantID, hash string) ([]serviceQualityProductGroup, bool, error) {
			groups, ok := cache[key(tenantID, hash)]
			return groups, ok, nil
		},
		saveCache: func(_ context.Context, tenantID, hash string, groups []serviceQualityProductGroup) error {
			cache[key(tenantID, hash)] = groups
			return nil
		},
		aiClient: func(context.Context, string) (ai.AIProvider, error) { return provider, nil },
		logUsage: func(context.Context, string, ai.AIResponse) {},
	}

	a := []string{"E-24"}
	b := []string{"FF818"}
	for _, call := range []struct {
		tenant string
		names  []string
	}{{"tenant-a", a}, {"tenant-a", b}, {"tenant-a", a}, {"tenant-b", a}} {
		if _, err := resolveProductGroups(context.Background(), call.tenant, call.names, deps); err != nil {
			t.Fatalf("resolve tenant=%s names=%v: %v", call.tenant, call.names, err)
		}
	}
	if provider.calls != 3 {
		t.Fatalf("alternating hashes/tenants called AI %d times, want 3", provider.calls)
	}
	if len(cache) != 3 {
		t.Fatalf("cache entries = %d, want separate tenant/hash entries: %#v", len(cache), cache)
	}
}

func TestResolveProductGroupsRejectsInvalidAIMappings(t *testing.T) {
	names := []string{"E-24", "Mũ E-24"}
	tests := map[string]string{
		"missing original":   `{"groups":[{"name":"E-24","members":["E-24"]}]}`,
		"duplicate original": `{"groups":[{"name":"E-24","members":["E-24","E-24","Mũ E-24"]}]}`,
		"foreign label":      `{"groups":[{"name":"E-24","members":["E-24","Mũ E-24","Invented"]}]}`,
		"empty group":        `{"groups":[{"name":"E-24","members":[]},{"name":"E-24","members":["E-24","Mũ E-24"]}]}`,
	}
	for name, content := range tests {
		t.Run(name, func(t *testing.T) {
			provider := &productGroupingMockProvider{response: ai.AIResponse{Content: content}}
			deps := productGroupingDependencies{
				loadCache: func(context.Context, string, string) ([]serviceQualityProductGroup, bool, error) {
					return nil, false, nil
				},
				saveCache: func(context.Context, string, string, []serviceQualityProductGroup) error {
					t.Fatal("invalid mapping was cached")
					return nil
				},
				aiClient: func(context.Context, string) (ai.AIProvider, error) { return provider, nil },
				logUsage: func(context.Context, string, ai.AIResponse) {},
			}
			if _, err := resolveProductGroups(context.Background(), "tenant-a", names, deps); !errors.Is(err, errInvalidProductGrouping) {
				t.Fatalf("resolveProductGroups() error = %v, want invalid mapping", err)
			}
		})
	}
}

func TestProductGroupingAllowsBrandOnlyLabelWithEmptyCanonicalName(t *testing.T) {
	groups, err := parseAndValidateProductGroups(`{"groups":[{"name":"","members":["LS2"]}]}`, []string{"LS2"})
	if err != nil {
		t.Fatalf("brand-only group was rejected: %v", err)
	}
	if len(groups) != 1 || groups[0].Name != "" || !reflect.DeepEqual(groups[0].Members, []string{"LS2"}) {
		t.Fatalf("unexpected brand-only mapping: %#v", groups)
	}
}

func TestProductGroupingCannotSilentlyDiscardNamedProducts(t *testing.T) {
	for _, name := range []string{"mũ bảo hiểm nửa đầu EGO E-24", "mũ bảo hiểm EGO E-24", "E-24", "EGO E-24", "Bulldog Corgi", "LS2 FF 818"} {
		for _, canonical := range []string{"", "   "} {
			content, _ := json.Marshal(map[string]interface{}{"assignments": map[string]string{"1": canonical}})
			if _, err := parseAndValidateProductGroups(string(content), []string{name}); !errors.Is(err, errInvalidProductGrouping) {
				t.Fatalf("named product %q silently discarded: %v", name, err)
			}
		}
	}
}

func TestProductGroupingRejectsSemanticMappingErrors(t *testing.T) {
	for _, test := range []struct {
		names    []string
		response string
	}{
		{[]string{"LS2 OF618 Verso II"}, `{"assignments":{"1":"FF618VersoII"}}`},
		{[]string{"nón LS2 OF597"}, `{"assignments":{"1":"LS2OF597"}}`},
		{[]string{"mũ EGO E-24"}, `{"assignments":{"1":"EGO"}}`},
		{[]string{"xịt trượt nước cho nón bảo hiểm"}, `{"assignments":{"1":"bảo hiểm"}}`},
		{[]string{"E-24", "Mũ E-24"}, `{"assignments":{"1":"E-24","2":"E24"}}`},
	} {
		if _, err := parseAndValidateProductGroups(test.response, test.names); !errors.Is(err, errInvalidProductGrouping) {
			t.Fatalf("invalid model mapping accepted: %s err=%v", test.response, err)
		}
	}
	groups, err := parseAndValidateProductGroups(`{"assignments":{"1":"OF618VersoII","2":"OF597","3":"E-24","4":"FF818","5":"Corgi"}}`, []string{"mũ bảo hiểm 3/4 LS2 OF618 Verso II", "nón LS2 OF597", "mũ EGO E-24", "LS2 FF 818", "Bulldog Corgi"})
	if err != nil || len(groups) != 5 {
		t.Fatalf("valid AI model/code normalization rejected: %v", err)
	}
}

func TestProductGroupingRepairsDiscardedProductsAndRejectsTheirCache(t *testing.T) {
	names := []string{"mũ bảo hiểm nửa đầu EGO E-24", "EGO E-24", "LS2"}
	provider := &repairingProductGroupingProvider{responses: []string{
		`{"assignments":{"1":"","2":"E-24","3":""}}`,
		`{"assignments":{"1":"E-24","2":"E-24","3":""}}`,
	}}
	saves := 0
	deps := productGroupingDependencies{
		loadCache: func(context.Context, string, string) ([]serviceQualityProductGroup, bool, error) {
			return []serviceQualityProductGroup{{Name: "", Members: names}}, true, nil
		},
		saveCache: func(_ context.Context, _, _ string, groups []serviceQualityProductGroup) error {
			saves++
			if groups[0].Name != "E-24" || !reflect.DeepEqual(groups[0].Members, names[:2]) {
				t.Fatalf("incomplete product demand cached: %#v", groups)
			}
			return nil
		},
		aiClient: func(context.Context, string) (ai.AIProvider, error) { return provider, nil },
		logUsage: func(context.Context, string, ai.AIResponse) {},
	}
	groups, err := resolveProductGroups(context.Background(), "tenant", names, deps)
	if err != nil || len(provider.inputs) != 2 || saves != 1 || len(groups) != 2 {
		t.Fatalf("discarded products not repaired by AI: groups=%#v calls=%d saves=%d err=%v", groups, len(provider.inputs), saves, err)
	}
}

type repairingProductGroupingProvider struct {
	productGroupingMockProvider
	inputs    []string
	responses []string
}

func (p *repairingProductGroupingProvider) AnalyzeChat(ctx context.Context, prompt, input string) (ai.AIResponse, error) {
	p.inputs = append(p.inputs, input)
	if len(p.inputs) > len(p.responses) {
		return ai.AIResponse{}, errors.New("unexpected extra AI repair attempt")
	}
	p.prompt = prompt
	return ai.AIResponse{Content: p.responses[len(p.inputs)-1], Provider: "openai", InputTokens: 50, OutputTokens: 30}, nil
}

func TestResolveProductGroupsRepairsInvalidMappingWithAI(t *testing.T) {
	names := []string{"LS2 FF 818", "Mũ E-24", "E-24", "LS2"}
	valid := `{"groups":[{"name":"FF818","members":["LS2 FF 818"]},{"name":"E-24","members":["Mũ E-24","E-24"]},{"name":"","members":["LS2"]}]}`
	for name, invalid := range map[string]string{
		"missing brand-only label": `{"groups":[{"name":"FF818","members":["LS2 FF 818"]},{"name":"E-24","members":["Mũ E-24","E-24"]}]}`,
		"rewritten original label": `{"groups":[{"name":"FF818","members":["LS2 FF818"]},{"name":"E-24","members":["Mũ E-24","E-24"]},{"name":"","members":["LS2"]}]}`,
		"invalid JSON":             `{"groups":[`,
	} {
		t.Run(name, func(t *testing.T) {
			provider := &repairingProductGroupingProvider{responses: []string{invalid, valid}}
			starts, finishes, usages, saves := 0, 0, 0, 0
			deps := productGroupingDependencies{
				prompt: "Prompt do admin chỉnh sửa",
				loadCache: func(context.Context, string, string) ([]serviceQualityProductGroup, bool, error) {
					return nil, false, nil
				},
				saveCache: func(_ context.Context, _, _ string, groups []serviceQualityProductGroup) error {
					saves++
					if _, err := validateProductGroups(names, groups); err != nil {
						t.Fatal("cached incomplete mapping", err)
					}
					return nil
				},
				aiClient: func(context.Context, string) (ai.AIProvider, error) { return provider, nil },
				startRun: func(context.Context) error { starts++; return nil },
				finishRun: func(err error) {
					finishes++
					if err != nil {
						t.Fatal(err)
					}
				},
				logUsage: func(context.Context, string, ai.AIResponse) { usages++ },
			}
			groups, err := resolveProductGroups(context.Background(), "tenant", names, deps)
			if err != nil || len(groups) != 3 || len(provider.inputs) != 2 || saves != 1 || usages != 2 || starts != 1 || finishes != 1 {
				t.Fatalf("AI repair did not complete one run: groups=%v err=%v calls=%d saves=%d usage=%d starts=%d finishes=%d", groups, err, len(provider.inputs), saves, usages, starts, finishes)
			}
			var correction struct {
				Names      []string `json:"product_names"`
				Previous   string   `json:"previous_response"`
				Correction string   `json:"correction"`
			}
			if json.Unmarshal([]byte(provider.inputs[1]), &correction) != nil || !reflect.DeepEqual(correction.Names, names) || correction.Previous != invalid || !strings.Contains(correction.Correction, "members") || provider.prompt != deps.prompt {
				t.Fatal("repair did not use exact original labels, previous response and configured system prompt")
			}
		})
	}
}

func TestResolveProductGroupsBoundsInvalidAIRepairWithoutCaching(t *testing.T) {
	provider := &repairingProductGroupingProvider{responses: []string{`{"groups":[]}`, `{"groups":[]}`}}
	usages, finishes := 0, 0
	deps := productGroupingDependencies{
		loadCache: func(context.Context, string, string) ([]serviceQualityProductGroup, bool, error) {
			return nil, false, nil
		},
		saveCache: func(context.Context, string, string, []serviceQualityProductGroup) error {
			t.Fatal("cached invalid result")
			return nil
		},
		aiClient: func(context.Context, string) (ai.AIProvider, error) { return provider, nil },
		logUsage: func(context.Context, string, ai.AIResponse) { usages++ },
		finishRun: func(err error) {
			finishes++
			if !errors.Is(err, errInvalidProductGrouping) {
				t.Fatal("lost validation error", err)
			}
		},
	}
	if _, err := resolveProductGroups(context.Background(), "tenant", []string{"E-24"}, deps); !errors.Is(err, errInvalidProductGrouping) || len(provider.inputs) != 2 || usages != 2 || finishes != 1 {
		t.Fatalf("invalid repair not bounded: err=%v calls=%d usage=%d finish=%d", err, len(provider.inputs), usages, finishes)
	}
}

func TestParseProductGroupsUsesAISelectedIDsToPreserveExactOriginalLabels(t *testing.T) {
	names := []string{"Mũ\u00a0E-24", "E-24", "LS2 FF 818", "LS2"}
	groups, err := parseAndValidateProductGroups(`{"groups":[{"name":"E-24","member_ids":[1,2]},{"name":"FF818","member_ids":[3]},{"name":"","member_ids":[4]}]}`, names)
	want := []serviceQualityProductGroup{{Name: "E-24", Members: names[:2]}, {Name: "FF818", Members: names[2:3]}, {Name: "", Members: names[3:]}}
	if err != nil || !reflect.DeepEqual(groups, want) {
		t.Fatalf("AI ID mapping did not preserve exact labels: groups=%#v err=%v", groups, err)
	}
	for name, invalid := range map[string]string{
		"zero ID":          `{"groups":[{"name":"E-24","member_ids":[0,1,2,3,4]}]}`,
		"foreign ID":       `{"groups":[{"name":"E-24","member_ids":[1,2,3,4,5]}]}`,
		"duplicate ID":     `{"groups":[{"name":"E-24","member_ids":[1,2,3,4,1]}]}`,
		"missing ID":       `{"groups":[{"name":"E-24","member_ids":[1,2,3]}]}`,
		"fractional ID":    `{"groups":[{"name":"E-24","member_ids":[1,2,3,4.1]}]}`,
		"conflicting text": `{"groups":[{"name":"E-24","member_ids":[1,2,3,4],"members":["forged"]}]}`,
		"empty IDs":        `{"groups":[{"name":"E-24","member_ids":[],"members":["E-24"]}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := parseAndValidateProductGroups(invalid, names); !errors.Is(err, errInvalidProductGrouping) {
				t.Fatalf("invalid AI ID mapping accepted: %v", err)
			}
		})
	}
}

func TestResolveProductGroupsCanRepairLabelsUsingAISelectedIDs(t *testing.T) {
	names := []string{"Mũ\u00a0E-24", "E-24", "LS2"}
	provider := &repairingProductGroupingProvider{responses: []string{
		`{"groups":[{"name":"E-24","members":["Mũ E-24","E-24"]}]}`,
		`{"groups":[{"name":"E-24","member_ids":[1,2]},{"name":"","member_ids":[3]}]}`,
	}}
	saves := 0
	deps := productGroupingDependencies{
		loadCache: func(context.Context, string, string) ([]serviceQualityProductGroup, bool, error) {
			return nil, false, nil
		},
		saveCache: func(_ context.Context, _, _ string, groups []serviceQualityProductGroup) error {
			saves++
			if groups[0].Members[0] != names[0] {
				t.Fatal("AI original label was rewritten")
			}
			return nil
		},
		aiClient: func(context.Context, string) (ai.AIProvider, error) { return provider, nil },
		logUsage: func(context.Context, string, ai.AIResponse) {},
	}
	groups, err := resolveProductGroups(context.Background(), "tenant", names, deps)
	if err != nil || len(groups) != 2 || len(provider.inputs) != 2 || saves != 1 {
		t.Fatalf("AI did not repair transport errors using IDs: groups=%v err=%v calls=%d saves=%d", groups, err, len(provider.inputs), saves)
	}
}

func TestResolveProductGroupsRejectsOversizedInputsWithoutCallingAI(t *testing.T) {
	provider := &productGroupingMockProvider{}
	deps := productGroupingDependencies{
		loadCache: func(context.Context, string, string) ([]serviceQualityProductGroup, bool, error) {
			t.Fatal("oversized input must be rejected before cache lookup")
			return nil, false, nil
		},
		saveCache: func(context.Context, string, string, []serviceQualityProductGroup) error { return nil },
		aiClient:  func(context.Context, string) (ai.AIProvider, error) { return provider, nil },
		logUsage:  func(context.Context, string, ai.AIResponse) {},
	}

	tests := map[string][]string{
		"label count": make([]string, maxProductGroupingLabels+1),
		"input bytes": {strings.Repeat("x", maxProductGroupingInputBytes)},
	}
	for name, names := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := resolveProductGroups(context.Background(), "tenant-a", names, deps); !errors.Is(err, errProductGroupingInputTooLarge) {
				t.Fatalf("resolveProductGroups() error = %v, want input-too-large", err)
			}
		})
	}
	if provider.calls != 0 {
		t.Fatalf("oversized inputs called AI %d times", provider.calls)
	}
}

func TestProductGroupingInputHashIncludesPromptVersion(t *testing.T) {
	first := productGroupingInputHash([]string{"E-24"})
	second := productGroupingInputHash([]string{"E-24", "FF818"})
	if first == "" || first == second {
		t.Fatalf("input hashes should be stable and input-sensitive: %q %q", first, second)
	}
}

func TestProductGroupingCacheEncodingNearTextLimit(t *testing.T) {
	groups := make([]serviceQualityProductGroup, 0, maxProductGroupingLabels)
	for i := 0; i < maxProductGroupingLabels; i++ {
		groups = append(groups, serviceQualityProductGroup{
			Name:    deterministicCacheToken("name", i),
			Members: []string{deterministicCacheToken("member", i)},
		})
	}
	encoded, err := encodeProductGroupingCache(groups)
	if err != nil {
		t.Fatalf("near-limit cache encoding failed: %v", err)
	}
	if len(encoded) < 40<<10 || len(encoded) > maxProductGroupingCacheEncodedBytes {
		t.Fatalf("encoded cache size = %d, want a meaningful near-limit payload", len(encoded))
	}
	decoded, err := decodeProductGroupingCache(encoded)
	if err != nil || !reflect.DeepEqual(decoded, groups) {
		t.Fatalf("cache round trip failed: groups=%d err=%v", len(decoded), err)
	}

	for i := range groups {
		groups[i].Name += deterministicCacheToken("extra-a", i) + deterministicCacheToken("extra-b", i)
	}
	if _, err := encodeProductGroupingCache(groups); !errors.Is(err, errProductGroupingCacheTooLarge) {
		t.Fatalf("oversized encoded cache error = %v, want cache-too-large", err)
	}
}

func TestProductGroupingCachePrunesOnlyEntriesAfterNewestTwenty(t *testing.T) {
	settings := make([]models.AppSetting, 0, maxProductGroupingCacheEntries+2)
	for i := 0; i < maxProductGroupingCacheEntries+2; i++ {
		settings = append(settings, models.AppSetting{SettingKey: fmt.Sprintf("%s%02d", messengerProductGroupsCacheKeyPrefix, i)})
	}
	want := []string{settings[20].SettingKey, settings[21].SettingKey}
	if got := productGroupingCacheKeysToPrune(settings); !reflect.DeepEqual(got, want) {
		t.Fatalf("prune keys = %#v, want %#v", got, want)
	}
	if got := productGroupingCacheKeysToPrune(settings[:20]); len(got) != 0 {
		t.Fatalf("cache at limit should not prune: %#v", got)
	}
}

func TestResolveProductGroupsReturnsValidGroupsWhenCachePersistenceFails(t *testing.T) {
	provider := &productGroupingEchoProvider{}
	deps := productGroupingDependencies{
		loadCache: func(context.Context, string, string) ([]serviceQualityProductGroup, bool, error) {
			return nil, false, errors.New("cache read unavailable")
		},
		saveCache: func(context.Context, string, string, []serviceQualityProductGroup) error {
			return errors.New("cache write unavailable")
		},
		aiClient: func(context.Context, string) (ai.AIProvider, error) { return provider, nil },
		logUsage: func(context.Context, string, ai.AIResponse) {},
	}
	groups, err := resolveProductGroups(context.Background(), "tenant-a", []string{"E-24"}, deps)
	if err != nil || len(groups) != 1 || groups[0].Name != "E-24" {
		t.Fatalf("valid AI mapping lost after cache errors: groups=%#v err=%v", groups, err)
	}
}

func deterministicCacheToken(prefix string, index int) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(fmt.Sprintf("%s-%d", prefix, index))))
}
