package engine

import (
	"strings"
	"unicode"
)

// UntrustedText prepares text a user (or a model summarising a user) wrote
// for inclusion in a system prompt. It folds line breaks and control
// characters into single spaces, so the text cannot open a new line that
// reads like a fresh instruction, and drops < and >, so it cannot close the
// data tag it is placed in.
func UntrustedText(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	space := false
	for _, r := range s {
		switch {
		case r == '<' || r == '>':
			continue
		case unicode.IsSpace(r) || unicode.IsControl(r):
			space = true
			continue
		}
		if space && b.Len() > 0 {
			b.WriteByte(' ')
		}
		space = false
		b.WriteRune(r)
	}
	return b.String()
}
