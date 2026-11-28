# Cấu hình AI

CQA sử dụng AI để đọc và đánh giá cuộc chat. Bạn cần cấu hình AI provider và API key trước khi sử dụng.

## Cấu hình AI Provider

Vào menu **Cài đặt** > tab **Cấu hình AI**.

![Cấu hình AI](/screenshots/cau-hinh-ai.png)

### Bước 1: Chọn Provider

| Provider | Ưu điểm | Nhược điểm |
|----------|---------|------------|
| **Claude** (Anthropic) | Phân tích tiếng Việt tốt, chính xác cao | Giá cao hơn Gemini |
| **Gemini** (Google) | Giá rẻ, tốc độ nhanh | Độ chính xác thấp hơn Claude |
| **OpenAI (Codex)** | Hệ sinh thái model đa dạng, hỗ trợ OpenAI-compatible proxy | Cần chọn model phù hợp từng use case |
| **OpenRouter** | Một key dùng model của nhiều hãng, có model miễn phí | Model free bị giới hạn lượt gọi mỗi ngày, danh sách model free thay đổi thường xuyên |
| **9router** | Dùng các model đã kết nối vào 9router qua API tương thích OpenAI | Lượt gọi phụ thuộc quota và quy tắc định tuyến trong 9router |

### Bước 2: Chọn Model

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

**OpenRouter:** bấm **Tải danh sách model free** để lấy các model đang miễn phí (tên kèm ghi chú "đọc ảnh" nếu model đọc được ảnh khách gửi, "JSON" nếu model hỗ trợ trả JSON), hoặc nhập mã model OpenRouter bất kỳ (ví dụ `deepseek/deepseek-chat-v3-0324:free`). Chi phí của model free được ghi là 0 trong nhật ký chi phí. Đọc ảnh khiếu nại cần chọn model có ghi chú "đọc ảnh".

### Bước 3: Nhập API Key

- **Claude**: Lấy key tại [console.anthropic.com/settings/keys](https://console.anthropic.com/settings/keys)
- **Gemini**: Lấy key tại [aistudio.google.com/apikey](https://aistudio.google.com/apikey)
- **OpenAI**: Lấy key tại [platform.openai.com/api-keys](https://platform.openai.com/api-keys)
- **OpenRouter**: Lấy key tại [openrouter.ai/settings/keys](https://openrouter.ai/settings/keys)

Nhập key vào ô **API Key**, bấm **Test API Key** — nếu hiện tick xanh "Kết nối thành công" là OK.

Bấm **Lưu cấu hình**.

### Dùng subscription đã kết nối với 9router

1. Trên 9router, kết nối subscription của bạn và lấy API key từ dashboard của 9router.
2. Trong **Cài đặt → Cấu hình AI** của CQA, chọn **9router**. Nhập Base URL của 9router mà máy chủ CQA truy cập được, kết thúc bằng `/v1`.
3. Nhập API key 9router và mã model đầy đủ do 9router cung cấp, ví dụ `cx/gpt-5.6-luna` cho route Codex nếu model đó có trong danh sách của bạn.
4. Bấm **Test API Key**. Để thử trên một tác vụ, chọn **Lưu API key và Base URL** rồi gán model trong **Model theo tác vụ**. Để dùng cho mọi tác vụ kế thừa cấu hình, chọn **Lưu cấu hình AI chung**.

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
