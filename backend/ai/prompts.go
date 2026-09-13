package ai

import (
	"encoding/json"
	"fmt"
)

// BuildQCPrompt creates the system prompt for QC analysis.
func BuildQCPrompt(rulesContent, skipConditions string) string {
	skipSection := ""
	if skipConditions != "" {
		skipSection = fmt.Sprintf(`
## Điều kiện bỏ qua (không đánh giá):
%s

Nếu cuộc chat thỏa mãn bất kỳ điều kiện nào trên, trả về verdict="SKIP", violations=[], score=0, review=lý do bỏ qua ngắn gọn.
`, skipConditions)
	}

	return fmt.Sprintf(`Bạn là chuyên gia đánh giá chất lượng chăm sóc khách hàng (CSKH).

## Quy định CSKH cần tuân thủ:
%s
%s
## Nhiệm vụ:
Phân tích đoạn chat CSKH dưới đây và tìm các vi phạm quy định.

## Yêu cầu output:
Trả về JSON với cấu trúc sau:
{
  "verdict": "PASS", "FAIL" hoặc "SKIP",
  "score": 0-100,
  "review": "Nhận xét tổng quan cuộc chat: chat tốt hay chưa tốt, cần cải thiện điều gì",
  "violations": [
    {
      "severity": "NGHIEM_TRONG" hoặc "CAN_CAI_THIEN",
      "rule": "Tên quy tắc bị vi phạm",
      "evidence": "Trích dẫn chính xác đoạn chat vi phạm",
      "explanation": "Giải thích ngắn gọn tại sao đây là vi phạm",
      "suggestion": "Gợi ý cách trả lời đúng"
    }
  ],
  "summary": "Tổng quan ngắn gọn về chất lượng chat"
}

- "verdict": "PASS" nếu cuộc chat đạt yêu cầu chất lượng, "FAIL" nếu có vấn đề cần khắc phục, "SKIP" nếu thỏa điều kiện bỏ qua
- "review": Nhận xét chi tiết về cuộc chat (2-3 câu), đánh giá chất lượng chăm sóc khách hàng
- Nếu không có vi phạm: verdict="PASS", violations=[], score gần 100
- Nếu có vi phạm nghiêm trọng: verdict="FAIL"
CHỈ trả về JSON, không thêm text khác.`, rulesContent, skipSection)
}

// BuildClassificationPrompt creates the system prompt for conversation classification.
func BuildClassificationPrompt(rulesConfigJSON string) string {
	config := ParseClassificationConfig(rulesConfigJSON)
	rulesJSON, err := json.Marshal(config.Rules)
	if err != nil {
		rulesJSON = []byte("[]")
	}
	insightsOutput := ""
	insightsInstructions := ""
	if config.MessengerInsightsEnabled() {
		insightsOutput = `,
  "insights": {
    "intents": ["mục đích trao đổi cụ thể"],
    "products": [{"name":"tên sản phẩm","sku":"SKU nếu được nói rõ, nếu không để rỗng","evidence":"trích dẫn nguyên văn của khách"}],
    "feedback": [{"category":"sản phẩm|giá|giao hàng|CSKH|khác","sentiment":"positive|neutral|negative|mixed|unknown","evidence":"trích dẫn nguyên văn của khách"}],
    "lead_quality": {"level":"high|medium|low|spam|unknown","evidence":"trích dẫn nguyên văn của khách hoặc để rỗng nếu thiếu dữ kiện","reason":"lý do ngắn gọn"}
  }`
		insightsInstructions = `
- Luôn trả về trường "insights", kể cả khi các mảng rỗng.
- Chỉ trích xuất sản phẩm và SKU khi khách hàng thực sự đề cập; TUYỆT ĐỐI không suy đoán hoặc tự tạo SKU.
- "evidence" phải là trích dẫn chính xác lời khách hàng, không diễn giải và không dùng lời nhân viên làm bằng chứng về nhu cầu/feedback.
- Khi không đủ dữ kiện về tiềm năng mua hàng, dùng lead_quality.level="unknown".
- Khiếu nại hoặc cảm xúc tiêu cực không đồng nghĩa khách hàng chất lượng thấp. Đánh giá lead_quality theo mức độ nhu cầu và ý định mua.
- Không suy đoán hoặc quy kết hội thoại cho một nhân viên cụ thể.`
	}

	return fmt.Sprintf(`Bạn là hệ thống phân loại nội dung hội thoại CSKH/Sales.

## Các quy tắc phân loại:
%s

## Nhiệm vụ:
Phân tích đoạn chat dưới đây và gán các nhãn phân loại phù hợp.

## Yêu cầu output:
Trả về JSON:
{
  "tags": [
    {
      "rule_name": "Tên rule đã match",
      "confidence": 0.0-1.0,
      "evidence": "Trích dẫn đoạn chat liên quan",
      "explanation": "Giải thích ngắn gọn tại sao"
    }
  ],
  "summary": "Mô tả chi tiết nội dung cuộc chat: khách hàng nói gì, phía Fanpage xử lý ra sao, kết quả thế nào (2-3 câu, KHÔNG lặp lại tên nhãn phân loại)"%s
}

- "summary" phải mô tả CỤ THỂ nội dung cuộc chat, không được viết chung chung như "Cuộc chat được phân loại: X"
- Ví dụ tốt: "Khách hàng hỏi về tính năng webhook nhưng nhân viên không nắm rõ, hướng dẫn sai cách cấu hình. Khách phản hồi tiêu cực."
- Ví dụ xấu: "Cuộc chat được phân loại: Góp ý tính năng"
%s
CHỈ trả về JSON, không thêm text khác.`, string(rulesJSON), insightsOutput, insightsInstructions)
}

// FormatBatchTranscript formats multiple conversations for batch analysis.
func FormatBatchTranscript(items []BatchItem) string {
	result := ""
	for i, item := range items {
		result += fmt.Sprintf("=== CUỘC HỘI THOẠI %d (ID: %s) ===\n%s\n\n", i+1, item.ConversationID, item.Transcript)
	}
	return result
}

// WrapBatchPrompt wraps a single-conversation system prompt into a batch prompt.
func WrapBatchPrompt(basePrompt string, count int) string {
	return fmt.Sprintf(`%s

QUAN TRỌNG: Bạn sẽ nhận được %d cuộc hội thoại, mỗi cuộc được đánh dấu "=== CUỘC HỘI THOẠI N (ID: xxx) ===".
Trả về JSON ARRAY chứa %d phần tử, mỗi phần tử là kết quả đánh giá cho 1 cuộc hội thoại theo đúng thứ tự.
Format: [{"conversation_id": "xxx", ...kết quả...}, ...]
CHỈ trả về JSON array, không thêm text khác.`, basePrompt, count, count)
}

// FormatChatTranscript formats messages into a readable transcript for AI analysis.
func FormatChatTranscript(messages []ChatMessage) string {
	result := ""
	for _, msg := range messages {
		label := msg.SenderName
		if label == "" {
			label = msg.SenderType
		} else if msg.SenderType != "" {
			label = fmt.Sprintf("%s (%s)", label, msg.SenderType)
		}
		result += fmt.Sprintf("[%s] %s: %s\n", msg.SentAt, label, msg.Content)
	}
	return result
}

// ChatMessage is a simplified message for transcript formatting.
type ChatMessage struct {
	SenderType string
	SenderName string
	Content    string
	SentAt     string
}
