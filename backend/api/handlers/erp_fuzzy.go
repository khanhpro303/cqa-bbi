package handlers

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/vietbui/chat-quality-agent/ai"
	"github.com/vietbui/chat-quality-agent/config"
	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/db/models"
	"github.com/vietbui/chat-quality-agent/engine"
	"gorm.io/gorm"
)

// getAIClient creates an AI provider client based on tenant settings
// (provider, model, API key, optional base URL). Used by every fuzzy
// matching path that needs to ask an LLM for a single best-of-N pick.
func getAIClient(ctx context.Context, tenantID, task string) (ai.AIProvider, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}
	resolved, err := engine.ResolveTenantAIProvider(tenantID, cfg, task)
	if err != nil {
		return nil, err
	}
	if resolved.Client == nil {
		return nil, fmt.Errorf("API key not configured for provider %s", resolved.Provider)
	}
	return resolved.Client, nil
}

// fuzzyMatchMaChaWithLLM uses the LLM to find the best matching ma_cha
// from the local cached_products table when SQL LIKE returns no results.
// The prompt includes a representative ten_dong_bo_web + ten per ma_cha so
// the LLM can bridge name-level variations (e.g. "Storm 3" ↔ "Storm III",
// "mu bao hiem" ↔ "mũ bảo hiểm") in addition to code-format variants.
//
// scopes narrow the candidate parents (e.g. engine.ExcludeDKCopies for
// catalog lookups, where "_DK" copy parents would take slots from real lines).
func fuzzyMatchMaChaWithLLM(ctx context.Context, tenantID, keyword string, scopes ...func(*gorm.DB) *gorm.DB) (string, error) {
	type maChaCandidate struct {
		MaCha        string `gorm:"column:ma_cha"`
		TenDongBoWeb string `gorm:"column:ten_dong_bo_web"`
		Ten          string `gorm:"column:ten"`
	}
	var candidates []maChaCandidate
	err := db.DB.WithContext(ctx).Scopes(scopes...).
		Table((&models.CachedProduct{}).TableName()).
		Select("ma_cha, MAX(ten_dong_bo_web) AS ten_dong_bo_web, MAX(ten) AS ten").
		Where("tenant_id = ? AND ma_cha != ''", tenantID).
		Group("ma_cha").
		Scan(&candidates).Error
	if err != nil || len(candidates) == 0 {
		return "", fmt.Errorf("no ma_cha candidates found: %w", err)
	}

	const fuzzyCandidateCap = 1500
	if len(candidates) > fuzzyCandidateCap {
		log.Printf("[fuzzy] tenant=%s has %d ma_cha, truncating to %d for LLM prompt",
			tenantID, len(candidates), fuzzyCandidateCap)
		candidates = candidates[:fuzzyCandidateCap]
	}

	aiClient, err := getAIClient(ctx, tenantID, engine.PromptProductCodeMatch)
	if err != nil || aiClient == nil {
		return "", fmt.Errorf("AI client not available: %w", err)
	}

	lines := make([]string, 0, len(candidates))
	for _, c := range candidates {
		webName := strings.TrimSpace(c.TenDongBoWeb)
		ten := strings.TrimSpace(c.Ten)
		label := webName
		if label == "" {
			label = ten
		}
		if label == "" {
			lines = append(lines, c.MaCha)
			continue
		}
		if webName != "" && ten != "" && !strings.EqualFold(webName, ten) {
			label = webName + " — " + ten
		}
		lines = append(lines, fmt.Sprintf("%s | %s", c.MaCha, label))
	}

	systemPrompt := engine.BuildTaskPrompt(tenantID, engine.PromptProductCodeMatch, map[string]string{
		"keyword":    strconv.Quote(keyword),
		"candidates": strings.Join(lines, "\n"),
	})

	llmCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	resp, err := aiClient.AnalyzeChat(llmCtx, systemPrompt, keyword)
	if err != nil {
		return "", err
	}

	matched := strings.TrimSpace(resp.Content)
	matched = strings.Trim(matched, "`'\"")
	if matched == "" || strings.EqualFold(matched, "NONE") {
		return "", nil
	}

	for _, c := range candidates {
		if strings.EqualFold(c.MaCha, matched) {
			return c.MaCha, nil
		}
	}

	return "", nil
}

// fuzzyMatchAttributesWithLLM resolves user-supplied attribute filters
// (typed in Vietnamese / mixed language) against the canonical stored
// values for a parent SKU. Used when the SQL filter returns zero rows
// because, for example, the cache stores "Gloss Black" but the customer
// asked for "đen bóng".
//
// One LLM call resolves color/size/brand together to keep latency and
// cost low. The LLM is constrained to return values from the supplied
// candidate lists (or NONE), and the result is validated against those
// lists before being returned.
func fuzzyMatchAttributesWithLLM(
	ctx context.Context,
	tenantID, color, size, brand string,
	availableColors, availableSizes, availableBrands []string,
) (matchedColor, matchedSize, matchedBrand string, err error) {
	// Skip when the caller gave nothing to resolve.
	if strings.TrimSpace(color) == "" && strings.TrimSpace(size) == "" && strings.TrimSpace(brand) == "" {
		return "", "", "", nil
	}

	aiClient, clientErr := getAIClient(ctx, tenantID, engine.PromptAttributeMatch)
	if clientErr != nil || aiClient == nil {
		return "", "", "", fmt.Errorf("AI client not available: %w", clientErr)
	}

	// Build a compact bilingual-matching prompt. The LLM gets every
	// candidate set and must respond with line-tagged values it picked
	// from those sets (or NONE), nothing else.
	systemPrompt := engine.BuildTaskPrompt(tenantID, engine.PromptAttributeMatch, map[string]string{
		"color":  strconv.Quote(color),
		"size":   strconv.Quote(size),
		"brand":  strconv.Quote(brand),
		"colors": joinForPrompt(availableColors),
		"sizes":  joinForPrompt(availableSizes),
		"brands": joinForPrompt(availableBrands),
	})

	llmCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	resp, llmErr := aiClient.AnalyzeChat(llmCtx, systemPrompt, "Map thuộc tính theo hướng dẫn.")
	if llmErr != nil {
		return "", "", "", llmErr
	}

	matchedColor = pickValidatedAttribute(parseAttributeLine(resp.Content, "COLOR"), availableColors)
	matchedSize = pickValidatedAttribute(parseAttributeLine(resp.Content, "SIZE"), availableSizes)
	matchedBrand = pickValidatedAttribute(parseAttributeLine(resp.Content, "BRAND"), availableBrands)
	return matchedColor, matchedSize, matchedBrand, nil
}

// joinForPrompt renders a candidate list inline for the LLM prompt, with
// a visible placeholder when the list is empty so the model never sees a
// blank section it might hallucinate around.
func joinForPrompt(values []string) string {
	if len(values) == 0 {
		return "(không có giá trị nào)"
	}
	return strings.Join(values, " | ")
}

// parseAttributeLine extracts the value following "TAG:" from a multi-line
// LLM response. Case-insensitive on the tag itself; trims whitespace and
// surrounding quote/backtick wrappers from the value.
func parseAttributeLine(body, tag string) string {
	prefix := tag + ":"
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(strings.ToUpper(trimmed), strings.ToUpper(prefix)) {
			value := strings.TrimSpace(trimmed[len(prefix):])
			value = strings.Trim(value, "`'\"")
			return value
		}
	}
	return ""
}

// pickValidatedAttribute returns candidate iff it appears (case-insensitive)
// in the allowed list. NONE / empty / hallucinated values become "".
func pickValidatedAttribute(candidate string, allowed []string) string {
	c := strings.TrimSpace(candidate)
	if c == "" || strings.EqualFold(c, "NONE") {
		return ""
	}
	for _, a := range allowed {
		if strings.EqualFold(a, c) {
			return a
		}
	}
	return ""
}
