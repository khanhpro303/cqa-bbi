package handlers

import "testing"

func TestUnresolvedERPProductResultRequiresScopedEmptyLookup(t *testing.T) {
	for _, test := range []struct {
		resource string
		body     string
		want     bool
	}{
		{"inventory", "{\"status\":\"success\",\"lookup_outcome\":\"no_visible_match\",\"data\":[]}", true},
		{"inventory", "{\"status\":\"success\",\"data\":[{\"available\":0}]}", false},
		{"orders", "{\"status\":\"success\",\"lookup_outcome\":\"no_visible_match\",\"data\":[]}", false},
		{"inventory", "{\"status\":\"error\",\"lookup_outcome\":\"no_visible_match\"}", false},
		{"products", "{\"status\":\"success\",\"data\":[]}", false},
	} {
		if got := unresolvedERPProductResult(test.resource, []byte(test.body)); got != test.want {
			t.Fatalf("%s %s: got %t, want %t", test.resource, test.body, got, test.want)
		}
	}
}
