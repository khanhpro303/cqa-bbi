package workers

import (
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/vietbui/chat-quality-agent/engine"
	"github.com/vietbui/chat-quality-agent/knowledge"
)

const (
	advicePassageRunes = 575
	adviceWebsiteRunes = 2300
	advicePassageLimit = 5
)

var (
	adviceFeatureHeading = regexp.MustCompile(`\b(?:dac diem|dac tinh|tinh nang|thong so|ky thuat|uu diem|thiet ke|chat lieu|cau tao|cong nghe|tieu chuan|chung nhan|features|specifications|materials|construction|certification)\b`)
	adviceOtherHeading   = regexp.MustCompile(`^(?:san pham (?:lien quan|tuong tu|hot|noi bat|ban chay)|co the ban quan tam|gioi thieu ve xuat xu|danh gia|binh luan|doi tac|cac chinh sach|chinh sach|lien he|ho tro truc tuyen|related products|you may also like|reviews|customer reviews|shipping policy)\b`)
	adviceStandardTerms  = regexp.MustCompile(`\b(?:tieu chuan|chung nhan|dat chuan|en [0-9]{3,}|ece [0-9]{2,}|dot fmvss|level [0-9]|wr rating|ce certified|certified)\b`)
	adviceFeatureTerms   = regexp.MustCompile(`\b(?:co cao|mat ca|got chan|ngon chan|phan quang|da bo|chong (?:tham|nuoc|truot|va dap|mai mon|tia uv)|thoang khi|lot (?:bang|vai|thao roi)|vat lieu|chat lieu|thong gio|pinlock|carbon|abs|polycarbonate|polyester|gore tex|waterproof|reflective|breathable|impact protection|leather|ventilation|abrasion resistant)\b`)
)

// productAdvicePassages selects source excerpts from across the stored page.
// Prefer specifications and concrete features and omit unrelated page sections.
func productAdvicePassages(source string) []string {
	type passage struct {
		text  string
		score int
	}
	var candidates []passage
	seen := make(map[string]bool)
	featureSection, ignoredHeadingDepth := false, 0
	for line := range strings.SplitSeq(source, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if title := strings.TrimLeft(line, "#"); title != line && strings.HasPrefix(title, " ") {
			depth := len(line) - len(title)
			if ignoredHeadingDepth > 0 && depth > ignoredHeadingDepth {
				continue
			}
			heading := strings.Join(adviceTokens(title), " ")
			ignoredHeadingDepth = 0
			if adviceOtherHeading.MatchString(heading) {
				ignoredHeadingDepth = depth
			}
			featureSection = adviceFeatureHeading.MatchString(heading)
			if ignoredHeadingDepth > 0 || (!adviceStandardTerms.MatchString(heading) && !adviceFeatureTerms.MatchString(heading)) {
				continue
			}
			line = title
		}
		if ignoredHeadingDepth > 0 {
			continue
		}
		for _, chunk := range knowledge.SplitChunks(line, advicePassageRunes, 0) {
			chunk = engine.UntrustedText(chunk)
			if chunk == "" || seen[chunk] {
				continue
			}
			seen[chunk] = true
			normalized := strings.Join(adviceTokens(chunk), " ")
			score := 8*len(adviceStandardTerms.FindAllStringIndex(normalized, 2)) +
				3*len(adviceFeatureTerms.FindAllStringIndex(normalized, 4))
			if featureSection {
				score += 2
			}
			candidates = append(candidates, passage{chunk, score})
		}
	}

	slices.SortStableFunc(candidates, func(a, b passage) int { return b.score - a.score })
	remaining := adviceWebsiteRunes
	var selected []string
	for _, candidate := range candidates {
		if candidate.score == 0 && candidates[0].score > 0 {
			break
		}
		size := utf8.RuneCountInString(candidate.text)
		if size > remaining {
			continue
		}
		selected = append(selected, candidate.text)
		remaining -= size
		if len(selected) == advicePassageLimit {
			break
		}
	}
	return selected
}
