package engine

import (
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"github.com/vietbui/chat-quality-agent/db/models"
	"golang.org/x/text/unicode/norm"
)

var (
	orderPhonePattern   = regexp.MustCompile(`(?:^|\D)(?:\+?84|0)(?:\D*\d){9}(?:\D|$)`)
	trackingCodePattern = regexp.MustCompile(
		`(?i)(?:ma\s+(?:van\s+don|van\s+chuyen|giao\s+hang|bill)|mvd|tracking(?:\s+(?:number|no))?|bill)` +
			`(?:\s+cua\s+(?:minh|chi|anh|ban))?(?:\s+(?:la|code|so))?\.?\s*[:=#-]?\s*([a-z0-9][a-z0-9._-]{5,})`,
	)
	orderTotalPattern = regexp.MustCompile(
		`(?i)(?:tong(?:\s+(?:tien(?:\s+don(?:\s+nay)?)?|don(?:\s+(?:hang|nay))?|thanh\s+toan|cong|bill|chot\s+don))?|thanh\s+tien|` +
			`(?:can|con\s+phai)\s+thanh\s+toan|so\s+tien\s+(?:can\s+)?(?:thanh\s+toan|chuyen\s+khoan|ck))` +
			`\s*(?:la|[:=-])?\s*([0-9][0-9.,\s]{0,20})\s*(k|nghin|ngan|₫|d|vnd)(?:\s|[.,;!?)\]]|$)`,
	)
	streetAddressPattern = regexp.MustCompile(`(?:^|\W)\d{1,5}[a-z]?\s+[a-z]{2,}(?:\s+[a-z]{2,})+(?:\W|$)`)
	orderNegationPattern = regexp.MustCompile(`(?:^|\W)(?:chua|khong|chang)\s+(?:co\s+)?(?:tao|len|chot|gui|giao|mua|lay|nhan|huy|hoan|` +
		`ma\s+(?:van\s+don|bill)|don)(?:\W|$)`)
	orderFuturePattern = regexp.MustCompile(`(?:^|\W)(?:se|sap|mai|lat|ti\s+nua)\s+(?:tao|len|chot|gui|giao|mua|lay|nhan|huy|hoan|ghe|den)(?:\W|$)`)
)

func normalizedEvidenceText(content string) string {
	decomposed := norm.NFD.String(strings.ToLower(content))
	var builder strings.Builder
	for _, r := range decomposed {
		if unicode.Is(unicode.Mn, r) {
			continue
		}
		if r == 'đ' {
			r = 'd'
		}
		builder.WriteRune(r)
	}
	return strings.Join(strings.Fields(builder.String()), " ")
}

func containsAny(text string, fragments ...string) bool {
	for _, fragment := range fragments {
		if strings.Contains(text, fragment) {
			return true
		}
	}
	return false
}

func obviousNonConfirmation(text string) bool {
	trimmed := strings.Trim(text, " .,!?:;\t\r\n")
	if trimmed == "ok" || trimmed == "da ok" || trimmed == "vang" || trimmed == "uh" || trimmed == "uk" {
		return true
	}
	return containsAny(text,
		"ma qr", "quet qr", "bao gia", "gui qr", "chuyen khoan vao qr",
		"cho xin thong tin", "xin so dien thoai", "xin sdt", "xin dia chi",
		"se len don", "se tao don", "hua len don", "de shop len don", "de shop tao don",
	)
}

func hasPendingOrNegatedOrderAction(text string) bool {
	return orderNegationPattern.MatchString(text) || orderFuturePattern.MatchString(text) || strings.Contains(text, "?")
}

func validTrackingCode(code string) bool {
	hasLetter := strings.IndexFunc(code, func(r rune) bool { return r >= 'a' && r <= 'z' }) >= 0
	hasDigit := strings.IndexFunc(code, func(r rune) bool { return r >= '0' && r <= '9' }) >= 0
	if !hasDigit || orderPhonePattern.MatchString(code) {
		return false
	}
	// Mixed carrier codes are strong evidence. Pure-numeric codes are accepted
	// only when long enough not to be a common price such as 450000.
	return hasLetter || len(strings.Trim(code, "._-")) >= 8
}

func customerSuppliedPhoneAndAddressBefore(messages []models.Message, confirmation models.Message) bool {
	hasPhone := false
	hasAddress := false
	for _, message := range messages {
		if message.SenderType != "customer" || message.SentAt.After(confirmation.SentAt) {
			continue
		}
		text := normalizedEvidenceText(message.Content)
		hasPhone = hasPhone || orderPhonePattern.MatchString(text)
		if containsAny(text, "chua co dia chi", "dia chi chua", "se gui dia chi", "gui dia chi sau", "xin dia chi") {
			continue
		}
		addressSignal := containsAny(text,
			"dia chi", " duong ", " phuong ", " quan ", " huyen ", " tinh ", " thanh pho ", " tp.",
			" ap ", " xa ", " thon ", " khom ", " to ", " khu pho ",
		) || strings.HasPrefix(text, "ap ") || strings.HasPrefix(text, "xa ") || strings.HasPrefix(text, "thon ") || streetAddressPattern.MatchString(text)
		if addressSignal && !strings.Contains(text, "?") {
			// A phone number alone next to the word "address" is not an address.
			withoutPhones := orderPhonePattern.ReplaceAllString(text, " ")
			withoutLabel := strings.NewReplacer("dia chi", "", "sdt", "", "so dien thoai", "").Replace(withoutPhones)
			hasAddress = hasAddress || len(strings.Fields(strings.Trim(withoutLabel, " ,:;-"))) >= 2
		}
	}
	return hasPhone && hasAddress
}

// validOrderConfirmation accepts only deterministic evidence forms from the
// order rules. Ambiguous purchase language remains for AI review rather than
// being promoted to a confirmed order by the persistence layer.
func validOrderConfirmation(message models.Message, messages []models.Message) bool {
	if message.SenderType != "customer" && message.SenderType != "agent" {
		return false
	}
	text := normalizedEvidenceText(message.Content)
	if text == "" || obviousNonConfirmation(text) || hasPendingOrNegatedOrderAction(text) || containsAny(text, "neu ") {
		return false
	}

	if message.SenderType == "agent" {
		if match := trackingCodePattern.FindStringSubmatch(text); len(match) > 1 {
			if validTrackingCode(match[1]) {
				return true
			}
		}
		created := containsAny(text,
			"da tao don", "da len don", "tao don thanh cong", "len don thanh cong", "len don xong", "tao don xong",
			"don da duoc tao", "don minh tao xong", "don cua minh len xong",
			"nhan don", "da nhan don", "shop nhan don", "ben shop nhan don",
			"chot don", "da chot don",
		)
		return created && customerSuppliedPhoneAndAddressBefore(messages, message)
	}

	visitedStore := containsAny(text,
		"da den cua hang", "da toi cua hang", "da qua cua hang", "da ghe cua hang", "vua den cua hang", "vua ghe cua hang",
		"da qua shop", "da den shop", "da toi shop", "da ghe shop", "vua qua shop", "vua ghe shop",
		"tai cua hang", "o cua hang", "truc tiep tai shop", "truc tiep o shop", "tai shop", "o shop",
	)
	purchasedOrPickedUp := containsAny(text,
		"da mua", "mua roi", "mua xong", "da lay", "lay roi", "lay xong", "lay hang roi", "lay hang xong", "da nhan", "nhan roi", "nhan xong",
		"lay san pham roi", "mua tai cua hang", "mua o cua hang", "lay tai cua hang", "nhan tai cua hang roi",
		"mua truc tiep tai shop", "mua truc tiep o shop", "mua truc tiep tai cua hang", "mua truc tiep o cua hang",
		"mua o shop roi", "mua tai shop roi", "lay hang o shop roi", "lay hang tai shop roi", "nhan hang o shop roi",
	)
	return visitedStore && purchasedOrPickedUp
}

func validOrderTransition(status string, message models.Message) bool {
	if message.SenderType != "customer" && message.SenderType != "agent" {
		return false
	}
	text := normalizedEvidenceText(message.Content)
	if text == "" || strings.Contains(text, "?") || orderFuturePattern.MatchString(text) || containsAny(text, "neu ") {
		return false
	}
	switch status {
	case "cancelled":
		cancelled := containsAny(text,
			"don da huy", "da huy don", "huy don nay", "huy don nhe", "huy don", "huy dh-", "da huy", "huy thanh cong",
			"khach bo don", "khach khong lay nua", "khach khong nhan nua", "don bi huy", "don duoc huy",
		)
		return cancelled && !containsAny(text, "chua huy", "khong huy", "chang huy")
	case "refunded":
		refunded := containsAny(text,
			"da hoan tien", "da duoc hoan tien", "da nhan lai tien", "hoan tien thanh cong", "refund thanh cong",
			"refund roi", "tien da hoan", "tien da ve", "tien ve tai khoan roi", "tien ve tk roi", "da chuyen tra tien", "da chuyen khoan tra lai",
		)
		return refunded && !containsAny(text, "chua hoan", "khong hoan", "chang hoan")
	default:
		return false
	}
}

func clearOrderTotal(content string, expected int64) bool {
	content = normalizedEvidenceText(content)
	for _, match := range orderTotalPattern.FindAllStringSubmatch(content, -1) {
		digits := regexp.MustCompile(`\D`).ReplaceAllString(match[1], "")
		value, err := strconv.ParseInt(digits, 10, 64)
		if err == nil && (match[2] == "k" || match[2] == "nghin" || match[2] == "ngan") {
			value *= 1000
		}
		if err == nil && value == expected {
			return true
		}
	}
	return false
}

func customerAcceptedQuotedTotal(confirmation models.Message, expected int64) bool {
	if confirmation.SenderType != "customer" {
		return false
	}
	text := normalizedEvidenceText(confirmation.Content)
	if obviousNonConfirmation(text) {
		return false
	}
	accepted := containsAny(text,
		"chot don", "minh lay", "em lay", "toi lay", "dong y tong", "dong y gia", "lay nhe", "lay nha",
		"ok chot", "chot nhe", "chot nha", "dat don", "dat giup",
	)
	return accepted && (clearOrderTotal(text, expected) || containsAny(text, "gia tren", "tong tren", "muc gia nay", "gia nay", "so tien nay"))
}

func validOrderAmount(message models.Message, value int64, messages []models.Message, confirmation models.Message) bool {
	if value <= 0 || (message.SenderType != "customer" && message.SenderType != "agent") {
		return false
	}
	text := normalizedEvidenceText(message.Content)
	if containsAny(text, "ma qr", "quet qr", "gui qr", "so dien thoai", "sdt", "gia niem yet", "don gia", "gia san pham") || !clearOrderTotal(text, value) {
		return false
	}
	if strings.Contains(text, "?") {
		return false
	}
	if !message.SentAt.Before(confirmation.SentAt) {
		return true
	}
	if !customerAcceptedQuotedTotal(confirmation, value) {
		return false
	}
	for _, candidate := range messages {
		if candidate.ID == message.ID && candidate.SentAt.After(confirmation.SentAt) {
			return false
		}
	}
	return true
}

// validOrderReview requires a concrete order signal; generic acknowledgement,
// a price quote or a request for contact details is not an order.
func validOrderReview(message models.Message) bool {
	if message.SenderType != "agent" && message.SenderType != "customer" {
		return false
	}
	text := normalizedEvidenceText(message.Content)
	if obviousNonConfirmation(text) {
		return false
	}
	return containsAny(text,
		"don hang", "don nay", "don cua", "da dat", "dat don", "ma van don", "mvd", "tracking", "ma don",
		"huy don", "hoan tien", "refund", "giao don", "nhan don",
	)
}
