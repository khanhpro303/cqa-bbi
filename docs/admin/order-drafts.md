# Tạo, sửa, huỷ đơn qua chatbot

Chatbot Zalo có thể tạo đơn đặt hàng mới, sửa đơn hoặc huỷ đơn trên Cloudify. AI không ghi thẳng lên ERP: nó chỉ tạo **bản nháp**. Đơn chỉ được ghi khi chính người yêu cầu trả lời **1** (hoặc "xác nhận").

## Lên đơn nhiều sản phẩm qua chat (giỏ hàng)

Khách (hoặc nhân viên đặt hộ) có thể lên một đơn hoàn chỉnh ngay trong cuộc chat, giống đặt trên web dealers. Ví dụ:

> **Khách:** lên đơn giúp em: FF800 Storm đen L 1 cái, kính FF800 1 cái, FF901 1 cái
> **Bot:** Dạ em đã ghi FF800 Storm Gloss Black L và kính FF800. Riêng FF901 có 2 dòng: 1) LS2 FF901, 2) LS2 FF901 Carbon. Anh lấy dòng nào ạ?
> **Khách:** carbon size M
> **Bot:** Dạ đã thêm FF901 Carbon M. Anh còn lấy gì nữa không ạ?
> **Khách:** à Storm lấy 2 cái, bỏ kính đi. Chốt
> **Bot:** *(bản xác nhận đầy đủ: từng dòng, số lượng, đơn giá, tạm tính)* Trả lời 1 để lên đơn.
> **Khách:** 1
> **Bot:** ✅ Đã lên đơn CQA-261119-XXXXXX cho KH125 - Shop A. Đơn gồm 2 sản phẩm (3 món), tạm tính ... Bên em sẽ báo khi đơn đổi trạng thái.

### Cách hoạt động

- Mỗi cuộc chat có **một giỏ hàng** (bảng `order_carts`), lưu qua nhiều lượt chat, giữ 24 giờ kể từ lần sửa cuối. Bot luôn thấy giỏ hiện tại trong system prompt, nên khách sửa lúc nào cũng được.
- Agent gọi 4 tool MCP (cần quyền `order_create`):
  - `order_cart_update`: thêm, đổi số lượng (`op=set`), bỏ (`op=remove`) sản phẩm; trả lời câu hỏi chọn sản phẩm (`pending_id` + `option` hoặc `line`/`color`/`size`). Agent ghi `product` đúng như khách gõ, **backend** tự tìm SKU.
  - `order_cart_view`, `order_cart_clear`.
  - `order_cart_checkout`: gửi bản xác nhận cuối cùng.
- Backend phân giải sản phẩm:
  - Tên khớp **nhiều dòng** (FF901 và FF901 Carbon): không đoán, trả câu hỏi `pending` kèm lựa chọn đánh số. Chỉ khi khách gõ đúng tên đầy đủ của một dòng mới tự chọn dòng đó.
  - Đã rõ dòng nhưng **thiếu màu/size**: hỏi lại, chỉ đưa các biến thể còn hàng ở kho tổng, kèm danh sách màu và size.
  - Màu tiếng Việt ("đen") được đổi sang giá trị trong kho ("Gloss Black") bằng prompt "Khớp màu, size, thương hiệu".
  - Không tìm thấy tên nào khớp: dùng tìm kiếm gần đúng, nhưng vẫn hỏi khách xác nhận sản phẩm gần nhất.
  - Giá lấy từ `DON_GIA_BAN`, tồn lấy từ báo cáo tồn 1.KHO-TONG (giữ 2 phút). Dòng vượt tồn được đánh dấu và không cho chốt.
- Khi chốt: backend đọc lại giá và tồn, tạo bản nháp `order_actions` (gắn `cart_id`), gửi bản xác nhận đầy đủ, chờ **30 phút**.
  - Khách sửa giỏ sau khi đã nhận bản xác nhận: bản cũ bị huỷ, agent gửi bản xác nhận mới. Trả lời "1" luôn chốt đúng danh sách khách thấy cuối cùng.
  - Khách trả lời "2"/"huỷ": chưa lên đơn, giỏ vẫn giữ để sửa tiếp.
  - Khách trả lời "1": ghi đơn lên Cloudify, gửi lời chốt (mã đơn, số sản phẩm, tạm tính), đóng giỏ.
- Khi giỏ đang mở, câu trả lời ngắn như "2", "ok", "cái carbon" không bị bộ phân loại coi là tán gẫu và đóng phiên.
- Cron theo dõi đơn không gửi thêm tin "đơn đã được tạo" cho đơn khách vừa chốt trong chat, nhưng vẫn báo khi đơn đổi trạng thái.

### Sửa cách bot lên đơn

AI Engine → **Prompt cho các tác vụ AI** → **Lên đơn qua chatbot (giỏ hàng)**. Phần này được nối vào system prompt của bot khi người chat có quyền tạo đơn hoặc đang có giỏ dở. Phần giỏ hàng hiện tại và quy ước `[RICH_MESSAGE_SENT]` do hệ thống thêm, không sửa được. Chỉ áp dụng khi kênh hoặc AI Engine có system prompt; nếu để trống, flow dùng prompt riêng trong Langflow và bot chỉ biết giỏ hàng qua tool.

Sau khi deploy, mở node MCP Tools trong các flow Langflow v2 và làm mới danh sách tool để agent thấy 4 tool `order_cart_*`. Tool `draft_order_create` không còn được liệt kê (vẫn chạy cho flow chưa làm mới).

## Luồng xử lý (bản nháp sửa, huỷ đơn)

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
