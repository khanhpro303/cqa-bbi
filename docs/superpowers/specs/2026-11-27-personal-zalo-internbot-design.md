# Internbot cho Zalo cá nhân

Internbot theo dõi tin nhắn mới của khách trong các nhóm mà tài khoản Zalo cá nhân đang tham gia và trong chat 1:1 gửi tới tài khoản đó. Internbot tạo phản hồi dự thảo bằng cấu hình AI hiện có của tenant và gửi dự thảo vào một nhóm giám thị được chọn trên kênh.

## Giao diện kênh chat

- Thêm mục **Chế độ chatbot** với hai lựa chọn **Chatbot** và **Internbot** cho kênh Zalo cá nhân trong form kết nối mới và hộp thoại chỉnh sửa kênh. Thẻ kênh hiển thị chế độ đang chọn và trạng thái hoạt động. Kênh Zalo OA tiếp tục dùng công tắc bật/tắt hiện tại.
- **Chatbot** gửi phản hồi vào hội thoại khách theo quy tắc hiện hành, bao gồm danh sách hội thoại được duyệt. **Internbot** gửi dự thảo vào nhóm giám thị. Mỗi kênh chỉ chạy một chế độ tại một thời điểm. Người quản trị có thể tạm dừng chế độ đang chọn.
- Nếu chọn Internbot khi tạo kênh, luồng là **kết nối QR → chọn nhóm giám thị → bật Internbot**. Form đã lưu có các trạng thái **Chờ kết nối**, **Chưa chọn nhóm**, **Đang hoạt động**, **Đã tạm dừng** và **Nhóm không còn khả dụng**. Nếu gateway chưa có danh sách nhóm, bộ chọn hiện trạng thái tải hoặc lỗi và cho tải lại.
- Bộ chọn **Nhóm giám thị** tìm theo tên, có ID để phân biệt nhóm trùng tên và hiển thị thời điểm đồng bộ danh sách. Thông báo ngay tại bộ chọn rằng thành viên nhóm sẽ thấy trích đoạn tin nhắn khách và dự thảo. Trước khi bật, quản trị viên xác nhận nhóm được chọn là nhóm nội bộ. Một kênh có một nhóm giám thị.
- Kênh Zalo cá nhân hiện có mặc định được diễn giải là Chatbot và giữ trạng thái bật/tắt cũ. Kênh mới mặc định là Chatbot ở trạng thái tắt. Khi chuyển giữa hai chế độ, chế độ vừa chọn bắt đầu ở trạng thái tắt và được bật bằng thao tác rõ ràng.

## Hội thoại nguồn và nhóm giám thị

| Tình huống | Hành vi Internbot |
| --- | --- |
| Khách nhắn 1:1 vào tài khoản cá nhân | Sinh dự thảo từ tin nhắn mới, kể cả khi khách chưa kết bạn. |
| Khách nhắn trong nhóm mà tài khoản cá nhân là thành viên | Sinh dự thảo khi tin nhắn đủ điều kiện. Khách có thể nhắn bình thường, không cần tag hoặc gọi tên bot. |
| Nhóm có OA, tài khoản cá nhân cũng là thành viên | Gateway cá nhân nhận tin như ở các nhóm khác. Internbot chỉ gửi dự thảo vào nhóm giám thị. |
| Nhóm chỉ có OA | Chế độ Internbot không khả dụng cho kênh OA. |
| Người gửi là nhân viên, tài khoản bot, hoặc tin phát sinh trong nhóm giám thị | Bỏ qua trước khi gọi AI. |

- Đề xuất áp dụng việc duyệt theo hội thoại cho Chatbot như hiện tại. Với Internbot, thao tác bật của quản trị viên áp dụng cho các tin mới từ cả nhóm và 1:1, kể cả hội thoại mới. Thiết lập phạm vi đồng bộ lịch sử tiếp tục điều khiển luồng import. Phạm vi quan sát Internbot gồm cả hai loại hội thoại theo yêu cầu.
- Xác định nhân viên bằng ID Zalo cá nhân theo tenant, kênh và tài khoản đang kết nối. Dùng whitelist còn hiệu lực của kênh, liên kết nhân viên đã xác thực và ánh xạ tài khoản nhân viên còn hiệu lực khi xử lý nhóm. Không so khớp tên hiển thị hoặc suy ra ID cá nhân từ ID OA. Thiếu dữ liệu xác minh do lỗi hệ thống thì hoãn lượt, tránh phân loại nhầm nhân viên thành khách.
- Dùng listener trực tiếp và webhook nội bộ có chữ ký của gateway hiện tại. Chỉ nhận sự kiện trực tiếp sau thời điểm bật hoặc đổi cấu hình. Bỏ qua lịch sử, tin tự gửi, echo và lượt trùng. Backfill hoặc import thủ công không tạo dự thảo. Đầu vào của bản đầu là tin văn bản mới, theo khả năng nhận tin của Chatbot cá nhân hiện tại.

## Dùng chung luồng tạo phản hồi

- Dùng chung phần giải cấu hình và tạo phản hồi với Chatbot cá nhân. Đọc model, Langflow, system prompt, kho tri thức sản phẩm và chính sách quyền từ cấu hình đang hoạt động của tenant. Thay đổi cấu hình AI áp dụng cho chế độ đang chạy. Internbot không cần cấu hình AI riêng.
- Giữ quyền của đúng khách nguồn. Khách chưa xác thực dùng kho tri thức được phép cho khách. Khách đã xác thực dùng cùng quy tắc tra cứu chỉ đọc và phạm vi dữ liệu của chính họ nếu luồng Chatbot tại hội thoại nguồn đã cho phép. Tin chuyển vào nhóm giám thị chỉ chứa nội dung được phép chia sẻ tại đích và không mang mã xác minh, token hoặc thông tin xác thực. Trường hợp cần quyền cao hơn hiển thị yêu cầu xử lý cho giám thị.
- Điểm kích hoạt của Internbot là tin khách gửi, còn AI vẫn có thể quyết định câu xã giao hoặc tin thiếu nội dung không cần dự thảo. Lưu ngữ cảnh theo tenant, kênh, tài khoản Zalo, loại và ID hội thoại nguồn trong một phiên Internbot. Phiên Chatbot đang chạy và chat của nhóm giám thị nằm ngoài ngữ cảnh này. Trong nhóm nguồn có nhiều khách, các lượt nối tiếp và lựa chọn nhiều bước gắn với ID khách tương ứng.
- Kết quả dùng chung gồm nội dung dự thảo, ảnh được phép chia sẻ và tín hiệu cần nhân viên xử lý. Internbot biểu diễn thao tác có tác động thật như tạo phiếu khiếu nại, gửi thông báo bàn giao, cập nhật ERP hoặc xác minh danh tính thành đề xuất trong dự thảo. Luồng mô phỏng không cấp quyền ghi cho AI và không gọi các bước thực thi của Chatbot.

Ví dụ tin gửi vào nhóm giám thị.

> **Internbot · Nhóm Đại lý Hà Nội**
>
> **Khách** Trần An · 14:07
>
> **Tin nhắn** Storm III còn màu đen không em?
>
> **Phản hồi đề xuất** Dạ, anh cho em xin kích thước cần tìm để em kiểm tra đúng phiên bản ạ.

## API và định tuyến

- Bổ sung `GET /channels/:channelId/personal-zalo/groups` dưới API tenant. Trả về danh sách phân trang gồm ID nhóm, tên hiển thị và thời điểm quan sát. Dùng ảnh chụp danh sách nhóm đang tham gia của gateway. Hội thoại đã import, nhóm chỉ còn trong lịch sử và danh sách CRM không chứng minh tài khoản còn là thành viên. Bộ chọn cũng thấy nhóm tài khoản vừa vào dù nhóm chưa có tin nhắn. Giới hạn quyền theo kênh và tenant, chỉ owner/admin được cấu hình.
- Form tạo có thể lưu `chatbot_mode: "internbot"` trước QR. Khi chỉnh sửa, API kiểm tra và lưu chế độ, trạng thái bật và `supervisor_group_id` cùng nhau. Nhóm phải còn trong danh sách mới của chính tài khoản đã kết nối thì mới bật được. API từ chối cấu hình Internbot trên OA. Giữ ngữ nghĩa cờ trả lời cũ của Zalo cá nhân dành cho Chatbot, với trạng thái bật Internbot riêng. Chuyển sang Internbot phải tắt cờ trả lời cũ trong cùng giao dịch.
- Gateway trên `origin/main` đã có cơ chế đồng bộ thành viên nhóm. Mở điều kiện đồng bộ cho kênh đã chọn Internbot ngay cả khi chưa bật, để hoàn thành bước chọn nhóm sau QR. Giữ việc đồng bộ tư cách thành viên độc lập với quyết định duyệt gửi của Chatbot. Khi gateway mất kết nối, nhóm giám thị bị rời, hoặc tài khoản kết nối thay đổi, API phải đánh dấu cấu hình chưa khả dụng và yêu cầu xác minh lại đích.
- Backend quyết định chế độ từ cấu hình kênh và xác minh nguồn. Gateway khai báo hỗ trợ giao thức Internbot qua webhook có chữ ký. Phản hồi Internbot dùng trường `internbot_delivery` riêng và chỉ được trả cho gateway có hỗ trợ, để gateway cũ không thể diễn giải dự thảo thành phản hồi Chatbot gửi về nguồn. Chỉ dẫn phải gắn tài khoản, kênh, hội thoại nguồn, nhóm đích, nội dung và phiên bản cấu hình. Gateway kiểm tra lại trước từng lần gửi, dùng đúng tài khoản Zalo cá nhân đã kết nối và gửi dưới loại hội thoại nhóm. Nếu đích thay đổi, thiếu quyền gửi, không xác minh được tư cách thành viên hoặc bằng nhóm nguồn thì dừng lượt.
- Tận dụng cơ chế hàng đợi, giới hạn gửi theo tài khoản và chống lặp trong gateway. Định danh lượt theo tenant, kênh, tài khoản, loại/ID hội thoại và ID tin nhắn nguồn. Sau khi bật lại hoặc chuyển nhóm, chỉ xử lý tin tới dưới cấu hình mới. Ghi nhận đã gửi, thất bại hoặc chưa xác nhận để tránh gửi lại khi chưa biết lần trước có thành công. Nếu khách có nhân viên trả lời trong hội thoại nguồn, Internbot vẫn gửi dự thảo phục vụ giám thị.

## Điểm tích hợp và nghiệm thu

Thiết kế đối chiếu với `origin/main` tại `64f4a01`. `frontend/src/views/Channels.vue` có form tạo và thẻ kênh, còn `frontend/src/components/channels/ChannelEditDialog.vue` dùng chung cho chỉnh sửa. `backend/api/handlers/personal_zalo_chatbot.go` gắn điều kiện nhận tin, AI và tác động gửi trong cùng handler. `backend/api/handlers/personal_zalo_roster.go` và `personal-zalo-gateway/src/zalo/roster.ts` có danh sách hội thoại đang hiện diện. `personal-zalo-gateway/src/zalo/account-pool.ts` giữ hàng đợi và thực hiện gửi Zalo, nên đây là điểm cần kiểm tra định tuyến riêng cho Internbot.

1. Tạo kênh cá nhân, chọn Internbot trước QR, kết nối, thấy nhóm vừa tham gia, chọn được đích và bật. OA chỉ hiện công tắc hiện tại. Kênh cũ vẫn dùng đúng trạng thái Chatbot đã lưu.
2. Tin mới của khách trong một nhóm cá nhân, một nhóm có OA và một chat 1:1 tạo dự thảo ở cùng nhóm giám thị. Cả ba nguồn nhận 0 phản hồi từ Internbot.
3. Nhân viên whitelist, tin của chính bot, tin trong nhóm giám thị, tin cũ hoặc webhook trùng không sinh thêm dự thảo. Khách chưa xác thực không truy cập dữ liệu ERP. Yêu cầu tạo khiếu nại chỉ tạo đề xuất cho giám thị.
4. Đổi nhóm giám thị hoặc tạm dừng khi AI đang chạy. Lượt cũ không được gửi sang nhóm trước, nhóm sau hay hội thoại nguồn. Kiểm tra tương tự khi tài khoản rời nhóm, đổi đăng nhập, hoặc backend và gateway ở hai phiên bản trong lúc nâng cấp.
5. Đổi một giá trị cấu hình AI trong tenant, gửi cùng tình huống khách dưới mỗi chế độ và xác nhận cả hai đều nhận cấu hình mới. Kiểm tra các lượt hỏi tiếp và lựa chọn sản phẩm không dùng nhầm phiên hoặc lựa chọn của khách khác.
