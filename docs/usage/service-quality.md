# Chất lượng CSKH Messenger

Trang **Chất lượng CSKH** dùng dữ liệu tin nhắn Facebook đã đồng bộ để theo dõi phản hồi, khách đang chờ, nhu cầu mua hàng, sản phẩm được hỏi, feedback và quality mess.

## Bắt đầu

1. Kết nối và đồng bộ Fanpage trong **Kênh chat**. Kiểm tra thời điểm và trạng thái đồng bộ ở mục **Nguồn dữ liệu Messenger**.
2. Mở **Chất lượng CSKH**. Thời gian phản hồi và hàng chờ hoạt động ngay với lịch sử đã đồng bộ, không cần chạy AI.
3. Bấm **Ngưỡng phản hồi** để chỉnh giờ trực và mục tiêu. Mặc định **08:00–22:00 hằng ngày**, múi giờ **Asia/Ho_Chi_Minh**, mục tiêu **5 phút**, quá hạn **15 phút**. Cấu hình áp dụng chung cho các Fanpage trong công ty đang chọn.
4. Bấm **Tạo phân tích nội dung**. Mẫu Messenger điền sẵn loại tác vụ phân loại, nhóm nhu cầu, feedback, quality mess và lịch chạy sau đồng bộ. Chọn Fanpage, cấu hình AI theo tác vụ hiện có, kiểm tra rồi lưu tác vụ. Có thể chạy ngay để phân tích lịch sử hiện có.

Tác vụ QC và phân loại cũ tiếp tục hoạt động. Với tác vụ phân loại có sẵn, bật **Trích xuất insight có cấu trúc (sản phẩm, feedback, chất lượng khách hàng)** tại bước quy tắc để bổ sung dữ liệu cho trang này. Chỉ những lần phân tích sau khi bật mới có các trường bổ sung.

## Cách tính thời gian

- Một lượt chờ bắt đầu ở tin nhắn đầu tiên của khách chưa được Fanpage trả lời. Khách nhắn nhiều tin liên tiếp không làm đặt lại đồng hồ.
- Phản hồi đầu tiên từ Fanpage kết thúc lượt chờ. Chỉ thời gian trong ca trực được cộng. Ví dụ khách nhắn 21:58, Fanpage trả lời 08:03 hôm sau thì thời gian phản hồi là **5 phút**.
- Tỷ lệ đúng hạn = số lượt đã phản hồi trong mục tiêu / tổng số lượt đã phản hồi. Khách còn chờ được hiển thị riêng; không được xem tỷ lệ này là tỷ lệ xử lý toàn bộ khách.
- Bộ lọc ngày lấy những lượt có thời điểm khách bắt đầu chờ trong khoảng đã chọn. Phản hồi sau ngày cuối vẫn kết thúc lượt đó. Hàng chờ hiện tại luôn hiển thị cả yêu cầu cũ ngoài khoảng ngày.
- “Lượt đầu quan sát được” dựa trên tin nhắn có trong hệ thống; không khẳng định đó là lần đầu khách liên hệ trong toàn bộ lịch sử Facebook.
- Tin nhắn không có thời gian hợp lệ hoặc nằm trong tương lai bị loại và có cảnh báo. Tin hệ thống không kết thúc lượt chờ. Một số câu kết thúc ngắn như “cảm ơn”, “ok” hoặc sticker trong 30 phút sau phản hồi được bỏ qua; nếu câu đó nối tiếp một yêu cầu đang chờ, yêu cầu vẫn còn chờ.

Trang tự tải lại mỗi 60 giây khi đang mở. Mức quá hạn tăng theo thời gian trong ca, kể cả không có tin nhắn mới. Dữ liệu vẫn phụ thuộc lịch đồng bộ Fanpage.

## Khách bị bỏ sót

Chọn **Quá hạn** hoặc **Đang chờ**, mở chi tiết để xem lượt chờ và chuyển sang hội thoại. Nếu đã xử lý qua điện thoại hoặc không cần phản hồi, bấm **Đánh dấu đã xử lý** và ghi lý do.

Đánh dấu xử lý không tạo tin nhắn và không được tính là một phản hồi đúng hạn. Khi khách nhắn tiếp, hội thoại trở lại hàng chờ. Nếu có tin mới được đồng bộ trong lúc thao tác, hệ thống yêu cầu tải lại trước khi ghi nhận. Hành động được ghi vào nhật ký hệ thống.

## Nội dung, feedback và quality mess

Mẫu AI trích xuất nhu cầu, tên sản phẩm/SKU được khách đề cập, feedback theo chủ đề và cảm xúc, cùng mức tiềm năng mua: cao, vừa, thấp, spam hoặc chưa rõ. Chi tiết có trích dẫn và lý do để kiểm tra; kết quả AI có thể cần người dùng đối chiếu với hội thoại. Khiếu nại không đồng nghĩa với khách tiềm năng thấp.

Mỗi hội thoại được tính một lần cho mỗi nhãn hoặc sản phẩm. Chỉ tổng hợp kết quả mới nhất, còn khớp thời điểm tin nhắn nguồn và được tạo trong khoảng ngày đã chọn, trong các hội thoại xuất hiện ở báo cáo. Hội thoại có tin mới sau khi phân tích được đánh dấu kết quả cũ và tạm loại khỏi tổng hợp cho đến lần phân tích tiếp theo.

Các mục này là kết quả AI. Chúng không phản ánh việc nhân viên đã chọn giai đoạn “phù hợp”, “chưa phù hợp”, “tiềm năng” trong Meta Business Suite. Hiện chưa xác minh được API công khai đọc mục giai đoạn đó; vì vậy không dùng kết quả AI còn thiếu để đếm nhân viên quên phân loại trên Meta. Xem [nghiên cứu về giai đoạn khách hàng Meta](../research/meta-native-lead-stages.md).

## Phạm vi và phân quyền

Mục **Phân loại thủ công trên Meta Inbox** là bộ đếm riêng theo dữ liệu nhãn tự tạo trên Meta. Không dùng kết quả AI và không phụ thuộc bộ lọc ngày của KPI phản hồi. Xem [quy trình nhãn Meta](./meta-inbox-labels.md).

- Dữ liệu hiện nhận diện bên trả lời là **Fanpage**, chưa xác định được nhân viên cụ thể hoặc phân biệt chắc chắn bot với người trực. Dùng số liệu để đánh giá vận hành Fanpage; việc chấm từng nhân viên cần bổ sung dữ liệu phân công/người gửi từ nguồn hỗ trợ.
- Cảnh báo đang nằm trong giao diện. Tính năng này không tự gửi Messenger, email hoặc thông báo ra bên ngoài.
- Quyền đọc tin nhắn cho phép xem báo cáo; quyền sửa tin nhắn cho phép đánh dấu xử lý; quyền sửa cài đặt cho phép đổi giờ trực/ngưỡng; quyền sửa tác vụ cho phép tạo phân tích AI.
- Báo cáo đọc tối đa 5.000 hội thoại hoặc 100.000 tin nhắn mỗi lần. Vượt mức sẽ yêu cầu lọc một Fanpage, không trả KPI từ dữ liệu bị cắt. Nếu một Fanpage vẫn vượt mức, cần triển khai tổng hợp dữ liệu trước khi dùng báo cáo cho quy mô đó.
- Backend tự tạo bảng lưu sự kiện xử lý qua cơ chế AutoMigrate hiện có khi khởi động phiên bản mới. Không cần nhập lại tin nhắn; cần chạy tác vụ Messenger để có phân tích nội dung có cấu trúc.
