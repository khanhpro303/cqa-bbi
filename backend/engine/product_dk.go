package engine

import (
	"strings"

	"gorm.io/gorm"
)

// dkCopySuffix marks the ERP "_DK" copies of real product lines: SKUs such as
// SP000556_DK under parent SP000557_DK sit beside the real SP000557 line with
// DON_GIA_BAN 0 and colour/size "false". They hold real stock, so stock sums
// keep them, but the bot must not show them when it prices a line, lists its
// colours/sizes or searches products (LS2 FF399 answered "Liên hệ", 22/11).
const dkCopySuffix = "_DK"

func hasDKSuffix(code string) bool {
	return strings.HasSuffix(strings.ToUpper(strings.TrimSpace(code)), dkCopySuffix)
}

// IsDKCopy reports whether a product row is an ERP "_DK" copy, by its own
// code or its parent code.
func IsDKCopy(ma, maCha string) bool {
	return hasDKSuffix(ma) || hasDKSuffix(maCha)
}

// RealParentOf maps a "_DK" copy parent code to the real line's code
// (SP000557_DK -> SP000557). Other codes come back trimmed and unchanged.
func RealParentOf(maCha string) string {
	code := strings.TrimSpace(maCha)
	if hasDKSuffix(code) {
		return strings.TrimSpace(code[:len(code)-len(dkCopySuffix)])
	}
	return code
}

// DKCopyParentOf returns the "_DK" copy parent code of a real parent
// (SP000557 -> SP000557_DK); a code that already is a copy comes back as is.
func DKCopyParentOf(maCha string) string {
	code := strings.TrimSpace(maCha)
	if code == "" || hasDKSuffix(code) {
		return code
	}
	return code + dkCopySuffix
}

// ExcludeDKCopies is a GORM scope that keeps "_DK" copy rows out of a
// cached_products catalog query, so they neither show up nor use up the
// query's LIMIT or decide whether a search pass "matched". Stock queries must
// not use it. ESCAPE keeps "_" literal whatever the server's sql_mode;
// UPPER(TRIM()) matches IsDKCopy's case- and padding-insensitive check.
func ExcludeDKCopies(tx *gorm.DB) *gorm.DB {
	return tx.Where("UPPER(TRIM(COALESCE(ma, ''))) NOT LIKE ? ESCAPE '!' AND UPPER(TRIM(COALESCE(ma_cha, ''))) NOT LIKE ? ESCAPE '!'", dkLikePattern, dkLikePattern)
}

const dkLikePattern = "%!" + dkCopySuffix
