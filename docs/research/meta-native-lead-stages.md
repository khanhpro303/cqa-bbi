# Giai đoạn khách hàng trên Meta và khả năng theo dõi bằng Graph API

Ngày kiểm tra: 13/09/2026.

## Yêu cầu đã xác nhận

Đếm hội thoại chưa được nhân viên phân loại qua **mục trạng thái/giai đoạn khách hàng có sẵn trong Meta Business Suite**, ví dụ “chưa phù hợp”, “phù hợp”, “tiềm năng”. Người dùng xác nhận đây không phải nhãn tự tạo và không phải kết quả AI.

## Kết luận triển khai

**Chưa có đủ hợp đồng API công khai để triển khai bộ đếm này theo đúng quy trình hiện tại.** Trong các tài liệu chính thức đã đọc, không tìm thấy trường trả về giai đoạn thủ công của Business Suite hoặc webhook thông báo thay đổi giai đoạn đó.

Đây là kết luận về bề mặt API công khai đã kiểm tra, không khẳng định Meta không có API nội bộ hoặc cơ chế riêng dành cho đối tác. Chưa gọi API trên tài khoản thật trong đợt nghiên cứu này.

Không có dữ liệu giai đoạn phải được hiểu là **chưa xác định**, không phải “nhân viên chưa phân loại”, “chưa phù hợp” hoặc số lượng bằng 0. Do đó không thêm KPI từ phép trừ tổng hội thoại với số nhãn AI.

## Bằng chứng chính thức

| Nguồn | Quan sát | Ý nghĩa đối với yêu cầu |
| --- | --- | --- |
| [Conversation reference](https://developers.facebook.com/docs/graph-api/reference/conversation/) | Tài liệu đang hiển thị v26.0; các trường được liệt kê gồm `id`, `is_owner`, `messages`, `participants`, `updated_time`. | Không có trường giai đoạn thủ công. `updated_time` được mô tả là thời điểm thêm tin nhắn cuối, không phải mốc thay đổi phân loại. |
| [SDK chính thức: UnifiedThread](https://github.com/facebook/facebook-python-business-sdk/blob/main/facebook_business/adobjects/unifiedthread.py) | Có các trường như folder, unread count, participants và messages; không thấy trường giai đoạn khách hàng. | Trạng thái đọc, thư mục và quyền sở hữu luồng không thay thế được phân loại khách hàng. |
| [SDK chính thức: Lead](https://github.com/facebook/facebook-python-business-sdk/blob/main/facebook_business/adobjects/lead.py) | Các trường liên quan dữ liệu biểu mẫu/quảng cáo, không thấy trạng thái pipeline do nhân viên cập nhật. | Không thể dùng bản ghi Lead Ads để mặc định suy ra giai đoạn hiện tại trong Business Suite. |
| [Page webhook reference](https://developers.facebook.com/docs/graph-api/webhooks/reference/page/) | Có messaging events, `leadgen`, `inbox_labels` và các trường khác; không tìm thấy sự kiện thay đổi giai đoạn thủ công được mô tả. | Chưa có căn cứ để đồng bộ sự kiện phân loại thủ công hoặc xác định nhân viên đã thao tác. |
| [Custom Labels for Customers](https://developers.facebook.com/documentation/business-messaging/messenger-platform/identity/custom-labels.md) | Có GET nhãn theo Page/PSID và `inbox_labels`. Tài liệu cũng mô tả nhãn hoàn tất/không đạt/đang tiến hành/chưa hoàn tất của luồng quảng cáo Messenger Lead Ads. | Đây là dữ liệu nhãn và kết quả luồng câu hỏi quảng cáo. Không có bằng chứng rằng chúng phản ánh mục giai đoạn thủ công mà người dùng yêu cầu. |
| [Messenger Lead Generation Ads](https://developers.facebook.com/documentation/business-messaging/messenger-platform/conversation-routing/messenger-lead-ads.md) | Có summary và metadata khi luồng câu hỏi hoàn tất hoặc chưa hoàn tất, cùng chuyển quyền hội thoại. | Những sự kiện này không chứng minh nhân viên đã chọn một giai đoạn trên Meta. |

Custom Labels API chỉ tài liệu hóa `id` và `page_label_name`; không có trường
`source`, `type`, `is_automatic`, `ad_id`, campaign hoặc actor. Nhãn gắn với PSID,
không phải trường được tài liệu hóa trên conversation object. Vì vậy backend
không được suy đoán provenance bằng tên. Bản triển khai lưu lần quan sát đầu tiên
làm baseline bất biến và cấm toàn bộ ID baseline tham gia tracking. Attribution
quảng cáo chính thức nếu cần phải lấy từ webhook referral (`source=ADS`, `ad_id`),
không suy ra từ custom label.

Các trang tham chiếu và SDK được đọc qua bản nội dung trích xuất từ nguồn chính thức; đường dẫn Meta cũ đôi khi trả lỗi khi truy cập trực tiếp. Adapter của dự án hiện dùng `v21.0`; không nâng phiên bản API chỉ vì tài liệu tham chiếu đang hiển thị phiên bản mới hơn. Không có bằng chứng rằng nâng phiên bản sẽ mở trường còn thiếu.

## Hiện trạng mã nguồn

- `backend/channels/facebook.go` đọc hội thoại qua `id,link,updated_time,participants`; lưu metadata từ phản hồi đó.
- Báo cáo CSKH mới đọc kết quả AI `conversation_insight` và thời gian tin nhắn. Nó không nhận dữ liệu phân loại thủ công từ Meta.
- Không thể dùng việc thiếu `conversation_insight` hoặc nhãn AI làm bằng chứng nhân viên chưa thao tác trong Meta.

## Phương án nếu chấp nhận đổi cách thao tác

**Nhãn tự tạo trong Meta Inbox:** nhân viên vẫn làm việc ngay trên Meta, nhưng chọn nhãn được quy định để phân loại. API được tài liệu hóa là `GET /{PAGE-ID}/custom_labels?fields=page_label_name` và `GET /{PSID}/custom_labels?fields=page_label_name`; webhook `inbox_labels` báo thay đổi nhãn.

Trước khi bật KPI cần kiểm chứng trên Fanpage thật: thêm, đổi và xóa nhãn trong Inbox phải phản ánh đúng qua API/webhook. Giữ nhãn theo ID; chỉ các nhãn nghiệp vụ được cấu hình mới được tính là phân loại. Nhãn khác không đủ điều kiện. Nhiều nhãn phân loại mâu thuẫn phải hiện riêng. Khách chưa đọc nhãn thành công hoặc chưa đồng bộ phải ở nhóm “chưa xác định”. Lỗi quyền, rate limit hoặc phân trang chưa hoàn tất không được biến thành “chưa phân loại”. Tài liệu cũng nêu hạn chế lấy nhãn cho một số tài khoản Messenger tạo bằng số điện thoại.

Đây là thay đổi quy trình so với mục giai đoạn sẵn có. Chỉ triển khai khi người dùng chọn phương án này. Không tự tạo nhãn, đăng ký webhook, thay đổi giai đoạn hoặc gửi tin nhắn ra Meta trong bước nghiên cứu.

Nếu giữ nguyên giai đoạn có sẵn, giữ trạng thái tính năng là **chưa có nguồn API được xác minh**. Không xây tích hợp dựa trên endpoint GraphQL nội bộ của giao diện hoặc đưa số đếm giả định vào dashboard.

## Phương án đã chọn

Người dùng đã chốt dùng nhãn tự tạo trong Meta Inbox. Bản triển khai đọc nhãn, đếm và cảnh báo độc lập với AI. Xem [hướng dẫn cấu hình và vận hành](../usage/meta-inbox-labels.md).
