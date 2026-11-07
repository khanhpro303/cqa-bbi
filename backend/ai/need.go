package ai

import (
	"strings"
	"unicode"
)

// Customer need groups exported to AdBazooka (ABZ-MSG-BI-01). The codes are a
// fixed enum so no free text leaves CQA. Order is the tie-break priority: when a
// customer has several needs, the earlier code wins.
const (
	NeedComplaint = "complaint" // Khiếu nại
	NeedWarranty  = "warranty"  // Bảo hành / đổi trả
	NeedOrder     = "order"     // Đặt hàng / giao hàng
	NeedPrice     = "price"     // Hỏi giá / khuyến mãi
	NeedStock     = "stock"     // Hỏi còn hàng / màu
	NeedAdvice    = "advice"    // Tư vấn mẫu / size
	NeedOther     = "other"     // Khác
	NeedUnknown   = "unknown"   // Không có tin khách
)

// NeedPriority lists the need codes from strongest to weakest signal.
var NeedPriority = []string{NeedComplaint, NeedWarranty, NeedOrder, NeedPrice, NeedStock, NeedAdvice, NeedOther}

// NeedRulesVersion changes whenever needKeywords change, so a replica can tell
// which rule set produced a keyword-derived need.
const NeedRulesVersion = "2026-11-07-v1"

// needKeywords match whole words of an AI-written intent after lowercasing and
// replacing punctuation with spaces. Diacritics stay: "màu" (colour) and "mẫu"
// (model) differ only by them. Unaccented variants cover intents typed without
// diacritics.
var needKeywords = map[string][]string{
	NeedComplaint: {"khiếu nại", "phàn nàn", "bức xúc", "không hài lòng", "thất vọng", "phản ánh", "lừa đảo", "giao sai", "giao nhầm", "giao chậm", "giao trễ", "thái độ", "khieu nai", "phan nan", "complaint"},
	NeedWarranty:  {"bảo hành", "đổi trả", "đổi hàng", "trả hàng", "hoàn tiền", "đổi size", "đổi màu", "đổi sang", "sửa chữa", "bị lỗi", "hàng lỗi", "sản phẩm lỗi", "bị hỏng", "hư hỏng", "bị vỡ", "bị nứt", "thay thế", "linh kiện", "bao hanh", "doi tra", "hoan tien", "warranty"},
	NeedOrder:     {"đặt hàng", "đặt mua", "đặt cọc", "chốt đơn", "lên đơn", "tạo đơn", "đơn hàng", "muốn mua", "mua hàng", "thanh toán", "chuyển khoản", "địa chỉ", "nhận hàng", "kiểm tra đơn", "theo dõi đơn", "tình trạng đơn", "mã vận đơn", "dat hang", "chot don", "thanh toan", "order"},
	NeedPrice:     {"giá", "báo giá", "bao nhiêu tiền", "bao nhiêu", "khuyến mãi", "khuyến mại", "giảm giá", "ưu đãi", "sale", "voucher", "mã giảm", "trả góp", "combo", "quà tặng", "gia", "bao nhieu", "khuyen mai", "giam gia", "uu dai", "price"},
	NeedStock:     {"còn hàng", "hết hàng", "có sẵn", "có hàng", "còn màu", "còn size", "còn không", "còn mẫu", "màu", "màu sắc", "tồn kho", "hàng về", "con hang", "het hang", "co san", "mau sac", "stock"},
	NeedAdvice:    {"tư vấn", "size", "kích thước", "kích cỡ", "cỡ", "mẫu", "chọn", "so sánh", "thông số", "chất liệu", "phù hợp", "vòng đầu", "loại nào", "khác nhau", "thông tin sản phẩm", "tính năng", "đánh giá", "tu van", "kich thuoc", "so sanh", "advice"},
}

// shippingKeywords are a weak order signal: "giá bao nhiêu, ship mất mấy ngày"
// is still a price question. They count only when no stronger need matched.
var shippingKeywords = []string{"giao hàng", "vận chuyển", "ship", "phí ship", "cod", "giao về", "bao lâu nhận", "giao hang", "van chuyen"}

func needText(value string) string {
	var builder strings.Builder
	builder.WriteByte(' ')
	for _, r := range strings.ToLower(value) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.Is(unicode.Mn, r) {
			builder.WriteRune(r)
		} else {
			builder.WriteByte(' ')
		}
	}
	builder.WriteByte(' ')
	return strings.Join(strings.Fields(builder.String()), " ")
}

func needMatches(text, code string) bool {
	return containsKeyword(text, needKeywords[code])
}

func containsKeyword(text string, keywords []string) bool {
	padded := " " + text + " "
	for _, keyword := range keywords {
		if strings.Contains(padded, " "+keyword+" ") {
			return true
		}
	}
	return false
}

// NeedFromIntents maps AI-written intents to one need code with keyword rules.
// It costs no AI call, so old insights get a need group too. No intents means
// no customer need was recorded.
func NeedFromIntents(intents []string) string {
	texts := make([]string, 0, len(intents))
	for _, intent := range intents {
		if text := needText(intent); text != "" {
			texts = append(texts, text)
		}
	}
	if len(texts) == 0 {
		return NeedUnknown
	}
	for _, code := range NeedPriority[:len(NeedPriority)-1] {
		for _, text := range texts {
			if needMatches(text, code) {
				return code
			}
		}
	}
	for _, text := range texts {
		if containsKeyword(text, shippingKeywords) {
			return NeedOrder
		}
	}
	return NeedOther
}

// NormalizeNeed returns a valid need code or "" for an unknown value.
func NormalizeNeed(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == NeedUnknown {
		return value
	}
	for _, code := range NeedPriority {
		if value == code {
			return code
		}
	}
	return ""
}

// ResolveNeed prefers the AI need field and falls back to keyword rules for
// insights saved before the field existed or with an invalid value.
func ResolveNeed(aiNeed string, intents []string) (need, source string) {
	if need = NormalizeNeed(aiNeed); need != "" {
		return need, "ai"
	}
	return NeedFromIntents(intents), "keyword"
}
