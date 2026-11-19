package engine

import (
	"strings"

	"github.com/vietbui/chat-quality-agent/db"
	"github.com/vietbui/chat-quality-agent/db/models"
)

// TaskPrompt is an AI task prompt the tenant can edit in the AI Engine page.
// Instructions is the editable part (default below, override stored in
// app_settings under SettingKey). Format is the fixed part the code depends
// on to read the answer or to pass the inputs; it is appended after the
// instructions and shown read-only in the UI, so an edit cannot break the
// task.
type TaskPrompt struct {
	Key          string `json:"key"`
	Group        string `json:"group"`
	Title        string `json:"title"`
	Description  string `json:"description"`
	Instructions string `json:"default"`
	Format       string `json:"format"`
}

// SettingKey is the app_settings key holding the tenant's override.
func (p TaskPrompt) SettingKey() string { return "ai_prompt_" + p.Key }

// Task prompt keys.
const (
	PromptChatMemorySummary = "chat_memory_summary"
	PromptChatMemoryProfile = "chat_memory_profile"
	PromptChatMemoryIssues  = "chat_memory_issues"
	PromptChatMemoryContext = "chat_memory_bot_context"
	PromptIntentClassifier  = "chatbot_intent_classifier"
	PromptInventoryKeyword  = "chatbot_inventory_keyword"
	PromptProductCodeMatch  = "erp_product_code_match"
	PromptAttributeMatch    = "erp_attribute_match"
	PromptOrderCart         = "chatbot_order_cart"
)

var taskPrompts = []TaskPrompt{
	{
		Key:         PromptChatMemorySummary,
		Group:       "chat_memory",
		Title:       "Tóm tắt phiên chat",
		Description: "Cách tóm tắt một phiên chat Zalo sau khi phiên im lặng, và cách viết thẻ khách cho bot đọc.",
		Instructions: `Bạn phân tích một phiên chat Zalo giữa khách hàng với chatbot (và đôi khi nhân viên) của một doanh nghiệp bán hàng.

Tóm tắt:
- "summary": 2-4 câu, nêu khách hỏi gì, bot hoặc nhân viên trả lời ra sao, kết quả thế nào.
- "intents": các ý định chính, ví dụ: hỏi tồn kho, hỏi giá, tra đơn, tra công nợ, khiếu nại.
- "products": mã hoặc tên sản phẩm được nhắc tới.
- "outcome": answered (đã trả lời), partially_answered, unanswered, handover (chuyển nhân viên), order_intent (khách muốn đặt hàng), abandoned (bỏ dở) hoặc other.
- "sentiment": positive, neutral hoặc negative.
- Chỉ dựa trên phiên chat và hồ sơ hiện tại. Không bịa. Mục nào không có thông tin thì để chuỗi rỗng hoặc mảng rỗng.

Thẻ khách ("card"):
- Tối đa 500 ký tự, viết cho chatbot đọc trước khi trả lời khách này.
- Giọng trung tính: cách xưng hô nên dùng, khách hay quan tâm gì, lưu ý khi trả lời.
- Không nhắc lại nội dung riêng tư.

Viết bằng tiếng Việt.`,
	},
	{
		Key:         PromptChatMemoryProfile,
		Group:       "chat_memory",
		Title:       "Hồ sơ khách",
		Description: "Những gì được ghi vào hồ sơ khách sau mỗi phiên, và những gì không được ghi.",
		Instructions: `Hồ sơ khách ("profile") là hồ sơ ĐÃ CẬP NHẬT: giữ các nhận định cũ còn đúng, bổ sung điều mới, bỏ điều phiên này cho thấy đã sai.

Các mục của hồ sơ:
- "address_form": cách khách xưng hô và muốn được gọi.
- "communication_style": ngắn gọn hay chi tiết, trang trọng hay thân mật, hay dùng viết tắt...
- "active_hours": khung giờ hay nhắn nếu thấy rõ.
- "interests": dòng sản phẩm, nhu cầu thường hỏi.
- "price_behavior": hay hỏi giá, hay xin chiết khấu, nhạy cảm giá...
- "service_notes": lưu ý khi phục vụ khách này.
- "risk_flags": dấu hiệu cần chú ý, ví dụ hay khiếu nại, công nợ quá hạn được nhắc tới.

Quy tắc:
- Không suy đoán sức khoẻ, tôn giáo, chính trị, đời tư, giới tính.
- Không ghi số điện thoại, địa chỉ, số tài khoản, mật khẩu vào hồ sơ và thẻ khách.
- Không dùng từ ngữ chê bai khách.
- Với chat nhóm, hồ sơ là của cả nhóm (cửa hàng), không phải của một người.`,
	},
	{
		Key:         PromptChatMemoryIssues,
		Group:       "chat_memory",
		Title:       "Quy tắc phát hiện vấn đề và cảnh báo",
		Description: "Vấn đề nào cần ghi lại và mức độ nào. Vấn đề mức high được gửi vào nhóm cảnh báo của kênh.",
		Instructions: `Vấn đề ("issues") chỉ gồm việc cần người xử lý:
- complaint: khách khiếu nại, bức xúc về dịch vụ hoặc thái độ.
- delivery: giao sai, giao thiếu, giao chậm.
- product_defect: hàng lỗi, hỏng.
- price: sai giá, thắc mắc giá chưa được giải quyết.
- payment: sai tiền, công nợ, thanh toán.
- bot_failure: bot trả lời sai hoặc không trả lời được câu quan trọng.
- staff_request: khách đòi gặp nhân viên.
- other: vấn đề khác cần người xử lý.
Không có vấn đề thì để [].

Mức độ ("severity"):
- high: khách bức xúc, có nguy cơ mất đơn hoặc sai tiền, hoặc cần xử lý trong ngày. Mức này được gửi cảnh báo vào nhóm Zalo.
- medium: cần theo dõi.
- low: góp ý nhỏ.

"evidence" trích nguyên văn lời khách, tối đa 200 ký tự.`,
	},
	{
		Key:         PromptChatMemoryContext,
		Group:       "chat_memory",
		Title:       "Lời dặn bot khi dùng hồ sơ khách",
		Description: "Đặt trước thẻ khách và tóm tắt lần gần nhất khi nối vào system prompt của bot (chỉ khi kênh bật \"Bot dùng hồ sơ khách\").",
		Instructions: `THÔNG TIN TỪ CÁC LẦN TRÒ CHUYỆN TRƯỚC (chỉ để tham khảo cách phục vụ; không đọc lại cho khách, dữ liệu tồn kho/giá/đơn/công nợ luôn phải tra bằng công cụ):`,
	},
	{
		Key:         PromptIntentClassifier,
		Group:       "chatbot",
		Title:       "Phân loại tin nhắn của khách",
		Description: "Chạy trước mỗi lượt chat để quyết định bot trả lời, chuyển nhân viên hay bỏ qua.",
		Instructions: `Bạn là trợ lý lọc tin nhắn thông minh cho Chatbot doanh nghiệp BBI.
Nhiệm vụ của bạn là phân loại tin nhắn của khách hàng thành một trong bốn nhãn sau:
1. "IN_SCOPE": Bất kỳ tin nhắn nào hỏi về nghiệp vụ, ngành hàng hoặc kiến thức sản phẩm. Bao gồm:
   - Câu hỏi giao dịch: tồn kho, giá cả, đơn hàng, công nợ (financial debt).
   - Câu hỏi KIẾN THỨC / THÔNG SỐ / CẤU TẠO / CÔNG DỤNG / HƯỚNG DẪN sử dụng sản phẩm, kể cả khi không gắn với một giao dịch cụ thể. Ví dụ: "mũ bảo hiểm có bao nhiêu bộ phận", "sản phẩm này làm bằng chất liệu gì", "size áo tương ứng cân nặng bao nhiêu", "cách bảo quản giày".
   Luôn chọn IN_SCOPE cho câu hỏi về sản phẩm, kể cả khi chưa rõ bot có dữ liệu hoặc quyền tra cứu. Ví dụ: "bên mình có mũ nguyên đầu không", "có loại nửa đầu nào". Quyền truy cập và dữ liệu do công cụ tra cứu xác định.
2. "HANDOVER": Tin nhắn phàn nàn về chất lượng dịch vụ, sản phẩm lỗi, chậm giao hàng, thái độ phục vụ, yêu cầu gặp nhân viên hoặc các vấn đề nghiêm trọng cần con người giải quyết.
3. "CASUAL": Tin nhắn chỉ chào hỏi, cảm ơn, xác nhận hoặc trò chuyện xã giao và không có câu hỏi hay yêu cầu cần hỗ trợ. Ví dụ: "ok", "dạ", "cảm ơn", "hello", "hi".
4. "OUT_OF_SCOPE": Câu hỏi hoặc yêu cầu rõ ràng không liên quan đến sản phẩm, ngành hàng hay nghiệp vụ BBI, ví dụ hỏi dự báo thời tiết hoặc yêu cầu giải bài toán trường học.

Nếu chưa rõ câu hỏi có thuộc phạm vi hỗ trợ hay là câu hỏi tiếp nối, chọn IN_SCOPE. Tin nhắn có lời chào kèm câu hỏi vẫn phải được phân loại theo câu hỏi.`,
		Format: `Định dạng trả về duy nhất là một đối tượng JSON:
{
  "label": "IN_SCOPE" | "HANDOVER" | "CASUAL" | "OUT_OF_SCOPE",
  "reason": "Giải thích ngắn gọn lý do phân loại"
}
CHỈ trả về JSON, không thêm bất kỳ văn bản nào khác.`,
	},
	{
		Key:         PromptInventoryKeyword,
		Group:       "chatbot",
		Title:       "Trích từ khoá hỏi tồn kho",
		Description: "Nhận ra câu hỏi tồn kho và lấy mã hoặc tên sản phẩm khách nhắc tới.",
		Instructions: `Bạn là trợ lý trích xuất từ khóa sản phẩm thông minh của doanh nghiệp BBI.
Nhiệm vụ của bạn là phân tích tin nhắn của khách hàng và thực hiện:
1. Xác định xem khách hàng có đang hỏi về tồn kho, còn hàng, hết hàng của một dòng sản phẩm nào đó hay không.
2. Trích xuất từ khóa sản phẩm chính xác (ví dụ: "FF800", "FF600", "áo thun trơn",...). Chỉ trích xuất từ khóa ngắn gọn, đại diện cho mã/tên sản phẩm được nhắc đến trong câu hỏi tồn kho.`,
		Format: `Định dạng trả về duy nhất là một đối tượng JSON:
{
  "is_inventory_query": true | false,
  "keyword": "từ khóa sản phẩm hoặc rỗng"
}
CHỈ trả về JSON, không thêm bất kỳ văn bản nào khác.`,
	},
	{
		Key:         PromptOrderCart,
		Group:       "chatbot",
		Title:       "Lên đơn qua chatbot (giỏ hàng)",
		Description: "Cách bot nhận một đơn nhiều sản phẩm: hỏi lại khi tên sản phẩm khớp nhiều loại, sửa đơn ngay trong lúc chat, gửi bản xác nhận cuối và chốt đơn. Chỉ nối vào system prompt của bot khi người chat có quyền tạo đơn hoặc đang có giỏ hàng dở.",
		Instructions: `LÊN ĐƠN QUA CHAT
Khi người dùng muốn đặt hàng, dùng giỏ hàng của cuộc chat:
- order_cart_update: thêm sản phẩm, đổi số lượng, bỏ sản phẩm, trả lời câu hỏi chọn sản phẩm.
- order_cart_view: xem lại giỏ.
- order_cart_checkout: gửi bản xác nhận cuối cùng.
- order_cart_clear: người dùng nói không đặt nữa.

1. Ghi nhận: mỗi sản phẩm người dùng nhắn là một item. product ghi đúng tên/mã họ gõ (ví dụ "FF800 Storm", "kính FF800"), kèm color, size, quantity nếu họ nói. Gửi tất cả sản phẩm của tin nhắn trong một lần gọi. Không tự đoán SKU.
2. Sản phẩm chưa rõ: nếu giỏ có pending (tên khớp nhiều dòng như FF901 và FF901 Carbon, hoặc còn thiếu màu/size), hỏi lại ngắn gọn, liệt kê các lựa chọn đánh số theo options. Thiếu màu/size thì hỏi theo colors và sizes còn hàng, đừng đọc hết danh sách mã. Khi người dùng trả lời (số thứ tự, tên dòng, màu, size), gọi lại order_cart_update với pending_id cùng option hoặc line/color/size. Không hỏi lại điều người dùng đã nói.
3. Sửa trong lúc chat: người dùng đổi ý ("lấy 2 cái thôi", "bỏ kính", "thêm 1 cái nữa", "đổi sang màu trắng") thì gọi order_cart_update với op set, remove hoặc add. Đổi màu/size là bỏ dòng cũ và thêm dòng mới trong cùng một lần gọi. Sau mỗi lần cập nhật, nhắc ngắn những gì vừa đổi và hỏi người dùng còn lấy gì nữa không.
4. Tồn kho: dòng có warning (kho tổng không đủ) thì báo số còn lại và hỏi giảm số lượng hay bỏ. problems thì báo lại cho người dùng bằng lời tự nhiên.
5. Chốt đơn: khi người dùng nói đủ rồi, chốt, lên đơn, và stage là ready, gọi order_cart_checkout. Hệ thống tự gửi bản xác nhận đầy đủ và tự gửi lời chốt khi người dùng trả lời 1. Không tự viết danh sách xác nhận, không tự nói đã lên đơn.
6. Sau bản xác nhận mà người dùng còn sửa: cập nhật giỏ rồi gọi lại order_cart_checkout để gửi bản xác nhận mới.
7. Nhân viên đặt hộ: hỏi mã khách hàng nếu giỏ chưa có và truyền customer_code.
8. Giỏ hàng dở từ lâu (xem giờ cập nhật): hỏi người dùng muốn tiếp tục đơn đó hay làm đơn mới trước khi thêm sản phẩm.
Giá trong giỏ là giá bán ERP, chưa gồm chiết khấu. Không hứa giá hay chiết khấu khác.`,
		Format: `Khi kết quả công cụ có message=zalo_rich_message_sent_directly, chỉ trả lời đúng [RICH_MESSAGE_SENT].

GIỎ HÀNG CỦA CUỘC CHAT NÀY:
{{cart}}`,
	},
	{
		Key:         PromptProductCodeMatch,
		Group:       "erp",
		Title:       "Khớp mã sản phẩm ERP",
		Description: "Khi không tìm thấy mã khách gõ, AI chọn mã sản phẩm (ma_cha) gần nhất trong danh mục.",
		Instructions: `Bạn là trợ lý mapping mã sản phẩm BBI.

Hãy chọn 1 MÃ SẢN PHẨM (ma_cha) khớp nhất từ danh sách bên dưới.

Quy tắc khớp:
- Khớp cả theo MÃ và theo TÊN (web/ERP) sau dấu "|".
- Bỏ dấu gạch ngang, dấu chấm, số 0 đầu khi so mã (E05 = E-5, E.5 = E-5).
- Số La Mã ↔ số Ả Rập tương đương (Storm III = Storm 3, MK II = MK 2).
- Bỏ dấu tiếng Việt khi so tên ("mu bao hiem" = "mũ bảo hiểm").
- Viết tắt thương hiệu chấp nhận được (LS2 FF818 ≈ FF818).`,
		Format: `Khách hàng tìm kiếm: {{keyword}}

TRẢ LỜI CHỈ ma_cha duy nhất, không thêm chữ, dấu câu, hay tên.
Nếu thực sự không có dòng nào hợp lý → trả "NONE".

Danh sách (ma_cha | tên):
{{candidates}}`,
	},
	{
		Key:         PromptAttributeMatch,
		Group:       "erp",
		Title:       "Khớp màu, size, thương hiệu",
		Description: "Đổi màu, size, thương hiệu khách gõ (Việt hoặc Anh) sang giá trị có trong kho.",
		Instructions: `Bạn là trợ lý map thuộc tính sản phẩm song ngữ Việt-Anh.

Nhiệm vụ:
- Với mỗi trường khách nhập KHÔNG rỗng, chọn 1 giá trị KHỚP NHẤT từ danh sách tương ứng.
- "Đen bóng" / "den bong" / "black gloss" / "gloss black" → khớp "Gloss Black".
- "Trắng nhám" → "Matt White". "Đỏ" → có thể là "Red" hoặc "Red Matte" tuỳ list.
- Nếu trường khách nhập là rỗng → trả NONE cho trường đó.
- Nếu thật sự không có giá trị nào hợp lý trong list → trả NONE.`,
		Format: `Khách hàng nhập:
- COLOR: {{color}}
- SIZE: {{size}}
- BRAND: {{brand}}

Danh sách giá trị HỢP LỆ trong kho (chỉ được chọn từ đây):
- COLORS: {{colors}}
- SIZES: {{sizes}}
- BRANDS: {{brands}}

CHỈ trả về đúng 3 dòng theo format (không thêm chữ khác):
COLOR: <giá trị từ COLORS hoặc NONE>
SIZE: <giá trị từ SIZES hoặc NONE>
BRAND: <giá trị từ BRANDS hoặc NONE>`,
	},
}

// TaskPrompts lists every editable task prompt in display order.
func TaskPrompts() []TaskPrompt {
	out := make([]TaskPrompt, len(taskPrompts))
	copy(out, taskPrompts)
	return out
}

// LookupTaskPrompt returns the prompt definition for key.
func LookupTaskPrompt(key string) (TaskPrompt, bool) {
	for _, p := range taskPrompts {
		if p.Key == key {
			return p, true
		}
	}
	return TaskPrompt{}, false
}

// TaskPromptInstructions returns the tenant's instructions for key, or the
// default when the tenant has no override.
func TaskPromptInstructions(tenantID, key string) string {
	p, ok := LookupTaskPrompt(key)
	if !ok {
		return ""
	}
	if tenantID != "" && db.DB != nil {
		var s models.AppSetting
		if err := db.DB.Select("value_plain").Where("tenant_id = ? AND setting_key = ?", tenantID, p.SettingKey()).First(&s).Error; err == nil {
			if v := strings.TrimSpace(s.ValuePlain); v != "" {
				return v
			}
		}
	}
	return p.Instructions
}

// BuildTaskPrompt returns the full system prompt for key: the tenant's
// instructions followed by the fixed format, with {{name}} placeholders in
// the format filled from vars.
func BuildTaskPrompt(tenantID, key string, vars map[string]string) string {
	p, _ := LookupTaskPrompt(key)
	return ComposeTaskPrompt(TaskPromptInstructions(tenantID, key), p.Format, vars)
}

// ComposeTaskPrompt joins instructions and format and fills placeholders.
// Only the format is filled: values never pass through tenant text.
func ComposeTaskPrompt(instructions, format string, vars map[string]string) string {
	if len(vars) > 0 && format != "" {
		pairs := make([]string, 0, len(vars)*2)
		for k, v := range vars {
			pairs = append(pairs, "{{"+k+"}}", v)
		}
		format = strings.NewReplacer(pairs...).Replace(format)
	}
	instructions = strings.TrimSpace(instructions)
	if format == "" {
		return instructions
	}
	return instructions + "\n\n" + format
}
