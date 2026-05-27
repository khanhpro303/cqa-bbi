package handlers

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/vietbui/chat-quality-agent/ai"
	"github.com/vietbui/chat-quality-agent/config"
	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/db/models"
	"github.com/vietbui/chat-quality-agent/pkg"
)

// getAIClient creates an AI provider client based on tenant settings
// (provider, model, API key, optional base URL). Used by every fuzzy
// matching path that needs to ask an LLM for a single best-of-N pick.
func getAIClient(ctx context.Context, tenantID string) (ai.AIProvider, error) {
	provider := "claude"
	var providerSetting models.AppSetting
	if err := db.DB.Where("tenant_id = ? AND setting_key = 'ai_provider'", tenantID).First(&providerSetting).Error; err == nil && providerSetting.ValuePlain != "" {
		provider = providerSetting.ValuePlain
	}

	var setting models.AppSetting
	keyFound := false
	for _, key := range ai.ProviderAPIKeySettingKeys(provider) {
		if err := db.DB.Where("tenant_id = ? AND setting_key = ?", tenantID, key).First(&setting).Error; err == nil {
			keyFound = true
			break
		}
	}

	cfg, _ := config.Load()
	apiKey := ""
	if keyFound {
		if setting.ValueEncrypted != nil && len(setting.ValueEncrypted) > 0 {
			decrypted, err := pkg.Decrypt(setting.ValueEncrypted, cfg.EncryptionKey)
			if err != nil {
				return nil, fmt.Errorf("failed to decrypt API key: %w", err)
			}
			apiKey = string(decrypted)
		} else {
			apiKey = setting.ValuePlain
		}
	}

	if apiKey == "" {
		if provider == "openai" {
			apiKey = cfg.LangflowAPIKey
		}
	}

	if apiKey == "" {
		return nil, fmt.Errorf("API key not configured for provider %s", provider)
	}

	model := "claude-haiku-4-5"
	if provider == "gemini" {
		model = "gemini-2.0-flash"
	} else if provider == "openai" {
		model = "gpt-5-mini"
	}
	var modelSetting models.AppSetting
	if err := db.DB.Where("tenant_id = ? AND setting_key = 'ai_model'", tenantID).First(&modelSetting).Error; err == nil && modelSetting.ValuePlain != "" {
		model = modelSetting.ValuePlain
	}

	var baseURL string
	var baseURLSetting models.AppSetting
	if err := db.DB.Where("tenant_id = ? AND setting_key = 'ai_base_url'", tenantID).First(&baseURLSetting).Error; err == nil {
		baseURL = baseURLSetting.ValuePlain
	}

	switch provider {
	case "claude":
		return ai.NewClaudeProvider(apiKey, model, cfg.AIMaxTokens, baseURL), nil
	case "gemini":
		return ai.NewGeminiProvider(apiKey, model, baseURL), nil
	case "openai":
		return ai.NewOpenAIProvider(apiKey, model, baseURL), nil
	default:
		return nil, fmt.Errorf("unsupported AI provider: %s", provider)
	}
}

// fuzzyMatchMaChaWithLLM uses the LLM to find the best matching ma_cha
// from the local cached_products table when SQL LIKE returns no results.
func fuzzyMatchMaChaWithLLM(ctx context.Context, tenantID, keyword string) (string, error) {
	// 1. Fetch unique parent product codes (ma_cha) from cache
	var maChaList []string
	err := db.DB.Model(&models.CachedProduct{}).
		Where("tenant_id = ?", tenantID).
		Distinct("ma_cha").
		Where("ma_cha != ''").
		Pluck("ma_cha", &maChaList).Error
	if err != nil || len(maChaList) == 0 {
		return "", fmt.Errorf("no ma_cha values found: %w", err)
	}

	// 2. Create AI client
	aiClient, err := getAIClient(ctx, tenantID)
	if err != nil || aiClient == nil {
		return "", fmt.Errorf("AI client not available: %w", err)
	}

	// 3. Prompt LLM to match the keyword
	systemPrompt := fmt.Sprintf(`Bạn là trợ lý mapping mã sản phẩm.
Nhiệm vụ: Khách hàng tìm kiếm "%s". Hãy chọn MÃ SẢN PHẨM khớp nhất từ danh sách bên dưới.
Lưu ý:
- Bỏ qua dấu gạch ngang, dấu chấm, số 0 đầu khi so sánh (VD: E05 = E-5, E5 = E-5, E.5 = E-5)
- Trả lời CHỈ mã sản phẩm khớp nhất, không thêm bất kỳ từ ngữ hay dấu câu nào khác
- Nếu hoàn toàn không có mã nào khớp, trả về "NONE"

Danh sách mã sản phẩm:
%s`, keyword, strings.Join(maChaList, ", "))

	llmCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	resp, err := aiClient.AnalyzeChat(llmCtx, systemPrompt, keyword)
	if err != nil {
		return "", err
	}

	matched := strings.TrimSpace(resp.Content)
	if matched == "" || strings.EqualFold(matched, "NONE") {
		return "", nil
	}

	// Clean up markdown quotes or wrappers if any
	if strings.HasPrefix(matched, "`") && strings.HasSuffix(matched, "`") {
		matched = strings.Trim(matched, "`")
	}

	// Validate matched value exists in our list (case-insensitive)
	for _, mc := range maChaList {
		if strings.EqualFold(mc, matched) {
			return mc, nil
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

	aiClient, clientErr := getAIClient(ctx, tenantID)
	if clientErr != nil || aiClient == nil {
		return "", "", "", fmt.Errorf("AI client not available: %w", clientErr)
	}

	// Build a compact bilingual-matching prompt. The LLM gets every
	// candidate set and must respond with line-tagged values it picked
	// from those sets (or NONE), nothing else.
	systemPrompt := fmt.Sprintf(`Bạn là trợ lý map thuộc tính sản phẩm song ngữ Việt-Anh.

Khách hàng nhập:
- COLOR: %q
- SIZE: %q
- BRAND: %q

Danh sách giá trị HỢP LỆ trong kho (chỉ được chọn từ đây):
- COLORS: %s
- SIZES: %s
- BRANDS: %s

Nhiệm vụ:
- Với mỗi trường khách nhập KHÔNG rỗng, chọn 1 giá trị KHỚP NHẤT từ danh sách tương ứng.
- "Đen bóng" / "den bong" / "black gloss" / "gloss black" → khớp "Gloss Black".
- "Trắng nhám" → "Matt White". "Đỏ" → có thể là "Red" hoặc "Red Matte" tuỳ list.
- Nếu trường khách nhập là rỗng → trả NONE cho trường đó.
- Nếu thật sự không có giá trị nào hợp lý trong list → trả NONE.

CHỈ trả về đúng 3 dòng theo format (không thêm chữ khác):
COLOR: <giá trị từ COLORS hoặc NONE>
SIZE: <giá trị từ SIZES hoặc NONE>
BRAND: <giá trị từ BRANDS hoặc NONE>`,
		color, size, brand,
		joinForPrompt(availableColors),
		joinForPrompt(availableSizes),
		joinForPrompt(availableBrands),
	)

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
