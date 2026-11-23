package knowledge

import (
	"strings"
	"unicode/utf8"
)

// Chunk sizes in characters (runes). About 1,200 characters of Vietnamese is
// 300-400 tokens: small enough that a hit is on topic, big enough to keep a
// policy clause together.
const (
	ChunkTargetChars  = 1200
	ChunkOverlapChars = 200
)

// SplitChunks splits text into passages of about target runes. It breaks at
// paragraph boundaries first, then at sentence ends, then at spaces, and
// carries the last overlap runes of each passage into the next so a sentence
// cut at a boundary is still found whole.
func SplitChunks(text string, target, overlap int) []string {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	if target <= 0 {
		target = ChunkTargetChars
	}
	if overlap < 0 || overlap >= target/2 {
		overlap = target / 6
	}
	var pieces []string
	for _, para := range strings.Split(text, "\n\n") {
		para = strings.TrimSpace(para)
		if para == "" {
			continue
		}
		if utf8.RuneCountInString(para) <= target {
			pieces = append(pieces, para)
			continue
		}
		pieces = append(pieces, splitLong(para, target)...)
	}

	var chunks []string
	var cur strings.Builder
	curLen := 0
	flush := func() {
		s := strings.TrimSpace(cur.String())
		if s != "" {
			chunks = append(chunks, s)
		}
		cur.Reset()
		curLen = 0
		if overlap > 0 && s != "" {
			tail := tailRunes(s, overlap)
			cur.WriteString(tail)
			curLen = utf8.RuneCountInString(tail)
		}
	}
	for _, p := range pieces {
		pl := utf8.RuneCountInString(p)
		if curLen > 0 && curLen+2+pl > target+overlap {
			flush()
		}
		if curLen > 0 {
			cur.WriteString("\n\n")
			curLen += 2
		}
		cur.WriteString(p)
		curLen += pl
	}
	if s := strings.TrimSpace(cur.String()); s != "" {
		// Skip a final piece that is only the carried-over overlap.
		if len(chunks) == 0 || !strings.HasSuffix(chunks[len(chunks)-1], s) {
			chunks = append(chunks, s)
		}
	}
	return chunks
}

// splitLong cuts one long paragraph at line breaks, sentence ends or spaces.
func splitLong(para string, target int) []string {
	var out []string
	runes := []rune(para)
	for len(runes) > target {
		cut := -1
		for _, seps := range [][]rune{{'\n'}, {'.', '!', '?', ';', '。'}, {' '}} {
			for i := target; i > target/2; i-- {
				if containsRune(seps, runes[i-1]) {
					cut = i
					break
				}
			}
			if cut > 0 {
				break
			}
		}
		if cut <= 0 {
			cut = target
		}
		out = append(out, strings.TrimSpace(string(runes[:cut])))
		runes = runes[cut:]
	}
	if s := strings.TrimSpace(string(runes)); s != "" {
		out = append(out, s)
	}
	return out
}

func containsRune(set []rune, r rune) bool {
	for _, s := range set {
		if s == r {
			return true
		}
	}
	return false
}

// tailRunes returns about the last n runes of s, starting at a word boundary.
func tailRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	t := r[len(r)-n:]
	if i := indexRune(t, ' '); i >= 0 && i < len(t)-1 {
		t = t[i+1:]
	}
	return string(t)
}

func indexRune(r []rune, c rune) int {
	for i, x := range r {
		if x == c {
			return i
		}
	}
	return -1
}
