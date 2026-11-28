package knowledge

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"golang.org/x/text/unicode/norm"
	"gorm.io/gorm"
)

// MaxDocumentTags caps the tags of one document.
const MaxDocumentTags = 20

// tagExpr is a tag after normalization: letters, digits, spaces, "_", "."
// and "-", starting with a letter or digit, at most 48 characters.
var tagExpr = regexp.MustCompile(`^[\p{L}\p{N}][\p{L}\p{N} _.-]{0,47}$`)

// ErrInvalidTag is returned for a tag that tagExpr rejects.
type ErrInvalidTag struct{ Tag string }

func (e ErrInvalidTag) Error() string { return fmt.Sprintf("nhãn %q không hợp lệ", e.Tag) }

// NormalizeTag returns the stored form of a tag: Unicode NFC, lower case,
// single spaces. Documents and specialist agents use the same form, so a
// tag matches whatever case or Vietnamese input method was used to type it.
func NormalizeTag(tag string) string {
	return strings.ToLower(strings.Join(strings.Fields(norm.NFC.String(tag)), " "))
}

// NormalizeTags normalizes, de-duplicates and sorts tags, dropping empty ones.
func NormalizeTags(tags []string) ([]string, error) {
	seen := make(map[string]bool, len(tags))
	out := make([]string, 0, len(tags))
	for _, tag := range tags {
		tag = NormalizeTag(tag)
		if tag == "" || seen[tag] {
			continue
		}
		if !tagExpr.MatchString(tag) {
			return nil, ErrInvalidTag{Tag: tag}
		}
		seen[tag] = true
		out = append(out, tag)
	}
	sort.Strings(out)
	return out, nil
}

// SplitTagInput reads tags from form values, each of which may itself hold
// several tags separated by commas ("bảo hành, giao hàng").
func SplitTagInput(values []string) []string {
	var out []string
	for _, v := range values {
		out = append(out, strings.Split(v, ",")...)
	}
	return out
}

// whereDocumentTagged keeps documents (alias d) carrying at least one of
// tags. No tags keeps every document. tags must be normalized.
func whereDocumentTagged(q *gorm.DB, tags []string) *gorm.DB {
	if len(tags) == 0 {
		return q
	}
	raw, _ := json.Marshal(tags)
	return q.Where("JSON_OVERLAPS(d.tags, CAST(? AS JSON))", string(raw))
}

// filterTags normalizes a search filter. Invalid entries are dropped rather
// than failing the search; a filter left empty that way matches nothing, so
// a broken agent configuration never widens what the agent can read.
func filterTags(tags []string) (normalized []string, matchNothing bool) {
	asked := 0
	for _, tag := range tags {
		n := NormalizeTag(tag)
		if n == "" {
			continue
		}
		asked++
		if tagExpr.MatchString(n) {
			normalized = append(normalized, n)
		}
	}
	sort.Strings(normalized)
	return normalized, asked > 0 && len(normalized) == 0
}
