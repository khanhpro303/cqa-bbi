package ai

import (
	"encoding/json"
	"fmt"
	"strings"
)

const MessengerInsightsPromptSettingKey = "ai_engine_system_prompt_messenger_insights"

const MessengerProductGroupingPromptVersion = "2026-10-01-v4"

const MessengerProductGroupingPrompt = `Bạn là tác vụ chuẩn hóa và gom nhóm tên sản phẩm từ dữ liệu Messenger Insights.

Mỗi nhãn có ID trong products=[{"id":1,"name":"nhãn gốc"},...], bắt đầu từ 1. Dùng đúng id đi kèm name, không tự đếm hoặc thay đổi thứ tự. product_names chứa cùng danh sách để tương thích.
Trả về assignments là object ánh xạ từng ID (key chuỗi "1", "2", ...) sang tên sản phẩm chuẩn (value chuỗi).
Mỗi nhãn đầu vào phải xuất hiện đúng một lần: bắt buộc trả đủ mọi ID từ 1 đến số nhãn. Không bỏ sót, không lặp ID, không thêm ID.

Quy tắc tên chuẩn:
- Gom mọi cách gọi tương đương của cùng một model về cùng một tên chuẩn. Ví dụ "Mũ bảo hiểm E-24", "Mũ E-24" và "E-24" đều có giá trị "E-24".
- Bỏ tên brand EGO, LS2, BULLDOG, YOHE, ZEUS và tiền tố mô tả chung Mũ bảo hiểm/Mũ.
- Mã sản phẩm không có dấu cách giữa các thành phần: "FF 818" thành "FF818", "E - 24" thành "E-24".
- Chỉ dùng model/mã có trong nhãn đầu vào; không suy đoán model hoặc tự tạo SKU.
- Chỉ nhãn hoàn toàn là brand hoặc mô tả chung không có model mới cho phép name là chuỗi rỗng (giá trị ""); vẫn trả ID đó.
- Tuyệt đối không bỏ sản phẩm có model/mã hoặc tên riêng. "mũ bảo hiểm nửa đầu EGO E-24", "mũ bảo hiểm EGO E-24", "EGO E-24" đều phải ánh xạ "E-24", không được trả chuỗi rỗng. "Bulldog Corgi" phải giữ "Corgi".
- Tên chuẩn phải phản ánh đúng nhãn của chính ID đó; không lấy tên model từ ID khác. Kiểm tra lại từng cặp id/name trước khi trả kết quả.
- Cùng model phải có cùng tên chuẩn, không tách lẻ theo cách viết, màu hoặc tiền tố.

Chỉ trả về JSON, không thêm markdown hoặc giải thích. Ví dụ product_names=["Mũ E-24","E-24","LS2 FF 818","LS2"]:
{"assignments":{"1":"E-24","2":"E-24","3":"FF818","4":""}}`

const LegacyMessengerProductGroupingAssignmentsPrompt = `Bạn là tác vụ chuẩn hóa và gom nhóm tên sản phẩm từ dữ liệu Messenger Insights.

Mỗi nhãn trong mảng product_names có ID là vị trí trong mảng, bắt đầu từ 1.
Trả về assignments là object ánh xạ từng ID (key chuỗi "1", "2", ...) sang tên sản phẩm chuẩn (value chuỗi).
Mỗi nhãn đầu vào phải xuất hiện đúng một lần: bắt buộc trả đủ mọi ID từ 1 đến số nhãn. Không bỏ sót, không lặp ID, không thêm ID.

Quy tắc tên chuẩn:
- Gom mọi cách gọi tương đương của cùng một model về cùng một tên chuẩn. Ví dụ "Mũ bảo hiểm E-24", "Mũ E-24" và "E-24" đều có giá trị "E-24".
- Bỏ tên brand EGO, LS2, BULLDOG, YOHE, ZEUS và tiền tố mô tả chung Mũ bảo hiểm/Mũ.
- Mã sản phẩm không có dấu cách giữa các thành phần: "FF 818" thành "FF818", "E - 24" thành "E-24".
- Chỉ dùng model/mã có trong nhãn đầu vào; không suy đoán model hoặc tự tạo SKU.
- Nhãn chỉ có brand hoặc mô tả chung không xác định được sản phẩm: cho phép name là chuỗi rỗng (giá trị ""); vẫn trả ID đó.
- Cùng model phải có cùng tên chuẩn, không tách lẻ theo cách viết, màu hoặc tiền tố.

Chỉ trả về JSON, không thêm markdown hoặc giải thích. Ví dụ product_names=["Mũ E-24","E-24","LS2 FF 818","LS2"]:
{"assignments":{"1":"E-24","2":"E-24","3":"FF818","4":""}}`

const LegacyMessengerProductGroupingIDsPrompt = `Bạn là tác vụ chuẩn hóa và gom nhóm tên sản phẩm từ dữ liệu Messenger Insights.

Mỗi nhãn trong mảng product_names có ID là vị trí trong mảng, bắt đầu từ 1. Ví dụ product_names=["Mũ E-24","E-24","LS2 FF 818","LS2"] tương ứng ID 1, 2, 3, 4.

Yêu cầu bắt buộc:
- Chỉ dùng các nhãn sản phẩm có trong product_names; không thêm hoặc suy đoán nhãn đầu vào.
- Mỗi nhãn đầu vào phải xuất hiện đúng một lần bằng ID của nó trong member_ids của toàn bộ kết quả. Không chép lại tên gốc vào members; dùng member_ids để tránh lỗi dấu cách, chữ hoa/thường và Unicode.
- Gom mọi cách gọi tương đương của cùng một model vào một nhóm duy nhất, kể cả alias lịch sử như "Mũ bảo hiểm E-24", "Mũ E-24" và "E-24".
- name là model/mã sản phẩm chuẩn dùng cho biểu đồ. Bỏ các tên brand EGO, LS2, BULLDOG, YOHE, ZEUS.
- Mã sản phẩm không có dấu cách giữa các thành phần: "FF 818" thành "FF818", "E - 24" thành "E-24".
- Nếu nhãn chỉ có brand và không có model/mã sản phẩm, cho phép name là chuỗi rỗng; vẫn phải đưa ID của nhãn đó vào một nhóm.
- member_ids chỉ gồm số nguyên trong khoảng 1 đến số nhãn đầu vào. Không bỏ sót, không lặp ID, không trả nhóm rỗng.

Chỉ trả về JSON đúng cấu trúc sau, không thêm markdown hoặc giải thích:
{"groups":[{"name":"E-24","member_ids":[1,2]},{"name":"FF818","member_ids":[3]},{"name":"","member_ids":[4]}]}`

// Only this exact untouched default is upgraded; tenant-authored prompts stay editable and unchanged.
const LegacyMessengerProductGroupingPrompt = `Bạn là tác vụ chuẩn hóa và gom nhóm tên sản phẩm từ dữ liệu Messenger Insights.

Yêu cầu bắt buộc:
- Chỉ dùng các nhãn sản phẩm có trong product_names; không thêm, sửa hoặc suy đoán nhãn đầu vào.
- Mỗi nhãn đầu vào phải xuất hiện đúng một lần trong members của toàn bộ kết quả.
- Gom mọi cách gọi tương đương của cùng một model vào một nhóm duy nhất, kể cả alias lịch sử như "Mũ bảo hiểm E-24", "Mũ E-24" và "E-24".
- name là model/mã sản phẩm chuẩn dùng cho biểu đồ. Bỏ các tên brand EGO, LS2, BULLDOG, YOHE, ZEUS.
- Mã sản phẩm không có dấu cách giữa các thành phần: "FF 818" thành "FF818", "E - 24" thành "E-24".
- Nếu nhãn chỉ có brand và không có model/mã sản phẩm, cho phép name là chuỗi rỗng.
- members phải giữ nguyên chính xác từng nhãn đầu vào.

Chỉ trả về JSON đúng cấu trúc sau, không thêm markdown hoặc giải thích:
{"groups":[{"name":"tên chuẩn hoặc chuỗi rỗng","members":["nhãn gốc"]}]}`

const mandatorySenderRoleHeading = "## Quy tắc vai trò bắt buộc"

const mandatoryProductNormalizationHeading = "## Quy tắc chuẩn hóa sản phẩm bắt buộc"

const mandatorySenderRoleInstructions = `

## Quy tắc vai trò bắt buộc
- Chỉ dòng có nhãn (customer) mới được xem là lời nói, nhu cầu, thông tin hoặc hành động của khách hàng.
- Dòng có nhãn (agent) là lời của Fanpage/nhân viên; không được dùng lời agent làm bằng chứng cho nhu cầu, sản phẩm, phản hồi hoặc chất lượng lead của khách.
- Không được suy ra nội dung của bình luận hoặc tin nhắn không xuất hiện trong transcript.
- Việc agent yêu cầu khách cung cấp chiều cao, cân nặng hoặc thông tin khác không có nghĩa khách đã cung cấp thông tin đó.
- Nếu không có dòng (customer), phải ghi rõ chưa ghi nhận tin nhắn khách hàng, không gán nhãn dựa trên ý định khách và để các customer insights rỗng với lead_quality.level="unknown".
- Ví dụ: "[agent] Cho em xin chiều cao và cân nặng để tư vấn size" phải được hiểu là Fanpage đang hỏi; không được viết "khách đã cung cấp chiều cao và cân nặng".`

const mandatoryProductNormalizationInstructions = `

## Quy tắc chuẩn hóa sản phẩm bắt buộc
- Trong insights.products[].name, bỏ tên brand EGO, LS2, BULLDOG, YOHE, ZEUS; chỉ giữ model/mã sản phẩm mà khách thực sự nhắc tới.
- Bỏ tiền tố mô tả chung "Mũ bảo hiểm" hoặc "Mũ" khỏi tên sản phẩm. Ví dụ: "Mũ bảo hiểm E-24", "Mũ E-24" và "E-24" đều phải trả về name="E-24".
- Mã sản phẩm không được có dấu cách giữa các thành phần. Ví dụ: "FF 818" phải chuẩn hóa thành "FF818"; "E - 24" thành "E-24".
- Gộp cách gọi khác nhau của cùng một model về đúng một tên chuẩn, không tách thành các sản phẩm lẻ tẻ.
- Không suy đoán model hoặc tự tạo SKU. Nếu SKU không được khách nói rõ thì để sku="".
- evidence phải giữ nguyên trích dẫn chính xác lời khách hàng; không sửa nội dung evidence theo tên đã chuẩn hóa.`

func appendMandatorySenderRoleInstructions(prompt string) string {
	if strings.Contains(prompt, mandatorySenderRoleHeading) {
		return prompt
	}
	return prompt + mandatorySenderRoleInstructions
}

func appendMandatoryMessengerInsightsInstructions(prompt string) string {
	prompt = appendMandatorySenderRoleInstructions(prompt)
	if strings.Contains(prompt, mandatoryProductNormalizationHeading) {
		return prompt
	}
	return prompt + mandatoryProductNormalizationInstructions
}

// DefaultMessengerInsightsPrompt exposes the existing structured prompt as an
// editable template, with job-specific classification rules filled in at runtime.
func DefaultMessengerInsightsPrompt() string {
	prompt := BuildClassificationPrompt(`{"profile":"messenger_insights","rules":[]}`)
	return strings.Replace(prompt, "## Các quy tắc phân loại:\n[]", "## Các quy tắc phân loại:\n{{rules}}", 1)
}

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
func BuildClassificationPrompt(rulesConfigJSON string, promptOverride ...string) string {
	config := ParseClassificationConfig(rulesConfigJSON)
	rulesJSON, err := json.Marshal(config.Rules)
	if err != nil {
		rulesJSON = []byte("[]")
	}
	if config.MessengerInsightsEnabled() && len(promptOverride) > 0 && strings.TrimSpace(promptOverride[0]) != "" {
		prompt := promptOverride[0]
		if strings.Contains(prompt, "{{rules}}") {
			prompt = strings.ReplaceAll(prompt, "{{rules}}", string(rulesJSON))
			return appendMandatoryMessengerInsightsInstructions(prompt)
		}
		// Keep each job's rules even if the administrator removes the placeholder.
		prompt += "\n\n## Các quy tắc phân loại:\n" + string(rulesJSON)
		return appendMandatoryMessengerInsightsInstructions(prompt)
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

	prompt := fmt.Sprintf(`Bạn là hệ thống phân loại nội dung hội thoại CSKH/Sales.

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
	if config.MessengerInsightsEnabled() {
		return appendMandatoryMessengerInsightsInstructions(prompt)
	}
	return prompt
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
