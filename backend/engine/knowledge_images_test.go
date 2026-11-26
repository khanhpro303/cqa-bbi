package engine

import (
	"slices"
	"strings"
	"testing"
)

func TestParseKnowledgeImageReply(t *testing.T) {
	const first = "cea6c2f1-40bc-4c42-a256-e2f676d24e9d"
	const second = "1410386e-87f7-4b56-a975-7c0bc2805745"
	const product = "[PRODUCT_IMAGE:1]"
	marker := func(id string) string { return "[KB_IMAGE:" + id + "]" }
	for _, tc := range []struct {
		name, reply, clean string
		max                int
		ids                []string
	}{
		{"plain", "Có ạ. ", "Có ạ. ", 1, nil},
		{"valid", "Có ạ.\n" + marker(first), "Có ạ.", 1, []string{first}},
		{"spaces and newline", "Có ạ.\r\n [ kb_image :\r\n " + strings.ToUpper(first) + " ]", "Có ạ.", 1, []string{first}},
		{"mixed", "Có ạ.\n" + marker(first) + "\n" + product, "Có ạ.\n\n" + product, 1, []string{first}},
		{"product first", "Có ạ.\n" + product + "\n" + marker(first), "Có ạ.\n" + product, 1, []string{first}},
		{"deduplicated in order", "Có ạ.\n" + marker(first) + "\n" + marker(second) + "\n" + marker(first), "Có ạ.", 2, []string{first, second}},
		{"cap", "Có ạ.\n" + marker(first) + "\n" + marker(second), "Có ạ.", 1, []string{first}},
		{"malformed", "Có ạ.\n[KB_IMAGE:bad\n[KB_IMAGE:]", "Có ạ.", 1, nil},
		{"malformed multiline", "Có ạ.\n[KB_IMAGE:\n broken ]", "Có ạ.", 1, nil},
		{"inline stripped", "Có " + marker(first) + " ạ.", "Có  ạ.", 1, nil},
		{"leading stripped", marker(first) + "\nCó ạ.", "Có ạ.", 1, nil},
		{"empty", marker(first), "", 1, nil},
		{"only product marker after stripping", marker(first) + "\n" + product, product, 1, nil},
		{"media disabled", "Có ạ.\n" + marker(first), "Có ạ.", 0, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clean, ids := ParseKnowledgeImageReply(tc.reply, tc.max)
			if clean != tc.clean || !slices.Equal(ids, tc.ids) {
				t.Fatalf("got %q / %v, want %q / %v", clean, ids, tc.clean, tc.ids)
			}
		})
	}
}
