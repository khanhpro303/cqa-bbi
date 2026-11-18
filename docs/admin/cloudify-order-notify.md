# Báo đơn hàng Cloudify cho khách qua Zalo

Khi có đơn mới hoặc đơn đổi trạng thái trên Cloudify, CQA tự nhắn vào nhóm Zalo (GMF) của khách đã gắn mã KH của đơn đó.

## Vì sao không dùng websocket như Pancake

Pancake chạy được vì máy chủ Pancake **tự đẩy** sự kiện "nhãn vừa đổi" qua websocket của web Pancake, và CQA nghe chung đường đó.

Cloudify (nền Odoo) chỉ cung cấp REST: `saorders/search` lọc theo ngày tạo (`TU_NGAY`/`DEN_NGAY`) hoặc mã đơn (`SO_DON_HANG`). Tài liệu API không có webhook và đơn hàng không có trường "thời điểm sửa". Websocket `/websocket` của giao diện Odoo mặc định chỉ đẩy chat, thông báo và hoạt động, không đẩy thay đổi đơn hàng. Nếu kiểm tra thực tế (DevTools → Network → WS khi tạo hoặc sửa đơn) thấy có tin về đơn, có thể thêm một bộ nghe làm nguồn thứ ba. Nguồn đó chỉ cần gọi cùng bước `Ingest` bên dưới.

## Cách hoạt động

```
           ┌──────────── mỗi 30s ────────────┐
Cloudify ──┤ saorders/search (N ngày gần đây) ├──► Ingest ──► cloudify_order_events (outbox) ──► nhóm Zalo của mã KH
           └─ webhook /webhooks/cloudify/... ─┘   so với          (mỗi thay đổi đúng 1 dòng)
              (tuỳ chọn, đọc lại 1 đơn ngay)       snapshot
```

1. **Quét** (`backend/cloudifyorders/watcher.go`): mỗi tenant có một vòng lặp. Khoá (lease) trong DB đảm bảo chỉ một máy chủ quét mỗi tenant. Mỗi chu kỳ đọc toàn bộ đơn trong cửa sổ N ngày.
2. **So sánh** (`diff.go`): mỗi đơn được băm toàn bộ nội dung (trạng thái, dòng hàng, ghi chú, giảm giá). Nếu khác bản lưu, đơn được phân loại:
   - `created`: đơn lần đầu thấy, ngày tạo từ ngày bật tính năng trở đi.
   - `status_changed`: `TRANG_THAI` đổi.
   - `updated`: nội dung khác đổi, trạng thái giữ nguyên.
3. **Không trùng**: mỗi thay đổi có `dedup_key = hash(tenant, id đơn, bản băm cũ, bản băm mới)` duy nhất. Lượt quét và webhook cùng thấy một thay đổi thì vẫn chỉ có một sự kiện.
4. **Gửi** (`deliver.go`): sự kiện đang chờ được nhắn vào mọi nhóm Zalo có mã KH đó, gồm `crm_groups.customer_code` và `crm_group_customer_codes`. Lỗi gửi được thử lại tối đa 5 lần (30 giây, 1, 2, 4 phút). Sự kiện quá 2 giờ chưa gửi được thì bỏ qua để không báo trễ.

Lần quét đầu tiên chỉ ghi nhận hiện trạng, **không nhắn khách**, để bật tính năng không làm khách nhận tin về các đơn cũ.

## Bật tính năng

Vào **AI Engine**, mục **Báo đơn hàng Cloudify cho khách qua Zalo**:

| Mục | Mặc định | Ghi chú |
|---|---|---|
| Theo dõi đơn hàng Cloudify | tắt | Bật để bắt đầu quét. |
| Nhắn Zalo cho khách | tắt | Tắt thì vẫn ghi nhận thay đổi nhưng không gửi. Nên bật theo dõi trước, xem bảng "Thay đổi gần đây" một thời gian, rồi mới bật nhắn. |
| Chu kỳ quét | 30 giây | 15 đến 3600 giây. |
| Quét đơn trong | 7 ngày | Đơn tạo trước khoảng này sẽ không được theo dõi đổi trạng thái. |
| Báo khi | Đơn mới, Đổi trạng thái | |
| Mẫu tin | xem `settings.go` | Biến: `{ma_don} {khach_hang} {trang_thai} {trang_thai_cu} {tong_tien} {ngay}` |

Biến môi trường `CQA_CLOUDIFY_ORDER_WATCH_ENABLED` (mặc định `true`) bật hoặc tắt vòng quét cho cả máy chủ.

Mã trạng thái: `0` Hủy, `1` Đang thực hiện, `2` Hoàn thành, `3` Đang giao.

## Webhook (tuỳ chọn, độ trễ khoảng 1 giây)

Nếu bên Cloudify bật được **Automated Action → Send Webhook Notification** (Odoo 17 trở lên) trên model đơn bán hàng, khi tạo hoặc khi cập nhật:

1. Bấm **Tạo URL** trong thẻ cài đặt để lấy URL dạng `https://<cqa>/api/v1/webhooks/cloudify/<tenant>?secret=...`. URL chỉ hiện một lần. CQA chỉ lưu bản băm của secret.
2. Dán URL vào Automated Action và chọn gửi kèm trường `name` (mã đơn).

CQA không tin nội dung webhook. Nó chỉ dùng mã đơn để đọc lại đơn đó từ Cloudify. Nếu không có mã đơn, CQA quét ngay một lượt. Lượt quét định kỳ vẫn chạy làm lưới an toàn.

## Giới hạn

- Chỉ nhắn vào **nhóm Zalo** đã gắn mã KH. Khách chỉ chat 1:1 với OA chưa được nhắn, vì Zalo OA giới hạn tin chủ động ngoài khung tương tác.
- Đơn bị xoá không được báo: không phân biệt được "bị xoá" với "nằm ngoài cửa sổ".
- Mỗi lượt quét đọc toàn bộ đơn trong cửa sổ. Nếu số đơn lớn, nên tăng chu kỳ hoặc giảm số ngày.
