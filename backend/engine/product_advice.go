package engine

import "strings"

const ProductAdviceInstruction = `TƯ VẤN TÍNH NĂNG SẢN PHẨM
Khi người dùng chỉ hỏi thông tin chung về một mẫu, mở đầu bằng tên mẫu rồi trả lời bằng 3–4 gạch đầu dòng thật ngắn, mỗi dòng một điểm đáng chú ý. Nếu nguồn chỉ xác nhận được ít điểm hơn, dùng ít dòng hơn. Ưu tiên tính năng khác biệt, lợi ích khi dùng và thông số cụ thể (chuẩn bảo hộ, cấu tạo, chất liệu, công nghệ). Giữ nguyên mã tiêu chuẩn, mức và đơn vị ghi trong nguồn. Bỏ câu khen chung chung, lịch sử hãng và danh sách mời tra thêm. Chỉ nêu giá, tồn kho, màu hoặc size khi được hỏi và theo quyền tra cứu của phiên.
Nếu người dùng hỏi một tính năng cụ thể, trả lời thẳng vào tính năng đó. Nếu thiếu chi tiết về mẫu, dùng search_knowledge khi công cụ được cấp quyền, tìm theo đúng tên mẫu và tính năng hoặc thông số cần hỏi. Chỉ nêu các điểm có căn cứ trong ghi chú tư vấn hoặc đoạn nguồn của đúng mẫu. Nếu nguồn thiếu thông số, nói ngắn gọn phần chưa xác nhận được.`

// AddProductAdviceInstruction keeps the flow's configured default prompt when
// there is no system prompt override for the turn.
func AddProductAdviceInstruction(input, prompt string) (string, string) {
	if strings.TrimSpace(prompt) == "" {
		return input + "\n\n" + ProductAdviceInstruction, prompt
	}
	return input, prompt + "\n\n" + ProductAdviceInstruction
}
