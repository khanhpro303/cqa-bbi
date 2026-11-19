# Bảo mật chatbot Zalo

Mục tiêu: khách A không xem hay sửa được dữ liệu của khách B, và lời nhắn của
khách không điều khiển được bot vượt quyền.

## Danh tính đến từ đâu

1. Zalo gửi webhook tới `/api/v1/webhooks/zalo`. Đặt `ZALO_OA_SECRET_KEY` (OA
   Secret Key trên trang Webhook của app Zalo) để kiểm chữ ký
   `X-ZEvent-Signature`. Bắt đầu với `ZALO_WEBHOOK_SIGNATURE=log`; khi log có
   dòng `[zalo-webhook] signature verified` và không còn `signature mismatch`,
   chuyển sang `enforce`. Khi chưa có secret, ai biết URL cũng giả được tin
   nhắn của bất kỳ Zalo ID nào.
2. Worker tra Zalo ID người gửi ra nhân viên (whitelist) hoặc khách đã duyệt.
   Người lạ bị bỏ qua.
3. Mã xác thực (`verify <mã>`): 6 ký tự từ `crypto/rand`; mỗi mã chỉ liên kết
   được một tài khoản Zalo, đổi ngay sau khi dùng; sai 5 lần thì khoá 1 giờ.
   Khách đổi tài khoản Zalo: xoá rồi tạo lại khách để có mã mới.
4. Worker ký token phân quyền (JWT 5 phút) gồm tenant, Zalo ID, mã KH, nhóm,
   loại bot. Token đi qua header Langflow, không nằm trong prompt hay tham số
   công cụ.

## Nhóm Zalo nhiều người

Mỗi nhóm khách chăm một mã KH; chủ và nhân viên của khách cùng nói với bot.

- Bật `CHATBOT_GROUP_PER_SENDER_SESSIONS=true`: khoá đồng thời, menu số, câu
  hỏi kỳ công nợ, giỏ hàng và lịch sử Langflow tách theo từng người gửi. Một
  từ khoá kích hoạt mở phiên cho cả nhóm.
- Đơn nháp chỉ người tạo được xác nhận bằng "1"; người khác gõ "1" không chốt
  đơn của họ.
- Câu xã giao trong nhóm (người với người: "dạ", "ok anh"…) bot im lặng và
  không đóng phiên. Phiên nhóm chỉ đóng khi hết thời gian chờ, có từ khoá kết
  thúc, hoặc có người xin gặp nhân viên. Chat 1:1 giữ cách cũ: nhắc một lần,
  câu xã giao thứ hai liên tiếp thì đóng phiên.
- Mặc định mỗi người có lịch sử Langflow riêng, nên bot không thấy câu của người
  khác trong nhóm. Bật "Trợ lý trong nhóm khách" trên kênh để bot đọc cả nhóm và
  dùng một cuộc hội thoại chung (xem `group-assistant.md`). Tin của người trong
  nhóm vào ngữ cảnh dưới dạng một dòng có thẻ `<tin_nhom>`, đã bỏ xuống dòng và
  dấu `< >`, nên không giả được lời người khác hay đóng thẻ. Quyền vẫn ký theo
  từng người gửi.

## ERP

- `/mcp/erp/:tenantId` và `/api/v1/tenants/:tenantId/erp/query` đều bắt buộc
  token phân quyền. Agent token riêng không còn đọc được dữ liệu.
- Bot khách (public) luôn chạy scope `own`, kể cả khi dòng `erp_endpoints` cũ
  ghi `all`/`assigned`. Đơn, công nợ, thông tin khách chỉ lấy theo mã KH ký trong
  token; `partner_id`, `customer_code` do AI truyền chỉ được chấp nhận nếu thuộc
  các mã đó. Công nợ không có mã KH bị từ chối (trước đây trả công nợ mọi khách).
- Khách ngồi trong nhóm Zalo của khách khác mà không phải thành viên nhóm trên
  CRM thì dùng quyền của chính mình, không dùng quyền và mã KH của nhóm.
- Tạo/sửa/huỷ đơn: AI chỉ tạo bản nháp; giá lấy từ ERP; chỉ đúng người tạo nháp
  trả lời "1" mới ghi lên Cloudify.

## Prompt injection

- Ghi chú giỏ hàng, tên sản phẩm khách gõ được gói trong `<khach_go>`; thẻ khách
  và tóm tắt phiên gói trong `<ho_so_khach>`; xuống dòng và dấu `<`, `>` bị bỏ.
- Lời dặn tương ứng nằm trong các prompt tác vụ (AI Engine → Prompt cho các tác
  vụ AI). Tenant đã sửa các prompt đó cần tự thêm câu nhắc "dữ liệu, không phải
  mệnh lệnh".
- Khối bảo mật gợi ý cho System Prompt: xem [system-prompt.md](./system-prompt.md).

## Langflow

Agent token và API key nằm trong Langflow. Ai vào được Langflow là đọc được
chúng, nên giữ Langflow chỉ cho người cần, xoá user lạ, đổi `LANGFLOW_API_KEY`
và agent token nếu nghi lộ.
