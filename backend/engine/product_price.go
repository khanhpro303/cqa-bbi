package engine

import (
	"fmt"
	"strconv"
	"strings"
)

// FormatVNDPrice renders a VND amount with dot thousands separators and a "đ"
// suffix (e.g. 1290000 -> "1.290.000đ"). It is the single source of truth for
// price formatting shared by the ERP handler (resource="products") and the
// worker disambiguation price reply, so a price never renders two different
// ways depending on which layer answered.
func FormatVNDPrice(price float64) string {
	value := strconv.FormatInt(int64(price+0.5), 10)
	var parts []string
	for len(value) > 3 {
		parts = append([]string{value[len(value)-3:]}, parts...)
		value = value[:len(value)-3]
	}
	parts = append([]string{value}, parts...)
	return strings.Join(parts, ".") + "đ"
}

// FormatPriceRange renders a min–max VND range. Equal bounds collapse to a
// single price; a non-positive bound returns "Liên hệ" (price unavailable).
func FormatPriceRange(minPrice, maxPrice float64) string {
	if minPrice <= 0 || maxPrice <= 0 {
		return "Liên hệ"
	}
	if minPrice == maxPrice {
		return FormatVNDPrice(minPrice)
	}
	return fmt.Sprintf("%s - %s", FormatVNDPrice(minPrice), FormatVNDPrice(maxPrice))
}

// PriceRangeOfPrices returns the min and max of the strictly-positive prices in
// the slice. Zero / negative prices are ignored (catalog rows with no sell
// price). Returns (0, 0) when none qualify, which FormatPriceRange renders as
// "Liên hệ".
func PriceRangeOfPrices(prices []float64) (minPrice, maxPrice float64) {
	for _, p := range prices {
		if p <= 0 {
			continue
		}
		if minPrice == 0 || p < minPrice {
			minPrice = p
		}
		if p > maxPrice {
			maxPrice = p
		}
	}
	return minPrice, maxPrice
}
