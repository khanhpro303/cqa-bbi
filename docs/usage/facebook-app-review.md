# Chuẩn bị Facebook App Review

Luồng kết nối Page giúp reviewer thấy việc cấp quyền và dữ liệu thực sự sử dụng trong CQA. Có luồng OAuth không tự bảo đảm được Meta duyệt: cần cấu hình Dashboard, quyền phù hợp và video chứng minh tính năng đang hoạt động.

## Cấu hình trước khi quay

- Dùng Meta App của CQA, không dùng App khác của người nhập token.
- Trong **Facebook Login for Business → Configurations**, tạo cấu hình **User access token** cho các quyền Page: `pages_show_list`, `pages_read_engagement`, `pages_messaging`, `pages_manage_metadata`. Không dùng lại cấu hình chỉ dành cho Instagram.
- Cấu hình asset/Page selection và quyền truy cập phù hợp với Page dùng kiểm thử. Nếu use case Meta bắt buộc `business_management`, kiểm tra và giải thích đúng phần chọn/cấp quyền tài sản; CQA hiện không có tính năng quản trị doanh nghiệp riêng.
- Backend cần `FACEBOOK_APP_ID`, `FACEBOOK_APP_SECRET`, `FACEBOOK_API_VERSION` và `FACEBOOK_PAGE_LOGIN_CONFIG_ID`. Không đưa App Secret hoặc access token vào video, frontend hay tài liệu submission.
- Production Valid OAuth Redirect URI: `https://crm.bbi.vn/api/v1/channels/facebook/callback`.
- Cấu hình webhook Page với callback `https://crm.bbi.vn/api/v1/webhooks/facebook`, verification token của backend, và các trường `messages`, `messaging_postbacks`, `messaging_referrals`. Backend đăng ký Page đã chọn qua `/{page-id}/subscribed_apps`; thao tác này không thay thế cấu hình callback và fields của Meta App trong Dashboard.
- Kiểm tra Privacy Policy, Data Deletion URL/callback và Deauthorization theo yêu cầu Dashboard. Luồng kết nối Page không tự bổ sung các callback này.
- Chuẩn bị tài khoản CQA test có quyền chỉnh sửa kênh, tài khoản Facebook có quyền trên Page test và dữ liệu Messenger không nhạy cảm. Trong Development mode, dùng tài khoản/asset được phép test theo cấu hình Meta.

Sau khi cập nhật biến môi trường, cần recreate container app để áp dụng. Backend sẽ không bật kết nối OAuth Page nếu thiếu cấu hình; nhập token thủ công vẫn hoạt động.

## Video resubmit

Quay liên tục, bắt đầu khi **CQA đã đăng xuất**:

1. Đăng nhập CQA bằng tài khoản reviewer.
2. Mở Kênh chat, chọn kết nối Facebook Page.
3. Hiển thị đầy đủ màn hình đăng nhập và cấp quyền Meta. Nếu tài khoản đã cấp quyền trước đó, chuẩn bị tài khoản/quyền để video thể hiện được consent thực tế.
4. Quay lại CQA, hiển thị danh sách Page và chọn Page test.
5. Xác nhận, mở chi tiết kênh và chạy đồng bộ.
6. Hiển thị cuộc hội thoại/tin nhắn thật đã nhập, và tính năng sử dụng dữ liệu đó trong CQA.

Dùng giao diện tiếng Anh hoặc phụ đề tiếng Anh; reviewer cần làm lại được từng bước bằng hướng dẫn và tài khoản test. Không chỉ quay Graph API Explorer hoặc nhập token có sẵn để thay cho consent của luồng OAuth.

## Mô tả quyền đúng với app hiện tại

| Quyền | Minh chứng trong CQA |
| --- | --- |
| `pages_show_list` | Danh sách Page sau khi cấp quyền và lựa chọn rõ ràng của người dùng. |
| `pages_read_engagement` | Dữ liệu Page được đọc trong luồng đồng bộ; giải thích cụ thể endpoint và dữ liệu phục vụ tính năng. |
| `pages_messaging` | Nhập lịch sử hội thoại/tin nhắn Messenger để xem và phân tích chất lượng. Không mô tả khả năng gửi/trả lời tin nếu chưa được triển khai. |
| `pages_manage_metadata` | Đăng ký webhook cho Page đã chọn. Nếu demo referral, tạo sự kiện có `ref`/`ad_id` thật và hiển thị attribution lưu trong CQA. |

Webhook hiện lưu attribution/referral, **không nhập mọi tin nhắn thường theo thời gian thực**. Lịch sử tin nhắn được lấy qua đồng bộ. Hàm gửi tin Facebook hiện chưa được triển khai. Không hứa các tính năng này trong submission.

Nhập token thủ công vẫn là luồng riêng. Với tích hợp server-to-server/System User, đối chiếu ngoại lệ và hướng dẫn review tương ứng của Meta; không coi OAuth là bắt buộc cho mọi mô hình tích hợp.

## Tài liệu Meta

- [Screen recordings for App Review](https://developers.facebook.com/docs/app-review/submission-guide/screen-recordings)
- [Facebook Login for Business](https://developers.facebook.com/documentation/facebook-login/facebook-login-for-business)
- [Pages API](https://developers.facebook.com/docs/pages-api)
- [Page subscribed_apps](https://developers.facebook.com/docs/graph-api/reference/page/subscribed_apps/)
- [Messenger Webhooks](https://developers.facebook.com/docs/messenger-platform/webhooks)
- [Pages use case](https://developers.facebook.com/documentation/development/create-an-app/pages-use-case)
