# Personal Zalo Gateway

Sidecar mỏng cho `chat-quality-agent` để ingest `Zalo cá nhân`.

## Trách nhiệm

- QR login bằng `zca-js`
- lưu session cục bộ
- auto reconnect khi listener rớt
- normalize message thành contract import của CQA
- flush theo batch có HMAC signing sang `/api/internal/imports/personal-zalo`
- với tin nhắn văn bản 1:1 mới, gọi AI Engine của CQA bằng HMAC rồi gửi phản hồi qua đúng phiên Zalo đang kết nối
- chatbot chỉ hoạt động khi có `PERSONAL_ZALO_CHATBOT_BASE_URL` và `PERSONAL_ZALO_CHATBOT_SECRET` dài tối thiểu 32 ký tự

## API v1

Các route `/api/v1/accounts` yêu cầu header `Authorization: Bearer <token>`. Đặt cùng giá trị `PERSONAL_ZALO_GATEWAY_ADMIN_SECRET` (ít nhất 32 ký tự) cho CQA và gateway. Quy trình deploy VPS tự cấp khóa khi cài đặt lần đầu.

- `GET /health` kiểm tra tiến trình HTTP.
- `GET /ready` trả trạng thái listener, đồng bộ và lượt chatbot ở dạng tổng hợp. Kiểm tra phản hồi HTTP 503 để biết lý do dịch vụ suy giảm.
- `GET /api/v1/accounts`
- `GET /api/v1/accounts/:accountId`
- `POST /api/v1/accounts`
- `PUT /api/v1/accounts/:accountId`
- `POST /api/v1/accounts/:accountId/login/qr`
- `POST /api/v1/accounts/:accountId/reconnect`
- `POST /api/v1/accounts/:accountId/sync`

## Account payload

```json
{
  "tenantId": "tenant-uuid",
  "channelId": "channel-uuid",
  "importEndpoint": "http://app:8080/api/internal/imports/personal-zalo",
  "importSecret": "secret-from-cqa-channel",
  "accountExternalId": "",
  "displayName": "Sales Admin 01"
}
```

`accountExternalId` có thể để trống lúc đầu. Sau khi login thành công, gateway sẽ bind bằng `ownId` của tài khoản Zalo.

## Bật chatbot Zalo cá nhân

1. Tạo khóa dùng chung giữa CQA và gateway, rồi đặt cùng giá trị vào `PERSONAL_ZALO_CHATBOT_SECRET` của hai service:

   ```bash
   openssl rand -hex 32
   ```

2. Đặt `PERSONAL_ZALO_CHATBOT_BASE_URL` trỏ tới CQA đáng tin cậy. Docker Compose dùng mặc định `http://app:8080`. Môi trường chạy trực tiếp phải khai báo giá trị này rõ ràng.
3. Trong **AI Engine** của CQA, cấu hình hai flow Langflow riêng cho khách hàng (Public) và nhân viên (Private). Bật ERP và đặt token, quyền tra cứu cho từng nhóm người dùng theo cấu hình CQA.
4. Tạo kênh Zalo cá nhân với phạm vi đồng bộ `direct` hoặc `all`, kết nối tài khoản bằng QR và xác nhận trạng thái đã kết nối.
5. Bật công tắc chatbot toàn tenant, sau đó bật **Tự động trả lời** trên chính kênh Zalo cá nhân.
6. Trong chi tiết kênh, owner hoặc admin mở **Xác thực người dùng chatbot**, chọn nhân viên trong whitelist hoặc khách đã được duyệt và cấp lệnh `verify`. Gửi lệnh cho đúng người trong hội thoại riêng. Người nhận nhắn lệnh vào kênh Zalo cá nhân để liên kết tài khoản. Mã có hạn và dùng được một lần.
7. Gửi một tin nhắn văn bản 1:1 mới tới tài khoản. Tin lịch sử, ảnh/tệp, tin do chính tài khoản gửi và bản ghi trùng không kích hoạt chatbot.

**Kênh chat → Chỉnh sửa → Chatbot & Session** cho phép đặt từ khóa mở/đóng phiên, lời chào, lời kết và số phút không hoạt động. Dùng dấu `;` để nhập nhiều từ khóa. Khi đặt từ khóa mở, bot 1:1 chờ từ khóa trước khi bắt đầu phiên. Bỏ trống từ khóa mở để bot trả lời ngay.

**Kênh chat → Chỉnh sửa → Nhóm Zalo** cho phép bật trả lời nhóm và đặt tên gọi bot. Chọn phạm vi đồng bộ có nhóm, sau đó nhắn `Bot ơi, tư vấn giúp` trong nhóm có tài khoản cá nhân đó. Mỗi người gửi có phiên riêng. Có thể chọn một nhóm nhân viên đã liên kết OA hoặc một nhân viên trong whitelist để nhận yêu cầu chuyển tiếp. Trong nhóm, gọi bot kèm yêu cầu, ví dụ `Bot ơi, cho tôi gặp nhân viên`.

Trong chat 1:1, nhân viên đã liên kết dùng flow Private và quyền tra cứu `private_bot` của tenant. Khách đã liên kết dùng flow Public. Quyền ERP của khách giới hạn trong các nhóm được cấp và các mã khách được gắn với hồ sơ CRM của họ. Người chưa liên kết chỉ nhận hướng dẫn xác thực. Luồng tra cứu không cấp thao tác tạo, sửa hoặc huỷ đơn qua ERP.

Gateway xử lý tối đa ba lượt 1:1 và ba lượt nhóm đồng thời trên mỗi phiên Zalo. Các lượt hợp lệ chờ trong dữ liệu cục bộ khi hội thoại hoặc cả ba slot đang bận. Khi tài khoản tự nhắn vào cùng hội thoại, gateway hủy phản hồi đang chờ trong hội thoại đó.

Ba lớp bảo vệ áp dụng trên từng tài khoản:
- Giãn cách tối thiểu 3 giây và tối đa 20 lần gửi trong bất kỳ cửa sổ 60 giây nào.
- Nhận diện tin bot lặp theo nội dung và hội thoại trong 30 giây.
- Tạm ngưng chatbot 60 giây với người gửi quá 5 tin hợp lệ trong 3 giây. Tin nhắn vẫn được đồng bộ vào CQA.

Khi kết quả gọi CQA hoặc gửi Zalo chưa rõ, gateway giữ lượt để nhân viên đối chiếu. Dùng API quản trị `GET /api/v1/accounts/:accountId/delivery-issues` để lấy hội thoại và mã tin cần kiểm tra. Sau khi kiểm tra trên Zalo và hỗ trợ khách nếu cần, gọi `POST /api/v1/accounts/:accountId/delivery-issues/:key/acknowledge` với key đã URL-encode để đóng cảnh báo. Thao tác này giữ nguyên lịch sử hội thoại và không gửi tin cho khách.

Thiết kế listener, chống phản hồi sự kiện hệ thống và gửi trả qua phiên cá nhân tham khảo kiến trúc của [Zalo Flow](https://github.com/aizaloapp/zalo-flow). Phần tích hợp này được triển khai riêng theo contract nội bộ của CQA.
