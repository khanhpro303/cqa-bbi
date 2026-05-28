package engine

import (
	"fmt"
	"sort"
	"strings"
)

// WebGroupMatch represents a group of product variants that share a
// ten_dong_bo_web (synced web product name). When a product has no
// ten_dong_bo_web, IsFallback is set and WebName carries the raw MA_CHA so
// the option list still renders something, but downstream routing knows to
// fall back to the legacy MA_CHA-based handlers.
type WebGroupMatch struct {
	WebName     string
	ParentCodes []string
	Count       int
	IsFallback  bool
}

// RankProductWebGroups buckets products by TEN_DONG_BO_WEB (case-sensitive,
// trimmed) and returns the groups ordered by descending Count then by WebName
// ascending. Products with an empty web name are grouped under their MA_CHA
// as IsFallback=true. ParentCodes are deduped per group.
func RankProductWebGroups(products []map[string]interface{}) []WebGroupMatch {
	type bucket struct {
		webName     string
		isFallback  bool
		parentCodes []string
		parentSeen  map[string]struct{}
		count       int
	}

	buckets := make(map[string]*bucket)

	for _, product := range products {
		webName := strings.TrimSpace(productGroupingString(product, "TEN_DONG_BO_WEB", "ten_dong_bo_web"))
		parentCode := strings.TrimSpace(productGroupingString(product, "MA_CHA", "ma_cha"))
		if parentCode == "" {
			parentCode = strings.TrimSpace(productGroupingString(product, "MA", "ma_hang", "ma", "code"))
		}

		var key string
		isFallback := false
		if webName != "" {
			key = "web:" + webName
		} else if parentCode != "" {
			key = "fallback:" + parentCode
			webName = parentCode
			isFallback = true
		} else {
			continue
		}

		b, ok := buckets[key]
		if !ok {
			b = &bucket{
				webName:    webName,
				isFallback: isFallback,
				parentSeen: make(map[string]struct{}),
			}
			buckets[key] = b
		}
		b.count++
		if parentCode != "" {
			if _, seen := b.parentSeen[parentCode]; !seen {
				b.parentSeen[parentCode] = struct{}{}
				b.parentCodes = append(b.parentCodes, parentCode)
			}
		}
	}

	groups := make([]WebGroupMatch, 0, len(buckets))
	for _, b := range buckets {
		sort.Strings(b.parentCodes)
		groups = append(groups, WebGroupMatch{
			WebName:     b.webName,
			ParentCodes: b.parentCodes,
			Count:       b.count,
			IsFallback:  b.isFallback,
		})
	}

	sort.Slice(groups, func(i, j int) bool {
		if groups[i].Count != groups[j].Count {
			return groups[i].Count > groups[j].Count
		}
		return groups[i].WebName < groups[j].WebName
	})

	return groups
}

func productGroupingString(m map[string]interface{}, keys ...string) string {
	for _, k := range keys {
		val, ok := m[k]
		if !ok || val == nil {
			continue
		}
		if s, ok := val.(string); ok {
			return s
		}
		return fmt.Sprintf("%v", val)
	}
	return ""
}
