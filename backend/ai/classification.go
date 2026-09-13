package ai

import (
	"encoding/json"
	"strings"
)

const MessengerInsightsProfile = "messenger_insights"

type ClassificationRule struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Severity    string `json:"severity,omitempty"`
}

type ClassificationConfig struct {
	Profile string               `json:"profile,omitempty"`
	Rules   []ClassificationRule `json:"rules"`
}

// ParseClassificationConfig accepts both the legacy rules array and the
// profile-aware object used by Messenger insights jobs.
func ParseClassificationConfig(raw string) ClassificationConfig {
	var legacyRules []ClassificationRule
	if err := json.Unmarshal([]byte(raw), &legacyRules); err == nil {
		return ClassificationConfig{Rules: legacyRules}
	}

	var config ClassificationConfig
	if err := json.Unmarshal([]byte(raw), &config); err != nil {
		return ClassificationConfig{}
	}
	return config
}

func (c ClassificationConfig) MessengerInsightsEnabled() bool {
	return c.Profile == MessengerInsightsProfile
}

type ClassificationTag struct {
	RuleName    string  `json:"rule_name"`
	Confidence  float64 `json:"confidence"`
	Evidence    string  `json:"evidence"`
	Explanation string  `json:"explanation"`
}

type ProductInsight struct {
	Name     string `json:"name"`
	SKU      string `json:"sku"`
	Evidence string `json:"evidence"`
}

type FeedbackInsight struct {
	Category  string `json:"category"`
	Sentiment string `json:"sentiment"`
	Evidence  string `json:"evidence"`
}

type LeadQualityInsight struct {
	Level    string `json:"level"`
	Evidence string `json:"evidence"`
	Reason   string `json:"reason"`
}

type ConversationInsights struct {
	Intents     []string           `json:"intents"`
	Products    []ProductInsight   `json:"products"`
	Feedback    []FeedbackInsight  `json:"feedback"`
	LeadQuality LeadQualityInsight `json:"lead_quality"`
}

type ClassificationResponse struct {
	Tags     []ClassificationTag   `json:"tags"`
	Summary  string                `json:"summary"`
	Insights *ConversationInsights `json:"insights,omitempty"`
}

// ParseClassificationResponse normalizes structured insights while retaining
// the existing tags/summary response. A nil Insights value means an old AI
// payload and must not create a conversation_insight record.
func ParseClassificationResponse(raw string) (ClassificationResponse, error) {
	var response ClassificationResponse
	if err := json.Unmarshal([]byte(raw), &response); err != nil {
		return ClassificationResponse{}, err
	}
	response.Summary = strings.TrimSpace(response.Summary)
	if response.Insights != nil {
		normalizeConversationInsights(response.Insights)
	}
	return response, nil
}

func normalizeConversationInsights(insights *ConversationInsights) {
	seenIntents := make(map[string]bool)
	normalizedIntents := make([]string, 0, len(insights.Intents))
	for _, intent := range insights.Intents {
		intent = strings.TrimSpace(intent)
		key := strings.ToLower(intent)
		if intent != "" && !seenIntents[key] {
			seenIntents[key] = true
			normalizedIntents = append(normalizedIntents, intent)
		}
	}
	insights.Intents = normalizedIntents

	normalizedProducts := make([]ProductInsight, 0, len(insights.Products))
	for _, product := range insights.Products {
		product.Name = strings.TrimSpace(product.Name)
		product.SKU = strings.TrimSpace(product.SKU)
		product.Evidence = strings.TrimSpace(product.Evidence)
		if product.Name != "" && product.Evidence != "" {
			normalizedProducts = append(normalizedProducts, product)
		}
	}
	insights.Products = normalizedProducts

	validSentiments := map[string]bool{"positive": true, "neutral": true, "negative": true, "mixed": true, "unknown": true}
	normalizedFeedback := make([]FeedbackInsight, 0, len(insights.Feedback))
	for _, feedback := range insights.Feedback {
		feedback.Category = strings.TrimSpace(feedback.Category)
		feedback.Sentiment = strings.ToLower(strings.TrimSpace(feedback.Sentiment))
		feedback.Evidence = strings.TrimSpace(feedback.Evidence)
		if !validSentiments[feedback.Sentiment] {
			feedback.Sentiment = "unknown"
		}
		if feedback.Category != "" && feedback.Evidence != "" {
			normalizedFeedback = append(normalizedFeedback, feedback)
		}
	}
	insights.Feedback = normalizedFeedback

	insights.LeadQuality.Level = strings.ToLower(strings.TrimSpace(insights.LeadQuality.Level))
	insights.LeadQuality.Evidence = strings.TrimSpace(insights.LeadQuality.Evidence)
	insights.LeadQuality.Reason = strings.TrimSpace(insights.LeadQuality.Reason)
	validLeadLevels := map[string]bool{"high": true, "medium": true, "low": true, "spam": true, "unknown": true}
	if !validLeadLevels[insights.LeadQuality.Level] {
		insights.LeadQuality.Level = "unknown"
	}
	if insights.LeadQuality.Level != "unknown" && insights.LeadQuality.Evidence == "" {
		insights.LeadQuality.Level = "unknown"
	}
}
