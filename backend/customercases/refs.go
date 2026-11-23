package customercases

import (
	"regexp"
	"strings"
)

// Customers must never be given a ticket or case number. Case ids never reach
// the bot, but free text can carry one: a staff "cập nhật cho khách" copied
// from another system ("Mã ticket: #66656"), or a model that invents one.
// StripRefs removes such references from text going to the bot and from the
// bot's reply.
var (
	// Strong labels name a ticket for sure: "ticket", "mã ticket/case/hồ
	// sơ/khiếu nại/yêu cầu", "số phiếu", "phiếu số", "case id", "case no".
	// Any code with a digit after them is a reference ("Mã ticket ABC-123",
	// "ticket số 66656"). Markdown emphasis may sit anywhere in between.
	caseRefStrong = regexp.MustCompile(`(?i)(?:\b(?:mã|số)\s+(?:ticket|case|phiếu|hồ\s*sơ|khiếu\s*nại|yêu\s*cầu)|\bphiếu\s+số|\bcase\s*(?:id|no\.?)|\bticket)[\s*_]*(?:số|id|no\.?)?[\s*_]*[:：]?[\s*_]*#?[\s*_]*[A-Za-z0-9-]*\d[A-Za-z0-9-]*[*_]*`)
	// Weak labels ("case", "phiếu") need a "#" or a plain number: "case #12",
	// "case 4567", but not "Case FF800 bị nứt" or "phiếu xuất kho XK01".
	caseRefWeak = regexp.MustCompile(`(?i)\b(?:case|phiếu)[\s*_]*(?:số|id|no\.?)?[\s*_]*[:：]?[\s*_]*(?:#[\s*_]*[A-Za-z0-9][A-Za-z0-9-]*|\d{2,}\b)`)
	// a bare "#66656"
	caseRefHash = regexp.MustCompile(`#\d{3,}\b`)
	// what removing a reference can leave behind
	emptyParens = regexp.MustCompile(`\(\s*[,;:]?\s*\)`)
	spaceBefore = regexp.MustCompile(`[ \t]+([,.;:!?)])`)
	multiSpace  = regexp.MustCompile(`[ \t]{2,}`)
	doubleStop  = regexp.MustCompile(`([^.])\.\.([^.]|$)`)
	// Markdown emphasis left empty around a removed label: "**", "__".
	orphanMark = regexp.MustCompile(`(^|\s)[*_]+(\s|$)`)
)

// StripRefs removes ticket and case number references from s.
func StripRefs(s string) string {
	if s == "" {
		return s
	}
	out := cutMatches(caseRefStrong, s)
	out = cutMatches(caseRefWeak, out)
	out = cutMatches(caseRefHash, out)
	if out == s {
		return s
	}
	out = emptyParens.ReplaceAllString(out, "")
	out = orphanMark.ReplaceAllString(out, "$1$2")
	out = spaceBefore.ReplaceAllString(out, "$1")
	out = multiSpace.ReplaceAllString(out, " ")
	out = doubleStop.ReplaceAllString(out, "$1.$2")
	lines := strings.Split(out, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight(l, " \t")
		// A line that held only the reference ("Mã ticket: #66656.").
		if t := strings.Trim(lines[i], " \t.,;:-"); t == "" && strings.TrimSpace(l) != "" {
			lines[i] = ""
		}
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

// cutMatches removes re's matches from s. "_" (markdown emphasis, "__Case
// ID: X__") counts as a word character for \b, so matching runs on a copy
// with "_" read as a space; both are one byte, so the offsets hold.
func cutMatches(re *regexp.Regexp, s string) string {
	locs := re.FindAllStringIndex(strings.ReplaceAll(s, "_", " "), -1)
	if len(locs) == 0 {
		return s
	}
	var b strings.Builder
	last := 0
	for _, l := range locs {
		b.WriteString(s[last:l[0]])
		last = l[1]
	}
	b.WriteString(s[last:])
	return b.String()
}
