package engine

import (
	"slices"
	"testing"
)

func TestParsePersonalZaloKnowledgeImageReply(t *testing.T) {
	const first = "cea6c2f1-40bc-4c42-a256-e2f676d24e9d"
	const second = "1410386e-87f7-4b56-a975-7c0bc2805745"
	const third = "aa123abc-03e1-4000-8350-93eabde031f4"
	reply := "Dạ đây là ảnh Goby ạ.\n[ KB_IMAGE:" + first + "]\n[KB_IMAGE:" + second +
		"]\n[KB_IMAGE:" + first + "]\n[KB_IMAGE:" + third + "]"
	text, ids := ParsePersonalZaloKnowledgeImageReply(reply)
	if text != "Dạ đây là ảnh Goby ạ." || !slices.Equal(ids, []string{first, second, third}) {
		t.Fatalf("wrong text or image order: %q / %v", text, ids)
	}
}
