# Đếm phân loại thủ công bằng nhãn Meta Inbox

Tính năng này **chỉ đọc dữ liệu nhãn, đếm và cảnh báo**. Nó không gọi AI, không dùng kết quả AI để xác định trạng thái, và không tự gán hoặc xóa nhãn trên Meta.

## Quy trình hằng ngày

1. Nhân viên trò chuyện với khách trong Meta Inbox như hiện tại.
2. Nhân viên chọn nhãn phân loại, ví dụ **Phù hợp**, **Chưa phù hợp**, **Tiềm năng**. Khi chuyển nhóm, bỏ nhãn cũ và gán nhãn mới.
3. CQA đọc nhãn từ Meta. Quản lý vào **Chất lượng CSKH → Phân loại thủ công trên Meta Inbox** để xem số đếm và danh sách cần xử lý.
4. Nếu có hội thoại chưa có nhãn phân loại, trang hiện cảnh báo và nút **Xem chưa phân loại**.

**Lưu ý về thao tác:** mục **Nhãn/Labels** và mục **giai đoạn khách hàng có sẵn** trong Meta là hai dữ liệu khác nhau. Chọn giai đoạn không tự tạo nhãn. Nếu công ty vẫn dùng cả hai mục, nhân viên cần cập nhật cả hai. Tính năng CQA này theo dõi nhãn đã thống nhất.

## Cấu hình một lần cho mỗi Fanpage

1. Tạo các nhãn cần dùng trong Meta Inbox, hoặc dùng các nhãn đã có.
2. Trên CQA, chọn Fanpage trong mục **Phân loại thủ công trên Meta Inbox**, bấm **Cấu hình nhãn**.
3. Bấm **Lấy danh mục nhãn từ Meta** để tải nhãn thật của Page. Thao tác này chỉ đọc Meta.
4. Chọn nhãn tương ứng cho ba nhóm **Phù hợp**, **Chưa phù hợp**, **Tiềm năng**. Có thể dùng nhiều nhãn cho cùng một nhóm; một nhãn chỉ thuộc một nhóm.
5. Bật theo dõi, lưu cấu hình rồi bấm **Đồng bộ nhãn**.

Mỗi nhãn được nhận diện bằng ID, không đoán theo tên. Nhãn khác như “VIP” hoặc “Đã gọi” không được xem là phân loại nếu chưa được đưa vào một trong ba nhóm. Khi nhãn bị xóa khỏi Meta, cần lấy lại danh mục và sửa cấu hình; hệ thống không coi việc thiếu nhãn cấu hình là khách chưa phân loại.

### Boundary với nhãn tự động từ quảng cáo

Meta chỉ trả về ID và tên nhãn, không trả nguồn gắn nhãn hoặc người/công cụ đã gắn. Vì vậy CQA lưu bất biến lần đọc nhãn thành công đầu tiên của từng hội thoại làm **nhãn mặc định lúc tiếp nhận**. Tất cả ID từng xuất hiện trong baseline này trên Fanpage được khóa ở backend:

- vẫn lưu và hiển thị cùng hội thoại để giữ thông tin nguồn/campaign/ad;
- không xuất hiện trong danh sách chọn nhãn theo dõi;
- API từ chối nếu cố ánh xạ chúng vào Phù hợp, Chưa phù hợp hoặc Tiềm năng;
- bộ phân loại luôn bỏ qua chúng, kể cả khi cấu hình cũ từng ánh xạ nhầm;
- nếu baseline mới được lưu đồng thời với thao tác cấu hình, backend tự loại rule xung đột trong cùng transaction.

Nhân viên phải dùng các nhãn khác, được gắn sau khi tiếp nhận, cho ba giai đoạn theo dõi. Hội thoại chưa đọc được baseline thành công nằm ở **Chưa xác định**, không được suy ra là chưa phân loại. Với dữ liệu lịch sử có trước bản nâng cấp, lần đọc thành công đầu tiên sau nâng cấp là baseline bảo thủ; hệ thống không thể dựng lại ai đã gắn nhãn hay thời điểm gắn từ API Meta.

## Cách đọc số liệu

| Mục | Điều kiện |
| --- | --- |
| Chưa phân loại | Đã đọc đầy đủ nhãn thành công trong 30 phút gần nhất và không có nhãn nào thuộc ba nhóm đã cấu hình. |
| Phù hợp / Chưa phù hợp / Tiềm năng | Có một hoặc nhiều nhãn, cùng thuộc một nhóm. |
| Đã phân loại | Tổng ba nhóm hợp lệ ở trên. |
| Xung đột nhãn | Có nhãn thuộc từ hai nhóm khác nhau, ví dụ vừa Phù hợp vừa Chưa phù hợp. Cần nhân viên sửa trên Meta. |
| Chưa xác định | Chưa bật/cấu hình, chưa đọc được, đọc lỗi, hoặc dữ liệu quá cũ. Không tính là chưa phân loại. |

Tổng gồm tất cả hội thoại đã đồng bộ vào CQA của Fanpage đang chọn. Mỗi hội thoại chỉ thuộc một nhóm đếm. Danh sách này không phụ thuộc bộ lọc ngày của báo cáo tốc độ phản hồi bên trên. Nhãn Meta gắn với khách hàng theo Page-scoped ID, không gắn với từng lượt chat hay từng tin nhắn; bộ đếm hiển thị nhãn đó trên hội thoại đã lưu.

Nhân viên hoặc công cụ khác đều có thể gán nhãn. API cho biết nhãn hiện có; bộ đếm này không chứng minh ai đã gán nhãn hoặc đó có phải là thao tác thủ công hay không. Boundary baseline ở trên chỉ bảo đảm nhãn đã có sẵn không được dùng làm tracking; nó không suy đoán provenance từ tên nhãn.

## Đồng bộ và cảnh báo

- Sau mỗi lần đồng bộ Facebook thành công, CQA bổ sung baseline cho mọi hội thoại chưa có baseline, kể cả khi chưa bật theo dõi. Khi bật theo dõi, CQA tiếp tục đọc lại nhãn hiện tại cho toàn bộ hội thoại đã lưu của Page. Không chỉ đọc hội thoại có tin mới, vì nhãn có thể thay đổi mà không có tin nhắn.
- **Đồng bộ nhãn** chạy lại việc đọc từ Meta theo yêu cầu. **Làm mới số liệu** chỉ đọc dữ liệu đã lưu trong CQA.
- Trang làm mới số liệu mỗi 60 giây; khi đang đồng bộ thì mỗi 5 giây. Không hứa thời gian thực: độ trễ phụ thuộc lịch đồng bộ Facebook, quy mô hội thoại và giới hạn API.
- Cảnh báo nằm trong giao diện CQA. Chưa gửi thông báo ra email, Telegram, Messenger hoặc dịch vụ bên ngoài.
- Nếu một số khách không đọc được nhãn, hệ thống báo đồng bộ một phần và giữ nhóm Chưa xác định. Nếu Page bị lỗi quyền/token hoặc giới hạn API khiến toàn bộ lượt đọc phải dừng, các số phân loại được xem là chưa xác định cho tới khi đồng bộ lại.
- Trong lúc đồng bộ, các hội thoại tạm nằm ở Chưa xác định và cảnh báo chưa phân loại được ẩn. Báo cáo chỉ dùng lượt đọc đã hoàn tất, tránh lẫn số liệu đang cập nhật.
- Tiến độ được lưu theo nhóm 100 hội thoại. Nếu máy chủ dừng hoặc kết nối lỗi, lượt đồng bộ theo lịch/yêu cầu tiếp theo chạy tiếp từ nhóm đã lưu; nhóm chưa lưu được đọc lại. Mỗi hội thoại vẫn hết hạn dữ liệu sau 30 phút tính từ lúc đọc thực tế.
- Mỗi máy chủ xử lý tối đa hai Fanpage cùng lúc, bốn lượt đọc khách hàng trên mỗi Fanpage. Cùng một Page chỉ có một lượt đồng bộ giữa các máy chủ; có thời gian chờ tối thiểu một phút sau mỗi lượt. Khi Meta giới hạn API, hệ thống dừng và báo lỗi, không tự lặp gọi liên tục.
- Báo cáo giới hạn 5.000 hội thoại đã lưu mỗi Page. Vượt giới hạn sẽ báo rõ và vẫn cho sửa/tắt cấu hình; không trả tổng từ dữ liệu bị cắt như thể đã đầy đủ. Một lượt chạy tối đa hai giờ, gia hạn khóa định kỳ và giữ tiến độ nếu bị gián đoạn. Với lượt chạy dài, những hội thoại đã đọc quá 30 phút được tính riêng vào Chưa xác định.

## Phân quyền và kiểm chứng

Người có quyền đọc tin nhắn xem được báo cáo. Quyền sửa tin nhắn cho phép yêu cầu đồng bộ. Quyền sửa cài đặt cho phép lấy danh mục nhãn và thay đổi cấu hình. Mặc định chưa bật cho Fanpage nào.

Trước khi dùng để đánh giá vận hành, kiểm tra trên Fanpage thật ba tình huống: thêm nhãn, đổi nhãn, xóa nhãn trong Meta rồi đồng bộ CQA. Cần xác nhận quyền của ứng dụng/Page token, điều khoản liên hệ của Page và khả năng đọc nhãn của khách đó. Meta nêu hạn chế với một số tài khoản Messenger tạo bằng số điện thoại.

Tài liệu API chính thức: [Custom Labels](https://developers.facebook.com/documentation/business-messaging/messenger-platform/identity/custom-labels.md). Xem thêm [nghiên cứu về giai đoạn có sẵn của Meta](../research/meta-native-lead-stages.md).
