package handlers

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/vietbui/chat-quality-agent/config"
	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/db/models"
	"github.com/vietbui/chat-quality-agent/engine"
)

// searchVariantsByAttributes returns child variants under a parent SKU
// (ma_cha) that match the optional color/size/brand attribute filters.
//
// Filters use case-insensitive substring matching against:
//   - thuoc_tinh_1 (color, e.g. "đen bóng")
//   - thuoc_tinh_2 (size, e.g. "L", "size L")
//   - nhan_hieu_name (brand)
//
// tenantID is always enforced as the first WHERE clause so the LLM agent
// cannot leak data across tenants by manipulating other arguments.
func searchVariantsByAttributes(ctx context.Context, tenantID, parentCode, color, size, brand string, limit int) ([]map[string]interface{}, error) {
	parentCode = strings.TrimSpace(parentCode)
	if parentCode == "" {
		return nil, fmt.Errorf("parent_code is required for variant attribute search")
	}
	if limit <= 0 {
		limit = 20
	}

	query := db.DB.WithContext(ctx).
		Where("tenant_id = ?", tenantID).
		Where("ma_cha = ?", parentCode)
	if c := strings.TrimSpace(color); c != "" {
		query = query.Where("LOWER(thuoc_tinh_1) LIKE LOWER(?)", "%"+c+"%")
	}
	if s := normalizeSizeFilter(size); s != "" {
		// Size uses exact case-insensitive equality so "L" does not also
		// match "XL" / "XXL". The agent strips the "size " prefix via
		// normalizeSizeFilter before the comparison.
		query = query.Where("LOWER(thuoc_tinh_2) = LOWER(?)", s)
	}
	if b := strings.TrimSpace(brand); b != "" {
		query = query.Where("LOWER(nhan_hieu_name) LIKE LOWER(?)", "%"+b+"%")
	}

	var products []models.CachedProduct
	if err := query.Limit(limit).Find(&products).Error; err != nil {
		return nil, fmt.Errorf("local MySQL variant attribute query failed: %w", err)
	}

	results := make([]map[string]interface{}, 0, len(products))
	for _, p := range products {
		results = append(results, mapCachedProductToAPIResponse(map[string]interface{}{
			"MA":                 p.MA,
			"ma":                 p.MA,
			"code":               p.MA,
			"ma_hang":            p.MA,
			"product_code":       p.MA,
			"TEN_DONG_BO_WEB":    p.TEN_DONG_BO_WEB,
			"TEN":                p.TEN,
			"ten":                p.TEN,
			"ten_hang":           p.TEN,
			"THUOC_TINH_1":       p.THUOC_TINH_1,
			"THUOC_TINH_2":       p.THUOC_TINH_2,
			"DON_GIA_BAN":        p.DON_GIA_BAN,
			"LINK_ANH":           p.LINK_ANH,
			"NHAN_HIEU_NAME":     p.NHAN_HIEU_NAME,
			"LIST_TEN_NHOM_VTHH": p.LIST_TEN_NHOM_VTHH,
			"KHO":                p.KHO,
			"MA_CHA":             p.MA_CHA,
			"ma_cha":             p.MA_CHA,
			"DVT":                p.DVT,
		}))
	}
	return results, nil
}

// resolveParentMaCha turns the model/parent code the agent supplies (e.g.
// "FF901") into the internal parent SKU (ma_cha, e.g. "SP458484") via the Astra
// hybrid (label) search. It exists because the agent passes a *human* model code
// that only appears in the product label — it is NOT the cache's ma_cha value,
// so every exact `ma_cha = parent_code` filter returns zero for these queries.
//
// Returns the resolved ma_cha when the top hybrid hit's label actually carries
// parentCode (identity via label, see parentCodeInLabel); returns "" when fuzzy
// is disabled, the call errors, nothing passes the relevance floor, or the match
// does not look like the requested model. Callers should fall back to treating
// parentCode itself as the ma_cha.
func resolveParentMaCha(ctx context.Context, tenantID, parentCode, brand string) string {
	parentCode = strings.TrimSpace(parentCode)
	if parentCode == "" {
		return ""
	}

	appCfg, cfgErr := config.Load()
	if cfgErr != nil {
		log.Printf("[erp_query] parent resolve config load error: %v", cfgErr)
		return ""
	}
	if !appCfg.ERPEmbeddingFuzzyEnabled {
		return ""
	}
	embedCfg := engine.ProductEmbeddingConfig{
		AstraEndpoint: appCfg.AstraDBAPIEndpoint,
		AstraToken:    appCfg.AstraDBToken,
		AstraKeyspace: appCfg.AstraDBKeyspace,
	}

	keyword := strings.TrimSpace(strings.Join([]string{parentCode, brand}, " "))
	match, embedErr := engine.FuzzyMatchProductWithEmbedding(ctx, embedCfg, tenantID, keyword)
	if embedErr != nil {
		log.Printf("[erp_query] parent resolve embedding error: %v", embedErr)
		return ""
	}
	resolved := strings.TrimSpace(match.MaCha)
	if resolved == "" {
		return ""
	}
	// Trust the resolution only when the matched product's label carries the
	// requested model code — this is the "identity via label" guard that
	// replaces the broken `ma_cha == parent_code` assumption.
	if !parentCodeInLabel(parentCode, match.Label) {
		log.Printf("[erp_query] parent resolve rejected: model=%s not in label=%q (ma_cha=%s)",
			parentCode, match.Label, resolved)
		return ""
	}
	log.Printf("[erp_query] parent resolved model=%s → ma_cha=%s", parentCode, resolved)
	return resolved
}

// hybridMatchVariant runs the same Astra hybrid search the products flow uses
// (BM25 lexical + vector via FuzzyMatchProductWithEmbedding, erp.go:396), but
// scoped to a single parent SKU. It exists because searchVariantsByAttributes
// matches MySQL with exact size equality + substring colour, which silently
// misses real variants when the cache stores size as "Size L" / "L (40)" or the
// colour in another language ("trắng" vs the stored "Gloss White"). The hybrid
// index embeds each SKU as "<parent> — <colour> — <size>", so a query like
// "FF901 trắng L" resolves the right SKU regardless of those storage quirks.
//
// parentCode is the agent-supplied model code (used in the keyword and as the
// label identity check); effectiveParent is the resolved internal ma_cha (from
// resolveParentMaCha) used to confine results to the right parent. The guard
// matters because the hybrid filter only scopes tenant_id, so a sibling parent's
// SKU could otherwise leak into a variant answer.
//
// Returns the pinpointed variant rows (one SKU), or nil when embedding fuzzy is
// disabled, the config/Astra call errors, no specific SKU is found, or the top
// match belongs to a different parent.
func hybridMatchVariant(ctx context.Context, tenantID, parentCode, effectiveParent, color, size, brand string) []map[string]interface{} {
	keyword := strings.TrimSpace(strings.Join([]string{parentCode, color, size, brand}, " "))
	if keyword == "" {
		return nil
	}

	appCfg, cfgErr := config.Load()
	if cfgErr != nil {
		log.Printf("[erp_query] variant embedding config load error: %v", cfgErr)
		return nil
	}
	if !appCfg.ERPEmbeddingFuzzyEnabled {
		return nil
	}
	embedCfg := engine.ProductEmbeddingConfig{
		AstraEndpoint: appCfg.AstraDBAPIEndpoint,
		AstraToken:    appCfg.AstraDBToken,
		AstraKeyspace: appCfg.AstraDBKeyspace,
	}

	match, embedErr := engine.FuzzyMatchProductWithEmbedding(ctx, embedCfg, tenantID, keyword)
	if embedErr != nil {
		log.Printf("[erp_query] variant embedding fuzzy error: %v", embedErr)
		return nil
	}
	// match.MA is only populated when the top SKU dominates its siblings
	// (Specific). A variant query already names parent + attributes, so the
	// pinpoint case is exactly what we want; a non-specific result falls through
	// to the existing bilingual LLM remap.
	if match.MA == "" {
		return nil
	}
	// Identity guard: accept when the match belongs to the resolved parent SKU
	// OR the matched label carries the requested model code. This replaces the
	// old `ma_cha == parent_code` check, which always failed because the agent's
	// parent_code is a model name, not the internal ma_cha.
	if !variantBelongsToParent(parentCode, effectiveParent, match.MaCha, match.Label) {
		log.Printf("[erp_query] variant embedding matched MA=%s ma_cha=%s label=%q but does not belong to model=%s parent=%s; discarding",
			match.MA, match.MaCha, match.Label, parentCode, effectiveParent)
		return nil
	}

	rows, fetchErr := getProductByMaFromAstraDB(ctx, tenantID, match.MA)
	if fetchErr != nil {
		log.Printf("[erp_query] variant embedding fetch MA=%s failed: %v", match.MA, fetchErr)
		return nil
	}
	// Defensive re-check against the fetched row: never return a SKU that does
	// not belong to this parent. Compare to the resolved ma_cha when we have it,
	// otherwise fall back to the model-code-in-label check.
	for _, r := range rows {
		rc := strings.TrimSpace(getFirstNonEmptyMapString(r, "MA_CHA", "ma_cha"))
		if !variantBelongsToParent(parentCode, effectiveParent, rc, getFirstNonEmptyMapString(r, "TEN_DONG_BO_WEB", "ten_dong_bo_web", "TEN", "ten")) {
			return nil
		}
	}
	return rows
}

// variantBelongsToParent reports whether a matched SKU (identified by its ma_cha
// and label/name) belongs to the requested parent. It accepts either an exact
// ma_cha match against the resolved internal parent (effectiveParent) or the
// model code (parentCode) appearing in the label — so it works both when the
// parent has been resolved to a real ma_cha and when it has not.
func variantBelongsToParent(parentCode, effectiveParent, candidateMaCha, candidateLabel string) bool {
	if ep := strings.TrimSpace(effectiveParent); ep != "" {
		if strings.EqualFold(strings.TrimSpace(candidateMaCha), ep) {
			return true
		}
	}
	return parentCodeInLabel(parentCode, candidateLabel)
}

// parentCodeInLabel reports whether the model/parent code the agent supplied
// (e.g. "FF901") appears in the product label, ignoring case, spaces and
// dashes. This is the "identity via label" check that lets a human model code
// resolve to the right product even though it never equals the internal ma_cha.
func parentCodeInLabel(parentCode, label string) bool {
	p := normalizeMatchToken(parentCode)
	if p == "" {
		return false
	}
	return strings.Contains(normalizeMatchToken(label), p)
}

// normalizeMatchToken lowercases and strips spaces/dashes so codes like
// "FF 901", "ff-901" and "FF901" all compare equal.
func normalizeMatchToken(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.ReplaceAll(s, " ", "")
	s = strings.ReplaceAll(s, "-", "")
	return s
}

// filterVariantsByAttributes applies the same case-insensitive substring
// matching used by searchVariantsByAttributes to an in-memory variant list.
// Empty filter strings are skipped (do not constrain the result).
func filterVariantsByAttributes(variants []map[string]interface{}, color, size, brand string) []map[string]interface{} {
	color = strings.ToLower(strings.TrimSpace(color))
	size = strings.ToLower(normalizeSizeFilter(size))
	brand = strings.ToLower(strings.TrimSpace(brand))
	if color == "" && size == "" && brand == "" {
		return variants
	}

	out := make([]map[string]interface{}, 0, len(variants))
	for _, v := range variants {
		if color != "" {
			val := strings.ToLower(getFirstNonEmptyMapString(v, "THUOC_TINH_1", "thuoc_tinh_1"))
			if !strings.Contains(val, color) {
				continue
			}
		}
		if size != "" {
			// Size uses exact case-insensitive equality after normalisation
			// so "L" does not also match "XL"/"XXL".
			val := strings.ToLower(strings.TrimSpace(getFirstNonEmptyMapString(v, "THUOC_TINH_2", "thuoc_tinh_2")))
			if val != size {
				continue
			}
		}
		if brand != "" {
			val := strings.ToLower(getFirstNonEmptyMapString(v, "NHAN_HIEU_NAME", "nhan_hieu_name"))
			if !strings.Contains(val, brand) {
				continue
			}
		}
		out = append(out, v)
	}
	return out
}

// normalizeSizeFilter strips common prefixes ("size ", "size-", "kích cỡ ")
// and trims surrounding whitespace so that "size L" and "L" both compare
// equally against the stored thuoc_tinh_2 value.
func normalizeSizeFilter(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ""
	}
	lower := strings.ToLower(s)
	for _, prefix := range []string{"size ", "size-", "size:", "kích cỡ ", "co ", "cỡ "} {
		if strings.HasPrefix(lower, prefix) {
			s = strings.TrimSpace(s[len(prefix):])
			break
		}
	}
	return s
}

// collectAvailableAttributes returns sorted unique non-empty values of
// thuoc_tinh_1 (colors), thuoc_tinh_2 (sizes) and nhan_hieu_name (brands)
// from the supplied variants. Used both to populate the "did you mean"
// fallback message and to give the LLM the candidate set for bilingual
// attribute matching when a filter returns zero rows.
func collectAvailableAttributes(variants []map[string]interface{}) (colors, sizes, brands []string) {
	colorSet := make(map[string]struct{})
	sizeSet := make(map[string]struct{})
	brandSet := make(map[string]struct{})
	for _, v := range variants {
		if c := strings.TrimSpace(getFirstNonEmptyMapString(v, "THUOC_TINH_1", "thuoc_tinh_1")); c != "" {
			colorSet[c] = struct{}{}
		}
		if s := strings.TrimSpace(getFirstNonEmptyMapString(v, "THUOC_TINH_2", "thuoc_tinh_2")); s != "" {
			sizeSet[s] = struct{}{}
		}
		if b := strings.TrimSpace(getFirstNonEmptyMapString(v, "NHAN_HIEU_NAME", "nhan_hieu_name")); b != "" {
			brandSet[b] = struct{}{}
		}
	}
	return sortedStringKeys(colorSet), sortedStringKeys(sizeSet), sortedStringKeys(brandSet)
}

// slimVariantsForLLM produces a compact representation of variants for the
// Langflow agent. Each entry carries the SKU, the matched attributes, the
// concrete price, brand, and image link so the LLM can answer
// variant-specific price/stock questions without parsing the full record.
func slimVariantsForLLM(variants []map[string]interface{}) []map[string]interface{} {
	const maxVariants = 10
	slim := make([]map[string]interface{}, 0, len(variants))
	for _, v := range variants {
		if len(slim) >= maxVariants {
			break
		}
		slim = append(slim, map[string]interface{}{
			"ma":             getFirstNonEmptyMapString(v, "MA", "ma", "ma_hang"),
			"name":           getFirstNonEmptyMapString(v, "TEN_DONG_BO_WEB", "ten_dong_bo_web", "TEN", "ten"),
			"color":          getFirstNonEmptyMapString(v, "THUOC_TINH_1", "thuoc_tinh_1"),
			"size":           getFirstNonEmptyMapString(v, "THUOC_TINH_2", "thuoc_tinh_2"),
			"price":          getMapFloat(v, "DON_GIA_BAN", "don_gia_ban", "price"),
			"nhan_hieu_name": getFirstNonEmptyMapString(v, "NHAN_HIEU_NAME", "nhan_hieu_name"),
			"dvt":            getFirstNonEmptyMapString(v, "DVT", "dvt", "dvt_chinh_id"),
			"link_anh":       getFirstNonEmptyMapString(v, "LINK_ANH", "link_anh"),
		})
	}
	return slim
}

// sortedStringKeys returns the keys of a set in stable lexicographic order.
// Kept dependency-free (no slices.Sort) for compatibility with the Go
// toolchain version pinned by this module.
func sortedStringKeys(set map[string]struct{}) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j-1] > out[j]; j-- {
			out[j-1], out[j] = out[j], out[j-1]
		}
	}
	return out
}
