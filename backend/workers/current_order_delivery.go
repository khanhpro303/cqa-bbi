package workers

import (
	"context"
	"regexp"
	"strings"
	"time"

	"github.com/vietbui/chat-quality-agent/chatmemory"
	"github.com/vietbui/chat-quality-agent/db"
)

// CurrentOrderDeliveryLookup is supplied by the existing signed ERP handler.
type CurrentOrderDeliveryLookup func(ctx context.Context, tenantID, agentToken, permissionToken string, days int) (reply string, unresolved bool, err error)

var upcomingOrderQuestion = regexp.MustCompile(`^(?:(?:mình|em|anh|chị|tôi|bên mình) )?(?:có (?:đang có )?|kiểm tra |xem |cho (?:mình|em|anh|chị|tôi) hỏi )?(?:đơn(?: hàng)? (?:nào )?(?:sắp giao|đang giao|chờ giao))(?: (?:không|chưa))?(?: (?:em|ạ|không ạ|giúp mình|giúp em))?$`)

func isCurrentOrderDeliveryTurn(message string, history func() []chatmemory.RecentTurn) bool {
	text := normalizedOrderFollowupText(message)
	if upcomingOrderQuestion.MatchString(text) {
		return true
	}
	return isCurrentOrderPreference(text) && isCurrentOrderFollowup(text, history())
}

const publicOrderLookupInstruction = `QUY TẮC ƯU TIÊN TRA ĐƠN HÀNG CỦA KHÁCH
- Với "đơn hàng nào sắp giao", "đơn nào đang giao" hoặc yêu cầu xem tình trạng đơn hiện tại, trước khi trả lời phải gọi lookup_order. Khi khách chưa nêu mã đơn hay khoảng ngày, dùng search="đơn hàng" để ERP tra các đơn phát sinh hôm nay theo giờ Việt Nam. Không hỏi số ngày trước lần tra đầu.
- Nếu đang nói về đơn và khách muốn mặc định xem kết quả hiện tại, tra lại hôm nay bằng search="đơn hàng"; đừng lặp lại khoảng 3/5/7 ngày cũ. Dùng khoảng ngày khách vừa chủ động yêu cầu nếu có.
- Sau khi có kết quả, ưu tiên trạng thái ERP khi nói đơn đang giao hoặc chờ giao. Chỉ nêu ngày hẹn của đơn khi có scheduled_delivery_date. Nếu hôm nay không có kết quả phù hợp, nêu phạm vi đã tra rồi mới gợi ý mở rộng.`

// A request to default to today's results has no business keywords of its
// own. Require an exact short preference and the sender's own order context.
func isCurrentOrderFollowup(message string, recent []chatmemory.RecentTurn) bool {
	if !isCurrentOrderPreference(normalizedOrderFollowupText(message)) {
		return false
	}
	for i := len(recent) - 1; i >= 0; i-- {
		turn := recent[i]
		if turn.Speaker == "Khách" && strings.Contains(strings.ToLower(turn.Text), "đơn hàng") {
			return true
		}
	}
	return false
}

func normalizedOrderFollowupText(message string) string {
	return strings.TrimRight(strings.ToLower(strings.Join(strings.Fields(message), " ")), ".!?")
}

func isCurrentOrderPreference(text string) bool {
	switch text {
	case "luôn mặc định trả lời kết quả của hiện tại", "mặc định trả lời kết quả hiện tại",
		"mặc định xem hôm nay", "luôn mặc định xem hôm nay", "mặc định xem kết quả hôm nay":
		return true
	default:
		return false
	}
}

var orderPeriodAnswer = regexp.MustCompile(`^(?:xem thêm |xem )?([357]) ngày(?: gần đây| qua)?(?: ạ| nhé)?$`)

func orderFollowupDays(message string) int {
	if match := orderPeriodAnswer.FindStringSubmatch(normalizedOrderFollowupText(message)); len(match) > 0 {
		return int(match[1][0] - '0')
	}
	return 0
}

var lookupPeriodAnswer = regexp.MustCompile(`^(?:hôm nay|hôm qua|tháng (?:này|trước|[0-9]{1,2})|quý (?:này|trước|[1-4])|[0-9]{1,2} ngày(?: gần đây| qua)?)$`)

func isExpectedLookupFollowup(message string) bool {
	return isDisambiguationReply(message) || orderFollowupDays(message) > 0 ||
		lookupPeriodAnswer.MatchString(normalizedOrderFollowupText(message))
}

func hasOrderPeriodChoice(ctx context.Context, key string) bool {
	return db.RedisClient != nil && db.RedisClient.Exists(ctx, key).Val() == 1
}

func rememberOrderPeriodChoice(ctx context.Context, key string, ttl time.Duration) {
	if db.RedisClient != nil {
		db.RedisClient.Set(ctx, key, "1", ttl)
	}
}
