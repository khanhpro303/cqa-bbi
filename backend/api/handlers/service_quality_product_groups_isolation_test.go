package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vietbui/chat-quality-agent/ai"
)

// promptCompliantBatchedProvider answers exactly as the default prompt asks
// (drop "Mũ", colour, typos and aliases), including answers the semantic
// checks cannot verify against the source label. Production failed every run
// that contained one such label.
type promptCompliantBatchedProvider struct {
	productGroupingMockProvider
	mu    sync.Mutex
	calls map[string]int
}

func (p *promptCompliantBatchedProvider) AnalyzeJSONSchema(_ context.Context, _, input string, _ map[string]interface{}) (ai.AIResponse, error) {
	var request struct {
		Names []string `json:"product_names"`
	}
	if err := json.Unmarshal([]byte(input), &request); err != nil {
		return ai.AIResponse{}, err
	}
	p.mu.Lock()
	p.calls[request.Names[0]]++
	p.mu.Unlock()
	canonical := map[string]string{"Mũ 3/4 màu đen": "3/4", "Bulldog Doggo": "Dogo", "LS2 FF 818": "FF818"}
	assignments := make(map[string]string, len(request.Names))
	for _, name := range request.Names {
		value, ok := canonical[name]
		if !ok {
			value = "E-24"
		}
		assignments[name] = value
	}
	content, _ := json.Marshal(map[string]interface{}{"assignments": assignments})
	return ai.AIResponse{Content: string(content), Provider: "openai"}, nil
}

func productGroupingIsolationNames() []string {
	names := productGroupingBatchRepairNames()[:8]
	return append(names, "Mũ 3/4 màu đen", "Bulldog Doggo", "LS2 FF 818")
}

func TestProductGroupingBatchesIsolateUnverifiableLabelsInsteadOfFailingJob(t *testing.T) {
	names := productGroupingIsolationNames()
	provider := &promptCompliantBatchedProvider{calls: make(map[string]int)}
	var cached []serviceQualityProductGroup
	deps := productGroupingDependencies{
		loadCache: func(context.Context, string, string) ([]serviceQualityProductGroup, bool, error) {
			return nil, false, nil
		},
		saveCache: func(_ context.Context, _, _ string, groups []serviceQualityProductGroup) error {
			cached = groups
			return nil
		},
		aiClient: func(context.Context, string) (ai.AIProvider, error) { return provider, nil },
		logUsage: func(context.Context, string, ai.AIResponse) {},
	}
	groups, err := resolveProductGroups(context.Background(), "tenant", names, deps)
	if err != nil {
		t.Fatalf("one unverifiable label failed the whole grouping job: %v", err)
	}
	want := []serviceQualityProductGroup{
		{Name: "E-24", Members: names[:8]},
		{Name: "Mũ 3/4 màu đen", Members: []string{"Mũ 3/4 màu đen"}},
		{Name: "Bulldog Doggo", Members: []string{"Bulldog Doggo"}},
		{Name: "FF818", Members: []string{"LS2 FF 818"}},
	}
	if !reflect.DeepEqual(groups, want) {
		t.Fatalf("verified AI groups must be kept and only rejected labels left verbatim:\n got %#v\nwant %#v", groups, want)
	}
	if provider.calls[names[0]] != 1 || provider.calls[names[8]] != 2 {
		t.Fatalf("each batch must be repaired at most once and never re-run as a whole: %#v", provider.calls)
	}

	// The persisted result must be reusable, not re-sent to AI on every report view.
	deps.loadCache = func(context.Context, string, string) ([]serviceQualityProductGroup, bool, error) {
		return cached, true, nil
	}
	deps.aiClient = func(context.Context, string) (ai.AIProvider, error) {
		t.Fatal("valid cached grouping with isolated labels was rejected")
		return nil, nil
	}
	if fromCache, err := resolveProductGroups(context.Background(), "tenant", names, deps); err != nil || !reflect.DeepEqual(fromCache, want) {
		t.Fatalf("cached grouping: groups=%#v err=%v", fromCache, err)
	}
}

func TestResolveProductGroupsIsolatesLabelsTheAIRepairDidNotFix(t *testing.T) {
	names := []string{"EGO E-24", "Bulldog Beagle", "Mũ E-24", "LS2"}
	stillWrong := `{"assignments":{"EGO E-24":"E-24","Bulldog Beagle":"E-24","Mũ E-24":"","LS2":""}}`
	provider := &repairingProductGroupingProvider{responses: []string{stillWrong, stillWrong}}
	deps := productGroupingDependencies{
		prompt: "Prompt do admin chỉnh sửa",
		loadCache: func(context.Context, string, string) ([]serviceQualityProductGroup, bool, error) {
			return nil, false, nil
		},
		saveCache: func(context.Context, string, string, []serviceQualityProductGroup) error { return nil },
		aiClient:  func(context.Context, string) (ai.AIProvider, error) { return provider, nil },
		logUsage:  func(context.Context, string, ai.AIResponse) {},
	}
	groups, err := resolveProductGroups(context.Background(), "tenant", names, deps)
	want := []serviceQualityProductGroup{
		{Name: "E-24", Members: []string{"EGO E-24"}},
		{Name: "Bulldog Beagle", Members: []string{"Bulldog Beagle"}},
		{Name: "Mũ E-24", Members: []string{"Mũ E-24"}},
		{Name: "", Members: []string{"LS2"}},
	}
	if err != nil || len(provider.inputs) != 2 || !reflect.DeepEqual(groups, want) {
		t.Fatalf("wrong model or discarded product must stay as its own label, never fail or vanish: groups=%#v err=%v calls=%d", groups, err, len(provider.inputs))
	}
}

func TestProductGroupingStructuralErrorsStillFailAfterRepair(t *testing.T) {
	missing := `{"assignments":{"E-24":"E-24"}}`
	provider := &repairingProductGroupingProvider{responses: []string{missing, missing}}
	deps := productGroupingDependencies{
		loadCache: func(context.Context, string, string) ([]serviceQualityProductGroup, bool, error) {
			return nil, false, nil
		},
		saveCache: func(context.Context, string, string, []serviceQualityProductGroup) error {
			t.Fatal("incomplete mapping was cached")
			return nil
		},
		aiClient: func(context.Context, string) (ai.AIProvider, error) { return provider, nil },
		logUsage: func(context.Context, string, ai.AIResponse) {},
	}
	if _, err := resolveProductGroups(context.Background(), "tenant", []string{"E-24", "Mũ E-24"}, deps); !errors.Is(err, errInvalidProductGrouping) {
		t.Fatalf("missing label must never be filled in code: err=%v", err)
	}
}

func TestProductGroupingIdentityIgnoresPunctuationAndUnicodeForm(t *testing.T) {
	for _, test := range []struct{ label, canonical string }{
		{"Mũ EGO E.24", "E-24"},
		{"FF_818 đen", "FF818"},
		{"Mũ trẻ em", "trẻ em"}, // decomposed source, precomposed AI answer
	} {
		content, _ := json.Marshal(map[string]interface{}{"assignments": map[string]string{test.label: test.canonical}})
		if _, err := parseAndValidateProductGroupingBatch(string(content), []string{test.label}); err != nil {
			t.Fatalf("%q -> %q rejected: %v", test.label, test.canonical, err)
		}
	}
	groups, err := parseAndValidateProductGroups(`{"assignments":{"E-24":"E-24","Mũ E-24":"e24"}}`, []string{"E-24", "Mũ E-24"})
	if err != nil || len(groups) != 1 || groups[0].Name != "E-24" || len(groups[0].Members) != 2 {
		t.Fatalf("equivalent canonical spellings must merge, not fail: groups=%#v err=%v", groups, err)
	}
	if strings.Contains(productMappingIdentity("3/4"), "34") {
		t.Fatal("3/4 helmet type must not collide with model 34")
	}
}

// slowBatchedProductGroupingProvider takes most of one call's budget per batch,
// so the whole run lasts several call budgets, like a large real report.
type slowBatchedProductGroupingProvider struct {
	batchedProductGroupingProvider
	delay       time.Duration
	callTimeout time.Duration
	badDeadline atomic.Bool
}

func (p *slowBatchedProductGroupingProvider) AnalyzeJSONSchema(ctx context.Context, prompt, input string, schema map[string]interface{}) (ai.AIResponse, error) {
	deadline, ok := ctx.Deadline()
	if !ok || time.Until(deadline) > p.callTimeout {
		p.badDeadline.Store(true)
	}
	select {
	case <-time.After(p.delay):
	case <-ctx.Done():
		return ai.AIResponse{}, ctx.Err()
	}
	return p.batchedProductGroupingProvider.AnalyzeJSONSchema(ctx, prompt, input, schema)
}

func TestProductGroupingTimeoutAppliesPerAICallNotToWholeRun(t *testing.T) {
	names := make([]string, 0, 8*productGroupingBatchConcurrency*3)
	for i := 0; i < cap(names); i++ {
		names = append(names, fmt.Sprintf("Mũ E-24 màu %d", i))
	}
	provider := &slowBatchedProductGroupingProvider{delay: 40 * time.Millisecond, callTimeout: 100 * time.Millisecond}
	deps := productGroupingDependencies{
		callTimeout: provider.callTimeout,
		runTimeout:  2 * time.Second,
		loadCache: func(context.Context, string, string) ([]serviceQualityProductGroup, bool, error) {
			return nil, false, nil
		},
		saveCache: func(context.Context, string, string, []serviceQualityProductGroup) error { return nil },
		aiClient:  func(context.Context, string) (ai.AIProvider, error) { return provider, nil },
		logUsage:  func(context.Context, string, ai.AIResponse) {},
	}
	started := time.Now()
	groups, err := resolveProductGroups(context.Background(), "tenant", names, deps)
	if err != nil || len(groups) != 1 || len(groups[0].Members) != len(names) {
		t.Fatalf("a run longer than one AI call budget failed: groups=%d err=%v", len(groups), err)
	}
	if elapsed := time.Since(started); elapsed < provider.callTimeout {
		t.Fatalf("test did not exercise a run longer than one call budget: %v", elapsed)
	}
	if provider.badDeadline.Load() {
		t.Fatal("an AI call ran without its own per-call deadline")
	}

	deps.runTimeout = 60 * time.Millisecond
	if _, err := resolveProductGroups(context.Background(), "tenant", names, deps); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("whole-run budget must still stop a runaway job: err=%v", err)
	}
}
