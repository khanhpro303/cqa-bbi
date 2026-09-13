package ai

import "testing"

func TestParseClassificationConfigSupportsLegacyAndProfile(t *testing.T) {
	legacy := ParseClassificationConfig(`[{"name":"Hỏi giá","description":"Khách hỏi giá"}]`)
	if legacy.MessengerInsightsEnabled() || len(legacy.Rules) != 1 {
		t.Fatalf("legacy array parsed incorrectly: %+v", legacy)
	}

	profile := ParseClassificationConfig(`{"profile":"messenger_insights","rules":[{"name":"Feedback","description":"Khách góp ý"}]}`)
	if !profile.MessengerInsightsEnabled() || len(profile.Rules) != 1 {
		t.Fatalf("profile object parsed incorrectly: %+v", profile)
	}
}

func TestParseClassificationResponseNormalizesInsights(t *testing.T) {
	response, err := ParseClassificationResponse(`{
		"tags": [],
		"summary": "  Khách hỏi mua Serum A.  ",
		"insights": {
			"intents": [" Hỏi hàng ", "hỏi hàng", ""],
			"products": [
				{"name":" Serum A ","sku":" ","evidence":" Serum A còn hàng không? "},
				{"name":"Sản phẩm thiếu bằng chứng","sku":"SKU-1","evidence":""}
			],
			"feedback": [
				{"category":" sản phẩm ","sentiment":"ANGRY","evidence":" dùng bị rát "},
				{"category":"","sentiment":"positive","evidence":"tốt"}
			],
			"lead_quality":{"level":"VIP","evidence":" ","reason":" chưa đủ dữ kiện "}
		}
	}`)
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	if response.Insights == nil {
		t.Fatal("expected structured insights")
	}
	if response.Summary != "Khách hỏi mua Serum A." {
		t.Fatalf("summary not trimmed: %q", response.Summary)
	}
	if len(response.Insights.Intents) != 1 || response.Insights.Intents[0] != "Hỏi hàng" {
		t.Fatalf("intents not normalized: %#v", response.Insights.Intents)
	}
	if len(response.Insights.Products) != 1 || response.Insights.Products[0].SKU != "" {
		t.Fatalf("products not normalized: %#v", response.Insights.Products)
	}
	if len(response.Insights.Feedback) != 1 || response.Insights.Feedback[0].Sentiment != "unknown" {
		t.Fatalf("feedback not normalized: %#v", response.Insights.Feedback)
	}
	if response.Insights.LeadQuality.Level != "unknown" {
		t.Fatalf("invalid lead level should normalize to unknown: %#v", response.Insights.LeadQuality)
	}
}

func TestParseClassificationResponseLeavesLegacyInsightsAbsent(t *testing.T) {
	response, err := ParseClassificationResponse(`{"tags":[],"summary":"Không có nhãn"}`)
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	if response.Insights != nil {
		t.Fatal("legacy response must not create structured insights")
	}
}

func TestParseClassificationResponseRequiresEvidenceForLeadLevel(t *testing.T) {
	response, err := ParseClassificationResponse(`{
		"tags":[],
		"summary":"Khách hỏi mua hàng.",
		"insights":{
			"intents":["Mua hàng"],
			"products":[],
			"feedback":[],
			"lead_quality":{"level":"high","evidence":"  ","reason":"Có ý định mua"}
		}
	}`)
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	if response.Insights.LeadQuality.Level != "unknown" {
		t.Fatalf("lead quality without evidence must be unknown: %#v", response.Insights.LeadQuality)
	}
}
