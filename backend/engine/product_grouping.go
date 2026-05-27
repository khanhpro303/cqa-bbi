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

// BuildNumberedWebNameList renders the groups as "1. <WebName>\n2. <WebName>"
// for embedding in the ERP query tool output. The Langflow agent's
// DISAMBIGUATION rule scans this exact format to map a follow-up "1/2/3"
// reply back to a group.
func BuildNumberedWebNameList(groups []WebGroupMatch) string {
	if len(groups) == 0 {
		return ""
	}
	var b strings.Builder
	for i, g := range groups {
		if i > 0 {
			b.WriteByte('\n')
		}
		fmt.Fprintf(&b, "%d. %s", i+1, g.WebName)
	}
	return b.String()
}

// FlattenWebGroupParentCodes returns the union of all parent codes across
// the supplied groups (in the order groups appear). Used to keep the legacy
// `parent_codes` JSON field populated for downstream consumers that still
// reason in MA_CHA terms.
func FlattenWebGroupParentCodes(groups []WebGroupMatch) []string {
	seen := make(map[string]struct{})
	var out []string
	for _, g := range groups {
		for _, code := range g.ParentCodes {
			if _, ok := seen[code]; ok {
				continue
			}
			seen[code] = struct{}{}
			out = append(out, code)
		}
	}
	return out
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
