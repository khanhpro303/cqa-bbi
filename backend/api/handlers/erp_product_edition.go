package handlers

import (
	"regexp"
	"strings"
)

var romanProductEdition = regexp.MustCompile(`(?i)^(?:x{1,3}(?:ix|iv|v?i{0,3})?|ix|iv|vi{0,3}|i{1,3})$`)

func hasRomanProductEdition(search string) bool {
	words := strings.Fields(search)
	return len(words) >= 2 && romanProductEdition.MatchString(words[len(words)-1])
}

func matchRomanProductEdition(products []map[string]interface{}, search string) []map[string]interface{} {
	if !hasRomanProductEdition(search) {
		return products
	}
	words := strings.Fields(search)
	for i, word := range words {
		words[i] = regexp.QuoteMeta(word)
	}
	// LIKE matches "Storm II" inside "Storm III". Require a full model
	// phrase, while still allowing variant text such as "Storm II - White L".
	model := regexp.MustCompile(`(?i)(?:^|[^\pL\pM\pN])` + strings.Join(words, `[\s\pZ]+`) + `(?:$|[^\pL\pM\pN])`)
	matched := products[:0]
	for _, product := range products {
		if model.MatchString(getMapString(product, "TEN_DONG_BO_WEB")) || model.MatchString(getMapString(product, "TEN")) {
			matched = append(matched, product)
		}
	}
	return matched
}
