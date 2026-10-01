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

type productGroupingMockProvider struct {
	response ai.AIResponse
	err      error
	calls    int
	prompt   string
	input    string
	timeout  time.Duration
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
