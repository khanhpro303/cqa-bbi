package pkg

import (
	"strings"
	"time"

	"github.com/google/uuid"
)

// UnverifiedZaloDisplayName is the placeholder display name Zalo returns for
// users who have not authorized profile sharing with the OA. It is not a real
// name: persisting it pollutes customer/sender records and renders as garbled
// Vietnamese in the AI-cost chart, masking the genuine "unknown sender" state.
const UnverifiedZaloDisplayName = "Khách chưa xác thực"

// IsUnverifiedPlaceholder checks if the display name is an unverified user placeholder
// returned by Zalo (either "Khách chưa xác thực", "Khách chưa xác định", or common garbled variants).
func IsUnverifiedPlaceholder(name string) bool {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return true
	}
	lower := strings.ToLower(trimmed)
	// Check for correct UTF-8 strings
	if strings.Contains(lower, "khách chưa") ||
		strings.Contains(lower, "chưa xác thực") ||
		strings.Contains(lower, "chưa xác định") {
		return true
	}
	// Check for common garbled UTF-8/ISO-8859-1 (Mojibake) strings
	if strings.Contains(lower, "khã¡ch chæ°a") ||
		strings.Contains(lower, "chæ°a xã¡c thá»±c") ||
		strings.Contains(lower, "chæ°a xã¡c ä‘á»‹nh") ||
		strings.Contains(lower, "chæ°a xã¡c") {
		return true
	}
	return false
}

// SanitizeZaloDisplayName drops Zalo's unverified-user placeholder, returning ""
// so callers fall back to their normal unknown-sender handling instead of
// caching a fake name. Any other (real) name is returned unchanged.
func SanitizeZaloDisplayName(name string) string {
	if IsUnverifiedPlaceholder(name) {
		return ""
	}
	return name
}

// VNLocation is the Asia/Ho_Chi_Minh timezone (UTC+7).
var VNLocation *time.Location

func init() {
	var err error
	VNLocation, err = time.LoadLocation("Asia/Ho_Chi_Minh")
	if err != nil {
		// Fallback to fixed UTC+7 if tzdata not available
		VNLocation = time.FixedZone("ICT", 7*60*60)
	}
}

// ToVN converts a time.Time to Vietnam timezone.
func ToVN(t time.Time) time.Time {
	return t.In(VNLocation)
}

// NewUUID generates a new UUID v4 string.
func NewUUID() string {
	return uuid.New().String()
}

// MaskSecret masks a secret string, showing only last 4 chars.
// e.g. "sk-ant-abc123xyz" → "sk-ant-****3xyz"
func MaskSecret(s string) string {
	if len(s) <= 4 {
		return "****"
	}
	visible := s[len(s)-4:]
	return "****" + visible
}
