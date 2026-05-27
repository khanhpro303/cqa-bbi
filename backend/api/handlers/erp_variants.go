package handlers

import (
	"context"
	"fmt"
	"strings"

	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/db/models"
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
