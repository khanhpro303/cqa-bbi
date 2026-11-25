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
| VietQR cho phiếu xác nhận | chưa cấu hình | Điền mã BIN, số tài khoản hoặc bí danh và tên chủ tài khoản trong **Cấu hình chi tiết**. Khi bật báo đơn mới, CQA gửi một ảnh xác nhận có chi tiết đơn, ảnh sản phẩm có trong cache và QR điền sẵn số tiền và nội dung chuyển khoản. Xóa cả ba mục để dùng lại mẫu tin văn bản. |

Phiếu xác nhận dùng giá và số lượng ghi nhận lúc tạo đơn. Nếu CQA đã ghi nhận đổi mã khách hàng, đổi mã đơn, hủy đơn hoặc đổi tổng tiền trước khi gửi, phiếu ban đầu được bỏ qua. Đơn mới có tổng tiền bằng 0 được gửi ảnh phiếu xác nhận không kèm QR.

Nội dung chuyển khoản là mã khách hàng Cloudify. Ví dụ, khách hàng `S084 - Chợ Bảo Hộ` có nội dung chuyển khoản `S084`.

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

## Theo dõi khách xác nhận đơn hàng

Mở **CRM khách hàng → Theo dõi Đơn hàng**. Tab hiển thị mỗi lượt gửi phiếu đến một nhóm Zalo đã liên kết từ khi tính năng được bật. Trạng thái Cloudify và trạng thái khách xác nhận được hiển thị riêng. Khi mở một đơn, CRM gọi trực tiếp Cloudify theo mã đơn để đọc trạng thái, chi tiết sản phẩm, chiết khấu và tổng tiền hiện tại.

Khách có thể dùng **Trả lời** trên phiếu trong nhóm rồi nhắn “OK”, “duyệt”, hoặc ghi rõ mã đơn trong tin nhắn. Với tin nhắn không có trích dẫn hay mã đơn, bot chỉ ghi nhận khi lịch sử GMF cho thấy khách nhắn ngay sau phiếu. Tài khoản Zalo của khách cần được duyệt trong CRM, có trong nhóm và được gắn đúng mã khách hàng. LLM của tenant phân loại câu xác nhận tự nhiên. CRM lưu nguyên văn, người gửi, thời điểm và mã tin nhắn từ webhook đã xác thực.

### Ảnh chụp hội thoại Zalo

CRM có thể lưu ảnh PNG do Cloudflare Browser Run chụp trực tiếp trên **Zalo OA Manager**. Việc chụp chạy sau khi lưu xác nhận. Nhân viên có quyền sửa Cài đặt cũng có thể đính kèm ảnh chụp PNG hoặc JPEG tối đa 5 MiB. CRM giữ nguyên tệp đầu tiên được lưu.

Để bật Cloudflare trên máy chạy worker CQA:

1. Tạo token Cloudflare cấp quyền **Browser Run - Edit** cho tài khoản chạy trình duyệt. Cấu hình `ZALO_ORDER_SCREENSHOT_CF_ACCOUNT_ID` và `ZALO_ORDER_SCREENSHOT_CF_API_TOKEN` trong môi trường worker.
2. Đăng nhập [Zalo OA Manager](https://oa.zalo.me/chatv2) bằng một trình duyệt riêng có quyền đọc nhóm của OA cần theo dõi. Xuất Playwright storage state bằng `context.storageState({ indexedDB: true })`. Dùng phiên riêng của OA, vì trạng thái này chứa thông tin đăng nhập.
3. Cấu hình biến môi trường bí mật `ZALO_ORDER_SCREENSHOT_PROFILE` dưới dạng JSON một dòng. Điền `tenant_id` và `channel_id` đúng với kênh Zalo OA trong CRM, `zalo_oa_id` bằng ID chính thức của OA và `zalo_group_ids` bằng danh sách nhóm được phép chụp. Chỉ cấu hình nhóm thử nghiệm trong lần chạy đầu tiên. Đặt đối tượng Playwright storage state vào `storage_state`.
4. Áp dụng cấu hình cho worker và kiểm tra bằng một đơn gửi đến nhóm thử nghiệm. Worker tra cứu hai ID tin nhắn trong lịch sử GMF rồi đối chiếu thời điểm và nội dung với giao diện OA Manager trước khi chụp. Thu hồi hoặc làm mới phiên trình duyệt khi tài khoản đăng xuất hay hết hạn.

Workflow **Configure Zalo Browser Run** mặc định dùng `scope=pilot` với ID nhóm thử nghiệm. Khi cần chụp cho mọi nhóm GMF đang hoạt động và liên kết với kênh OA này trong CRM, đặt `"all_linked_groups": true` trong profile, bỏ `zalo_group_ids`, rồi áp dụng bằng `scope=all_linked_groups` và để trống ID nhóm. Nhóm mới liên kết với kênh sẽ thuộc phạm vi này. Có thể dùng lại token Cloudflare và phiên Zalo OA còn hiệu lực.

Cloudflare nhận trạng thái đăng nhập và xử lý trang Zalo trong trình duyệt từ xa. Hạn chế quyền của tài khoản Zalo dùng để chụp và để các biến cấu hình trong nơi lưu bí mật của hệ thống. Worker giữ lại xác nhận trong CRM khi phiên hết hạn, lịch sử GMF không còn hai tin liên quan hoặc hai bong bóng không thể hiển thị cùng lúc. Xem [Cloudflare Browser Run custom scripts](https://developers.cloudflare.com/browser-run/script/) và [lịch sử nhóm GMF](https://stc-developers.zdn.vn/docs/v2/official-account/nhom-chat-gmf/quan-ly/group_conversation).
