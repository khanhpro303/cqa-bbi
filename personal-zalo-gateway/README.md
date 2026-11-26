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

- `GET /health`
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
3. Trong **AI Engine** của CQA, cấu hình Langflow và **public flow** dùng để trả lời khách hàng.
4. Tạo kênh Zalo cá nhân với phạm vi đồng bộ `direct` hoặc `all`, kết nối tài khoản bằng QR và xác nhận trạng thái đã kết nối.
5. Bật công tắc chatbot toàn tenant, sau đó bật **Tự động trả lời** trên chính kênh Zalo cá nhân.
6. Gửi một tin nhắn văn bản 1:1 mới tới tài khoản. Tin lịch sử, tin nhóm, ảnh/tệp, tin do chính tài khoản gửi và bản ghi trùng không kích hoạt chatbot.

Luồng này dùng public flow của CQA. Bản hiện tại chưa truyền quyền ERP có phạm vi cho tài khoản Zalo cá nhân.

Gateway xử lý tối đa ba hội thoại đồng thời trên mỗi phiên Zalo, giữ tin mới nhất đang chờ trong từng hội thoại và giãn cách các lần gửi tự động tối thiểu ba giây. Nếu tài khoản tự gửi tin trong cùng hội thoại khi bot đang xử lý, gateway hủy phản hồi đang chờ.

Tin đến ở hội thoại vượt giới hạn vẫn được đồng bộ. Khi AI hoặc thao tác gửi gặp lỗi, gateway vẫn lưu và đồng bộ tin nhắn theo lịch bình thường.

Thiết kế listener, chống phản hồi sự kiện hệ thống và gửi trả qua phiên cá nhân tham khảo kiến trúc của [Zalo Flow](https://github.com/aizaloapp/zalo-flow). Phần tích hợp này được triển khai riêng theo contract nội bộ của CQA.
