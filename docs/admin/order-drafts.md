# Tạo, sửa, huỷ đơn qua chatbot

Chatbot Zalo có thể tạo đơn đặt hàng mới, sửa đơn hoặc huỷ đơn trên Cloudify. AI không ghi thẳng lên ERP: nó chỉ tạo **bản nháp**. Đơn chỉ được ghi khi chính người yêu cầu trả lời **1** (hoặc "xác nhận").

## Luồng xử lý

1. Agent trong Langflow gọi một trong ba tool MCP của `/mcp/erp/:tenantId`:
   - `draft_order_create`: tham số `items` [{sku, quantity}], `note`. Nhân viên đặt hộ khách thì truyền thêm `customer_code`.
   - `draft_order_update`: tham số `order_code`, `items` (chỉ các dòng cần đổi; quantity là số lượng mới, 0 để bỏ dòng), `note`. Nhân viên được truyền thêm `status`.
   - `draft_order_cancel`: tham số `order_code`, `reason`. Huỷ đơn là chuyển `TRANG_THAI` sang `3`, không xoá đơn.
2. Backend tự dựng nội dung đơn từ dữ liệu tin cậy:
   - Khách hàng lấy từ token phân quyền. Khách chỉ đặt được cho mã của chính mình; nhân viên đặt được cho khách trong phạm vi được giao.
   - Giá lấy từ `DON_GIA_BAN` trong cache sản phẩm. Hiện chưa áp bảng giá hay chiết khấu.
   - Khi sửa hoặc huỷ, backend đọc đơn đang có trên Cloudify.
3. Backend lưu một dòng `order_actions` ở trạng thái `pending`, hết hạn sau 10 phút, rồi gửi tóm tắt đơn vào Zalo.
4. Worker đọc câu trả lời tiếp theo của đúng người đó, **trước** bộ phân loại ý định:
   - `1`, `xác nhận`, `ok`, `đồng ý`: ghi lên Cloudify.
   - `2`, `huỷ`, `không`: bỏ bản nháp.
   - Câu khác: đi tiếp vào agent như bình thường, bản nháp vẫn chờ.
5. Bản nháp được nhận bằng một lệnh UPDATE có điều kiện (`pending → executing`), nên mỗi bản nháp ghi lên ERP đúng một lần, kể cả khi Zalo gửi lặp tin nhắn.
   - Nếu Cloudify từ chối, bản nháp chuyển sang `failed`.
   - Nếu hết thời gian chờ mà không rõ kết quả, bản nháp chuyển sang `unknown` và **không** tự gửi lại. Nhân viên tìm đơn theo mã `CQA-...` để kiểm tra.

## Mã trạng thái Cloudify (`TRANG_THAI`)

| Mã | Ý nghĩa |
|---|---|
| 0 | Chưa thực hiện |
| 1 | Đang thực hiện |
| 2 | Hoàn thành |
| 3 | Huỷ bỏ |
| 4 | Đang giao |

Khách chỉ sửa hoặc huỷ được đơn ở trạng thái 0 hoặc 1. Nhân viên sửa được mọi đơn chưa huỷ.

## Những điều đã kiểm chứng trên Cloudify thật (18/11/2026)

- `saorders/create` nhận mã dạng chữ cho `MA_KHACH_HANG`, `MA_HANG` và `DVT`.
- Cloudify giữ nguyên `MA_HOA_DON` gửi lên và không tự sinh số. Vì vậy bot đặt mã `CQA-<yyMMdd>-<6 ký tự>`.
- `GHI_CHU` cấp đơn **không** được lưu. Ghi chú và thẻ `[CQA:...]` được đặt ở dòng hàng đầu tiên.
- `saorders/{id}/update` cần gửi **toàn bộ** đơn. Gửi riêng `TRANG_THAI` thì Cloudify trả lỗi 500. Danh sách dòng hàng bị **thay toàn bộ**: dòng nào không gửi lên sẽ bị xoá.
- Nếu SKU sai, Cloudify báo nhầm là lỗi "vượt quá số lượng tồn khả dụng". Vì vậy bot tự kiểm tra SKU trước khi gửi.

## Bật tính năng

Ba quyền mới, **mặc định tắt**: `order_create`, `order_update`, `order_cancel`.

- Nhân viên (bot nội bộ): AI Engine → phân quyền bot nội bộ → bật "Tạo đơn / Sửa đơn / Huỷ đơn (chatbot)". Khác với quyền đọc, bot nội bộ chưa cấu hình thì **không** có quyền ghi.
- Khách hàng: CRM → nhóm → phân quyền ERP → bật các quyền trên cho nhóm.

Sau khi deploy, mở node MCP Tools trong flow Langflow và làm mới danh sách tool, để agent thấy ba tool `draft_order_*`.
