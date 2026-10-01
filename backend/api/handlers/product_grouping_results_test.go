package handlers

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/vietbui/chat-quality-agent/servicequality"
)

func TestBuildProductGroupingResultGroupsIncludesCountsAndProvenance(t *testing.T) {
	from := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	to := from.AddDate(0, 1, 0)
	at := from.AddDate(0, 0, 2)
	insight := func(summary, level string, products ...map[string]string) json.RawMessage {
		raw, err := json.Marshal(map[string]interface{}{
			"summary":      summary,
			"products":     products,
			"lead_quality": map[string]string{"level": level, "reason": "Có ý định mua", "evidence": "Cho mình giá"},
		})
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	score := 84
	report := &servicequality.Report{From: from, To: to, Rows: []servicequality.Row{
		{ConversationID: "conv-b", CustomerName: "Bình", ChannelID: "page-1", ChannelName: "Fanpage 1", InsightAt: &at, InsightJobID: "insight-job",
			Insight:         insight("Hỏi hai cách gọi", " HIGH ", map[string]string{"name": "LS2 FF 818", "evidence": "FF 818 giá bao nhiêu?"}, map[string]string{"name": "Mũ FF818", "evidence": "Mũ FF818 còn không?"}),
			QualityAnalysis: &servicequality.QualityAnalysis{Verdict: "PASS", Score: &score, Review: "Tư vấn đủ", Violations: []servicequality.QualityViolation{}}, QualityAnalysisStale: true},
		{ConversationID: "conv-a", CustomerName: "An", ChannelID: "page-2", ChannelName: "Fanpage 2", InsightAt: &at,
			Insight: insight("Hỏi mẫu FF818", "medium", map[string]string{"name": "Mũ FF818", "sku": "SKU-1", "evidence": "Mẫu này còn hàng?"})},
		{ConversationID: "conv-stale", InsightAt: &at, InsightStale: true,
			Insight: insight("Cũ", "high", map[string]string{"name": "LS2 FF 818", "evidence": "cũ"})},
		{ConversationID: "conv-spam", InsightAt: &at,
			Insight: insight("Spam", "spam", map[string]string{"name": "LS2 FF 818", "evidence": "spam"})},
		{ConversationID: "conv-to-boundary", InsightAt: &to,
			Insight: insight("Ngoài cửa sổ", "high", map[string]string{"name": "LS2 FF 818", "evidence": "boundary"})},
		{ConversationID: "conv-excluded", CustomerName: "Cường", InsightAt: &at,
			Insight: insight("Chỉ nhắc thương hiệu", "low", map[string]string{"name": "LS2", "evidence": "Có LS2 không?"})},
	}}

	groups, excluded := buildProductGroupingResultGroups(report, []serviceQualityProductGroup{
		{Name: "FF818", Members: []string{"LS2 FF 818", "Mũ FF818"}},
		{Name: "", Members: []string{"LS2"}},
	})

	if !reflect.DeepEqual(excluded, []string{"LS2"}) {
		t.Fatalf("excluded labels = %#v", excluded)
	}
	if len(groups) != 2 || groups[0].Name != "FF818" || groups[0].Count != 2 || !reflect.DeepEqual(groups[0].Members, []string{"LS2 FF 818", "Mũ FF818"}) {
		t.Fatalf("group summary = %#v", groups)
	}
	if groups[1].Name != "" || groups[1].Count != 1 || len(groups[1].Sources) != 1 || groups[1].Sources[0].ConversationID != "conv-excluded" {
		t.Fatalf("omitted group lost provenance: %#v", groups[1])
	}
	if got := []string{groups[0].Sources[0].ConversationID, groups[0].Sources[1].ConversationID}; !reflect.DeepEqual(got, []string{"conv-a", "conv-b"}) {
		t.Fatalf("sources are not stable unique conversations: %#v", got)
	}
	first := groups[0].Sources[0]
	if first.CustomerName != "An" || first.LeadQuality.Level != "medium" || first.Summary != "Hỏi mẫu FF818" || len(first.MatchedProducts) != 1 || first.MatchedProducts[0].SKU != "SKU-1" {
		t.Fatalf("missing insight provenance: %#v", first)
	}
	second := groups[0].Sources[1]
	if len(second.MatchedProducts) != 2 || second.QualityAnalysis == nil || second.QualityAnalysis.Score == nil || *second.QualityAnalysis.Score != 84 || !second.QualityStale || second.LeadQuality.Level != "high" || second.InsightJobID != "insight-job" {
		t.Fatalf("missing product or quality provenance: %#v", second)
	}
}

func TestInvalidCachedProductGroupingLeavesRecoverablePendingResponse(t *testing.T) {
	from := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	report := &servicequality.Report{From: from, To: from.AddDate(0, 0, 1), GeneratedAt: from, Pages: []servicequality.Page{}, Rows: []servicequality.Row{}}
	for _, encoded := range []string{"corrupt", mustEncodeProductGroupingCache(t, []serviceQualityProductGroup{{Name: "E-24", Members: []string{"unknown"}}})} {
		response := pendingProductGroupingResultsResponse(report, "", []string{"E-24"})
		if err := applyCachedProductGroupingResults(&response, report, encoded, nil); err == nil {
			t.Fatalf("invalid cache accepted: %q", encoded)
		}
		if response.Status != "pending" || response.GroupingReady || !reflect.DeepEqual(response.ProductNames, []string{"E-24"}) || len(response.Groups) != 0 {
			t.Fatalf("invalid cache did not remain recoverable: %#v", response)
		}
	}
}

func mustEncodeProductGroupingCache(t *testing.T, groups []serviceQualityProductGroup) string {
	t.Helper()
	encoded, err := encodeProductGroupingCache(groups)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func TestBuildProductGroupingResultGroupsCountsConversationOncePerGroup(t *testing.T) {
	from := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	at := from.Add(time.Hour)
	raw := json.RawMessage(`{"products":[{"name":"E-24","evidence":"một"},{"name":"E-24","evidence":"hai"}],"lead_quality":{"level":"high","evidence":"hỏi mua"}}`)
	report := &servicequality.Report{From: from, To: from.AddDate(0, 0, 1), Rows: []servicequality.Row{{ConversationID: "conv-1", InsightAt: &at, Insight: raw}}}

	groups, _ := buildProductGroupingResultGroups(report, []serviceQualityProductGroup{{Name: "E-24", Members: []string{"E-24"}}})
	if len(groups) != 1 || groups[0].Count != 1 || len(groups[0].Sources) != 1 || len(groups[0].Sources[0].MatchedProducts) != 2 {
		t.Fatalf("duplicate mentions changed conversation count: %#v", groups)
	}
}
