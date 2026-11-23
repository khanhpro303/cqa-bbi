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
	// "mã ticket: #66656", "ticket số 123", "case #A-12", "số phiếu 4567"
	caseRefLabelled = regexp.MustCompile(`(?i)(?:\bmã\s+|\bsố\s+)?(?:\bticket|\bcase|\bphiếu|\bmã\s+hồ\s+sơ|\bmã\s+khiếu\s+nại|\bmã\s+yêu\s+cầu)\s*(?:số|id|no\.?)?\s*[:：]?\s*(?:#\s*[A-Za-z0-9][A-Za-z0-9-]*|\d{2,}[A-Za-z0-9-]*)`)
	// a bare "#66656"
	caseRefHash = regexp.MustCompile(`#\d{3,}\b`)
	// what removing a reference can leave behind
	emptyParens = regexp.MustCompile(`\(\s*[,;:]?\s*\)`)
	spaceBefore = regexp.MustCompile(`[ \t]+([,.;:!?)])`)
	multiSpace  = regexp.MustCompile(`[ \t]{2,}`)
	doubleStop  = regexp.MustCompile(`([^.])\.\.([^.]|$)`)
)

// StripRefs removes ticket and case number references from s.
func StripRefs(s string) string {
	if s == "" {
		return s
	}
	out := caseRefLabelled.ReplaceAllString(s, "")
	out = caseRefHash.ReplaceAllString(out, "")
	if out == s {
		return s
	}
	out = emptyParens.ReplaceAllString(out, "")
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
