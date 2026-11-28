# Cấu hình AI

CQA sử dụng AI để đọc và đánh giá cuộc chat. Bạn cần lưu API key của ít nhất một nhà cung cấp trước khi sử dụng.

Cấu hình AI chia làm hai chỗ:

- **Cài đặt › Cấu hình AI**: chìa khoá. API key (và Base URL nếu dùng proxy) của từng nhà cung cấp, token 9router, nút **Kiểm tra key** và nút **Làm mới danh sách model**.
- **Agents AI › Model tác vụ**: cách dùng. Model mặc định, model của chatbot Langflow, model của từng agent và model riêng cho từng tác vụ, chia nhóm **Quanh chatbot** và **Nghiệp vụ khác**. Tab này dùng được cả khi chưa bật agent.

## Lưu API key

Vào menu **Cài đặt** > tab **Cấu hình AI**. Mỗi nhà cung cấp là một mục riêng, ghi rõ đã lưu key chưa và nhà cung cấp nào đang là mặc định.

![Cấu hình AI](/screenshots/cau-hinh-ai.png)

### Các nhà cung cấp

| Provider | Ưu điểm | Nhược điểm |
|----------|---------|------------|
| **Claude** (Anthropic) | Phân tích tiếng Việt tốt, chính xác cao | Giá cao hơn Gemini |
| **Gemini** (Google) | Giá rẻ, tốc độ nhanh | Độ chính xác thấp hơn Claude |
| **OpenAI (Codex)** | Hệ sinh thái model đa dạng, hỗ trợ OpenAI-compatible proxy | Cần chọn model phù hợp từng use case |
| **OpenRouter** | Một key dùng model của nhiều hãng, có model miễn phí | Model free bị giới hạn lượt gọi mỗi ngày, danh sách model free thay đổi thường xuyên |
| **9router** | Dùng các model đã kết nối vào 9router qua API tương thích OpenAI | Lượt gọi phụ thuộc quota và quy tắc định tuyến trong 9router |

### Model gợi ý

Model được chọn ở **Agents AI › Model tác vụ**. Danh sách gợi ý:

**Claude:**
| Model | Đặc điểm | Phù hợp |
|-------|----------|---------|
| Claude Sonnet 4.6 | Cân bằng chất lượng và chi phí | Khuyến nghị cho hầu hết trường hợp |
| Claude Haiku 4.5 | Nhanh, rẻ nhất | Số lượng chat lớn, budget hạn chế |
| Claude Opus 4 | Mạnh nhất, đắt nhất | Yêu cầu phân tích phức tạp |

**Gemini:**
| Model | Đặc điểm | Phù hợp |
|-------|----------|---------|
| Gemini 2.0 Flash | Nhanh, rẻ | Phân loại đơn giản |
| Gemini 2.5 Pro | Mạnh hơn | Phân tích chi tiết |

**OpenAI (Codex):**
| Model | Đặc điểm | Phù hợp |
|-------|----------|---------|
| GPT-5.4 Mini | Cân bằng chất lượng/chi phí | Khuyến nghị mặc định |
| GPT-5.3-Codex | Tối ưu coding/agentic | Luồng phân tích phức tạp, nhiều bước |
| GPT-5.3 Chat | Mạnh cho hội thoại tổng quát | Luồng chat tổng quát |
| GPT-5 Mini | Chi phí thấp hơn | Bài toán đơn giản |
| GPT-5 Nano | Nhanh, rẻ nhất | Khối lượng lớn, yêu cầu nhẹ |

**OpenRouter:** danh sách model free tự tải, bấm **Làm mới danh sách model** ở Cấu hình AI để lấy bản mới nhất. Danh sách gồm các model đang miễn phí (tên kèm ghi chú "đọc ảnh" nếu model đọc được ảnh khách gửi, "JSON" nếu model hỗ trợ trả JSON), hoặc nhập mã model OpenRouter bất kỳ (ví dụ `deepseek/deepseek-chat-v3-0324:free`). Chi phí của model free được ghi là 0 trong nhật ký chi phí. Đọc ảnh khiếu nại cần chọn model có ghi chú "đọc ảnh".

### Nhập API key

- **Claude**: Lấy key tại [console.anthropic.com/settings/keys](https://console.anthropic.com/settings/keys)
- **Gemini**: Lấy key tại [aistudio.google.com/apikey](https://aistudio.google.com/apikey)
- **OpenAI**: Lấy key tại [platform.openai.com/api-keys](https://platform.openai.com/api-keys)
- **OpenRouter**: Lấy key tại [openrouter.ai/settings/keys](https://openrouter.ai/settings/keys)

Mở mục của nhà cung cấp, nhập key, bấm **Lưu API key và Base URL**, rồi **Kiểm tra API Key**. Nếu nhà cung cấp mặc định chưa có key, nhà cung cấp lưu key đầu tiên sẽ thành mặc định.

## Chọn model

Vào **Agents AI** › tab **Model tác vụ**:

1. **Model mặc định**: tác vụ nào để "Dùng model mặc định" chạy bằng model này. Chỉ chọn được nhà cung cấp đã lưu key.
2. **Quanh chatbot**: model trong flow Langflow của chatbot nội bộ và chatbot khách hàng (chọn rồi bấm **Đồng bộ vào Langflow**), bảng model của từng agent, và các tác vụ phụ của chatbot (phân loại tin nhắn, bộ điều phối chọn agent, xã giao, đọc ảnh, khiếu nại, bộ nhớ).
3. **Nghiệp vụ khác**: phân tích hội thoại, ERP (duyệt đơn Cloudify, đối chiếu mã), kho tri thức.

Bấm **Lưu N thay đổi** ở cuối trang để lưu model theo tác vụ.

### Dùng subscription đã kết nối với 9router

1. Trên 9router, kết nối subscription của bạn và lấy API key từ dashboard của 9router.
2. Trong **Cài đặt → Cấu hình AI** của CQA, mở mục **9router**. Nhập API key 9router và Base URL mà máy chủ CQA truy cập được, kết thúc bằng `/v1`.
3. Bấm **Lưu kết nối 9router**, rồi **Kiểm tra token**: CQA đọc danh sách model của 9router, đọc được là token hoạt động.
4. Vào **Agents AI › Model tác vụ**, chọn 9router và model đầy đủ (ví dụ `cx/gpt-5.6-luna`) cho từng tác vụ, hoặc đặt làm **Model mặc định** cho mọi tác vụ kế thừa. Thêm model mới trong 9router thì bấm **Làm mới danh sách model** ở Cấu hình AI.

Nếu mục tiêu là dùng quota của subscription, hãy chọn trực tiếp model của provider đã kết nối và kiểm tra quy tắc fallback trong 9router trước khi dùng một Combo. Langflow và embedding có cấu hình riêng.

## Batch Mode

Batch mode gom nhiều cuộc chat vào 1 lần gọi AI, giúp tiết kiệm token đáng kể.

### Cách hoạt động

- **Tắt batch**: Mỗi cuộc chat = 1 lần gọi API → chính xác nhất nhưng tốn token
- **Bật batch**: Gom N cuộc chat = 1 lần gọi API → tiết kiệm 60-80% token

### Cấu hình Batch

1. Bật **Chế độ Batch**
2. Chọn **Batch Size** (số cuộc chat gom lại):

| Batch Size | Tiết kiệm | Rủi ro | Khuyến nghị |
|-----------|-----------|--------|-------------|
| 3 | ~40% | Thấp | Mới bắt đầu |
| 5 | ~60% | Thấp | Khuyến nghị chung |
| 10 | ~75% | Trung bình | Đã quen, chat đơn giản |
| 15-20 | ~80% | Cao | Chat ngắn, phân loại đơn giản |
| 30 | ~85% | Cao | Chỉ dùng cho phân loại |

::: tip Khuyến nghị
Bắt đầu với batch size **5**. Sau khi kiểm tra kết quả chính xác, tăng lên **10**.
Với công việc **Phân loại** (không cần chấm điểm chi tiết), có thể dùng **10-20**.
:::

::: warning Lưu ý
Batch size lớn = nếu API lỗi, mất kết quả nhiều cuộc chat cùng lúc. Nên giữ ở mức 5-10 cho an toàn.
:::

### So sánh chi phí thực tế

Ví dụ đánh giá 100 cuộc chat:

| Chế độ | Số lần gọi API | Chi phí ước tính (Claude Sonnet) |
|--------|----------------|----------------------------------|
| Không batch | 100 lần | ~$2.00 |
| Batch 5 | 20 lần | ~$0.80 |
| Batch 10 | 10 lần | ~$0.50 |

*Chi phí thực tế phụ thuộc vào độ dài cuộc chat.*
