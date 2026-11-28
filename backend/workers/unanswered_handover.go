package workers

import (
	"strings"

	"github.com/vietbui/chat-quality-agent/engine"
)

// unansweredHandoverAction is what the bot answers when it has no grounded
// answer (task prompt chatbot_unanswered_handover).
const unansweredHandoverAction = engine.HumanReviewMarker

func isUnansweredHandoverAction(text string) bool {
	text = strings.TrimSpace(text)
	return text == unansweredHandoverAction ||
		strings.EqualFold(strings.TrimSuffix(text, "."), "No relevant supporting sources were found for that request")
}

func unansweredCustomerReply(delivered, hasDestination bool) string {
	if delivered {
		return "Dạ, em đã chuyển câu hỏi của anh/chị cho bộ phận phụ trách kiểm tra thêm ạ."
	}
	if hasDestination {
		return "Dạ, em chưa xác nhận được câu trả lời và chưa nhận được xác nhận chuyển tiếp. Anh/chị có thể nhắn trực tiếp cho nhân viên phụ trách giúp em ạ."
	}
	return "Dạ, em chưa xác nhận được câu trả lời và hiện chưa chuyển được câu hỏi cho bộ phận phụ trách. Anh/chị có thể nhắn trực tiếp cho nhân viên phụ trách giúp em ạ."
}

func isExplicitERPLookup(message string) bool {
	text := strings.ToLower(strings.TrimSpace(message))
	return productCodePattern.MatchString(text) ||
		strings.Contains(text, "đơn hàng") || strings.Contains(text, "công nợ") ||
		strings.Contains(text, "tồn kho") || strings.Contains(text, "mã sản phẩm")
}
